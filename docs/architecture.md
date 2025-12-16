# Garland Architecture

This document describes the internal architecture and data structures of the Garland library.

## Overview

Garland is a rope-based data structure optimized for:
- Large file editing with minimal memory usage
- Multiple addressing modes (bytes, runes, lines)
- Full version history with branching (forks)
- Decorations (annotations) that track positions through edits
- Lazy loading with configurable thresholds

## Core Concepts

### Rope Structure

A rope is a binary tree where:
- **Leaf nodes** hold actual data (byte arrays)
- **Internal nodes** hold references to two children and aggregate weights

This allows O(log n) insert/delete operations instead of O(n) for arrays.

### Three-Weight Tracking

Every node tracks three metrics:
- **Byte count**: Total bytes in subtree
- **Rune count**: Total Unicode code points in subtree
- **Line count**: Total newline characters in subtree

This enables efficient seeking by any addressing mode.

### Versioned Nodes

Nodes are versioned containers. Each node maintains a history of its state indexed by (ForkID, RevisionID). When viewing the structure at a particular version, each node lookup retrieves the appropriate snapshot.

```
Node {
    history: map[(Fork, Revision)] → NodeSnapshot
}

NodeSnapshot {
    // For internal nodes:
    left, right: NodeID

    // For leaf nodes:
    data, decorations, storage state

    // Shared:
    byteCount, runeCount, lineCount
}
```

### Storage Tiers

Data can exist in three tiers:

1. **Memory**: Fast access, highest memory cost
2. **Warm Storage**: Original file on disk, verified by checksum
3. **Cold Storage**: Library-managed external storage

Transitions between tiers are implementation details and don't constitute mutations (no version increment).

---

## Data Structures

### Library Level

```go
type Library struct {
    coldStoragePath    string
    coldStorageBackend ColdStorageInterface

    // Default file system implementation
    defaultFS FileSystemInterface

    // Active garlands (for potential cross-file operations)
    activeGarlands map[string]*Garland
}
```

### Garland (File Level)

```go
type Garland struct {
    lib *Library

    // Identity
    id           string  // unique folder name for cold storage
    sourcePath   string  // original file path, if any

    // Configuration
    loadingStyle    LoadingStyle
    readyThreshold  ReadyThreshold
    readAheadConfig ReadAheadConfig

    // Tree structure
    root         *Node
    nodeRegistry map[NodeID]*Node  // all nodes for this garland
    nextNodeID   NodeID

    // Versioning
    currentFork     ForkID
    currentRevision RevisionID
    forks           map[ForkID]*ForkInfo
    nextForkID      ForkID

    // Cursors
    cursors []*Cursor

    // Decoration cache (hints only, not authoritative)
    decorationCache map[string]*DecorationCacheEntry

    // Loading state
    loader        *Loader  // background loading goroutine state
    highestSeekPos int64   // furthest position any cursor has seeked

    // Counts (may be incomplete during loading)
    totalBytes    int64
    totalRunes    int64
    totalLines    int64
    countComplete bool  // true once EOF reached

    // File system for warm storage
    sourceFS     FileSystemInterface
    sourceHandle FileHandle

    // Optimized regions
    optimizedRegions []*OptimizedRegionHandle

    // Transaction state
    transaction *TransactionState
}
```

### Node

```go
type NodeID uint64

type Node struct {
    id   NodeID
    file *Garland  // back-reference

    // Version history: (fork, revision) → snapshot
    history map[ForkRevision]*NodeSnapshot
}

type ForkRevision struct {
    Fork     ForkID
    Revision RevisionID
}

type NodeSnapshot struct {
    // Type
    isLeaf bool

    // For internal nodes
    leftID  NodeID
    rightID NodeID

    // For leaf nodes
    data            []byte
    decorations     []Decoration
    storageState    StorageState
    dataHash        []byte  // for verification
    decorationHash  []byte

    // For warm storage eligibility (leaf only)
    originalFileOffset int64  // -1 if not from original file

    // Weights (aggregated for internal, direct for leaf)
    byteCount int64
    runeCount int64
    lineCount int64  // number of newlines

    // Line index within this leaf (for efficient line seeking)
    lineStarts []LineStart
}

type StorageState int

const (
    StorageMemory StorageState = iota
    StorageWarm
    StorageCold
    StoragePlaceholder  // cold storage failed, read-only placeholder
)

type LineStart struct {
    ByteOffset int64
    RuneOffset int64
}

type Decoration struct {
    Key      string
    Position int64  // relative byte offset within this node
}
```

### Cursor

```go
type Cursor struct {
    garland *Garland

    // Current position (always kept in sync)
    bytePos  int64
    runePos  int64
    line     int64
    lineRune int64

    // Version tracking for cursor history
    lastFork     ForkID
    lastRevision RevisionID

    // Cursor's own position history (sparse, by version)
    positionHistory map[ForkRevision]*CursorPosition

    // Ready state
    ready     bool
    readyChan chan struct{}  // closed when ready
}

type CursorPosition struct {
    BytePos  int64
    RunePos  int64
    Line     int64
    LineRune int64
}
```

### Fork Management

```go
type ForkInfo struct {
    ID              ForkID
    ParentFork      ForkID
    ParentRevision  RevisionID  // revision at which this fork split
    HighestRevision RevisionID
}
```

### Decoration Cache

```go
type DecorationCacheEntry struct {
    NodeID         NodeID
    RelativeOffset int64
    // Note: Must verify on EVERY lookup, this is just a hint
}
```

### Transaction State

```go
type TransactionState struct {
    // Nesting depth (1 = single transaction, 2+ = nested)
    depth int

    // Name for this transaction (from outermost TransactionStart)
    name string

    // Whether any inner transaction has rolled back
    poisoned bool

    // Snapshot of tree state before transaction started
    // Used for rollback
    preTransactionRoot   NodeID
    preTransactionFork   ForkID
    preTransactionRev    RevisionID

    // Cursor positions at transaction start (for rollback)
    preTransactionCursors map[*Cursor]*CursorPosition

    // Pending revision number (assigned at TransactionStart)
    pendingRevision RevisionID

    // Whether any mutation has occurred in this transaction
    hasMutations bool
}
```

### Revision Info

```go
// Stored per revision for undo history display
type RevisionInfo struct {
    Revision   RevisionID
    Name       string  // from TransactionStart
    HasChanges bool    // true if actual mutations occurred
}

// Stored in Garland
type Garland struct {
    // ... other fields ...

    // Revision metadata indexed by (fork, revision)
    revisionInfo map[ForkRevision]*RevisionInfo
}
```

### Loader (Background Loading)

```go
type Loader struct {
    garland *Garland

    // Source
    source     io.Reader  // or channel
    sourceType SourceType

    // Progress
    bytesLoaded int64
    runesLoaded int64
    linesLoaded int64
    eofReached  bool

    // Synchronization
    mu       sync.Mutex
    cond     *sync.Cond
    stopChan chan struct{}
}
```

---

## Key Algorithms

### Node Lookup by Version

When accessing the tree at a specific (fork, revision):

```
func (n *Node) snapshotAt(fork ForkID, rev RevisionID) *NodeSnapshot {
    // Try exact match first
    if snap, ok := n.history[{fork, rev}]; ok {
        return snap
    }

    // Walk back through revisions in this fork
    for r := rev; r >= 0; r-- {
        if snap, ok := n.history[{fork, r}]; ok {
            return snap
        }
    }

    // If fork has a parent, try parent fork
    forkInfo := n.file.forks[fork]
    if forkInfo.ParentFork != fork {
        return n.snapshotAt(forkInfo.ParentFork, forkInfo.ParentRevision)
    }

    return nil  // node didn't exist at this version
}
```

### Seeking by Address Mode

**By Byte Position:**
```
1. Start at root
2. Get left child's byteCount
3. If pos < leftBytes: recurse left
4. Else: recurse right with pos = pos - leftBytes
5. At leaf: return leaf and offset within leaf
```

**By Rune Position:**
```
1. Start at root
2. Get left child's runeCount
3. If pos < leftRunes: recurse left
4. Else: recurse right with pos = pos - leftRunes
5. At leaf: scan through data to find byte offset of rune
```

**By Line:Rune Position:**
```
1. Start at root
2. Get left child's lineCount
3. If line < leftLines: recurse left
4. Else: recurse right with line = line - leftLines
5. At leaf: use lineStarts index to find byte offset
```

### Split Operation

Split a leaf node at byte position `pos`:

```
func split(leaf *Node, pos int64, fork ForkID, rev RevisionID) (*Node, *Node) {
    snap := leaf.snapshotAt(fork, rev)

    leftData := snap.data[:pos]
    rightData := snap.data[pos:]

    // Partition decorations
    leftDecorations, rightDecorations := partitionDecorations(snap.decorations, pos)
    // Adjust right decoration positions by -pos

    // Create new nodes (or reuse if data unchanged)
    leftNode := createLeafNode(leftData, leftDecorations)
    rightNode := createLeafNode(rightData, rightDecorations)

    // Recompute weights and line indices

    return leftNode, rightNode
}
```

### Concatenate Operation

Join two nodes into a new internal node:

```
func concatenate(left, right *Node, fork ForkID, rev RevisionID) *Node {
    newNode := &Node{
        id:   nextNodeID(),
        file: left.file,
    }

    leftSnap := left.snapshotAt(fork, rev)
    rightSnap := right.snapshotAt(fork, rev)

    newNode.history[{fork, rev}] = &NodeSnapshot{
        isLeaf:    false,
        leftID:    left.id,
        rightID:   right.id,
        byteCount: leftSnap.byteCount + rightSnap.byteCount,
        runeCount: leftSnap.runeCount + rightSnap.runeCount,
        lineCount: leftSnap.lineCount + rightSnap.lineCount,
    }

    return newNode
}
```

### Insert Operation

```
func insert(root *Node, pos int64, data []byte, fork ForkID, rev RevisionID) *Node {
    // Find leaf containing position
    leaf, offset := findLeaf(root, pos, fork, rev)

    // Split leaf at insertion point
    left, right := split(leaf, offset, fork, rev)

    // Create new leaf with inserted data
    middle := createLeafNode(data, decorations)

    // Rebuild tree with new structure
    return rebuildTree(left, middle, right, fork, rev)
}
```

### Delete Operation

```
func delete(root *Node, start, end int64, fork ForkID, rev RevisionID) (*Node, []Decoration) {
    // Find leaves containing start and end
    startLeaf, startOffset := findLeaf(root, start, fork, rev)
    endLeaf, endOffset := findLeaf(root, end, fork, rev)

    // Collect decorations from deleted range
    deletedDecorations := collectDecorations(startLeaf, endLeaf, startOffset, endOffset)

    // Split at boundaries
    beforeStart, afterStart := split(startLeaf, startOffset, fork, rev)
    beforeEnd, afterEnd := split(endLeaf, endOffset, fork, rev)

    // Rebuild tree excluding deleted middle
    return rebuildTree(beforeStart, afterEnd, fork, rev), deletedDecorations
}
```

### Decoration Search (Cache Miss)

```
func findDecoration(g *Garland, key string) (*Node, int64, error) {
    // Check cache for hint
    if entry, ok := g.decorationCache[key]; ok {
        node := g.nodeRegistry[entry.NodeID]
        if verifyDecoration(node, key, entry.RelativeOffset) {
            return node, entry.RelativeOffset, nil
        }
    }

    // Cache miss: search outward from hint (or from middle if no hint)
    startNode := determineSearchStart(g, key)

    // Alternating search left and right
    leftCursor, rightCursor := startNode, startNode
    for leftCursor != nil || rightCursor != nil {
        if rightCursor != nil {
            if found, offset := searchInNode(rightCursor, key); found {
                updateCache(g, key, rightCursor, offset)
                return rightCursor, offset, nil
            }
            rightCursor = nextLeafRight(rightCursor)
        }
        if leftCursor != nil {
            if found, offset := searchInNode(leftCursor, key); found {
                updateCache(g, key, leftCursor, offset)
                return leftCursor, offset, nil
            }
            leftCursor = nextLeafLeft(leftCursor)
        }
    }

    return nil, 0, ErrDecorationNotFound
}
```

### Cursor Update on Mutation

When content changes, update all cursors:

```
func updateCursors(g *Garland, mutationPos int64, delta int64, fork ForkID, rev RevisionID) {
    for _, cursor := range g.cursors {
        if cursor.bytePos > mutationPos {
            cursor.bytePos += delta
            // Rune and line positions also need updating
            // This requires traversing to recalculate
            recalculateCursorPosition(cursor)
        }
        // Record new position in cursor history if version changed
        if cursor.lastFork != fork || cursor.lastRevision != rev {
            cursor.positionHistory[{fork, rev}] = &CursorPosition{
                BytePos:  cursor.bytePos,
                RunePos:  cursor.runePos,
                Line:     cursor.line,
                LineRune: cursor.lineRune,
            }
            cursor.lastFork = fork
            cursor.lastRevision = rev
        }
    }
}
```

### Transaction Management

Transactions batch multiple operations into a single revision.

**TransactionStart:**
```
func (g *Garland) TransactionStart(name string) error {
    if g.transaction == nil {
        // First level: create new transaction state
        // Always assign pending revision upfront (even for empty transactions)
        g.transaction = &TransactionState{
            depth:                 1,
            name:                  name,
            poisoned:              false,
            preTransactionRoot:    g.root.id,
            preTransactionFork:    g.currentFork,
            preTransactionRev:     g.currentRevision,
            preTransactionCursors: snapshotCursorPositions(g.cursors),
            pendingRevision:       g.currentRevision + 1,
            hasMutations:          false,
        }
    } else {
        // Nested: just increment depth (name ignored for inner transactions)
        g.transaction.depth++
    }
    return nil
}
```

**Mutation during transaction:**
```
func (g *Garland) mutate(...) {
    if g.transaction != nil {
        g.transaction.hasMutations = true
    }

    // Use pending revision for all changes during transaction
    rev := g.currentRevision
    if g.transaction != nil {
        rev = g.transaction.pendingRevision
    }

    // Perform mutation at this revision
    // (don't increment g.currentRevision yet)
}
```

**TransactionCommit:**
```
func (g *Garland) TransactionCommit() (ChangeResult, error) {
    if g.transaction == nil {
        return ChangeResult{}, ErrNoTransaction
    }

    g.transaction.depth--

    if g.transaction.depth > 0 {
        // Inner commit: just decrement, don't finalize
        return ChangeResult{Fork: g.currentFork, Revision: g.currentRevision}, nil
    }

    // Outermost commit
    if g.transaction.poisoned {
        // Poisoned: rollback instead (no revision created)
        rollbackToPreTransaction(g)
        g.transaction = nil
        return ChangeResult{}, ErrTransactionPoisoned
    }

    // ALWAYS create a new revision, even if no mutations
    // This allows external state to sync with garland revisions
    g.currentRevision = g.transaction.pendingRevision

    // Store revision info for undo history
    g.revisionInfo[ForkRevision{g.currentFork, g.currentRevision}] = &RevisionInfo{
        Revision:   g.currentRevision,
        Name:       g.transaction.name,
        HasChanges: g.transaction.hasMutations,
    }

    result := ChangeResult{
        Fork:     g.currentFork,
        Revision: g.currentRevision,
    }
    g.transaction = nil
    return result, nil
}
```

**TransactionRollback:**
```
func (g *Garland) TransactionRollback() error {
    if g.transaction == nil {
        return ErrNoTransaction
    }

    // Poison the transaction
    g.transaction.poisoned = true
    g.transaction.depth--

    if g.transaction.depth == 0 {
        // Outermost level: perform actual rollback
        rollbackToPreTransaction(g)
        g.transaction = nil
    }
    // Inner level: poison flag will cause outer commit to rollback

    return nil
}

func rollbackToPreTransaction(g *Garland) {
    // Restore tree state
    g.root = g.nodeRegistry[g.transaction.preTransactionRoot]
    g.currentFork = g.transaction.preTransactionFork
    g.currentRevision = g.transaction.preTransactionRev

    // Restore cursor positions
    for cursor, pos := range g.transaction.preTransactionCursors {
        cursor.bytePos = pos.BytePos
        cursor.runePos = pos.RunePos
        cursor.line = pos.Line
        cursor.lineRune = pos.LineRune
    }

    // Discard any nodes created during transaction
    // (They have history entries at pendingRevision that will be orphaned)
}
```

**UndoSeek/ForkSeek blocking:**
```
func (g *Garland) UndoSeek(revision RevisionID) error {
    if g.transaction != nil {
        return ErrTransactionPending
    }
    // ... normal UndoSeek logic
}

func (g *Garland) ForkSeek(fork ForkID) error {
    if g.transaction != nil {
        return ErrTransactionPending
    }
    // ... normal ForkSeek logic
}
```

---

## Storage Transitions

### Memory → Warm Storage

Only possible if:
1. Loading style allows warm storage
2. Leaf represents unmodified original file content
3. Original file hasn't changed (checksum verification)

```
func transitionToWarm(leaf *Node) error {
    if leaf.originalFileOffset < 0 {
        return ErrNotFromOriginalFile
    }

    // Verify checksum against original file
    currentHash := computeHash(readFromOriginalFile(leaf.originalFileOffset, len(leaf.data)))
    if !bytes.Equal(currentHash, leaf.dataHash) {
        return ErrWarmStorageMismatch  // must use cold storage instead
    }

    // Discard in-memory data, keep metadata
    leaf.data = nil
    leaf.storageState = StorageWarm
    return nil
}
```

### Memory → Cold Storage

```
func transitionToCold(lib *Library, leaf *Node) error {
    // Generate block name
    blockName := fmt.Sprintf("data_%d", leaf.id)

    // Store to cold storage
    if err := lib.coldStorageBackend.Set(leaf.file.id, blockName, leaf.data); err != nil {
        return err
    }

    // Discard in-memory data
    leaf.data = nil
    leaf.storageState = StorageCold
    return nil
}
```

### Warm/Cold → Memory

```
func transitionToMemory(leaf *Node) error {
    switch leaf.storageState {
    case StorageWarm:
        data := readFromOriginalFile(leaf.originalFileOffset, leaf.byteCount)
        if !bytes.Equal(computeHash(data), leaf.dataHash) {
            // Original file changed! Transition to placeholder
            return transitionToPlaceholder(leaf)
        }
        leaf.data = data

    case StorageCold:
        blockName := fmt.Sprintf("data_%d", leaf.id)
        data, err := lib.coldStorageBackend.Get(leaf.file.id, blockName)
        if err != nil {
            return transitionToPlaceholder(leaf)
        }
        if !bytes.Equal(computeHash(data), leaf.dataHash) {
            return transitionToPlaceholder(leaf)
        }
        leaf.data = data
    }

    leaf.storageState = StorageMemory
    return nil
}
```

### Placeholder (Storage Failure)

When cold storage becomes unavailable:

```
func transitionToPlaceholder(leaf *Node) error {
    // Create placeholder message
    var msg string
    if leaf.originalFileOffset >= 0 {
        msg = fmt.Sprintf("[Missing %d bytes from original file address %d]",
            leaf.byteCount, leaf.originalFileOffset)
    } else {
        msg = fmt.Sprintf("[Missing %d bytes from buffer fragment %d]",
            leaf.byteCount, leaf.id)
    }

    leaf.data = []byte(msg)
    leaf.byteCount = int64(len(msg))
    leaf.runeCount = int64(utf8.RuneCount(leaf.data))
    leaf.lineCount = 0
    leaf.storageState = StoragePlaceholder
    // Never swap out again

    return nil
}
```

---

## Concurrency Model

(To be designed in detail)

Initial thoughts:
- Background loader runs in separate goroutine
- Cursor operations that require unavailable data block on condition variable
- Mutations acquire exclusive lock
- Reads can be concurrent with other reads
- Storage tier transitions are protected by per-node locks

---

## Optimized Regions

Optimized regions bypass normal tree operations for a contiguous range:

```
type OptimizedRegionNode struct {
    // Appears as a single leaf in the tree
    startByte int64
    region    OptimizedRegion

    // Cached counts (updated by region)
    byteCount int64
    runeCount int64
    lineCount int64
}
```

When an operation touches an optimized region:
1. Garland detects overlap with active regions
2. Operation is forwarded to region's interface
3. Region updates its internal state
4. Garland queries region for new counts

Before UndoSeek:
1. Garland calls `CommitSnapshot()` on all active regions
2. Regions record their current state as a revision
3. UndoSeek proceeds normally

---

## Error Handling

### Storage Failures

When cold storage operations fail:
1. Generate `ErrColdStorageFailure` status for application
2. Convert affected nodes to placeholders
3. Mark those regions as read-only
4. Continue operation with reduced functionality

### Warm Storage Mismatch

When original file has changed:
1. Generate `ErrWarmStorageMismatch` status for application
2. If cold storage available: transition to cold storage
3. If not: transition to placeholder

### Position Errors

When seeking to unavailable positions:
1. Block until position becomes available (during loading)
2. Return `ErrNotReady` if timeout exceeded
3. Return `ErrInvalidPosition` if position is beyond EOF

---

## Memory Management

### Node Eviction

Nodes track:
- Last access time
- Access frequency

Background process periodically:
1. Identifies least-recently-used leaves
2. Transitions them to warm or cold storage
3. Keeps total memory usage under threshold

### Version History Pruning

Configurable limits:
- Max revisions per fork
- Max total forks

Pruning process:
1. Traverse node registry
2. Remove history entries for pruned versions
3. Compact remaining history
