package garland

import (
	"bytes"
	"crypto/sha256"
	"time"
	"unicode/utf8"
)

// NodeID uniquely identifies a node within a Garland.
type NodeID uint64

// ForkID uniquely identifies a fork within a Garland.
type ForkID uint64

// RevisionID identifies a revision within a fork.
type RevisionID uint64

// ForkRevision is a composite key for looking up versioned state.
type ForkRevision struct {
	Fork     ForkID
	Revision RevisionID
}

// StorageState indicates where a node's data is currently stored.
type StorageState int

const (
	// StorageMemory indicates data is in memory.
	StorageMemory StorageState = iota

	// StorageWarm indicates data is in the original file (warm storage).
	StorageWarm

	// StorageCold indicates data is in cold storage.
	StorageCold

	// StoragePlaceholder indicates data was lost due to storage failure.
	StoragePlaceholder
)

// Node is a versioned container in the rope structure.
// Each node maintains a history of its state indexed by (ForkID, RevisionID).
type Node struct {
	id   NodeID
	file *Garland // back-reference to the owning Garland

	// history maps (fork, revision) to the node's state at that version.
	// Snapshots are immutable once created.
	history map[ForkRevision]*NodeSnapshot
}

// NodeSnapshot represents the immutable state of a node at a specific version.
type NodeSnapshot struct {
	// isLeaf indicates whether this is a leaf node (true) or internal node (false).
	isLeaf bool

	// For internal nodes: child references
	leftID  NodeID
	rightID NodeID

	// For leaf nodes: data and decorations
	data           []byte
	decorations    []Decoration
	storageState   StorageState
	dataHash       []byte // SHA-256 hash for verification
	decorationHash []byte // SHA-256 hash for decoration verification

	// placeholderReason records WHY this snapshot became a placeholder,
	// captured at the moment the loss is discovered (cold-storage read
	// failure, hash mismatch, source file changed on disk, ...). It is
	// carried through to the ScarWarning when the block is scarred
	// during a save. Empty unless storageState is StoragePlaceholder.
	placeholderReason string

	// originalFileOffset is the byte offset in the original file where this
	// content came from. -1 if not from the original file (not eligible for warm storage).
	originalFileOffset int64

	// Weights (aggregated for internal nodes, direct for leaf nodes)
	byteCount int64
	runeCount int64
	lineCount int64 // number of newlines

	// runesAfterLastNewline is the number of runes after the last newline in this subtree.
	// For a leaf with no newlines, this equals runeCount.
	// For a leaf ending with a newline, this is 0.
	// For internal nodes, this is derived from children.
	runesAfterLastNewline int64

	// lineStarts contains the starting positions of each line within this leaf.
	// Only populated for leaf nodes.
	lineStarts []LineStart

	// lastAccessTime tracks when this snapshot's data was last accessed.
	// Used for LRU-based memory management. Zero value means never accessed.
	lastAccessTime time.Time
}

// becomePlaceholder marks the snapshot's data as lost, recording why.
// The reason is kept from this moment of discovery until it is reported
// back to the app (in a ScarWarning) when the block is scarred on save.
// The first recorded reason wins: a placeholder re-touched by a later
// failed access keeps the original cause.
func (snap *NodeSnapshot) becomePlaceholder(reason string) {
	if snap.storageState != StoragePlaceholder || snap.placeholderReason == "" {
		snap.placeholderReason = reason
	}
	snap.storageState = StoragePlaceholder
}

// newNode creates a new node with the given ID and Garland reference.
func newNode(id NodeID, g *Garland) *Node {
	return &Node{
		id:      id,
		file:    g,
		history: make(map[ForkRevision]*NodeSnapshot),
	}
}

// ID returns the node's unique identifier.
func (n *Node) ID() NodeID {
	return n.id
}

// snapshotAt returns the node's snapshot at the given fork and revision.
// It searches backwards through revisions if an exact match isn't found,
// and follows parent forks as needed.
func (n *Node) snapshotAt(fork ForkID, rev RevisionID) *NodeSnapshot {
	snap, _ := n.snapshotAtWithKey(fork, rev)
	return snap
}

// snapshotAtWithKey returns the node's snapshot and its actual ForkRevision key.
// It finds the highest revision ≤ target for the given fork, and follows parent forks as needed.
// Optimized to be O(len(history)) instead of O(revisions).
func (n *Node) snapshotAtWithKey(fork ForkID, rev RevisionID) (*NodeSnapshot, ForkRevision) {
	// Try exact match first
	key := ForkRevision{fork, rev}
	if snap, ok := n.history[key]; ok {
		return snap, key
	}

	// Find the highest revision ≤ rev for this fork by iterating through actual history entries
	// This is O(len(history)) which is typically very small (1-2 entries per node)
	var bestSnap *NodeSnapshot
	var bestKey ForkRevision
	bestRev := RevisionID(0)
	found := false

	for k, snap := range n.history {
		if k.Fork == fork && k.Revision <= rev {
			if !found || k.Revision > bestRev {
				bestSnap = snap
				bestKey = k
				bestRev = k.Revision
				found = true
			}
		}
	}

	if found {
		return bestSnap, bestKey
	}

	// If fork has a parent, try parent fork
	if n.file != nil {
		if forkInfo, ok := n.file.forks[fork]; ok && forkInfo.ParentFork != fork {
			return n.snapshotAtWithKey(forkInfo.ParentFork, forkInfo.ParentRevision)
		}
	}

	return nil, ForkRevision{} // node didn't exist at this version
}

// setSnapshot sets the node's snapshot for the given fork and revision.
func (n *Node) setSnapshot(fork ForkID, rev RevisionID, snap *NodeSnapshot) {
	n.history[ForkRevision{fork, rev}] = snap
}

// createLeafSnapshot creates a new leaf snapshot with the given data.
func createLeafSnapshot(data []byte, decorations []Decoration, originalOffset int64) *NodeSnapshot {
	snap := &NodeSnapshot{
		isLeaf:             true,
		data:               data,
		decorations:        decorations,
		storageState:       StorageMemory,
		originalFileOffset: originalOffset,
		lastAccessTime:     time.Now(), // Initialize access time for LRU tracking
	}

	// Calculate weights
	snap.byteCount = int64(len(data))
	snap.runeCount = int64(utf8.RuneCount(data))

	// Count newlines and build line starts index. Hops newline to
	// newline with IndexByte instead of decoding every rune - this runs
	// on every leaf rebuild, i.e. on every keystroke.
	snap.lineStarts = make([]LineStart, 0)
	snap.lineStarts = append(snap.lineStarts, LineStart{ByteOffset: 0, RuneOffset: 0})

	var runeOffset int64
	prev := 0
	for {
		i := bytes.IndexByte(data[prev:], '\n')
		if i < 0 {
			break
		}
		nl := prev + i
		snap.lineCount++
		runeOffset += int64(utf8.RuneCount(data[prev : nl+1]))
		if nl+1 < len(data) {
			snap.lineStarts = append(snap.lineStarts, LineStart{
				ByteOffset: int64(nl + 1),
				RuneOffset: runeOffset,
			})
		}
		prev = nl + 1
	}

	// Calculate runes after last newline from lineStarts
	if snap.lineCount == 0 {
		// No newlines - all runes are on line 0
		snap.runesAfterLastNewline = snap.runeCount
	} else if int(snap.lineCount) < len(snap.lineStarts) {
		// There's content after the last newline
		snap.runesAfterLastNewline = snap.runeCount - snap.lineStarts[snap.lineCount].RuneOffset
	} else {
		// Leaf ends with newline
		snap.runesAfterLastNewline = 0
	}

	// Hashes are computed LAZILY, at chill time (chillSnapshot /
	// chillToWarmStorage fill them in before data leaves memory), for
	// two reasons:
	//   - SHA-256 over up to 128KB on every leaf rebuild was the
	//     dominant per-keystroke cost, paid even though most leaves
	//     are superseded without ever being chilled.
	//   - An eagerly computed decorationHash used a DIFFERENT encoding
	//     (computeDecorationHash) than what cold storage writes and
	//     thaw verifies (encodeDecorations), so any chilled leaf with
	//     decorations failed verification on thaw and silently dropped
	//     its marks. With one writer - the chill path - the hash always
	//     matches the stored encoding.

	return snap
}

// createInternalSnapshot creates a new internal (non-leaf) snapshot.
func createInternalSnapshot(leftID, rightID NodeID, leftSnap, rightSnap *NodeSnapshot) *NodeSnapshot {
	// Calculate runesAfterLastNewline:
	// - If right has newlines, the last line is entirely in right
	// - If right has no newlines, the last line spans from left into right
	var runesAfterLastNewline int64
	if rightSnap.lineCount > 0 {
		runesAfterLastNewline = rightSnap.runesAfterLastNewline
	} else {
		runesAfterLastNewline = leftSnap.runesAfterLastNewline + rightSnap.runeCount
	}

	return &NodeSnapshot{
		isLeaf:                false,
		leftID:                leftID,
		rightID:               rightID,
		byteCount:             leftSnap.byteCount + rightSnap.byteCount,
		runeCount:             leftSnap.runeCount + rightSnap.runeCount,
		lineCount:             leftSnap.lineCount + rightSnap.lineCount,
		runesAfterLastNewline: runesAfterLastNewline,
	}
}

// IsLeaf returns true if this snapshot represents a leaf node.
func (s *NodeSnapshot) IsLeaf() bool {
	return s.isLeaf
}

// ByteCount returns the total bytes in this subtree.
func (s *NodeSnapshot) ByteCount() int64 {
	return s.byteCount
}

// RuneCount returns the total runes in this subtree.
func (s *NodeSnapshot) RuneCount() int64 {
	return s.runeCount
}

// LineCount returns the total newlines in this subtree.
func (s *NodeSnapshot) LineCount() int64 {
	return s.lineCount
}

// Data returns the leaf node's data. Returns nil for internal nodes.
func (s *NodeSnapshot) Data() []byte {
	return s.data
}

// Decorations returns the leaf node's decorations. Returns nil for internal nodes.
func (s *NodeSnapshot) Decorations() []Decoration {
	return s.decorations
}

// LeftID returns the left child's ID. Only valid for internal nodes.
func (s *NodeSnapshot) LeftID() NodeID {
	return s.leftID
}

// RightID returns the right child's ID. Only valid for internal nodes.
func (s *NodeSnapshot) RightID() NodeID {
	return s.rightID
}

// computeHash computes a SHA-256 hash of the given data.
func computeHash(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

// partitionDecorations splits decorations at a given position.
// When insertBefore=true: decorations at pos go to right (will be shifted)
// When insertBefore=false: decorations at pos go to left (stay in place)
// Right decorations have their positions adjusted by -pos.
// partitionDecorations splits a leaf's decorations around an insert at
// pos. Marks strictly before pos stay in the left piece; marks
// strictly after go to the right piece (rebased). A mark EXACTLY at
// pos is governed by insertBefore: true slides it past the inserted
// content (right piece), false keeps it at its absolute address, which
// is the FIRST BYTE OF THE INSERTED CONTENT - returned in boundary so
// the caller homes it at offset 0 of the middle (inserted) leaf.
// Storage invariant: a mark never lives at a leaf's end offset (only
// an EOF mark on the final leaf may), so the left piece never receives
// boundary marks.
func partitionDecorations(decorations []Decoration, pos int64, insertBefore bool) (left, boundary, right []Decoration) {
	for _, d := range decorations {
		switch {
		case d.Position < pos:
			left = append(left, d)
		case d.Position == pos && !insertBefore:
			boundary = append(boundary, Decoration{Key: d.Key, Position: 0})
		default:
			right = append(right, Decoration{
				Key:      d.Key,
				Position: d.Position - pos,
			})
		}
	}
	return
}

// ForkInfo contains metadata about a fork.
type ForkInfo struct {
	ID              ForkID
	ParentFork      ForkID
	ParentRevision  RevisionID // revision at which this fork split from parent
	HighestRevision RevisionID
	PrunedUpTo      RevisionID // revisions < this have been pruned from this fork's view
	Deleted         bool       // true if fork is soft-deleted (data may still exist for child forks)
}

// RevisionInfo contains metadata about a revision for undo history display.
type RevisionInfo struct {
	Revision         RevisionID
	Name             string // from TransactionStart
	HasChanges       bool   // true if actual mutations occurred
	RootID           NodeID // root node ID at this revision (for UndoSeek)
	StreamKnownBytes int64  // bytes of streaming content known when revision was created (-1 if complete)
}
