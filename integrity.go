package garland

import (
	"bytes"
	"fmt"
)

// integrity.go - block-level forensics for warm-storage mismatches.
//
// When a warm block's bytes on disk no longer hash to what we expect,
// the data is not necessarily lost: another program may have edited
// the file deliberately. Before declaring a hard loss (placeholder,
// scarred on the next save) the mismatch is triaged, in order of best
// outcome:
//
//   1. SLIDE - the file's size changed by delta since our baseline; if
//      our exact bytes are found at originalFileOffset+delta, an
//      external insert/delete merely shifted the block. Re-home it.
//      Nothing lost.
//   2. SWAP - the bytes found at our position are the exact content of
//      ANOTHER preserved block, and OUR exact bytes sit at that
//      block's position: an external move. Exchange the two backing
//      offsets. Nothing lost.
//   3. SOFT ADOPT - the nearest preserved blocks before and after us
//      still verify at their recorded positions, so the change is
//      confined to this block and looks like a deliberate same-size
//      external edit. Adopt the file's bytes into the buffer (so a
//      future save does NOT destroy someone else's modification) and
//      warn the app. Our previous bytes for the block are gone either
//      way - the file was their only copy. If the adopted content
//      duplicates another preserved block, that is flagged too: it
//      suggests an external move/copy rather than an edit (though
//      genuinely repetitive content can trip this innocently, so it is
//      a classification hint, never a different action).
//   4. HARD LOSS - nothing around the block matches expectations and
//      no relocation was found: placeholder, scarred on save.
//
// Every outcome is recorded as an IntegrityEvent at the moment of
// discovery and carried until reported to the app.

// IntegrityKind classifies what happened to a block whose backing
// bytes stopped matching expectations.
type IntegrityKind int

const (
	// IntegrityBlockSlid: the block was found intact at a shifted file
	// offset (external insert/delete earlier in the file) and was
	// re-homed. Nothing lost; buffer content unchanged.
	IntegrityBlockSlid IntegrityKind = iota

	// IntegrityBlockSwapped: the block's exact bytes were found at
	// another block's backing position, and that block's bytes at
	// ours - an external move. Backing offsets were exchanged.
	// Nothing lost; buffer content unchanged.
	IntegrityBlockSwapped

	// IntegrityBlockAdopted: soft loss of integrity. The surrounding
	// blocks still verify, so the mismatch looks like a deliberate
	// external edit confined to this block. The file's bytes were
	// adopted into the buffer (a future save preserves them); the
	// buffer's previous bytes for this block are gone.
	IntegrityBlockAdopted

	// IntegrityBlockAdoptedDuplicate: adopted as above, but the
	// adopted content is an exact duplicate of another preserved
	// block - an external move/copy is likely, so the app may want to
	// look more closely.
	IntegrityBlockAdoptedDuplicate

	// IntegrityBlockLost: hard corruption. Nothing around the block
	// matched expectations and no relocation was found. The block is a
	// placeholder and will be written as a scar by the next save.
	IntegrityBlockLost

	// IntegrityDecorationsLost: a block's CONTENT thawed fine but its
	// decorations could not be restored from cold storage (side block
	// missing, hash mismatch, or corrupt encoding). The marks in that
	// block are gone; everything else is intact.
	IntegrityDecorationsLost

	// IntegrityBlockResized: an external edit INSIDE this block changed
	// its length - its old bytes no longer exist contiguously anywhere,
	// so reads fail (placeholder), but the situation is precisely
	// diagnosed: the neighbor before verifies at its recorded offset
	// and the neighbor after verifies shifted by the file's size
	// change. RebaseOnSource() will adopt the resized content cleanly.
	IntegrityBlockResized
)

// String returns a short human-readable name for the kind.
func (k IntegrityKind) String() string {
	switch k {
	case IntegrityBlockSlid:
		return "slid"
	case IntegrityBlockSwapped:
		return "swapped"
	case IntegrityBlockAdopted:
		return "adopted"
	case IntegrityBlockAdoptedDuplicate:
		return "adopted-duplicate"
	case IntegrityBlockLost:
		return "lost"
	case IntegrityDecorationsLost:
		return "decorations-lost"
	case IntegrityBlockResized:
		return "resized"
	}
	return "unknown"
}

// IntegrityEvent records one block-level integrity finding, kept from
// the moment of discovery until it is reported to the app (peek with
// Garland.IntegrityEvents; a successful save drains pending events
// into its SaveReport).
type IntegrityEvent struct {
	Kind         IntegrityKind
	BufferOffset int64  // block's byte offset in the buffer at discovery (-1 if unknown)
	FileOffset   int64  // where its backing bytes lived in the source file (-1 if none)
	Length       int64  // byte count of the block
	Detail       string // human-readable specifics (shift distance, duplicate location, cause)
}

// IntegrityEvents returns a copy of the integrity events accumulated
// since the last successful save (which drains them into its
// SaveReport).
func (g *Garland) IntegrityEvents() []IntegrityEvent {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]IntegrityEvent, len(g.integrityLog))
	copy(out, g.integrityLog)
	return out
}

func (g *Garland) logIntegrityEvent(ev IntegrityEvent) {
	g.integrityLog = append(g.integrityLog, ev)
}

// drainIntegrityEvents hands the pending events to a SaveReport.
// Caller must hold the write lock.
func (g *Garland) drainIntegrityEvents() []IntegrityEvent {
	evs := g.integrityLog
	g.integrityLog = nil
	return evs
}

// markSnapshotLost records a hard loss: the snapshot becomes a
// placeholder (keeping the first-discovered reason) and an
// IntegrityBlockLost event is logged.
func (g *Garland) markSnapshotLost(snap *NodeSnapshot, reason string) {
	snap.becomePlaceholder(reason)
	bufOff := int64(-1)
	for _, sp := range g.currentLeafSpans() {
		if sp.snap == snap {
			bufOff = sp.bufOff
			break
		}
	}
	g.logIntegrityEvent(IntegrityEvent{
		Kind:         IntegrityBlockLost,
		BufferOffset: bufOff,
		FileOffset:   snap.originalFileOffset,
		Length:       snap.byteCount,
		Detail:       snap.placeholderReason,
	})
}

// leafSpan is one leaf of the current revision with its buffer offset.
type leafSpan struct {
	node   *Node
	snap   *NodeSnapshot
	bufOff int64
}

// currentLeafSpans walks the current revision and returns every leaf
// with its buffer offset (prefix sums in tree order).
func (g *Garland) currentLeafSpans() []leafSpan {
	var spans []leafSpan
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
		spans = append(spans, leafSpan{node, snap, off})
		off += snap.byteCount
	}
	if g.root != nil {
		walk(g.root.id)
	}
	return spans
}

// expectedLeafHash returns the hash the leaf's content is expected to
// have, or nil when no expectation is available. In-memory leaves are
// hashed on the fly (hashes are lazy and may not be stored yet).
func expectedLeafHash(snap *NodeSnapshot) []byte {
	if len(snap.dataHash) > 0 {
		return snap.dataHash
	}
	if snap.storageState == StorageMemory && snap.data != nil {
		return computeHash(snap.data)
	}
	return nil
}

// triageWarmMismatch investigates a warm read whose bytes did not hash
// to the expectation. got is what the file holds at the block's
// recorded offset. On any soft outcome the snapshot's data is left
// resident in memory and nil is returned; on hard loss the snapshot is
// a placeholder and ErrWarmStorageMismatch is returned.
// Caller must hold the lock (single-goroutine contract).
func (g *Garland) triageWarmMismatch(nodeID NodeID, snap *NodeSnapshot, got []byte, gotHash []byte) error {
	fs, fh := g.sourceFS, g.sourceHandle

	readAt := func(off, n int64) []byte {
		if off < 0 || fh == nil {
			return nil
		}
		if err := fs.SeekByte(fh, off); err != nil {
			return nil
		}
		d, err := fs.ReadBytes(fh, int(n))
		if err != nil || int64(len(d)) != n {
			return nil
		}
		return d
	}

	spans := g.currentLeafSpans()
	bufOff := int64(-1)
	for _, sp := range spans {
		if sp.snap == snap {
			bufOff = sp.bufOff
			break
		}
	}

	// ---- 1. Slide: did an external insert/delete shift the block? ----
	var delta, curSize int64 = 0, -1
	if g.sourceState != nil {
		if sz, err := fs.FileSize(fh); err == nil {
			curSize = sz
			delta = curSize - g.sourceState.originalSize
		}
	}
	if delta != 0 {
		cand := snap.originalFileOffset + delta
		if cand >= 0 && cand+snap.byteCount <= curSize {
			if d := readAt(cand, snap.byteCount); d != nil &&
				hashesEqual(snap.dataHash, computeHash(d)) {
				oldOff := snap.originalFileOffset
				snap.originalFileOffset = cand
				g.installRecoveredData(nodeID, snap, d)
				// Everything file-backed at or beyond this block slid by
				// the same amount - re-home it all in one pass so each
				// following block does not stumble through its own
				// mismatch triage. (Blocks re-homed wrongly - multiple
				// distinct external edits - still verify on access and
				// triage individually.)
				rehomed := 0
				for _, sp := range spans {
					if sp.snap != snap && sp.snap.originalFileOffset >= oldOff {
						sp.snap.originalFileOffset += delta
						rehomed++
					}
				}
				g.logIntegrityEvent(IntegrityEvent{
					Kind:         IntegrityBlockSlid,
					BufferOffset: bufOff,
					FileOffset:   cand,
					Length:       snap.byteCount,
					Detail: fmt.Sprintf(
						"content found intact %+d bytes away (external insert/delete earlier in the file); re-homed along with %d following blocks",
						delta, rehomed),
				})
				return nil
			}
		}
	}

	// ---- 2. Swap / duplicate: is this some OTHER block's content? ----
	// A duplicate alone is a hint (external move/copy suspected, but
	// repetitive content trips it innocently). Finding OUR bytes at
	// the duplicate's backing position is proof of a move: recover
	// both blocks by exchanging their backing offsets.
	var dup *leafSpan
	for i := range spans {
		sp := &spans[i]
		if sp.snap == snap || sp.snap.byteCount != snap.byteCount {
			continue
		}
		eh := expectedLeafHash(sp.snap)
		if eh == nil || !hashesEqual(eh, gotHash) {
			continue
		}
		if dup == nil {
			dup = sp
		}
		if sp.snap.originalFileOffset >= 0 &&
			sp.snap.originalFileOffset != snap.originalFileOffset {
			if d := readAt(sp.snap.originalFileOffset, snap.byteCount); d != nil &&
				hashesEqual(snap.dataHash, computeHash(d)) {
				ourOld := snap.originalFileOffset
				snap.originalFileOffset = sp.snap.originalFileOffset
				sp.snap.originalFileOffset = ourOld // its content verified at our old spot
				g.installRecoveredData(nodeID, snap, d)
				g.logIntegrityEvent(IntegrityEvent{
					Kind:         IntegrityBlockSwapped,
					BufferOffset: bufOff,
					FileOffset:   snap.originalFileOffset,
					Length:       snap.byteCount,
					Detail: fmt.Sprintf(
						"content found at file offset %d, exchanged with the block at buffer offset %d (external move); both recovered",
						snap.originalFileOffset, sp.bufOff),
				})
				return nil
			}
		}
	}

	// ---- 3. Soft adopt: do the surrounding blocks still verify? ----
	// Nearest file-backed spans before and after this block, by FILE
	// position. A missing side (start/end of file) is vacuous; an
	// existing side must verify at its recorded offset.
	var prev, next *leafSpan
	for i := range spans {
		sp := &spans[i]
		if sp.snap == snap || sp.snap.originalFileOffset < 0 {
			continue
		}
		if sp.snap.originalFileOffset+sp.snap.byteCount <= snap.originalFileOffset {
			if prev == nil || sp.snap.originalFileOffset+sp.snap.byteCount >
				prev.snap.originalFileOffset+prev.snap.byteCount {
				prev = sp
			}
		} else if sp.snap.originalFileOffset >= snap.originalFileOffset+snap.byteCount {
			if next == nil || sp.snap.originalFileOffset < next.snap.originalFileOffset {
				next = sp
			}
		}
	}
	verifiesAt := func(sp *leafSpan, shift int64) bool {
		if sp == nil {
			return true // vacuous: no preserved block on that side
		}
		d := readAt(sp.snap.originalFileOffset+shift, sp.snap.byteCount)
		if d == nil {
			return false
		}
		if sp.snap.storageState == StorageMemory && sp.snap.data != nil {
			return bytes.Equal(d, sp.snap.data)
		}
		if len(sp.snap.dataHash) > 0 {
			return hashesEqual(sp.snap.dataHash, computeHash(d))
		}
		return false
	}
	verifies := func(sp *leafSpan) bool { return verifiesAt(sp, 0) }
	if verifies(prev) && verifies(next) {
		kind := IntegrityBlockAdopted
		detail := "surrounding blocks verify; adopted the file's bytes as a deliberate external edit (this block's previous content is gone, but saving will no longer destroy the modification)"
		if dup != nil {
			kind = IntegrityBlockAdoptedDuplicate
			detail += fmt.Sprintf(
				"; adopted content exactly duplicates the preserved block at buffer offset %d (external move/copy suspected)",
				dup.bufOff)
		}
		g.adoptLeafContent(nodeID, snap, got, gotHash)
		g.logIntegrityEvent(IntegrityEvent{
			Kind:         kind,
			BufferOffset: bufOff,
			FileOffset:   snap.originalFileOffset,
			Length:       snap.byteCount,
			Detail:       detail,
		})
		return nil
	}

	// ---- 3b. Resized: an external edit INSIDE this block changed its
	// length. Signature: the neighbor before verifies at its recorded
	// offset, the neighbor after verifies shifted by the file's size
	// change, and both are byte-adjacent to us - so the entire length
	// change is confined to our region. The old bytes no longer exist
	// contiguously anywhere (no candidate offset can ever match), so
	// reads must fail - but the app is told that a deliberate RebaseOnSource()
	// will adopt the resized content cleanly, instead of a bare loss.
	if delta != 0 {
		prevAdjacent := prev == nil && snap.originalFileOffset == 0 ||
			prev != nil && prev.snap.originalFileOffset+prev.snap.byteCount == snap.originalFileOffset
		nextAdjacent := next == nil && g.sourceState != nil &&
			snap.originalFileOffset+snap.byteCount == g.sourceState.originalSize ||
			next != nil && next.snap.originalFileOffset == snap.originalFileOffset+snap.byteCount
		if prevAdjacent && nextAdjacent && snap.byteCount+delta >= 0 &&
			verifies(prev) && verifiesAt(next, delta) {
			reason := fmt.Sprintf(
				"external edit inside this block changed its length by %+d bytes; RebaseOnSource() can adopt the file's new content",
				delta)
			snap.becomePlaceholder(reason)
			g.logIntegrityEvent(IntegrityEvent{
				Kind:         IntegrityBlockResized,
				BufferOffset: bufOff,
				FileOffset:   snap.originalFileOffset,
				Length:       snap.byteCount,
				Detail:       reason,
			})
			return ErrWarmStorageMismatch
		}
	}

	// ---- 4. Hard loss ----
	g.markSnapshotLost(snap,
		"source file changed on disk (hash mismatch; surrounding blocks also fail verification)")
	return ErrWarmStorageMismatch
}

// installRecoveredData makes recovered bytes resident after a slide or
// swap: content is unchanged (it hashed to the expectation), only the
// backing-store bookkeeping moved, so no aggregates or cursors change.
func (g *Garland) installRecoveredData(nodeID NodeID, snap *NodeSnapshot, data []byte) {
	snap.data = data
	snap.storageState = StorageMemory
	g.updateMemoryTracking(int64(len(data)))
	g.touchSnapshot(snap)
	g.updateWarmVerification(nodeID)
}

// adoptLeafContent replaces the block's content IN PLACE with the
// file's bytes (same byte count, so no offsets move; rune/line
// aggregates are recomputed). The adopted bytes match the disk at the
// block's recorded offset, so it remains eligible for warm storage and
// an unmodified save skips it entirely - preserving the external edit.
func (g *Garland) adoptLeafContent(nodeID NodeID, snap *NodeSnapshot, data []byte, dataHash []byte) {
	ns := createLeafSnapshot(data, snap.decorations, snap.originalFileOffset)
	ns.storageState = StorageMemory
	ns.dataHash = dataHash
	*snap = *ns

	g.fixCurrentAggregates()
	g.reconcileCursorCoordinates()
	g.updateMemoryTracking(int64(len(data)))
	g.touchSnapshot(snap)
	g.updateWarmVerification(nodeID)
}
