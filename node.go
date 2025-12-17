package garland

import (
	"crypto/sha256"
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
	data            []byte
	decorations     []Decoration
	storageState    StorageState
	dataHash        []byte // SHA-256 hash for verification
	decorationHash  []byte // SHA-256 hash for decoration verification

	// originalFileOffset is the byte offset in the original file where this
	// content came from. -1 if not from the original file (not eligible for warm storage).
	originalFileOffset int64

	// Weights (aggregated for internal nodes, direct for leaf nodes)
	byteCount int64
	runeCount int64
	lineCount int64 // number of newlines

	// lineStarts contains the starting positions of each line within this leaf.
	// Only populated for leaf nodes.
	lineStarts []LineStart
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
// It searches backwards through revisions if an exact match isn't found,
// and follows parent forks as needed.
func (n *Node) snapshotAtWithKey(fork ForkID, rev RevisionID) (*NodeSnapshot, ForkRevision) {
	// Try exact match first
	key := ForkRevision{fork, rev}
	if snap, ok := n.history[key]; ok {
		return snap, key
	}

	// Walk back through revisions in this fork
	for r := rev; r > 0; r-- {
		key = ForkRevision{fork, r - 1}
		if snap, ok := n.history[key]; ok {
			return snap, key
		}
	}

	// Check revision 0
	key = ForkRevision{fork, 0}
	if snap, ok := n.history[key]; ok {
		return snap, key
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
	}

	// Calculate weights
	snap.byteCount = int64(len(data))
	snap.runeCount = int64(utf8.RuneCount(data))

	// Count newlines and build line starts index
	snap.lineStarts = make([]LineStart, 0)
	snap.lineStarts = append(snap.lineStarts, LineStart{ByteOffset: 0, RuneOffset: 0})

	var runeOffset int64 = 0
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == '\n' {
			snap.lineCount++
			// Next line starts after this newline
			if i+size < len(data) {
				snap.lineStarts = append(snap.lineStarts, LineStart{
					ByteOffset: int64(i + size),
					RuneOffset: runeOffset + 1,
				})
			}
		}
		i += size
		runeOffset++
	}

	// Compute hash
	snap.dataHash = computeHash(data)
	if len(decorations) > 0 {
		snap.decorationHash = computeDecorationHash(decorations)
	}

	return snap
}

// createInternalSnapshot creates a new internal (non-leaf) snapshot.
func createInternalSnapshot(leftID, rightID NodeID, leftSnap, rightSnap *NodeSnapshot) *NodeSnapshot {
	return &NodeSnapshot{
		isLeaf:    false,
		leftID:    leftID,
		rightID:   rightID,
		byteCount: leftSnap.byteCount + rightSnap.byteCount,
		runeCount: leftSnap.runeCount + rightSnap.runeCount,
		lineCount: leftSnap.lineCount + rightSnap.lineCount,
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

// computeDecorationHash computes a hash of decoration data.
func computeDecorationHash(decorations []Decoration) []byte {
	// Simple hash: concatenate key and position bytes
	var data []byte
	for _, d := range decorations {
		data = append(data, []byte(d.Key)...)
		data = append(data, byte(d.Position>>56), byte(d.Position>>48),
			byte(d.Position>>40), byte(d.Position>>32),
			byte(d.Position>>24), byte(d.Position>>16),
			byte(d.Position>>8), byte(d.Position))
	}
	return computeHash(data)
}

// partitionDecorations splits decorations at a given byte position.
// Decorations before pos go to left, decorations at or after pos go to right.
// Right decorations have their positions adjusted by -pos.
func partitionDecorations(decorations []Decoration, pos int64) (left, right []Decoration) {
	for _, d := range decorations {
		if d.Position < pos {
			left = append(left, d)
		} else {
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
}

// RevisionInfo contains metadata about a revision for undo history display.
type RevisionInfo struct {
	Revision         RevisionID
	Name             string // from TransactionStart
	HasChanges       bool   // true if actual mutations occurred
	RootID           NodeID // root node ID at this revision (for UndoSeek)
	StreamKnownBytes int64  // bytes of streaming content known when revision was created (-1 if complete)
}
