package garland

import "fmt"

// save.go - the in-place save engine.
//
// DESIGN CONSTRAINT: saving must never require a second copy of the
// document on disk. A temp-file-plus-rename save doubles peak disk
// usage, which defeats the point of a library built to edit a 1.2MB
// file on a 1.44MB floppy. The file is therefore rewritten IN PLACE:
//
//   - The file is opened read-write and is NEVER truncated up front;
//     it only shrinks (if at all) as the very last step.
//   - Warm storage (unmodified spans whose only copy of the bytes IS
//     the original file) keeps working: spans that did not move are
//     not even rewritten, and spans that moved are re-homed to their
//     new offsets afterwards, staying warm.
//   - Content is written in an order that never overwrites warm bytes
//     before they have been read (see below), so no migration is
//     needed for the CURRENT revision at all.
//   - Warm spans that only undo HISTORY references are surgically
//     migrated to cold storage first (SaveOptions.PreserveHistory),
//     because the rewrite may overwrite their backing bytes.
//
// WRITE ORDERING: the new file is a sequence of spans in tree order.
// Each span has a new offset (prefix sum of the new layout) and an old
// source range in the original file (empty for freshly typed content).
// Because the rope preserves relative order, both the old ranges and
// the new ranges are monotonically increasing across spans. Given
// that, a two-phase schedule is provably clobber-free:
//
//   Phase B first, BACK TO FRONT: warm spans that move RIGHT
//     (newOff > oldOff, i.e. net insertions before them). Processing
//     descending by new offset, each span's source is read while still
//     intact: later spans' writes land at or above this span's source
//     end, and phase A has not run yet.
//   Phase A second, FRONT TO BACK: everything else - fresh content,
//     cold/memory-sourced spans, and warm spans moving left or not at
//     all (unmoved ones are simply SKIPPED - their bytes are already
//     correct in the file). Ascending order cannot clobber a remaining
//     warm source: for any still-unwritten left-mover Z after span X,
//     newX+lenX <= newZ <= oldZ.
//
// Move/Copy operations can reorder leaves so that old offsets are no
// longer monotone in tree order; the offending spans are rescued into
// memory before any write (rare, bounded by the moved region).

// SaveOptions configures Save behavior.
type SaveOptions struct {
	// PreserveHistory protects warm-backed data that only OLDER
	// revisions reference: before the rewrite can overwrite its
	// backing bytes it is migrated to cold storage (or held in memory
	// when no cold backend is configured), so undo history survives
	// the save intact.
	//
	// When false, such history keeps pointing at its old file offsets;
	// regions the rewrite left untouched remain valid, and regions
	// that were overwritten fail their hash check on access and become
	// placeholders - undo history may be amputated, but never silently
	// corrupted.
	PreserveHistory bool

	// Concurrent (opt-in) runs the rewrite WITHOUT holding the buffer
	// lock: the app may keep reading and editing on its op goroutine
	// while the save writes (only Prune, DeleteFork, Rebase, Close,
	// and other saves wait for it). The cost is that every warm span
	// the rewrite displaces must first be EVACUATED - to cold storage
	// when a backend exists, else into memory - because the file
	// cannot be both the old layout (for live warm reads) and the new
	// layout (being written) at once. When there is no cold backend
	// and the evacuation would push memory past the configured hard
	// limit, the save transparently falls back to the locked zero-copy
	// path (SaveReport.Concurrent reports what actually ran).
	Concurrent bool
}

// saveSpan describes one leaf of the current revision in the new file
// layout.
type saveSpan struct {
	node   *Node
	snap   *NodeSnapshot
	key    ForkRevision
	newOff int64
	length int64
	oldOff int64 // source position in the OLD file layout
	oldLen int64 // length of the old-file source (0 = fresh content)
	warm   bool  // source bytes must be READ from the old file
	skip   bool  // bytes already correct at this offset - do not write
}

// ScarWarning reports one lost block that was written as a visible
// scar during a save. The app should surface these to the user:
// the save succeeded, but this data is gone.
type ScarWarning struct {
	Offset   int64  // byte offset of the scarred block in the saved content
	Length   int64  // byte count of the lost block (the scar occupies exactly this)
	Marker   string // the human-readable marker text
	Appended bool   // marker did not fit in the block; it was appended at EOF

	// Reason is why the data was lost, recorded at the moment the loss
	// was discovered (e.g. "cold storage read failed: ...", "source
	// file changed on disk (hash mismatch)"). Empty when the cause was
	// never observed by the library.
	Reason string
}

// SaveReport carries non-fatal outcomes of a save.
type SaveReport struct {
	// Scars lists blocks whose data was lost to storage failure and
	// were written as visible scars instead. Empty on a clean save.
	Scars []ScarWarning

	// Integrity lists block-level integrity events discovered since
	// the last successful save: external edits that slid, moved, or
	// modified warm-backed blocks (recovered or adopted without loss),
	// and hard losses. A successful save drains the pending log here;
	// IntegrityEvents() peeks at it between saves.
	Integrity []IntegrityEvent

	// Concurrent reports whether the save actually ran without holding
	// the buffer lock. A SaveOptions.Concurrent request falls back to
	// the locked zero-copy path (Concurrent=false here) when the
	// required evacuation cannot be afforded.
	Concurrent bool
}

// SaveWith overwrites the original file in place with the current
// content. See the file header for the full design. The report lists
// any lost blocks that were scarred; the app should warn the user.
func (g *Garland) SaveWith(opts SaveOptions) (SaveReport, error) {
	g.mu.RLock()
	noSource := g.sourcePath == ""
	g.mu.RUnlock()
	if noSource {
		return SaveReport{}, ErrNoDataSource
	}

	// One save at a time - a second Save (or SaveAs) blocks here until
	// the in-flight one finishes, whichever mode either uses.
	g.saveMu.Lock()
	defer g.saveMu.Unlock()

	fs := g.sourceFS
	if fs == nil {
		fs = g.lib.defaultFS
	}

	if opts.Concurrent {
		return g.saveConcurrent(fs, opts)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	return g.saveInPlace(fs, opts)
}

func (g *Garland) saveInPlace(fs FileSystemInterface, opts SaveOptions) (SaveReport, error) {
	// RULING: Save never refuses because data was lost. Placeholder
	// leaves become visible scars first (same byte count, so no other
	// offset moves), then the save proceeds normally - and the scars
	// are reported back so the app can warn the user.
	scars, err := g.scarifyPlaceholders()
	if err != nil {
		return SaveReport{}, err
	}
	report := SaveReport{Scars: scars}

	// A read handle on the old file is needed for warm sources and
	// history migration. Reuse the warm-storage handle when present.
	readHandle := g.sourceHandle
	ownReadHandle := false
	if readHandle == nil {
		h, err := fs.Open(g.sourcePath, OpenModeRead)
		if err == nil {
			readHandle = h
			ownReadHandle = true
		}
		// A missing file is fine when nothing needs warm reads; the
		// span walk below will fail loudly if it does.
	}
	if ownReadHandle {
		defer func() {
			if readHandle != nil {
				fs.Close(readHandle)
			}
		}()
	}

	// ---- Collect the span layout ----
	spans := make([]saveSpan, 0, 64)
	var walkErr error
	var newCursor, oldCursor int64
	var collect func(nodeID NodeID)
	collect = func(nodeID NodeID) {
		if walkErr != nil {
			return
		}
		node := g.nodeRegistry[nodeID]
		if node == nil {
			return
		}
		snap, key := node.snapshotAtWithKey(g.currentFork, g.currentRevision)
		if snap == nil {
			return
		}
		if !snap.isLeaf {
			collect(snap.leftID)
			collect(snap.rightID)
			return
		}
		if snap.byteCount == 0 {
			return
		}

		sp := saveSpan{
			node:   node,
			snap:   snap,
			key:    key,
			newOff: newCursor,
			length: snap.byteCount,
		}
		switch {
		case snap.storageState == StoragePlaceholder:
			walkErr = ErrColdStorageFailure
			return
		case snap.originalFileOffset >= 0 && snap.originalFileOffset >= oldCursor:
			// Backed by (or identical to) old file content, in order.
			sp.oldOff = snap.originalFileOffset
			sp.oldLen = snap.byteCount
			sp.warm = snap.storageState == StorageWarm
			sp.skip = sp.oldOff == sp.newOff
			oldCursor = sp.oldOff + sp.oldLen
		case snap.originalFileOffset >= 0 && snap.storageState == StorageWarm:
			// Out of order (a Move/Copy rearranged leaves): the
			// two-phase schedule cannot protect this source, so rescue
			// it into memory before any write happens.
			if err := g.readFromWarmStorageWithTrust(node.id, snap); err != nil {
				walkErr = err
				return
			}
			sp.oldOff = oldCursor
		default:
			// Fresh content (or out-of-order but memory/cold-sourced):
			// no old-file source to protect.
			sp.oldOff = oldCursor
		}
		newCursor += sp.length
		spans = append(spans, sp)
	}
	if g.root != nil {
		collect(g.root.id)
	}
	if walkErr != nil {
		return report, walkErr
	}
	newTotal := newCursor

	// The lowest offset the rewrite will disturb: everything before it
	// is untouched, so warm history pointing there stays valid.
	protectFrom := newTotal
	for _, sp := range spans {
		if !sp.skip && sp.newOff < protectFrom {
			protectFrom = sp.newOff
		}
	}

	// ---- Protect history's warm spans (surgical) ----
	currentSnaps := make(map[*NodeSnapshot]bool, len(spans))
	for _, sp := range spans {
		currentSnaps[sp.snap] = true
	}
	var oldSize int64 = -1
	if readHandle != nil {
		if sz, err := fs.FileSize(readHandle); err == nil {
			oldSize = sz
		}
	}
	if newTotal < oldSize {
		// Truncation destroys the tail too.
		if newTotal < protectFrom {
			protectFrom = newTotal
		}
	}
	if err := g.invalidateDisturbedHistory(currentSnaps, protectFrom, opts); err != nil {
		return report, err
	}

	// ---- Open the write handle: read-write, NO truncation ----
	writeHandle, err := fs.Open(g.sourcePath, OpenModeReadWrite)
	if err != nil {
		return report, err
	}
	defer fs.Close(writeHandle)

	readWarm := func(sp saveSpan) ([]byte, error) {
		if readHandle == nil {
			return nil, ErrWarmStorageMismatch
		}
		if err := fs.SeekByte(readHandle, sp.oldOff); err != nil {
			return nil, err
		}
		return fs.ReadBytes(readHandle, int(sp.oldLen))
	}
	writeSpan := func(sp *saveSpan) error {
		var data []byte
		switch {
		case sp.warm:
			d, err := readWarm(*sp)
			if err != nil {
				return err
			}
			if int64(len(d)) != sp.length {
				return ErrWarmStorageMismatch
			}
			data = d
		case sp.snap.storageState == StorageCold:
			if err := g.thawSnapshot(sp.node.id, sp.key, sp.snap); err != nil {
				return err
			}
			data = sp.snap.data
		default:
			data = sp.snap.data
		}
		if err := fs.SeekByte(writeHandle, sp.newOff); err != nil {
			return err
		}
		return fs.WriteBytes(writeHandle, data)
	}

	// ---- Phase B: warm right-movers, back to front ----
	for i := len(spans) - 1; i >= 0; i-- {
		sp := &spans[i]
		if sp.skip || !sp.warm || sp.newOff <= sp.oldOff {
			continue
		}
		if err := writeSpan(sp); err != nil {
			return report, err
		}
	}

	// ---- Phase A: everything else, front to back ----
	for i := range spans {
		sp := &spans[i]
		if sp.skip || (sp.warm && sp.newOff > sp.oldOff) {
			continue
		}
		if err := writeSpan(sp); err != nil {
			return report, err
		}
	}

	// ---- Shrink at the very end, and only then ----
	if oldSize >= 0 && newTotal < oldSize {
		if err := fs.Truncate(writeHandle, newTotal); err != nil {
			// A stale tail is silent corruption - refuse rather than
			// pretend the save succeeded.
			return report, err
		}
	}

	// ---- Re-home: the file now matches the buffer at NEW offsets ----
	for i := range spans {
		sp := &spans[i]
		sp.snap.originalFileOffset = sp.newOff
		if sp.warm {
			// Still warm, now against the rewritten file.
			g.updateWarmVerification(sp.node.id)
		} else if sp.skip && sp.snap.storageState == StorageCold &&
			g.loadingStyle == AllStorage && g.sourceHandle != nil {
			// A skipped cold span's bytes are provably in the file at
			// this offset - after a save the file is the better backing
			// store (the cold block may even be gone; a scarred save
			// proceeds regardless). Flip it to warm.
			sp.snap.storageState = StorageWarm
			g.updateWarmVerification(sp.node.id)
		}
	}

	// Re-baseline change detection so our own write is not reported as
	// an external modification.
	if g.sourceState != nil {
		g.sourceState.status = SourceStatusNormal
		_ = g.captureSourceInfo()
	}

	report.Integrity = g.drainIntegrityEvents()
	return report, nil
}

// invalidateDisturbedHistory protects / invalidates history snapshots
// the rewrite disturbs. INVARIANT: originalFileOffset >= 0 promises
// the file CURRENTLY holds the snapshot's bytes at that offset - the
// skip logic and warm reads trust it blindly. The rewrite breaks that
// promise for every snapshot outside the current view whose region
// intersects [protectFrom, EOF), so each one must be handled, whatever
// its storage state:
//   - warm + PreserveHistory: bytes are read back while the old file
//     is intact and migrated to cold (or held in memory).
//   - warm without PreserveHistory: the only copy is about to be
//     overwritten - marked lost NOW (undo history is amputated, never
//     silently corrupted).
//   - memory/cold: the bytes are safe elsewhere; only the stale
//     offset must be cleared, so no later save (after an undo or fork
//     seek makes this snapshot current again) can skip-trust a file
//     region that holds different bytes.
//
// Caller must hold the write lock.
func (g *Garland) invalidateDisturbedHistory(currentSnaps map[*NodeSnapshot]bool, protectFrom int64, opts SaveOptions) error {
	for _, node := range g.nodeRegistry {
		if node == nil {
			continue
		}
		for key, snap := range node.history {
			if snap == nil || !snap.isLeaf || currentSnaps[snap] {
				continue
			}
			if snap.originalFileOffset < 0 ||
				snap.originalFileOffset+snap.byteCount <= protectFrom {
				continue // rewrite never touches its bytes
			}
			if snap.storageState == StorageWarm {
				if opts.PreserveHistory {
					// Read it back while the old file is intact...
					if err := g.readFromWarmStorageWithTrust(node.id, snap); err != nil {
						return err
					}
					// ...and push it to cold if a backend exists (else
					// it simply stays in memory).
					if g.lib.coldStorageBackend != nil && g.loadingStyle != MemoryOnly {
						if err := g.chillSnapshot(node.id, key, snap); err != nil {
							return err
						}
					}
				} else {
					g.markSnapshotLost(snap,
						"backing bytes overwritten by save without PreserveHistory")
				}
			}
			snap.originalFileOffset = -1
		}
	}
	return nil
}

// ---- Placeholder scarification ----
//
// RULING: Save must NEVER refuse because data was lost (a placeholder
// leaf). The user's surviving work gets written, and the hole is
// scarred VISIBLY, at the exact size of the missing block so no other
// byte in the file moves:
//
//   - If the marker fits in the block (with a leading and a trailing
//     newline): "\n" + marker + "===...=" padding + "\n", exactly
//     blockLen bytes.
//   - If not, the block is filled with "\n" + "==...=" + "\n" (or
//     "\n\n" for two bytes, "\n" for one) and the marker is appended
//     at the END of the document instead, with a leading newline.
//
// Scarification mutates the buffer to match what will be written -
// the scar becomes real, readable content (one revision; the pre-scar
// revision remains reachable through undo, still erroring on access).

// placeholderMarker builds the human-readable marker for a lost block.
func placeholderMarker(snap *NodeSnapshot, nodeID NodeID) string {
	if snap.originalFileOffset >= 0 {
		return fmt.Sprintf("[Missing %d bytes from original file address %d]",
			snap.byteCount, snap.originalFileOffset)
	}
	return fmt.Sprintf("[Missing %d bytes from buffer fragment %d]",
		snap.byteCount, nodeID)
}

// scarBytes renders the in-block scar per the ruling. When the marker
// does not fit, the returned appendix is the marker to add at the end
// of the document (with its leading newline included).
func scarBytes(blockLen int64, marker string) (block []byte, appendix []byte) {
	if blockLen >= int64(len(marker))+2 {
		block = make([]byte, blockLen)
		block[0] = '\n'
		copy(block[1:], marker)
		for i := int64(len(marker)) + 1; i < blockLen-1; i++ {
			block[i] = '='
		}
		block[blockLen-1] = '\n'
		return block, nil
	}
	block = make([]byte, blockLen)
	for i := range block {
		block[i] = '='
	}
	if blockLen >= 1 {
		block[0] = '\n'
		block[blockLen-1] = '\n' // for blockLen 1 this is the same byte
	}
	return block, []byte("\n" + marker)
}

// scarifyPlaceholders converts every placeholder leaf of the current
// revision into its visible scar (and gathers overflow markers to
// append at the end). Returns one warning per scarred block; when any
// exist, one new revision was recorded.
func (g *Garland) scarifyPlaceholders() ([]ScarWarning, error) {
	type scarJob struct {
		node *Node
		snap *NodeSnapshot
		off  int64
	}
	var jobs []scarJob
	var off int64
	var walk func(id NodeID)
	walk = func(id NodeID) {
		node := g.nodeRegistry[id]
		if node == nil {
			return
		}
		snap := node.snapshotAt(g.currentFork, g.currentRevision)
		if snap == nil {
			return
		}
		if !snap.isLeaf {
			walk(snap.leftID)
			walk(snap.rightID)
			return
		}
		if snap.storageState == StoragePlaceholder && snap.byteCount > 0 {
			jobs = append(jobs, scarJob{node, snap, off})
		}
		off += snap.byteCount
	}
	if g.root != nil {
		walk(g.root.id)
	}
	if len(jobs) == 0 {
		return nil, nil
	}

	if g.transaction == nil {
		g.recordCursorPositionsInHistory()
	}

	var appendices []byte
	warnings := make([]ScarWarning, 0, len(jobs))
	for _, j := range jobs {
		marker := placeholderMarker(j.snap, j.node.ID())
		block, appendix := scarBytes(j.snap.byteCount, marker)
		appendices = append(appendices, appendix...)
		warnings = append(warnings, ScarWarning{
			Offset:   j.off,
			Length:   j.snap.byteCount,
			Marker:   marker,
			Appended: appendix != nil,
			Reason:   j.snap.placeholderReason,
		})

		// Replace the snapshot's content IN PLACE with the same byte
		// count: history that referenced this snapshot lost the same
		// data, and byte-for-byte replacement keeps every offset in
		// the tree valid. Only rune/line aggregates change.
		ns := createLeafSnapshot(block, j.snap.decorations, -1)
		ns.storageState = StorageMemory
		*j.snap = *ns
	}

	// Recompute internal aggregate weights along the whole current
	// view (rune/line counts changed under same byte counts).
	g.fixCurrentAggregates()

	// Overflow markers land at the very end of the document.
	if len(appendices) > 0 {
		rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
		if rootSnap == nil {
			return nil, ErrInternal
		}
		newRootID, err := g.insertInternal(g.root, rootSnap, g.totalBytes, 0, appendices, nil, false)
		if err != nil {
			return nil, err
		}
		g.root = g.nodeRegistry[newRootID]
		g.updateCountsFromRoot()
	}

	// Cursor byte positions are still valid (in-block scars are
	// byte-neutral; the appendix is past every cursor except EOF ones),
	// but rune/line coordinates under them may have changed.
	g.reconcileCursorCoordinates()

	g.recordMutation()
	return warnings, nil
}

// fixCurrentAggregates recomputes the internal aggregate weights of
// the whole current view after leaf content was replaced in place
// (rune/line counts changed under identical byte counts).
func (g *Garland) fixCurrentAggregates() {
	if g.root == nil {
		return
	}
	var fix func(id NodeID) *NodeSnapshot
	fix = func(id NodeID) *NodeSnapshot {
		node := g.nodeRegistry[id]
		if node == nil {
			return nil
		}
		snap := node.snapshotAt(g.currentFork, g.currentRevision)
		if snap == nil || snap.isLeaf {
			return snap
		}
		left := fix(snap.leftID)
		right := fix(snap.rightID)
		if left == nil || right == nil {
			return snap
		}
		snap.byteCount = left.byteCount + right.byteCount
		snap.runeCount = left.runeCount + right.runeCount
		snap.lineCount = left.lineCount + right.lineCount
		if right.lineCount > 0 {
			snap.runesAfterLastNewline = right.runesAfterLastNewline
		} else {
			snap.runesAfterLastNewline = left.runesAfterLastNewline + right.runeCount
		}
		return snap
	}
	fix(g.root.id)
	g.updateCountsFromRoot()
}

// reconcileCursorCoordinates recomputes every cursor's rune/line
// coordinates from its (still valid) byte position after leaf content
// was replaced byte-for-byte.
func (g *Garland) reconcileCursorCoordinates() {
	for _, cursor := range g.cursors {
		if cursor.bytePos > g.totalBytes {
			cursor.bytePos = g.totalBytes
		}
		cursor.runePos, _ = g.byteToRuneInternalUnlocked(cursor.bytePos)
		cursor.line, cursor.lineRune, _ = g.byteToLineRuneInternalUnlocked(cursor.bytePos)
		cursor.lineRuneDirty = false
	}
}
