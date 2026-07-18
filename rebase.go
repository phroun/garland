package garland

import "unicode/utf8"

// rebase.go - deliberate reconciliation of the buffer against a file.
//
// When the host app learns the source file changed underneath the
// buffer ("file changed on disk - reload or keep your version?"), it
// can call Rebase to take the FILE as the new base:
//
//   - Every current block is anchor-matched against the file by hash,
//     tolerating piecewise shifts from multiple external edits (a
//     running-shift tracker follows each insert/delete).
//   - Matched blocks keep their identity - content, decorations, warm
//     backing (re-homed to their new offsets) - without their bytes
//     ever being read into memory when warm storage is available.
//   - Placeholder blocks whose bytes still exist in the file (e.g.
//     lost cold storage over an unchanged region) are HEALED.
//   - Everything else - externally edited regions, resized blocks,
//     and unsaved local edits (the disk wins; that is what rebasing
//     means) - is adopted from the file and reported.
//   - The result is ONE recorded mutation: "keep your version" stays
//     available as a plain undo to PreviousRevision, because the
//     pre-rebase tree is untouched (path-copied, not mutated).
//   - Source-change tracking is re-baselined: warm trust is reset and
//     verified fresh, giving a clean starting point.
//
// RebaseOnFile(fs, name) does the same against a DIFFERENT file,
// which then becomes the buffer's source (path, handle, and warm
// backing all switch to it).
//
// NAMING: the bare "Rebase" name is deliberately left unclaimed - it
// is reserved for a possible future git-style rebase of the HISTORY
// tree (replaying one fork's revisions onto another base). These two
// methods rebase the CONTENT onto a file.

// RebaseRegion is one contiguous region of the new buffer whose
// content came from the file rather than from preserved blocks.
type RebaseRegion struct {
	Offset int64 // byte offset in the new buffer (== file offset)
	Length int64
}

// RebaseReport describes what a Rebase did.
type RebaseReport struct {
	// Adopted lists the regions whose content was taken from the file
	// (external edits ingested; unsaved local edits displaced).
	Adopted []RebaseRegion

	BytesKept    int64 // bytes preserved through matched blocks
	BytesAdopted int64 // bytes taken from the file
	BlocksKept   int   // matched blocks (identity preserved)
	BlocksHealed int   // placeholder blocks recovered from the file

	OldSize int64 // buffer size before the rebase
	NewSize int64 // buffer size after (== file size)

	// NoChange is true when the buffer already matched the file
	// byte-for-byte: no mutation was recorded (backing offsets may
	// still have been re-homed and placeholders healed in place).
	NoChange bool

	// PreviousRevision is the revision holding the pre-rebase buffer;
	// UndoSeek there is the "keep your version" escape hatch. Equal to
	// the current revision when NoChange.
	PreviousRevision RevisionID
}

// RebaseOnSource reconciles the buffer against its own source file.
// See the file header for semantics.
func (g *Garland) RebaseOnSource() (RebaseReport, error) {
	g.mu.RLock()
	noSource := g.sourcePath == ""
	g.mu.RUnlock()
	if noSource {
		return RebaseReport{}, ErrNoDataSource
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	fs := g.sourceFS
	if fs == nil {
		fs = g.lib.defaultFS
	}
	return g.rebaseLocked(fs, g.sourcePath)
}

// RebaseOnFile reconciles the buffer against the named file, which
// then becomes the buffer's source (path, handle, and warm backing
// switch to it). A nil fs uses the library default.
func (g *Garland) RebaseOnFile(fs FileSystemInterface, name string) (RebaseReport, error) {
	if name == "" {
		return RebaseReport{}, ErrNoDataSource
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if fs == nil {
		if name == g.sourcePath && g.sourceFS != nil {
			fs = g.sourceFS
		} else {
			fs = g.lib.defaultFS
		}
	}
	return g.rebaseLocked(fs, name)
}

func (g *Garland) rebaseLocked(fs FileSystemInterface, path string) (RebaseReport, error) {
	g.awaitNoSaveLocked() // rebase reads the file a save may be rewriting
	if g.transaction != nil {
		return RebaseReport{}, ErrTransactionPending
	}
	if g.loader != nil && !g.loader.eofReached {
		return RebaseReport{}, ErrNotSupported // still streaming in
	}

	switching := path != g.sourcePath
	handle := g.sourceHandle
	ownHandle := false
	if switching || handle == nil {
		h, err := fs.Open(path, OpenModeRead)
		if err != nil {
			return RebaseReport{}, err
		}
		handle = h
		ownHandle = true
	}
	closeOwn := func() {
		if ownHandle {
			fs.Close(handle)
		}
	}

	size, err := fs.FileSize(handle)
	if err != nil {
		closeOwn()
		return RebaseReport{}, err
	}

	readAt := func(off, n int64) ([]byte, error) {
		if err := fs.SeekByte(handle, off); err != nil {
			return nil, err
		}
		d, err := fs.ReadBytes(handle, int(n))
		if err != nil {
			return nil, err
		}
		if int64(len(d)) != n {
			return nil, ErrWarmStorageMismatch
		}
		return d, nil
	}

	report := RebaseReport{
		OldSize:          g.totalBytes,
		NewSize:          size,
		PreviousRevision: g.currentRevision,
	}

	// ---- Anchor matching: find each block's content in the file ----
	// Candidates per block: its offset adjusted by the running shift
	// (follows piecewise external edits), its recorded offset, and its
	// offset adjusted by the total size delta. Anchors are forced
	// monotone in file order, so externally MOVED content loses block
	// identity but never correctness (its region is simply adopted).
	spans := g.currentLeafSpans()
	canWarm := g.loadingStyle == AllStorage

	var delta int64
	if !switching && g.sourceState != nil {
		delta = size - g.sourceState.originalSize
	}

	type anchorInfo struct {
		sp     leafSpan
		newOff int64
		data   []byte // resident copy when warm backing is unavailable
		healed bool
	}
	var anchors []anchorInfo
	var shift, filePos, lastBufEnd int64
	nonEmpty := 0
	for _, sp := range spans {
		if sp.snap.byteCount == 0 {
			continue
		}
		nonEmpty++
		eh := expectedLeafHash(sp.snap)
		if eh == nil {
			continue
		}
		var cands []int64
		if off := sp.snap.originalFileOffset; off >= 0 {
			cands = append(cands, off+shift, off, off+delta)
		} else if sp.bufOff == lastBufEnd {
			// Fresh block contiguous with the previous anchor: its
			// content may sit at the natural next file position (e.g.
			// buffer already equals the file).
			cands = append(cands, filePos)
		}
		matched := false
		tried := map[int64]bool{}
		tryAt := func(c int64) bool {
			if tried[c] || c < filePos || c+sp.snap.byteCount > size {
				return false
			}
			tried[c] = true
			d, err := readAt(c, sp.snap.byteCount)
			if err != nil || !hashesEqual(eh, computeHash(d)) {
				return false
			}
			a := anchorInfo{sp: sp, newOff: c,
				healed: sp.snap.storageState == StoragePlaceholder}
			if !canWarm {
				a.data = d
			}
			anchors = append(anchors, a)
			if sp.snap.originalFileOffset >= 0 {
				shift = c - sp.snap.originalFileOffset
			}
			filePos = c + sp.snap.byteCount
			lastBufEnd = sp.bufOff + sp.snap.byteCount
			report.BlocksKept++
			report.BytesKept += sp.snap.byteCount
			if a.healed {
				report.BlocksHealed++
			}
			return true
		}
		for _, c := range cands {
			if tryAt(c) {
				matched = true
				break
			}
		}
		// Piecewise shifts: between two external edits a block's true
		// shift lies strictly between the shift seen so far and the
		// total size delta. Scan that interval, bounded by a work
		// budget so huge churn on huge blocks cannot stall the rebase
		// (unmatched blocks are simply adopted - correct, just less
		// identity preserved).
		if !matched && sp.snap.originalFileOffset >= 0 && shift != delta {
			lo, hi := shift, delta
			if lo > hi {
				lo, hi = hi, lo
			}
			if budget := int64(4<<20) / sp.snap.byteCount; hi-lo <= budget {
				for c := sp.snap.originalFileOffset + lo; c <= sp.snap.originalFileOffset+hi; c++ {
					if tryAt(c) {
						break
					}
				}
			}
		}
	}

	// ---- No-change fast path: every block anchored AND the anchors'
	// total length equals the file size - being monotone and
	// non-overlapping, they must tile [0, size] exactly, so the file
	// already equals the buffer. Only backing bookkeeping may need
	// refreshing (re-home moved offsets, heal placeholders in place);
	// no mutation is recorded.
	if len(anchors) == nonEmpty && report.BytesKept == size {
		for _, a := range anchors {
			snap := a.sp.snap
			snap.originalFileOffset = a.newOff
			if a.healed {
				snap.placeholderReason = ""
			}
			// Every anchored block was just verified against this very
			// file - the file is now the authoritative backing. Cold
			// blocks (whose backend may even be gone) and healed
			// placeholders flip to it; resident data stays resident.
			switch {
			case snap.storageState == StorageMemory && snap.data != nil:
				// keep
			case a.data != nil:
				snap.data = a.data
				snap.storageState = StorageMemory
				g.updateMemoryTracking(int64(len(a.data)))
			case canWarm:
				snap.data = nil
				snap.storageState = StorageWarm
			}
		}
		report.NoChange = true
		g.rebaseSourceBookkeeping(fs, path, handle, switching, ownHandle)
		return report, nil
	}

	// ---- Build the new leaf sequence in file order ----
	var newLeaves []*NodeSnapshot
	adoptGap := func(from, to int64) error {
		if to <= from {
			return nil
		}
		report.Adopted = append(report.Adopted, RebaseRegion{from, to - from})
		for off := from; off < to; {
			n := g.maxLeafSize
			if to-off < n {
				n = to - off
			}
			d, err := readAt(off, n)
			if err != nil {
				return err
			}
			if off+n < to {
				d = d[:trimToRuneBoundary(d)]
			}
			ns := createLeafSnapshot(d, nil, off)
			ns.storageState = StorageMemory
			newLeaves = append(newLeaves, ns)
			g.updateMemoryTracking(int64(len(d)))
			report.BytesAdopted += int64(len(d))
			off += int64(len(d))
		}
		return nil
	}
	pos := int64(0)
	for _, a := range anchors {
		if err := adoptGap(pos, a.newOff); err != nil {
			closeOwn()
			return report, err
		}
		cp := *a.sp.snap // content identical -> aggregates carry over
		cp.originalFileOffset = a.newOff
		cp.placeholderReason = ""
		switch {
		case cp.storageState == StorageMemory && cp.data != nil:
			// keep resident data
		case a.data != nil:
			cp.data = a.data
			cp.storageState = StorageMemory
			g.updateMemoryTracking(int64(len(a.data)))
		default:
			cp.data = nil
			cp.storageState = StorageWarm
		}
		leaf := cp
		newLeaves = append(newLeaves, &leaf)
		pos = a.newOff + cp.byteCount
	}
	if err := adoptGap(pos, size); err != nil {
		closeOwn()
		return report, err
	}
	if len(newLeaves) == 0 {
		ns := createLeafSnapshot([]byte{}, nil, -1)
		ns.storageState = StorageMemory
		newLeaves = append(newLeaves, ns)
	}

	// ---- Commit as ONE recorded mutation ----
	// The old tree is untouched, so PreviousRevision remains a plain
	// undo away ("keep your version" even after choosing to rebase).
	if g.transaction == nil {
		g.recordCursorPositionsInHistory()
	}
	newRootID := g.rebuildBalanced(newLeaves, 0, len(newLeaves))
	g.root = g.nodeRegistry[newRootID]
	g.updateCountsFromRoot()

	// Map cursors through the anchors: positions inside kept blocks
	// move with them; positions in replaced regions keep their local
	// distance, clamped to the replacement.
	mapping := make([]rebaseAnchor, len(anchors))
	for i, a := range anchors {
		mapping[i] = rebaseAnchor{a.sp.bufOff, a.sp.snap.byteCount, a.newOff}
	}
	for _, cursor := range g.cursors {
		cursor.bytePos = rebaseMapPos(cursor.bytePos, mapping, size)
	}
	g.reconcileCursorCoordinates()
	g.recordMutation()

	g.rebaseSourceBookkeeping(fs, path, handle, switching, ownHandle)
	return report, nil
}

// rebaseAnchor is one kept block's old buffer range and new position,
// for cursor mapping.
type rebaseAnchor struct {
	oldBufOff, length, newOff int64
}

// rebaseMapPos maps an old-buffer position to the new buffer through
// the anchor list.
func rebaseMapPos(p int64, anchors []rebaseAnchor, size int64) int64 {
	var prevOldEnd, prevNewEnd int64
	for _, a := range anchors {
		if p < a.oldBufOff {
			np := prevNewEnd + (p - prevOldEnd)
			if np > a.newOff {
				np = a.newOff
			}
			return np
		}
		if p < a.oldBufOff+a.length {
			return a.newOff + (p - a.oldBufOff)
		}
		prevOldEnd = a.oldBufOff + a.length
		prevNewEnd = a.newOff + a.length
	}
	np := prevNewEnd + (p - prevOldEnd)
	if np > size {
		np = size
	}
	if np < 0 {
		np = 0
	}
	return np
}

// rebaseSourceBookkeeping installs the (possibly new) source and
// re-baselines all change tracking: fresh starting point.
func (g *Garland) rebaseSourceBookkeeping(fs FileSystemInterface, path string,
	handle FileHandle, switching, ownHandle bool) {
	if switching {
		if g.sourceHandle != nil && g.sourceFS != nil {
			g.sourceFS.Close(g.sourceHandle)
		}
		g.sourcePath = path
		g.sourceFS = fs
		g.sourceHandle = handle
	} else if g.sourceHandle == nil && ownHandle {
		g.sourceHandle = handle
		if g.sourceFS == nil {
			g.sourceFS = fs
		}
	}
	if g.sourceState == nil {
		g.initSourceState()
	}
	g.sourceState.status = SourceStatusNormal
	_ = g.captureSourceInfo()
	// A fresh starting point is a hard edge for undo coalescing too.
	g.coalesce.active = false
	// Warm trust restarts clean: every warm block in the new view was
	// just verified (anchored by hash) against this very file.
	g.warmVerification = make(map[NodeID]*warmVerificationState)
	for _, sp := range g.currentLeafSpans() {
		if sp.snap.storageState == StorageWarm {
			g.updateWarmVerification(sp.node.id)
		}
	}
}

// trimToRuneBoundary returns the largest cut <= len(d) that does not
// split a UTF-8 sequence across the cut (falls back to len(d) for
// non-UTF-8 tails, e.g. binary content).
func trimToRuneBoundary(d []byte) int {
	n := len(d)
	for back := 1; back <= utf8.UTFMax && back <= n; back++ {
		b := d[n-back]
		if !utf8.RuneStart(b) {
			continue // continuation byte; keep looking for the leader
		}
		var need int
		switch {
		case b < utf8.RuneSelf:
			need = 1 // ASCII (back > 1 here means invalid tail: binary)
		case b&0xE0 == 0xC0:
			need = 2
		case b&0xF0 == 0xE0:
			need = 3
		case b&0xF8 == 0xF0:
			need = 4
		default:
			return n // invalid leader: not UTF-8, cut anywhere
		}
		if back >= need {
			return n // the final sequence fits entirely
		}
		return n - back // incomplete trailing sequence: cut before it
	}
	return n // no rune start in the last 4 bytes: not UTF-8
}
