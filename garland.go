package garland

import (
	"sync"
	"time"
)

// LoadingStyle determines which storage tiers are available.
type LoadingStyle int

const (
	// AllStorage allows memory, warm (original file), and cold storage.
	// Warm storage requires the original file to be unchanged.
	AllStorage LoadingStyle = iota

	// ColdAndMemory prevents warm storage, only memory and cold.
	ColdAndMemory

	// MemoryOnly keeps everything in memory, no external storage.
	MemoryOnly
)

// ChillLevel specifies how aggressively to move data to cold storage.
type ChillLevel int

const (
	// ChillInactiveForks moves data not used by the currently active fork
	// to cold storage. This is the gentlest level.
	ChillInactiveForks ChillLevel = iota

	// ChillOldHistory also moves data from older revisions in the undo
	// buffer that are more than a few steps back and not utilized by
	// any branching point.
	ChillOldHistory

	// ChillUnusedData moves all data not used at the current undo position.
	// This keeps only what's needed to display/edit the current state.
	ChillUnusedData

	// ChillEverything moves all data to cold storage. Use when switching
	// to another document or dropping to a shell. Data will be restored
	// from cold storage on access.
	ChillEverything
)

// Default leaf size constants for tree building.
const (
	// DefaultMaxLeafSize is the maximum bytes per leaf node.
	// Files larger than this are split into multiple leaves.
	DefaultMaxLeafSize = 128 * 1024 // 128KB

	// DefaultTargetLeafSize is the ideal leaf size (MaxLeafSize / 2).
	DefaultTargetLeafSize = 64 * 1024 // 64KB

	// DefaultMinLeafSize is the minimum leaf size before merging (MaxLeafSize / 4).
	DefaultMinLeafSize = 32 * 1024 // 32KB

	// DefaultInitialUsageWindow is the default byte range to keep in memory.
	DefaultInitialUsageWindow = 1024 * 1024 // 1MB
)

// ColdStorageInterface allows custom cold storage implementations.
type ColdStorageInterface interface {
	// Set stores data for a block within a folder.
	// Folder names are unique per loaded file.
	Set(folder, block string, data []byte) error

	// Get retrieves data for a block within a folder.
	Get(folder, block string) ([]byte, error)

	// Delete removes a block from a folder.
	Delete(folder, block string) error

	// DeleteFolder removes an empty folder.
	// Returns an error if the folder is not empty.
	DeleteFolder(folder string) error
}

// LibraryOptions configures the garland library.
type LibraryOptions struct {
	// ColdStoragePath is a filesystem path for cold storage.
	// Either this or ColdStorageBackend must be provided (or both).
	ColdStoragePath string

	// ColdStorageBackend is a custom cold storage implementation.
	ColdStorageBackend ColdStorageInterface

	// Memory management options
	// MemorySoftLimit is the target memory usage in bytes.
	// When exceeded, background maintenance starts chilling LRU nodes.
	// 0 means disabled (default).
	MemorySoftLimit int64

	// MemoryHardLimit is the maximum memory usage in bytes.
	// When exceeded, immediate (but still budgeted) chilling occurs.
	// 0 means disabled (default).
	MemoryHardLimit int64

	// ChillBudgetPerTick is the maximum nodes to chill per maintenance tick.
	// Default is 5 if not specified.
	ChillBudgetPerTick int

	// RebalanceBudget is the maximum rotations per mutation operation.
	// Default is 2 if not specified.
	RebalanceBudget int

	// BackgroundInterval is how often the background maintenance worker runs.
	// 0 means disabled (maintenance only happens opportunistically).
	// Typical value: 100ms to 1s.
	BackgroundInterval time.Duration
}

// Library manages garland instances and shared resources like cold storage.
type Library struct {
	coldStoragePath    string
	coldStorageBackend ColdStorageInterface
	defaultFS          FileSystemInterface

	// Active garlands indexed by their unique ID
	activeGarlands map[string]*Garland
	mu             sync.RWMutex

	nextGarlandID uint64

	// Memory management configuration
	memorySoftLimit    int64
	memoryHardLimit    int64
	chillBudgetPerTick int
	rebalanceBudget    int
	backgroundInterval time.Duration

	// Memory pressure state - set when hard limit exceeded and can't reduce
	memoryPressure bool

	// Background maintenance worker
	maintenanceStop chan struct{}
	maintenanceWg   sync.WaitGroup
}

// Init initializes the garland library with cold storage options.
// Cold storage is shared across all files opened through this library instance.
func Init(options LibraryOptions) (*Library, error) {
	// Set defaults for maintenance configuration
	chillBudget := options.ChillBudgetPerTick
	if chillBudget <= 0 {
		chillBudget = 5 // default: chill 5 nodes per tick
	}
	rebalanceBudget := options.RebalanceBudget
	if rebalanceBudget <= 0 {
		rebalanceBudget = 2 // default: 2 rotations per operation
	}

	lib := &Library{
		coldStoragePath:    options.ColdStoragePath,
		coldStorageBackend: options.ColdStorageBackend,
		activeGarlands:     make(map[string]*Garland),
		defaultFS:          &localFileSystem{},

		// Memory management
		memorySoftLimit:    options.MemorySoftLimit,
		memoryHardLimit:    options.MemoryHardLimit,
		chillBudgetPerTick: chillBudget,
		rebalanceBudget:    rebalanceBudget,
		backgroundInterval: options.BackgroundInterval,
	}

	// If a path was provided but no backend, create a file-based backend
	if options.ColdStoragePath != "" && options.ColdStorageBackend == nil {
		lib.coldStorageBackend = newFSColdStorage(lib.defaultFS, options.ColdStoragePath)
	}

	// Start background maintenance worker if configured
	if options.BackgroundInterval > 0 {
		lib.startMaintenanceWorker()
	}

	return lib, nil
}

// ReadyThreshold specifies when a garland is considered "ready".
type ReadyThreshold struct {
	Lines int64 // number of complete lines (0 = ignore)
	Bytes int64 // number of bytes (0 = ignore)
	Runes int64 // number of runes (0 = ignore)
	All   bool  // only ready when entire file processed
}

// ReadAheadConfig specifies lazy read-ahead behavior.
type ReadAheadConfig struct {
	Lines int64 // additional lines to read ahead (0 = ignore)
	Bytes int64 // additional bytes to read ahead (0 = ignore)
	Runes int64 // additional runes to read ahead (0 = ignore)
	All   bool  // read entire file ASAP
}

// FileOptions configures how a Garland is opened.
type FileOptions struct {
	// LoadingStyle determines storage tier availability
	LoadingStyle LoadingStyle

	// Data source (exactly one must be provided)
	FilePath    string              // load from file path using default FS
	FileSystem  FileSystemInterface // custom file system (use with FilePath)
	DataBytes   []byte              // literal byte content
	DataString  string              // literal string content
	DataChannel chan []byte         // streaming input

	// Initial decorations (optional, at most one)
	Decorations      []DecorationEntry // literal list
	DecorationChan   chan DecorationEntry
	DecorationPath   string // load from dump file
	DecorationString string // parse from dump format

	// Tree structure options
	// MaxLeafSize is the maximum bytes per leaf node (default 128KB).
	// Larger files are split into a balanced tree of leaves.
	// Target leaf size is MaxLeafSize/2, minimum is MaxLeafSize/4.
	MaxLeafSize int64

	// InitialUsageStart and InitialUsageEnd define a byte range to keep in memory.
	// Nodes outside this range are immediately chilled to cold storage after loading.
	// This avoids loading a huge file fully into RAM just to chill it immediately.
	// Set InitialUsageEnd to -1 (default) to use a reasonable default window.
	// Set both to 0 to chill everything immediately (pure cold storage load).
	InitialUsageStart int64
	InitialUsageEnd   int64 // -1 means auto (defaults to 1MB or file size, whichever is smaller)

	// Ready thresholds - ALL specified (non-zero) must be met
	// Measured from beginning of file at initial load
	ReadyLines int64
	ReadyBytes int64
	ReadyRunes int64
	ReadyAll   bool

	// Lazy read-ahead - ALL specified (non-zero) must be met
	// Measured from highest seek position after any seek
	ReadAheadLines int64
	ReadAheadBytes int64
	ReadAheadRunes int64
	ReadAheadAll   bool
}

// ChangeResult contains version information after a mutation.
type ChangeResult struct {
	Fork     ForkID
	Revision RevisionID
}

// CountResult contains a count and whether it is complete.
type CountResult struct {
	Value    int64
	Complete bool // true if EOF has been reached
}

// DivergenceDirection indicates the relationship of a fork to the current fork.
type DivergenceDirection int

const (
	// BranchedFrom means the current fork split off from the specified fork.
	BranchedFrom DivergenceDirection = iota
	// BranchedInto means the specified fork split off from the current fork.
	BranchedInto
)

// ForkDivergence describes where a fork split occurred.
type ForkDivergence struct {
	Fork          ForkID
	DivergenceRev RevisionID          // revision at which split occurred
	Direction     DivergenceDirection // relationship to current fork
}

// CacheTier indicates the priority level of a cached decoration.
type CacheTier int

const (
	// CacheTierWarm is for decorations seen during traversal but not actively used.
	CacheTierWarm CacheTier = iota
	// CacheTierHot is for decorations actively requested by the application.
	CacheTierHot
)

// DecorationCacheEntry caches the last known location of a decoration.
// The presence of an entry indicates the key has been used at some point.
// The cached location is a hint - it may be stale if fork/revision differs.
type DecorationCacheEntry struct {
	// Last known location (hint for search)
	LastKnownFork   ForkID
	LastKnownRev    RevisionID
	LastKnownNode   NodeID     // 0 means "confirmed not present at this fork/revision"
	LastKnownOffset int64

	// Cache management
	Tier       CacheTier // Hot = actively used, Warm = seen during traversal
	LastAccess time.Time
}

// pendingDecorationUpdate holds information for cache updates that will be
// applied when recordMutation is called with the new revision number.
type pendingDecorationUpdate struct {
	Key    string
	NodeID NodeID
	Offset int64
}

// TransactionState holds the state of an active transaction.
type TransactionState struct {
	depth    int    // nesting depth
	name     string // from outermost TransactionStart
	poisoned bool   // whether any inner transaction rolled back

	// Pre-transaction state for rollback
	preTransactionRoot    NodeID
	preTransactionFork    ForkID
	preTransactionRev     RevisionID
	preTransactionCursors map[*Cursor]*CursorPosition

	// Pending revision (assigned at TransactionStart)
	pendingRevision RevisionID
	hasMutations    bool
}

// Garland is the main data structure representing an editable file.
type Garland struct {
	lib *Library

	// Identity
	id         string // unique folder name for cold storage
	sourcePath string // original file path, if any

	// Configuration
	loadingStyle    LoadingStyle
	readyThreshold  ReadyThreshold
	readAheadConfig ReadAheadConfig

	// Leaf size configuration
	maxLeafSize    int64 // maximum bytes per leaf
	targetLeafSize int64 // ideal leaf size (max/2)
	minLeafSize    int64 // minimum before merging (max/4)

	// Tree structure
	root         *Node
	eofNode      *Node            // special node for EOF decorations
	nodeRegistry map[NodeID]*Node // all nodes
	nextNodeID   NodeID
	// Structure lookup: maps (leftID, rightID) to the internal node with those children
	// This allows us to reuse internal nodes instead of creating new ones
	internalNodesByChildren map[[2]NodeID]NodeID

	// Versioning
	currentFork     ForkID
	currentRevision RevisionID
	forks           map[ForkID]*ForkInfo
	nextForkID      ForkID
	revisionInfo    map[ForkRevision]*RevisionInfo

	// Cursors
	cursors []*Cursor

	// Decoration cache (hints only).
	// IMPORTANT: Never delete entries from this map! Deletions break undo/history.
	// To mark a decoration as "not present", set LastKnownNode to 0 instead.
	decorationCache map[string]*DecorationCacheEntry

	// Pending decoration cache updates (applied when recordMutation is called)
	pendingDecorationUpdates []pendingDecorationUpdate
	pendingDecorationDeletes []string

	// Loading state
	loader         *Loader
	highestSeekPos int64
	mu             sync.RWMutex

	// Counts (may be incomplete during loading)
	totalBytes    int64
	totalRunes    int64
	totalLines    int64
	countComplete bool

	// Streaming synchronization - for blocking waits on lazy loading
	streamCond *sync.Cond // Signaled when new data arrives or loading completes

	// File system for warm storage
	sourceFS     FileSystemInterface
	sourceHandle FileHandle

	// Optimized region configuration
	graceWindowSize int64 // bytes to capture around cursor when auto-creating regions

	// Transaction state
	transaction *TransactionState

	// Streaming state - for channel-based sources, tracks the rev 0 tree separately
	// from the working tree (which may be at a different revision due to edits)
	streamingRoot *Node // The root of the revision 0 streaming tree

	// Memory tracking for incremental maintenance
	memoryBytes int64 // total bytes of in-memory leaf data

	// Source file change detection
	sourceState      *sourceState
	warmVerification map[NodeID]*warmVerificationState
}

// Open creates or loads a Garland from various sources.
func (lib *Library) Open(options FileOptions) (*Garland, error) {
	// Validate options
	sourceCount := 0
	if options.FilePath != "" {
		sourceCount++
	}
	if options.DataBytes != nil {
		sourceCount++
	}
	if options.DataString != "" {
		sourceCount++
	}
	if options.DataChannel != nil {
		sourceCount++
	}

	if sourceCount == 0 {
		return nil, ErrNoDataSource
	}
	if sourceCount > 1 {
		return nil, ErrMultipleDataSources
	}

	lib.mu.Lock()
	lib.nextGarlandID++
	garlandID := lib.nextGarlandID
	lib.mu.Unlock()

	// Configure leaf sizes
	maxLeaf := options.MaxLeafSize
	if maxLeaf <= 0 {
		maxLeaf = DefaultMaxLeafSize
	}
	targetLeaf := maxLeaf / 2
	minLeaf := maxLeaf / 4

	g := &Garland{
		lib:        lib,
		id:         formatGarlandID(garlandID),
		sourcePath: options.FilePath,

		loadingStyle: options.LoadingStyle,
		readyThreshold: ReadyThreshold{
			Lines: options.ReadyLines,
			Bytes: options.ReadyBytes,
			Runes: options.ReadyRunes,
			All:   options.ReadyAll,
		},
		readAheadConfig: ReadAheadConfig{
			Lines: options.ReadAheadLines,
			Bytes: options.ReadAheadBytes,
			Runes: options.ReadAheadRunes,
			All:   options.ReadAheadAll,
		},

		maxLeafSize:     maxLeaf,
		targetLeafSize:  targetLeaf,
		minLeafSize:     minLeaf,
		graceWindowSize: 128, // default grace window for auto-created regions

		nodeRegistry:            make(map[NodeID]*Node),
		nextNodeID:              1,
		internalNodesByChildren: make(map[[2]NodeID]NodeID),
		forks:                   make(map[ForkID]*ForkInfo),
		revisionInfo:            make(map[ForkRevision]*RevisionInfo),
		cursors:                 make([]*Cursor, 0),
		decorationCache:         make(map[string]*DecorationCacheEntry),
	}

	// Initialize streaming condition variable (uses the garland's mutex)
	g.streamCond = sync.NewCond(&g.mu)

	// Initialize source change detection
	g.initSourceState()

	// Initialize fork 0
	g.forks[0] = &ForkInfo{
		ID:              0,
		ParentFork:      0,
		ParentRevision:  0,
		HighestRevision: 0,
	}

	// Set up file system
	if options.FileSystem != nil {
		g.sourceFS = options.FileSystem
	} else {
		g.sourceFS = lib.defaultFS
	}

	// Load initial data
	var initialData []byte
	var err error

	switch {
	case options.DataBytes != nil:
		initialData = options.DataBytes
		g.countComplete = true

	case options.DataString != "":
		initialData = []byte(options.DataString)
		g.countComplete = true

	case options.FilePath != "":
		initialData, err = g.loadFromFile(options.FilePath)
		if err != nil {
			return nil, err
		}
		// Capture source file info for change detection
		if err := g.captureSourceInfo(); err != nil {
			return nil, err
		}

	case options.DataChannel != nil:
		// Start async loading
		g.startChannelLoader(options.DataChannel)
		initialData = nil
	}

	// Build initial tree structure
	if initialData != nil {
		g.buildInitialTree(initialData, options.InitialUsageStart, options.InitialUsageEnd)
	} else {
		// Create empty tree for async loading
		g.buildEmptyTree()
	}

	// Load initial decorations if provided
	if err := g.loadInitialDecorations(options); err != nil {
		return nil, err
	}

	// Calculate initial memory usage
	g.recalculateMemoryUsage()

	// Register with library
	lib.mu.Lock()
	lib.activeGarlands[g.id] = g
	lib.mu.Unlock()

	// Check memory pressure after loading (will set pressure flag if over limit and can't evict)
	g.CheckMemoryPressure()

	return g, nil
}

// Close releases resources associated with the Garland.
func (g *Garland) Close() error {
	// Stop source file watching
	g.DisableSourceWatch()

	if g.lib != nil {
		g.lib.mu.Lock()
		delete(g.lib.activeGarlands, g.id)
		g.lib.mu.Unlock()
	}

	if g.sourceHandle != nil && g.sourceFS != nil {
		g.sourceFS.Close(g.sourceHandle)
		g.sourceHandle = nil
	}

	return nil
}

// Save overwrites the original file with the current content.
// Caller asserts that this replaces any warm storage source.
// Returns ErrNoDataSource if there is no original file path.
func (g *Garland) Save() error {
	if g.sourcePath == "" {
		return ErrNoDataSource
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Determine which filesystem to use
	fs := g.sourceFS
	if fs == nil {
		fs = g.lib.defaultFS
	}

	// Close warm storage handle if open
	if g.sourceHandle != nil {
		fs.Close(g.sourceHandle)
		g.sourceHandle = nil
	}

	// Stream write to file
	if err := g.streamWriteToFile(fs, g.sourcePath); err != nil {
		return err
	}

	// Reopen for warm storage if needed
	if g.loadingStyle == AllStorage {
		handle, err := fs.Open(g.sourcePath, OpenModeRead)
		if err == nil {
			g.sourceHandle = handle
		}
	}

	return nil
}

// SaveAs writes the current content to a new location.
// Warm storage remains pointing to the original file (if any).
func (g *Garland) SaveAs(fs FileSystemInterface, name string) error {
	if fs == nil {
		return ErrNotSupported
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	return g.streamWriteToFile(fs, name)
}

// streamWriteToFile writes the document to a file using streaming (no full materialization).
func (g *Garland) streamWriteToFile(fs FileSystemInterface, path string) error {
	// Open file for writing
	handle, err := fs.Open(path, OpenModeWrite)
	if err != nil {
		return err
	}
	defer fs.Close(handle)

	// Truncate the file
	if err := fs.Truncate(handle, 0); err != nil {
		// Truncate might not be supported, try seeking to start
		if err := fs.SeekByte(handle, 0); err != nil {
			return err
		}
	}

	// Stream write leaf data
	return g.streamWriteNode(fs, handle, g.root.id)
}

// streamWriteNode recursively writes node data to a file handle.
func (g *Garland) streamWriteNode(fs FileSystemInterface, handle FileHandle, nodeID NodeID) error {
	node := g.nodeRegistry[nodeID]
	if node == nil {
		return nil
	}

	snap := node.snapshotAt(g.currentFork, g.currentRevision)
	if snap == nil {
		return nil
	}

	if snap.isLeaf {
		// Thaw if needed
		if snap.storageState != StorageMemory {
			forkRev := ForkRevision{g.currentFork, g.currentRevision}
			if err := g.thawSnapshot(nodeID, forkRev, snap); err != nil {
				return err
			}
		}
		// Write leaf data directly to file
		if len(snap.data) > 0 {
			return fs.WriteBytes(handle, snap.data)
		}
		return nil
	}

	// Internal node: recurse left then right
	if err := g.streamWriteNode(fs, handle, snap.leftID); err != nil {
		return err
	}
	return g.streamWriteNode(fs, handle, snap.rightID)
}

// Chill moves data to cold storage based on the specified aggressiveness level.
// This frees memory by storing data externally, to be reloaded on demand.
//
// For MemoryOnly files, this is a no-op by design.
//
// Levels:
//   - ChillInactiveForks: Only chill data not used by the current fork
//   - ChillOldHistory: Also chill old undo history beyond recent revisions
//   - ChillUnusedData: Chill everything not used at current revision
//   - ChillEverything: Chill all data (for switching documents or shells)
func (g *Garland) Chill(level ChillLevel) error {
	// MemoryOnly files don't use cold storage
	if g.loadingStyle == MemoryOnly {
		return nil
	}

	// Check if cold storage is available
	if g.lib.coldStorageBackend == nil {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Collect nodes that are "in use" based on the level
	inUse := make(map[NodeID]bool)

	switch level {
	case ChillInactiveForks:
		// Keep nodes used by current fork's complete history
		g.markNodesInUseForFork(g.currentFork, inUse)

	case ChillOldHistory:
		// Keep nodes for current fork but only recent revisions (within 10 steps)
		minRev := g.currentRevision
		if minRev > 10 {
			minRev = g.currentRevision - 10
		}
		g.markNodesInUseForRevisionRange(g.currentFork, minRev, g.currentRevision, inUse)
		// Also keep nodes at fork branch points
		g.markNodesAtBranchPoints(inUse)

	case ChillUnusedData:
		// Only keep nodes at the current revision
		g.markNodesInUseForRevision(g.currentFork, g.currentRevision, inUse)

	case ChillEverything:
		// Mark nothing as in use - chill everything
	}

	// Move data for nodes not in use to cold storage
	chilledCount := 0
	for _, node := range g.nodeRegistry {
		if inUse[node.id] {
			continue
		}
		for forkRev, snap := range node.history {
			if snap.isLeaf && snap.storageState == StorageMemory && len(snap.data) > 0 {
				err := g.chillSnapshot(node.id, forkRev, snap)
				if err != nil {
					// Log error but continue chilling other nodes
					continue
				}
				chilledCount++
			}
		}
	}

	// For ChillEverything, also chill the "in use" nodes
	if level == ChillEverything {
		for nodeID := range inUse {
			node := g.nodeRegistry[nodeID]
			if node == nil {
				continue
			}
			for forkRev, snap := range node.history {
				if snap.isLeaf && snap.storageState == StorageMemory && len(snap.data) > 0 {
					err := g.chillSnapshot(node.id, forkRev, snap)
					if err != nil {
						continue
					}
					chilledCount++
				}
			}
		}
	}

	return nil
}

// Thaw restores data from cold storage to memory for the current fork.
// This is the inverse of Chill - it loads data back from cold storage.
func (g *Garland) Thaw() error {
	if g.lib.coldStorageBackend == nil {
		return nil // No cold storage configured
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	thawedCount := 0
	for _, node := range g.nodeRegistry {
		for forkRev, snap := range node.history {
			// Only thaw for current fork
			if forkRev.Fork != g.currentFork {
				continue
			}
			if snap.isLeaf && snap.storageState == StorageCold {
				err := g.thawSnapshot(node.id, forkRev, snap)
				if err != nil {
					// Log error but continue thawing other nodes
					continue
				}
				thawedCount++
			}
		}
	}

	return nil
}

// ThawRevision restores cold data for a specific revision range in the current fork.
// WARNING: This thaws ALL data for the revision(s), which could be very large.
// For large files, prefer ThawRange() to thaw only the bytes you need.
func (g *Garland) ThawRevision(startRev, endRev RevisionID) error {
	if g.lib.coldStorageBackend == nil {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Traverse the tree and thaw all reachable cold snapshots
	// We need to walk the tree for each revision in the range, as different
	// revisions may reference different nodes
	for rev := startRev; rev <= endRev; rev++ {
		g.thawNodesForRevision(g.currentFork, rev)
	}

	return nil
}

// ThawRange restores cold data for a specific byte range at the current revision.
// This is RAM-safe for large files - it only thaws the nodes needed to read the
// specified byte range instead of the entire file.
func (g *Garland) ThawRange(startByte, endByte int64) error {
	if g.lib.coldStorageBackend == nil {
		return nil
	}

	if startByte > endByte {
		startByte, endByte = endByte, startByte
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	return g.thawRangeUnlocked(startByte, endByte)
}

// thawRangeUnlocked thaws nodes covering a byte range. Caller must hold write lock.
func (g *Garland) thawRangeUnlocked(startByte, endByte int64) error {
	if g.root == nil {
		return nil
	}

	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return nil
	}

	// Clamp to valid range
	if startByte < 0 {
		startByte = 0
	}
	if endByte > rootSnap.byteCount {
		endByte = rootSnap.byteCount
	}

	return g.thawNodeRangeRecursive(g.root, g.currentFork, g.currentRevision, 0, startByte, endByte)
}

// thawNodeRangeRecursive thaws only the nodes that intersect with [startByte, endByte).
// nodeStart is the byte offset where this node's content begins in the document.
func (g *Garland) thawNodeRangeRecursive(node *Node, fork ForkID, rev RevisionID, nodeStart, startByte, endByte int64) error {
	if node == nil {
		return nil
	}

	snap, forkRev := node.snapshotAtWithKey(fork, rev)
	if snap == nil {
		return nil
	}

	nodeEnd := nodeStart + snap.byteCount

	// Check if this node's range intersects with our target range
	if nodeEnd <= startByte || nodeStart >= endByte {
		// No intersection - skip this subtree
		return nil
	}

	if snap.isLeaf {
		if snap.storageState == StorageCold {
			return g.thawSnapshot(node.id, forkRev, snap)
		}
		return nil
	}

	// Internal node - check which children intersect
	var leftBytes int64 = 0
	if snap.leftID != 0 {
		if leftNode := g.nodeRegistry[snap.leftID]; leftNode != nil {
			leftSnap := leftNode.snapshotAt(fork, rev)
			if leftSnap != nil {
				leftBytes = leftSnap.byteCount
			}
		}
	}

	// Recurse into left child if it intersects
	if snap.leftID != 0 && nodeStart+leftBytes > startByte {
		if leftNode := g.nodeRegistry[snap.leftID]; leftNode != nil {
			if err := g.thawNodeRangeRecursive(leftNode, fork, rev, nodeStart, startByte, endByte); err != nil {
				return err
			}
		}
	}

	// Recurse into right child if it intersects
	if snap.rightID != 0 && nodeStart+leftBytes < endByte {
		if rightNode := g.nodeRegistry[snap.rightID]; rightNode != nil {
			if err := g.thawNodeRangeRecursive(rightNode, fork, rev, nodeStart+leftBytes, startByte, endByte); err != nil {
				return err
			}
		}
	}

	return nil
}

// thawNodesForRevision thaws all cold snapshots reachable from the tree root
// at the specified fork/revision.
func (g *Garland) thawNodesForRevision(fork ForkID, rev RevisionID) {
	if g.root == nil {
		return
	}
	g.thawNodeRecursive(g.root, fork, rev)
}

// thawNodeRecursive recursively thaws a node and its children.
func (g *Garland) thawNodeRecursive(node *Node, fork ForkID, rev RevisionID) {
	if node == nil {
		return
	}

	// Find the actual snapshot and its ForkRevision key
	snap, forkRev := node.snapshotAtWithKey(fork, rev)
	if snap == nil {
		return
	}

	if snap.isLeaf {
		if snap.storageState == StorageCold {
			g.thawSnapshot(node.id, forkRev, snap)
		}
	} else {
		// Internal node - recurse into children
		if snap.leftID != 0 {
			if leftNode := g.nodeRegistry[snap.leftID]; leftNode != nil {
				g.thawNodeRecursive(leftNode, fork, rev)
			}
		}
		if snap.rightID != 0 {
			if rightNode := g.nodeRegistry[snap.rightID]; rightNode != nil {
				g.thawNodeRecursive(rightNode, fork, rev)
			}
		}
	}
}

// chillSnapshotWithTrust moves a snapshot's data to storage, respecting warm storage trust levels.
// It prefers warm storage if available and trusted, otherwise uses cold storage.
func (g *Garland) chillSnapshotWithTrust(nodeID NodeID, forkRev ForkRevision, snap *NodeSnapshot) error {
	// Check if warm storage is available for this block
	canUseWarm := snap.originalFileOffset >= 0 && g.sourceHandle != nil && g.sourceFS != nil

	if canUseWarm {
		trustLevel := g.getWarmTrustLevel(nodeID)

		switch trustLevel {
		case WarmTrustFull, WarmTrustVerified:
			// Warm storage is trusted - evict to warm
			return g.chillToWarmStorage(nodeID, snap)

		case WarmTrustStale:
			// Need to verify before evicting to warm
			if err := g.verifyWarmBlock(nodeID, snap); err == nil {
				// Verification passed - can use warm
				return g.chillToWarmStorage(nodeID, snap)
			}
			// Verification failed - fall through to cold storage

		case WarmTrustSuspended:
			// User hasn't responded - don't trust warm, use cold only
		}
	}

	// Use cold storage (either warm not available or not trusted)
	if g.lib.coldStorageBackend != nil {
		return g.chillSnapshot(nodeID, forkRev, snap)
	}

	// No cold storage available either - cannot chill
	return ErrColdStorageFailure
}

// chillToWarmStorage evicts data to warm storage (original file).
func (g *Garland) chillToWarmStorage(nodeID NodeID, snap *NodeSnapshot) error {
	// Compute hash if not already present (needed for future verification)
	if len(snap.dataHash) == 0 {
		snap.dataHash = computeHash(snap.data)
	}

	// Track bytes being freed
	bytesFreed := int64(len(snap.data))

	// Clear in-memory data and update state
	snap.data = nil
	snap.storageState = StorageWarm

	// Update memory tracking
	g.updateMemoryTracking(-bytesFreed)

	// Record verification state
	g.updateWarmVerification(nodeID)

	return nil
}

// chillSnapshot moves a snapshot's data to cold storage.
func (g *Garland) chillSnapshot(nodeID NodeID, forkRev ForkRevision, snap *NodeSnapshot) error {
	// Compute hash if not already present
	if len(snap.dataHash) == 0 {
		snap.dataHash = computeHash(snap.data)
	}

	// Track bytes being freed
	bytesFreed := int64(len(snap.data))

	// Store data in cold storage
	blockName := formatBlockName(nodeID, forkRev)
	err := g.lib.coldStorageBackend.Set(g.id, blockName, snap.data)
	if err != nil {
		return err
	}

	// Store decorations if present
	if len(snap.decorations) > 0 {
		if len(snap.decorationHash) == 0 {
			snap.decorationHash = computeHash(encodeDecorations(snap.decorations))
		}
		decBlockName := formatBlockName(nodeID, forkRev) + ".dec"
		err = g.lib.coldStorageBackend.Set(g.id, decBlockName, encodeDecorations(snap.decorations))
		if err != nil {
			return err
		}
		snap.decorations = nil
	}

	// Clear in-memory data and update state
	snap.data = nil
	snap.storageState = StorageCold

	// Update memory tracking
	g.updateMemoryTracking(-bytesFreed)

	return nil
}

// markNodesInUseForFork marks all nodes used by any revision in a fork.
func (g *Garland) markNodesInUseForFork(fork ForkID, inUse map[NodeID]bool) {
	forkInfo := g.forks[fork]
	if forkInfo == nil {
		return
	}

	// Mark nodes for all revisions in this fork
	for rev := RevisionID(0); rev <= forkInfo.HighestRevision; rev++ {
		g.markNodesInUseForRevision(fork, rev, inUse)
	}

	// If this fork has a parent, mark parent fork nodes too
	if forkInfo.ParentFork != fork {
		g.markNodesInUseForFork(forkInfo.ParentFork, inUse)
	}
}

// markNodesInUseForRevision marks all nodes reachable from a specific revision.
func (g *Garland) markNodesInUseForRevision(fork ForkID, rev RevisionID, inUse map[NodeID]bool) {
	revInfo := g.revisionInfo[ForkRevision{fork, rev}]
	if revInfo == nil {
		return
	}

	g.markNodesReachableFrom(revInfo.RootID, fork, rev, inUse)
}

// markNodesInUseForRevisionRange marks nodes for a range of revisions.
func (g *Garland) markNodesInUseForRevisionRange(fork ForkID, minRev, maxRev RevisionID, inUse map[NodeID]bool) {
	for rev := minRev; rev <= maxRev; rev++ {
		g.markNodesInUseForRevision(fork, rev, inUse)
	}
}

// markNodesAtBranchPoints marks nodes at fork divergence points.
func (g *Garland) markNodesAtBranchPoints(inUse map[NodeID]bool) {
	for _, forkInfo := range g.forks {
		if forkInfo.ParentFork != forkInfo.ID {
			// This fork branched from parent - mark the branch point
			g.markNodesInUseForRevision(forkInfo.ParentFork, forkInfo.ParentRevision, inUse)
		}
	}
}

// markNodesReachableFrom recursively marks all nodes reachable from a root.
func (g *Garland) markNodesReachableFrom(nodeID NodeID, fork ForkID, rev RevisionID, inUse map[NodeID]bool) {
	if nodeID == 0 || inUse[nodeID] {
		return
	}

	inUse[nodeID] = true

	node := g.nodeRegistry[nodeID]
	if node == nil {
		return
	}

	snap := node.snapshotAt(fork, rev)
	if snap == nil || snap.isLeaf {
		return
	}

	// Recurse into children
	g.markNodesReachableFrom(snap.leftID, fork, rev, inUse)
	g.markNodesReachableFrom(snap.rightID, fork, rev, inUse)
}

// formatBlockName creates a unique name for a cold storage block.
func formatBlockName(nodeID NodeID, forkRev ForkRevision) string {
	return formatNodeID(nodeID) + "_" + formatForkRev(forkRev)
}

func formatNodeID(id NodeID) string {
	return formatUint64(uint64(id))
}

func formatForkRev(fr ForkRevision) string {
	return formatUint64(uint64(fr.Fork)) + "_" + formatUint64(uint64(fr.Revision))
}

func formatUint64(n uint64) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 20)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	// Reverse
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

// encodeDecorations serializes decorations for cold storage.
func encodeDecorations(decs []Decoration) []byte {
	// Simple format: key\0position\n for each decoration
	var buf []byte
	for _, d := range decs {
		buf = append(buf, []byte(d.Key)...)
		buf = append(buf, 0)
		buf = append(buf, []byte(formatUint64(uint64(d.Position)))...)
		buf = append(buf, '\n')
	}
	return buf
}

// decodeDecorations parses decorations from the cold storage format.
func decodeDecorations(data []byte) []Decoration {
	if len(data) == 0 {
		return nil
	}

	var decs []Decoration
	i := 0
	for i < len(data) {
		// Find null terminator (end of key)
		keyEnd := i
		for keyEnd < len(data) && data[keyEnd] != 0 {
			keyEnd++
		}
		if keyEnd >= len(data) {
			break // Malformed data
		}
		key := string(data[i:keyEnd])

		// Find newline (end of position)
		posStart := keyEnd + 1
		posEnd := posStart
		for posEnd < len(data) && data[posEnd] != '\n' {
			posEnd++
		}
		if posEnd > posStart {
			posStr := string(data[posStart:posEnd])
			pos := parseUint64(posStr)
			decs = append(decs, Decoration{Key: key, Position: int64(pos)})
		}

		i = posEnd + 1
	}
	return decs
}

// parseUint64 parses a uint64 from a base-10 encoded string.
func parseUint64(s string) uint64 {
	var result uint64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			result = result*10 + uint64(c-'0')
		}
	}
	return result
}

// thawSnapshot restores a snapshot's data from cold storage.
func (g *Garland) thawSnapshot(nodeID NodeID, forkRev ForkRevision, snap *NodeSnapshot) error {
	if g.lib.coldStorageBackend == nil {
		return ErrNoColdStorage
	}

	// Retrieve data from cold storage
	blockName := formatBlockName(nodeID, forkRev)
	data, err := g.lib.coldStorageBackend.Get(g.id, blockName)
	if err != nil {
		snap.storageState = StoragePlaceholder
		return err
	}

	// Verify hash if present
	if len(snap.dataHash) > 0 {
		actualHash := computeHash(data)
		if !hashesEqual(snap.dataHash, actualHash) {
			snap.storageState = StoragePlaceholder
			return ErrColdStorageFailure
		}
	}

	// Restore data
	snap.data = data
	snap.storageState = StorageMemory

	// Update memory tracking
	g.updateMemoryTracking(int64(len(data)))

	// Mark as recently accessed
	g.touchSnapshot(snap)

	// Try to restore decorations if they were stored
	decBlockName := blockName + ".dec"
	decData, err := g.lib.coldStorageBackend.Get(g.id, decBlockName)
	if err == nil && len(decData) > 0 {
		// Verify decoration hash if present
		if len(snap.decorationHash) > 0 {
			actualHash := computeHash(decData)
			if hashesEqual(snap.decorationHash, actualHash) {
				snap.decorations = decodeDecorations(decData)
			}
		} else {
			snap.decorations = decodeDecorations(decData)
		}

		// Add restored decorations to cache for existence checking
		// Note: The offset is unknown, so we set it to 0 as a hint
		// GetDecorationPosition will update with correct offset on access
		for _, d := range snap.decorations {
			if _, exists := g.decorationCache[d.Key]; !exists {
				g.decorationCache[d.Key] = &DecorationCacheEntry{
					LastKnownFork:   forkRev.Fork,
					LastKnownRev:    forkRev.Revision,
					LastKnownNode:   nodeID,
					LastKnownOffset: 0, // Unknown, will be corrected on access
					Tier:            CacheTierWarm,
					LastAccess:      time.Now(),
				}
			}
		}
	}

	return nil
}

// hashesEqual compares two hash slices for equality.
func hashesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ensureSnapshotData ensures that a snapshot's data is loaded into memory.
// If the data is in cold storage, it will be thawed.
// If the data is in warm storage, it will be read from the source file.
// Caller must hold the write lock.
func (g *Garland) ensureSnapshotData(node *Node, forkRev ForkRevision, snap *NodeSnapshot) error {
	if snap == nil || !snap.isLeaf {
		return nil
	}

	switch snap.storageState {
	case StorageMemory:
		// Data is already in memory - touch it for LRU tracking
		g.touchSnapshot(snap)
		return nil

	case StorageCold:
		// Thaw from cold storage
		return g.thawSnapshot(node.id, forkRev, snap)

	case StorageWarm:
		// Read from warm storage (original file) with trust-aware verification
		return g.readFromWarmStorageWithTrust(node.id, snap)

	case StoragePlaceholder:
		// Data is unavailable
		return ErrColdStorageFailure
	}

	return nil
}

// readFromWarmStorageWithTrust reads data from warm storage using trust-aware verification.
func (g *Garland) readFromWarmStorageWithTrust(nodeID NodeID, snap *NodeSnapshot) error {
	if g.sourceHandle == nil || g.sourceFS == nil {
		return ErrWarmStorageMismatch
	}

	// Check trust level to decide verification strategy
	trustLevel := g.getWarmTrustLevel(nodeID)
	shouldVerify := true

	switch trustLevel {
	case WarmTrustFull:
		// No changes ever detected - skip verification unless configured otherwise
		if g.sourceState != nil && !g.sourceState.verifyOnRead {
			shouldVerify = false
		}

	case WarmTrustVerified:
		// Recently verified - optional verification
		if g.sourceState != nil && !g.sourceState.verifyOnRead {
			shouldVerify = false
		}

	case WarmTrustStale:
		// Must verify - changes detected since last verification
		shouldVerify = true

	case WarmTrustSuspended:
		// User notified but hasn't responded - read is risky but may be necessary
		shouldVerify = true
	}

	// Seek to the original position
	err := g.sourceFS.SeekByte(g.sourceHandle, snap.originalFileOffset)
	if err != nil {
		snap.storageState = StoragePlaceholder
		return err
	}

	// Read the data
	data, err := g.sourceFS.ReadBytes(g.sourceHandle, int(snap.byteCount))
	if err != nil {
		snap.storageState = StoragePlaceholder
		return err
	}

	// Verify hash if required
	if shouldVerify && len(snap.dataHash) > 0 {
		actualHash := computeHash(data)
		if !hashesEqual(snap.dataHash, actualHash) {
			// Warm storage mismatch - file was modified
			g.handleWarmStorageMismatch(nodeID)
			snap.storageState = StoragePlaceholder
			return ErrWarmStorageMismatch
		}
		// Verification passed - update tracking
		g.updateWarmVerification(nodeID)
	}

	snap.data = data
	snap.storageState = StorageMemory

	// Update memory tracking
	g.updateMemoryTracking(int64(len(data)))

	// Mark as recently accessed
	g.touchSnapshot(snap)

	return nil
}

// handleWarmStorageMismatch is called when a warm storage read fails checksum verification.
func (g *Garland) handleWarmStorageMismatch(nodeID NodeID) {
	if g.sourceState == nil {
		return
	}

	// Increment change counter if not already in modified state
	if g.sourceState.status != SourceStatusModified {
		g.incrementChangeCounter()
		g.sourceState.status = SourceStatusModified

		// Notify handler if set
		if g.sourceState.changeHandler != nil {
			info := SourceChangeInfo{
				Type:         SourceModified,
				PreviousSize: g.sourceState.originalSize,
			}
			// Call handler outside of any critical path
			go g.sourceState.changeHandler(g, SourceStatusModified, info)
		}
	}
}

// readFromWarmStorage reads data from the original file for warm storage (legacy, always verifies).
func (g *Garland) readFromWarmStorage(snap *NodeSnapshot) error {
	if g.sourceHandle == nil || g.sourceFS == nil {
		return ErrWarmStorageMismatch
	}

	// Seek to the original position
	err := g.sourceFS.SeekByte(g.sourceHandle, snap.originalFileOffset)
	if err != nil {
		snap.storageState = StoragePlaceholder
		return err
	}

	// Read the data
	data, err := g.sourceFS.ReadBytes(g.sourceHandle, int(snap.byteCount))
	if err != nil {
		snap.storageState = StoragePlaceholder
		return err
	}

	// Verify hash if present
	if len(snap.dataHash) > 0 {
		actualHash := computeHash(data)
		if !hashesEqual(snap.dataHash, actualHash) {
			// Warm storage mismatch - file was modified
			// Try cold storage as fallback if available
			if g.lib.coldStorageBackend != nil && snap.storageState == StorageWarm {
				// Can't thaw without nodeID and forkRev - mark as placeholder
				snap.storageState = StoragePlaceholder
				return ErrWarmStorageMismatch
			}
			snap.storageState = StoragePlaceholder
			return ErrWarmStorageMismatch
		}
	}

	snap.data = data
	snap.storageState = StorageMemory

	// Update memory tracking
	g.updateMemoryTracking(int64(len(data)))

	// Mark as recently accessed
	g.touchSnapshot(snap)

	return nil
}

// NewCursor creates a new cursor at position 0.
func (g *Garland) NewCursor() *Cursor {
	c := newCursor(g)

	g.mu.Lock()
	g.cursors = append(g.cursors, c)
	g.mu.Unlock()

	// Check if position 0 is ready
	g.updateCursorReady(c)

	return c
}

// RemoveCursor removes a cursor from the Garland.
func (g *Garland) RemoveCursor(c *Cursor) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	for i, cursor := range g.cursors {
		if cursor == c {
			g.cursors = append(g.cursors[:i], g.cursors[i+1:]...)
			c.garland = nil
			return nil
		}
	}
	return ErrCursorNotFound
}

// CurrentFork returns the current fork ID.
func (g *Garland) CurrentFork() ForkID {
	return g.currentFork
}

// CurrentRevision returns the current revision number within the current fork.
func (g *Garland) CurrentRevision() RevisionID {
	return g.currentRevision
}

// ByteCount returns total bytes (or known bytes if still loading).
// For revisions created during streaming, includes the streaming remainder.
func (g *Garland) ByteCount() CountResult {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Get the current revision's tree byte count
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return CountResult{Value: 0, Complete: g.countComplete}
	}
	treeBytes := rootSnap.byteCount

	// Check for streaming remainder
	// Only add remainder if current tree is NOT the streaming tree
	// (otherwise we'd double-count since g.root IS g.streamingRoot)
	revInfo, hasRevInfo := g.revisionInfo[ForkRevision{g.currentFork, g.currentRevision}]
	if hasRevInfo && revInfo.StreamKnownBytes >= 0 && g.streamingRoot != nil && g.root != g.streamingRoot {
		// This revision was created during streaming - add remainder
		streamSnap := g.streamingRoot.snapshotAt(0, 0)
		if streamSnap != nil {
			currentStreamBytes := streamSnap.byteCount
			if currentStreamBytes > revInfo.StreamKnownBytes {
				streamRemainderBytes := currentStreamBytes - revInfo.StreamKnownBytes
				treeBytes += streamRemainderBytes
			}
		}
	}

	return CountResult{
		Value:    treeBytes,
		Complete: g.countComplete,
	}
}

// RuneCount returns total runes (or known runes if still loading).
func (g *Garland) RuneCount() CountResult {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return CountResult{
		Value:    g.totalRunes,
		Complete: g.countComplete,
	}
}

// LineCount returns total newlines (or known newlines if still loading).
func (g *Garland) LineCount() CountResult {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return CountResult{
		Value:    g.totalLines,
		Complete: g.countComplete,
	}
}

// IsComplete returns true if EOF has been reached during loading.
func (g *Garland) IsComplete() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.countComplete
}

// IsReady returns true if initial ready threshold has been met.
func (g *Garland) IsReady() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.checkReadyThreshold()
}

// InTransaction returns true if any transaction is active.
func (g *Garland) InTransaction() bool {
	return g.transaction != nil
}

// TransactionDepth returns the current nesting depth (0 = no active transaction).
func (g *Garland) TransactionDepth() int {
	if g.transaction == nil {
		return 0
	}
	return g.transaction.depth
}

// TransactionStart begins a new transaction with an optional descriptive name.
func (g *Garland) TransactionStart(name string) error {
	if g.transaction == nil {
		// Top-level transaction: checkpoint any active optimized regions first
		// This ensures the transaction has a clean baseline to rollback to
		if err := g.checkpointUnlocked(); err != nil {
			return err
		}

		// Record cursor positions in history before starting transaction
		// This allows UndoSeek to restore positions from before the transaction
		g.recordCursorPositionsInHistory()

		// First level: create new transaction state
		g.transaction = &TransactionState{
			depth:                 1,
			name:                  name,
			poisoned:              false,
			preTransactionRoot:    g.root.id,
			preTransactionFork:    g.currentFork,
			preTransactionRev:     g.currentRevision,
			preTransactionCursors: g.snapshotCursorPositions(),
			pendingRevision:       g.currentRevision + 1,
			hasMutations:          false,
		}
	} else {
		// Nested: just increment depth
		g.transaction.depth++
	}
	return nil
}

// TransactionCommit commits the current transaction.
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
		// Poisoned: rollback instead
		g.discardAllRegions()
		g.rollbackToPreTransaction()
		g.transaction = nil
		return ChangeResult{}, ErrTransactionPoisoned
	}

	// Dissolve any active regions before committing
	if err := g.dissolveAllRegions(); err != nil {
		return ChangeResult{}, err
	}

	// ALWAYS create a new revision, even if no mutations
	g.currentRevision = g.transaction.pendingRevision

	// Update fork's highest revision
	if forkInfo, ok := g.forks[g.currentFork]; ok {
		if g.currentRevision > forkInfo.HighestRevision {
			forkInfo.HighestRevision = g.currentRevision
		}
	}

	// Store revision info for undo history with current root ID
	streamKnown := int64(-1) // -1 means streaming is complete
	if g.loader != nil && !g.loader.eofReached {
		streamKnown = g.loader.bytesLoaded
	}
	g.revisionInfo[ForkRevision{g.currentFork, g.currentRevision}] = &RevisionInfo{
		Revision:         g.currentRevision,
		Name:             g.transaction.name,
		HasChanges:       g.transaction.hasMutations,
		RootID:           g.root.id,
		StreamKnownBytes: streamKnown,
	}

	result := ChangeResult{
		Fork:     g.currentFork,
		Revision: g.currentRevision,
	}
	g.transaction = nil
	return result, nil
}

// TransactionRollback discards all changes in the current transaction.
func (g *Garland) TransactionRollback() error {
	if g.transaction == nil {
		return ErrNoTransaction
	}

	g.transaction.poisoned = true
	g.transaction.depth--

	if g.transaction.depth == 0 {
		// Outermost level: discard regions and perform actual rollback
		g.discardAllRegions()
		g.rollbackToPreTransaction()
		g.transaction = nil
	}
	// Inner level: poison flag will cause outer commit to rollback

	return nil
}

// GetRevisionInfo returns information about a specific revision.
func (g *Garland) GetRevisionInfo(revision RevisionID) (*RevisionInfo, error) {
	info, ok := g.revisionInfo[ForkRevision{g.currentFork, revision}]
	if !ok {
		return nil, ErrRevisionNotFound
	}
	return info, nil
}

// GetRevisionRange returns info for revisions in [start, end] inclusive.
func (g *Garland) GetRevisionRange(start, end RevisionID) ([]RevisionInfo, error) {
	var result []RevisionInfo
	for rev := start; rev <= end; rev++ {
		if info, ok := g.revisionInfo[ForkRevision{g.currentFork, rev}]; ok {
			result = append(result, *info)
		}
	}
	return result, nil
}

// UndoSeek navigates to a specific revision within the current fork.
// Cannot seek forward past the highest revision in this fork.
// Seeking backwards then making a change creates a new fork.
func (g *Garland) UndoSeek(revision RevisionID) error {
	// Block during transactions
	if g.transaction != nil {
		return ErrTransactionPending
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Get current fork info
	forkInfo, ok := g.forks[g.currentFork]
	if !ok {
		return ErrForkNotFound
	}

	// Validate revision is within this fork's range
	if revision > forkInfo.HighestRevision {
		return ErrRevisionNotFound
	}

	// Can't seek to pruned revisions
	if revision < forkInfo.PrunedUpTo {
		return ErrRevisionNotFound
	}

	// If already at this revision, nothing to do
	if revision == g.currentRevision {
		return nil
	}

	// Get revision info to restore the correct root
	revInfo := g.findRevisionInfo(g.currentFork, revision)
	if revInfo == nil {
		return ErrRevisionNotFound
	}

	// Restore the root to what it was at this revision
	if revInfo.RootID != 0 {
		if rootNode, ok := g.nodeRegistry[revInfo.RootID]; ok {
			g.root = rootNode
		}
	}

	// Update current revision
	g.currentRevision = revision

	// Update counts from the root snapshot at this revision
	g.updateCountsFromRoot()

	// Restore cursor positions if they have recorded positions for this version
	for _, cursor := range g.cursors {
		if pos, ok := cursor.positionHistory[ForkRevision{g.currentFork, revision}]; ok {
			cursor.restorePosition(pos)
		} else {
			// Cursor didn't exist at this revision or hasn't moved since - clamp to valid range
			if cursor.bytePos > g.totalBytes {
				cursor.bytePos = g.totalBytes
				// Recalculate other coordinates
				cursor.runePos = g.totalRunes
				cursor.line = g.totalLines
				cursor.lineRune = 0
			}
		}
		// Update cursor's last known fork/revision
		cursor.lastFork = g.currentFork
		cursor.lastRevision = g.currentRevision
	}

	return nil
}

// ForkSeek switches to a different fork.
// Retains current revision if it exists in both forks,
// otherwise retreats to the last common revision.
func (g *Garland) ForkSeek(fork ForkID) error {
	// Block during transactions
	if g.transaction != nil {
		return ErrTransactionPending
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Validate fork exists
	targetForkInfo, ok := g.forks[fork]
	if !ok {
		return ErrForkNotFound
	}

	// Can't switch to a deleted fork
	if targetForkInfo.Deleted {
		return ErrForkNotFound
	}

	// If already on this fork, nothing to do
	if fork == g.currentFork {
		return nil
	}

	// Find the revision to use in the target fork
	// If current revision exists in target fork (it's a common ancestor), use it
	// Otherwise, find the common ancestor
	targetRevision := g.findCommonRevision(g.currentFork, g.currentRevision, fork)

	// Clamp to target fork's highest revision
	if targetRevision > targetForkInfo.HighestRevision {
		targetRevision = targetForkInfo.HighestRevision
	}

	// Get revision info to restore the correct root
	revInfo := g.findRevisionInfo(fork, targetRevision)

	// Restore the root if we found revision info
	if revInfo != nil && revInfo.RootID != 0 {
		if rootNode, ok := g.nodeRegistry[revInfo.RootID]; ok {
			g.root = rootNode
		}
	}

	// Switch to the new fork and revision
	g.currentFork = fork
	g.currentRevision = targetRevision

	// Update counts from the root snapshot at this version
	g.updateCountsFromRoot()

	// Update cursor positions
	for _, cursor := range g.cursors {
		if pos, ok := cursor.positionHistory[ForkRevision{fork, targetRevision}]; ok {
			cursor.restorePosition(pos)
		} else {
			// Clamp cursor to valid range
			if cursor.bytePos > g.totalBytes {
				cursor.bytePos = g.totalBytes
				cursor.runePos = g.totalRunes
				cursor.line = g.totalLines
				cursor.lineRune = 0
			}
		}
		cursor.lastFork = fork
		cursor.lastRevision = targetRevision
	}

	return nil
}

// GetForkInfo returns information about a specific fork.
func (g *Garland) GetForkInfo(fork ForkID) (*ForkInfo, error) {
	forkInfo, ok := g.forks[fork]
	if !ok {
		return nil, ErrForkNotFound
	}
	return forkInfo, nil
}

// ListForks returns information about all forks.
func (g *Garland) ListForks() []ForkInfo {
	result := make([]ForkInfo, 0, len(g.forks))
	for _, info := range g.forks {
		result = append(result, *info)
	}
	return result
}

// Prune removes revision history before keepFromRevision in the current fork.
// Revisions >= keepFromRevision are kept.
// This sets the fork's PrunedUpTo watermark and cleans up:
// - RevisionInfo entries for pruned revisions
// - Cursor position history for pruned revisions
// - Node snapshots that are no longer needed by any fork
//
// Shared revisions (inherited from parent forks) are only truly deleted
// when all forks that share them have pruned past that point.
func (g *Garland) Prune(keepFromRevision RevisionID) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	forkInfo := g.forks[g.currentFork]
	if forkInfo == nil {
		return ErrForkNotFound
	}

	// Validate revision
	if keepFromRevision > forkInfo.HighestRevision {
		return ErrRevisionNotFound
	}

	// Can't prune to before where we already pruned
	if keepFromRevision <= forkInfo.PrunedUpTo {
		return nil // Nothing to do
	}

	// Can't prune past current revision (would leave us with no valid state)
	if keepFromRevision > g.currentRevision {
		return ErrInvalidPosition
	}

	// Set the watermark
	forkInfo.PrunedUpTo = keepFromRevision

	// Clean up revisionInfo for this fork
	for forkRev := range g.revisionInfo {
		if forkRev.Fork == g.currentFork && forkRev.Revision < keepFromRevision {
			delete(g.revisionInfo, forkRev)
		}
	}

	// Clean up cursor history for this fork
	for _, cursor := range g.cursors {
		if cursor != nil {
			g.pruneCursorHistory(cursor, g.currentFork, keepFromRevision)
		}
	}

	// Garbage collect node snapshots that are no longer needed
	g.garbageCollectSnapshots()

	return nil
}

// pruneCursorHistory removes position history entries for pruned revisions.
func (g *Garland) pruneCursorHistory(cursor *Cursor, fork ForkID, prunedUpTo RevisionID) {
	for forkRev := range cursor.positionHistory {
		if forkRev.Fork == fork && forkRev.Revision < prunedUpTo {
			delete(cursor.positionHistory, forkRev)
		}
	}
}

// DeleteFork soft-deletes a fork, preventing further navigation to it.
// The fork's data remains until no other forks depend on it.
// Cannot delete the current fork or the last remaining non-deleted fork.
func (g *Garland) DeleteFork(fork ForkID) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Can't delete current fork
	if fork == g.currentFork {
		return ErrInvalidPosition
	}

	forkInfo := g.forks[fork]
	if forkInfo == nil {
		return ErrForkNotFound
	}

	// Already deleted?
	if forkInfo.Deleted {
		return nil
	}

	// Can't delete if it would leave no non-deleted forks
	nonDeletedCount := 0
	for _, f := range g.forks {
		if !f.Deleted && f.ID != fork {
			nonDeletedCount++
		}
	}
	if nonDeletedCount == 0 {
		return ErrInvalidPosition
	}

	// Mark as deleted
	forkInfo.Deleted = true

	// Clean up cursor history for this fork
	for _, cursor := range g.cursors {
		if cursor != nil {
			for forkRev := range cursor.positionHistory {
				if forkRev.Fork == fork {
					delete(cursor.positionHistory, forkRev)
				}
			}
		}
	}

	// Clean up revisionInfo for this fork's unique revisions only.
	// Find the highest revision that any child fork needs from this fork.
	maxNeededRevision := RevisionID(0)
	for _, otherFork := range g.forks {
		if !otherFork.Deleted && otherFork.ParentFork == fork {
			if otherFork.ParentRevision > maxNeededRevision {
				maxNeededRevision = otherFork.ParentRevision
			}
		}
	}

	// Only delete revisionInfo for revisions beyond what child forks need
	for forkRev := range g.revisionInfo {
		if forkRev.Fork == fork && forkRev.Revision > maxNeededRevision {
			delete(g.revisionInfo, forkRev)
		}
	}

	// Garbage collect node snapshots
	g.garbageCollectSnapshots()

	return nil
}

// garbageCollectSnapshots removes node history entries that are no longer needed.
// It directly marks which snapshots would be used by simulating snapshotAt for each
// needed (fork, revision) combination.
func (g *Garland) garbageCollectSnapshots() {
	// Build set of snapshots that are actually in use
	inUse := make(map[NodeID]map[ForkRevision]bool)

	for forkID, forkInfo := range g.forks {
		if forkInfo.Deleted {
			// Check if any non-deleted fork still depends on this fork's history
			hasDependent := false
			for _, otherFork := range g.forks {
				if !otherFork.Deleted && g.forkDependsOn(otherFork.ID, forkID) {
					hasDependent = true
					break
				}
			}
			if !hasDependent {
				continue // Can skip this fork entirely
			}
		}

		// Mark snapshots used by this fork's needed revisions
		for rev := forkInfo.PrunedUpTo; rev <= forkInfo.HighestRevision; rev++ {
			g.markSnapshotsInUseForRevision(forkID, rev, inUse)
		}
	}

	// Remove snapshots not in use
	for _, node := range g.nodeRegistry {
		if node == nil {
			continue
		}
		nodeInUse := inUse[node.id]
		for forkRev := range node.history {
			if nodeInUse == nil || !nodeInUse[forkRev] {
				delete(node.history, forkRev)
			}
		}
	}
}

// markSnapshotsInUseForRevision marks all snapshots that would be used when accessing
// the tree at the given fork and revision.
func (g *Garland) markSnapshotsInUseForRevision(fork ForkID, rev RevisionID, inUse map[NodeID]map[ForkRevision]bool) {
	// Find the correct root for this fork/revision
	revInfo := g.findRevisionInfo(fork, rev)
	if revInfo == nil || revInfo.RootID == 0 {
		// No revision info - fall back to current root
		if g.root == nil {
			return
		}
		g.markSnapshotsReachableFrom(g.root.id, fork, rev, inUse)
		return
	}
	g.markSnapshotsReachableFrom(revInfo.RootID, fork, rev, inUse)
}

// markSnapshotsReachableFrom recursively marks snapshots reachable from a node.
func (g *Garland) markSnapshotsReachableFrom(nodeID NodeID, fork ForkID, rev RevisionID, inUse map[NodeID]map[ForkRevision]bool) {
	node := g.nodeRegistry[nodeID]
	if node == nil {
		return
	}

	snap, key := node.snapshotAtWithKey(fork, rev)
	if snap == nil {
		return
	}

	// Mark this snapshot as in use
	if inUse[nodeID] == nil {
		inUse[nodeID] = make(map[ForkRevision]bool)
	}
	if inUse[nodeID][key] {
		return // Already marked, avoid re-traversing
	}
	inUse[nodeID][key] = true

	// Recurse into children if internal node
	if !snap.isLeaf {
		g.markSnapshotsReachableFrom(snap.leftID, fork, rev, inUse)
		g.markSnapshotsReachableFrom(snap.rightID, fork, rev, inUse)
	}
}

// forkDependsOn checks if fork depends on otherFork's history.
func (g *Garland) forkDependsOn(fork, otherFork ForkID) bool {
	if fork == otherFork {
		return true
	}

	forkInfo := g.forks[fork]
	if forkInfo == nil {
		return false
	}

	// Walk up the parent chain
	current := forkInfo.ParentFork
	for current != fork { // Avoid infinite loop
		if current == otherFork {
			return true
		}
		parentInfo := g.forks[current]
		if parentInfo == nil {
			break
		}
		if current == parentInfo.ParentFork {
			break // Reached root or cycle
		}
		current = parentInfo.ParentFork
	}

	return false
}

// forkInheritsRevision checks if fork can access a revision from ancestorFork.
func (g *Garland) forkInheritsRevision(fork, ancestorFork ForkID, revision RevisionID) bool {
	if fork == ancestorFork {
		return true
	}

	// Walk up the parent chain to find if we inherit from ancestorFork
	forkInfo := g.forks[fork]
	for forkInfo != nil {
		if forkInfo.ParentFork == ancestorFork {
			// We inherit revisions up to ParentRevision
			return revision <= forkInfo.ParentRevision
		}
		if forkInfo.ParentFork == forkInfo.ID {
			break // Root fork
		}
		forkInfo = g.forks[forkInfo.ParentFork]
	}

	return false
}

// FindForksBetween returns all fork divergence points between two revisions
// from the perspective of the current fork.
func (g *Garland) FindForksBetween(revisionFirst, revisionLast RevisionID) ([]ForkDivergence, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if revisionFirst > revisionLast {
		revisionFirst, revisionLast = revisionLast, revisionFirst
	}

	currentForkInfo := g.forks[g.currentFork]
	if currentForkInfo == nil {
		return nil, ErrForkNotFound
	}

	// Validate revision range
	if revisionLast > currentForkInfo.HighestRevision {
		return nil, ErrRevisionNotFound
	}

	var divergences []ForkDivergence

	// Find child forks that branched from current fork in the range
	for forkID, forkInfo := range g.forks {
		if forkID == g.currentFork {
			continue
		}

		// Check if this fork branched off from the current fork
		if forkInfo.ParentFork == g.currentFork {
			// Check if the divergence point is within our range
			if forkInfo.ParentRevision >= revisionFirst && forkInfo.ParentRevision <= revisionLast {
				divergences = append(divergences, ForkDivergence{
					Fork:          forkID,
					DivergenceRev: forkInfo.ParentRevision,
					Direction:     BranchedInto, // This fork split off from current
				})
			}
		}
	}

	// If current fork has a parent, check if we branched from it within the range
	if currentForkInfo.ParentFork != g.currentFork {
		// Check if current fork's branching point is within the range
		if currentForkInfo.ParentRevision >= revisionFirst && currentForkInfo.ParentRevision <= revisionLast {
			divergences = append(divergences, ForkDivergence{
				Fork:          currentForkInfo.ParentFork,
				DivergenceRev: currentForkInfo.ParentRevision,
				Direction:     BranchedFrom, // Current fork split from parent
			})
		}
	}

	// Sort by revision
	for i := 0; i < len(divergences)-1; i++ {
		for j := i + 1; j < len(divergences); j++ {
			if divergences[j].DivergenceRev < divergences[i].DivergenceRev {
				divergences[i], divergences[j] = divergences[j], divergences[i]
			}
		}
	}

	return divergences, nil
}

// isAtHead returns true if the current revision is the highest in this fork.
func (g *Garland) isAtHead() bool {
	forkInfo, ok := g.forks[g.currentFork]
	if !ok {
		return true // shouldn't happen, but default to head behavior
	}
	return g.currentRevision >= forkInfo.HighestRevision
}

// findCommonRevision finds a common revision between two forks.
// Returns the revision in the target fork that corresponds to the source position.
func (g *Garland) findCommonRevision(sourceFork ForkID, sourceRev RevisionID, targetFork ForkID) RevisionID {
	// Walk up the ancestry of both forks to find common ancestor
	// For now, simple approach: if target fork is descendant of source, use sourceRev
	// If source fork is descendant of target, find where source forked from target

	targetInfo := g.forks[targetFork]

	// Check if target fork descended from source fork
	current := targetFork
	for current != 0 {
		info := g.forks[current]
		if info.ParentFork == sourceFork {
			// Target descended from source at info.ParentRevision
			if sourceRev <= info.ParentRevision {
				return sourceRev
			}
			return info.ParentRevision
		}
		if current == info.ParentFork {
			break // reached root
		}
		current = info.ParentFork
	}

	// Check if source fork descended from target fork
	sourceInfo := g.forks[sourceFork]
	current = sourceFork
	for current != 0 {
		info := g.forks[current]
		if info.ParentFork == targetFork {
			// Source descended from target at info.ParentRevision
			return info.ParentRevision
		}
		if current == info.ParentFork {
			break
		}
		current = info.ParentFork
	}

	// Both forks share a common ancestor - find it
	// Use targetInfo's parent revision as a safe fallback
	if targetInfo.ParentFork == sourceInfo.ParentFork {
		// Siblings - use the earlier of the two divergence points
		if targetInfo.ParentRevision < sourceInfo.ParentRevision {
			return targetInfo.ParentRevision
		}
		return sourceInfo.ParentRevision
	}

	// Default: start of target fork
	return 0
}

// updateCountsFromRoot updates totalBytes/Runes/Lines from the root snapshot.
func (g *Garland) updateCountsFromRoot() {
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap != nil {
		g.totalBytes = rootSnap.byteCount
		g.totalRunes = rootSnap.runeCount
		g.totalLines = rootSnap.lineCount
	}
}

// findRevisionInfo finds the revision info for a given fork and revision.
// It first looks in the specified fork, walking backwards through revisions.
// If not found, it follows the parent fork ancestry.
func (g *Garland) findRevisionInfo(fork ForkID, revision RevisionID) *RevisionInfo {
	currentFork := fork
	currentRev := revision

	// Limit iterations to prevent infinite loops
	maxIterations := 1000

	for i := 0; i < maxIterations; i++ {
		// Try exact match first
		if info, ok := g.revisionInfo[ForkRevision{currentFork, currentRev}]; ok {
			return info
		}

		// Check if we should jump to parent fork
		// If revision is at or before the divergence point, check parent directly
		forkInfo, ok := g.forks[currentFork]
		if ok && forkInfo.ParentFork != currentFork && currentRev <= forkInfo.ParentRevision {
			// This revision predates this fork - look in parent
			currentFork = forkInfo.ParentFork
			// Keep currentRev the same - we want the same revision in parent
			continue
		}

		// Walk back through revisions in this fork (handle uint64 underflow safely)
		if currentRev > 0 {
			currentRev--
			continue
		}

		// Reached revision 0 in this fork with no match - check parent fork
		if !ok {
			return nil
		}

		// If this is the root fork (fork 0) or parent is itself, we're done
		if forkInfo.ParentFork == currentFork {
			return nil
		}

		// Move to parent fork
		currentFork = forkInfo.ParentFork
		currentRev = forkInfo.ParentRevision
	}

	return nil
}

// snapshotCursorPositions creates a snapshot of all cursor positions.
func (g *Garland) snapshotCursorPositions() map[*Cursor]*CursorPosition {
	positions := make(map[*Cursor]*CursorPosition)
	for _, c := range g.cursors {
		positions[c] = c.snapshotPosition()
	}
	return positions
}

// rollbackToPreTransaction restores state to before the transaction.
func (g *Garland) rollbackToPreTransaction() {
	if g.transaction == nil {
		return
	}

	// Restore tree state
	g.root = g.nodeRegistry[g.transaction.preTransactionRoot]
	g.currentFork = g.transaction.preTransactionFork
	g.currentRevision = g.transaction.preTransactionRev

	// Restore counts from the root snapshot at pre-transaction revision
	g.updateCountsFromRoot()

	// Restore cursor positions
	for cursor, pos := range g.transaction.preTransactionCursors {
		cursor.restorePosition(pos)
	}
}

// Helper functions (stubs to be implemented)

func (g *Garland) loadFromFile(path string) ([]byte, error) {
	// Use the source filesystem to load the file
	fs := g.sourceFS
	if fs == nil {
		fs = g.lib.defaultFS
	}

	// Read the entire file
	data, err := fs.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Open file handle for warm storage if needed
	if g.loadingStyle == AllStorage {
		handle, err := fs.Open(path, OpenModeRead)
		if err == nil {
			g.sourceHandle = handle
		}
	}

	g.countComplete = true
	return data, nil
}

func (g *Garland) startChannelLoader(ch chan []byte) {
	g.loader = &Loader{
		garland:  g,
		dataChan: ch,
		stopChan: make(chan struct{}),
	}

	// Start background goroutine to read from channel
	go g.channelLoaderRoutine()
}

// channelLoaderRoutine reads data from the channel and appends to the streaming tree.
func (g *Garland) channelLoaderRoutine() {
	for {
		select {
		case <-g.loader.stopChan:
			return
		case data, ok := <-g.loader.dataChan:
			if !ok {
				// Channel closed - mark as complete and finalize streaming
				g.mu.Lock()
				g.countComplete = true
				g.loader.eofReached = true

				// Update revision 0's RootID to point to the final streaming tree
				// This ensures UndoSeek(0) shows all streamed content
				if g.streamingRoot != nil {
					if revInfo, exists := g.revisionInfo[ForkRevision{0, 0}]; exists {
						revInfo.RootID = g.streamingRoot.id
						revInfo.StreamKnownBytes = -1 // Mark as complete
					}
				}

				// Signal all waiting goroutines that loading is complete
				g.streamCond.Broadcast()

				g.mu.Unlock()

				// Check memory pressure after loading completes
				g.CheckMemoryPressure()
				return
			}
			if len(data) > 0 {
				g.appendStreamData(data)
			}
		}
	}
}

// appendStreamData appends data from a streaming source to the revision 0 tree.
// Streaming content is visible in ALL revisions because it was "always there" in
// the source file - we're just making it progressively visible.
// Uses streamingRoot to track the revision 0 tree separately from working tree.
func (g *Garland) appendStreamData(data []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Create a new leaf node for this chunk - always at revision 0
	g.nextNodeID++
	chunkNode := newNode(g.nextNodeID, g)
	g.nodeRegistry[chunkNode.id] = chunkNode

	snap := createLeafSnapshot(data, nil, 0)
	chunkNode.setSnapshot(0, 0, snap) // Always fork 0, revision 0

	// Get the streaming root (revision 0 tree)
	streamRoot := g.streamingRoot
	if streamRoot == nil {
		streamRoot = g.root
	}

	rootSnap := streamRoot.snapshotAt(0, 0)
	if rootSnap == nil {
		return
	}

	// Get the left child (content) and right child (EOF)
	leftID := rootSnap.leftID
	eofID := rootSnap.rightID

	// Insert the new chunk before the EOF node
	leftNode := g.nodeRegistry[leftID]
	leftSnap := leftNode.snapshotAt(0, 0)

	// Create new internal node combining left content with new chunk - at revision 0
	g.nextNodeID++
	newContentNode := newNode(g.nextNodeID, g)
	g.nodeRegistry[newContentNode.id] = newContentNode

	newContentSnap := createInternalSnapshot(leftID, chunkNode.id, leftSnap, snap)
	newContentNode.setSnapshot(0, 0, newContentSnap)

	// Create new root combining new content with EOF - at revision 0
	g.nextNodeID++
	newStreamRoot := newNode(g.nextNodeID, g)
	g.nodeRegistry[newStreamRoot.id] = newStreamRoot

	eofNode := g.nodeRegistry[eofID]
	eofSnap := eofNode.snapshotAt(0, 0)

	newRootSnap := createInternalSnapshot(newContentNode.id, eofID, newContentSnap, eofSnap)
	newStreamRoot.setSnapshot(0, 0, newRootSnap)

	// Update streaming root
	g.streamingRoot = newStreamRoot

	// If we're still at revision 0 (no edits yet), also update the working root
	if g.currentFork == 0 && g.currentRevision == 0 {
		g.root = newStreamRoot
	}

	// Update counts
	g.totalBytes += snap.byteCount
	g.totalRunes += snap.runeCount
	g.totalLines += snap.lineCount

	// Update loader progress
	if g.loader != nil {
		g.loader.bytesLoaded += snap.byteCount
		g.loader.runesLoaded += snap.runeCount
		g.loader.linesLoaded += snap.lineCount
	}

	// Signal waiting goroutines that new data is available
	g.streamCond.Broadcast()
}

func (g *Garland) buildInitialTree(data []byte, usageStart, usageEnd int64) {
	dataLen := int64(len(data))

	// Resolve usage window
	// A usageEnd of 0 or less means "auto" - use default window or entire file
	if usageEnd <= 0 {
		// Auto: use default window or entire file, whichever is smaller
		usageEnd = DefaultInitialUsageWindow
		if usageEnd > dataLen {
			usageEnd = dataLen
		}
	}
	if usageStart < 0 {
		usageStart = 0
	}
	if usageEnd > dataLen {
		usageEnd = dataLen
	}

	// Build content tree
	var contentNodeID NodeID
	var contentSnap *NodeSnapshot

	if dataLen <= g.maxLeafSize {
		// Small file - single leaf
		g.nextNodeID++
		contentNode := newNode(g.nextNodeID, g)
		g.nodeRegistry[contentNode.id] = contentNode

		contentSnap = createLeafSnapshot(data, nil, 0)
		contentNode.setSnapshot(0, 0, contentSnap)
		contentNodeID = contentNode.id
	} else {
		// Large file - build balanced tree
		contentNodeID, contentSnap = g.buildBalancedSubtree(data, 0)
	}

	// Create EOF node
	g.nextNodeID++
	g.eofNode = newNode(g.nextNodeID, g)
	g.nodeRegistry[g.eofNode.id] = g.eofNode
	eofSnap := createLeafSnapshot(nil, nil, -1)
	g.eofNode.setSnapshot(0, 0, eofSnap)

	// Create root as internal node pointing to content and EOF
	g.nextNodeID++
	g.root = newNode(g.nextNodeID, g)
	g.nodeRegistry[g.root.id] = g.root

	rootSnap := createInternalSnapshot(contentNodeID, g.eofNode.id, contentSnap, eofSnap)
	g.root.setSnapshot(0, 0, rootSnap)

	// Register the root structure for reuse
	g.internalNodesByChildren[[2]NodeID{contentNodeID, g.eofNode.id}] = g.root.id

	// Update counts
	g.totalBytes = contentSnap.byteCount
	g.totalRunes = contentSnap.runeCount
	g.totalLines = contentSnap.lineCount
	g.countComplete = true

	// Record initial revision (revision 0 with the initial tree)
	g.revisionInfo[ForkRevision{0, 0}] = &RevisionInfo{
		Revision:         0,
		Name:             "(initial)",
		HasChanges:       false,
		RootID:           g.root.id,
		StreamKnownBytes: -1, // -1 means complete (not streaming)
	}

	// Chill nodes outside the usage window
	if g.lib.coldStorageBackend != nil && g.loadingStyle != MemoryOnly {
		g.chillNodesOutsideRange(usageStart, usageEnd)
	}
}

// buildBalancedSubtree recursively builds a balanced tree from data.
// Returns the node ID and the snapshot for the subtree root.
func (g *Garland) buildBalancedSubtree(data []byte, fileOffset int64) (NodeID, *NodeSnapshot) {
	dataLen := int64(len(data))

	// Base case: data fits in a single leaf
	if dataLen <= g.targetLeafSize {
		g.nextNodeID++
		node := newNode(g.nextNodeID, g)
		g.nodeRegistry[node.id] = node

		snap := createLeafSnapshot(data, nil, fileOffset)
		node.setSnapshot(0, 0, snap)
		return node.id, snap
	}

	// Recursive case: split at midpoint and build subtrees
	mid := dataLen / 2

	// Align to rune boundary to avoid splitting UTF-8 characters
	mid = int64(alignToRuneBoundary(data, mid))

	leftID, leftSnap := g.buildBalancedSubtree(data[:mid], fileOffset)
	rightID, rightSnap := g.buildBalancedSubtree(data[mid:], fileOffset+mid)

	// Create internal node
	g.nextNodeID++
	node := newNode(g.nextNodeID, g)
	g.nodeRegistry[node.id] = node

	snap := createInternalSnapshot(leftID, rightID, leftSnap, rightSnap)
	node.setSnapshot(0, 0, snap)

	// Register for structure reuse
	g.internalNodesByChildren[[2]NodeID{leftID, rightID}] = node.id

	return node.id, snap
}

// chillNodesOutsideRange moves leaf data outside the specified byte range to cold storage.
// This is called after initial tree build to avoid keeping large files entirely in RAM.
func (g *Garland) chillNodesOutsideRange(usageStart, usageEnd int64) {
	if g.root == nil {
		return
	}

	rootSnap := g.root.snapshotAt(0, 0)
	if rootSnap == nil {
		return
	}

	g.chillSubtreeOutsideRange(g.root, rootSnap, 0, usageStart, usageEnd)
}

// chillSubtreeOutsideRange recursively chills leaf nodes outside the usage range.
// nodeStart is the byte offset where this subtree begins.
func (g *Garland) chillSubtreeOutsideRange(node *Node, snap *NodeSnapshot, nodeStart, usageStart, usageEnd int64) {
	if snap == nil {
		return
	}

	nodeEnd := nodeStart + snap.byteCount

	if snap.isLeaf {
		// Check if this leaf is entirely outside the usage range
		if nodeEnd <= usageStart || nodeStart >= usageEnd {
			// Chill this leaf - it's outside the usage window
			if snap.storageState == StorageMemory && len(snap.data) > 0 {
				forkRev := ForkRevision{Fork: 0, Revision: 0}
				g.chillSnapshot(node.id, forkRev, snap)
			}
		}
		return
	}

	// Internal node - recurse into children
	var leftBytes int64 = 0
	if snap.leftID != 0 {
		leftNode := g.nodeRegistry[snap.leftID]
		if leftNode != nil {
			leftSnap := leftNode.snapshotAt(0, 0)
			if leftSnap != nil {
				leftBytes = leftSnap.byteCount
				g.chillSubtreeOutsideRange(leftNode, leftSnap, nodeStart, usageStart, usageEnd)
			}
		}
	}

	if snap.rightID != 0 {
		rightNode := g.nodeRegistry[snap.rightID]
		if rightNode != nil {
			rightSnap := rightNode.snapshotAt(0, 0)
			if rightSnap != nil {
				g.chillSubtreeOutsideRange(rightNode, rightSnap, nodeStart+leftBytes, usageStart, usageEnd)
			}
		}
	}
}

func (g *Garland) buildEmptyTree() {
	// Create empty content node
	g.nextNodeID++
	contentNode := newNode(g.nextNodeID, g)
	g.nodeRegistry[contentNode.id] = contentNode
	contentSnap := createLeafSnapshot(nil, nil, -1)
	contentNode.setSnapshot(0, 0, contentSnap)

	// Create EOF node
	g.nextNodeID++
	g.eofNode = newNode(g.nextNodeID, g)
	g.nodeRegistry[g.eofNode.id] = g.eofNode
	eofSnap := createLeafSnapshot(nil, nil, -1)
	g.eofNode.setSnapshot(0, 0, eofSnap)

	// Create root
	g.nextNodeID++
	g.root = newNode(g.nextNodeID, g)
	g.nodeRegistry[g.root.id] = g.root
	rootSnap := createInternalSnapshot(contentNode.id, g.eofNode.id, contentSnap, eofSnap)
	g.root.setSnapshot(0, 0, rootSnap)

	// Register the root structure for reuse
	g.internalNodesByChildren[[2]NodeID{contentNode.id, g.eofNode.id}] = g.root.id

	// Record initial revision (revision 0 with the empty tree)
	// For channel sources, revision 0 starts with 0 bytes known (streaming)
	g.revisionInfo[ForkRevision{0, 0}] = &RevisionInfo{
		Revision:         0,
		Name:             "(initial)",
		HasChanges:       false,
		RootID:           g.root.id,
		StreamKnownBytes: 0, // 0 means streaming hasn't loaded anything yet
	}
}

func (g *Garland) loadInitialDecorations(options FileOptions) error {
	// TODO: Implement decoration loading
	return nil
}

func (g *Garland) checkReadyThreshold() bool {
	if g.readyThreshold.All && !g.countComplete {
		return false
	}
	if g.readyThreshold.Bytes > 0 && g.totalBytes < g.readyThreshold.Bytes && !g.countComplete {
		return false
	}
	if g.readyThreshold.Runes > 0 && g.totalRunes < g.readyThreshold.Runes && !g.countComplete {
		return false
	}
	if g.readyThreshold.Lines > 0 && g.totalLines < g.readyThreshold.Lines && !g.countComplete {
		return false
	}
	return true
}

func (g *Garland) updateCursorReady(c *Cursor) {
	// For now, mark as ready if position is within known bounds
	if c.bytePos <= g.totalBytes || g.countComplete {
		c.setReady(true)
	}
}

// IsByteReady returns true if the given byte position is available for reading.
// This is a non-blocking check that can be used to guard against blocking waits.
// A negative position returns false. A position beyond EOF returns true only
// when loading is complete (at which point seeking there would return ErrInvalidPosition).
func (g *Garland) IsByteReady(pos int64) bool {
	if pos < 0 {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.countComplete || pos <= g.totalBytes
}

// IsRuneReady returns true if the given rune position is available for reading.
// This is a non-blocking check that can be used to guard against blocking waits.
func (g *Garland) IsRuneReady(pos int64) bool {
	if pos < 0 {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.countComplete || pos <= g.totalRunes
}

// IsLineReady returns true if the given line number is available for reading.
// This is a non-blocking check that can be used to guard against blocking waits.
func (g *Garland) IsLineReady(line int64) bool {
	if line < 0 {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.countComplete || line <= g.totalLines
}

// waitForBytePosition blocks until the given byte position is available or timeout expires.
// If timeout is 0, it returns immediately with ErrNotReady if not available.
// If timeout is negative, it blocks indefinitely.
// Caller must NOT hold the lock when calling this function.
func (g *Garland) waitForBytePosition(pos int64, timeout time.Duration) error {
	if pos < 0 {
		return ErrInvalidPosition
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Fast path: already available or complete
	if g.countComplete {
		if pos > g.totalBytes {
			return ErrInvalidPosition
		}
		return nil
	}
	if pos <= g.totalBytes {
		return nil
	}

	// Non-blocking mode
	if timeout == 0 {
		return ErrNotReady
	}

	// Set up timeout if needed
	var deadline time.Time
	var timer *time.Timer
	timedOut := false

	if timeout > 0 {
		deadline = time.Now().Add(timeout)
		timer = time.AfterFunc(timeout, func() {
			g.mu.Lock()
			timedOut = true
			g.streamCond.Broadcast() // Wake up all waiters to check timeout
			g.mu.Unlock()
		})
		defer timer.Stop()
	}

	// Blocking wait loop
	for !g.countComplete && pos > g.totalBytes {
		if timedOut {
			return ErrTimeout
		}
		if timeout > 0 && time.Now().After(deadline) {
			return ErrTimeout
		}
		g.streamCond.Wait() // Releases lock, waits for signal, reacquires lock
	}

	// Check final state
	if g.countComplete && pos > g.totalBytes {
		return ErrInvalidPosition
	}
	return nil
}

// waitForRunePosition blocks until the given rune position is available or timeout expires.
// If timeout is 0, it returns immediately with ErrNotReady if not available.
// If timeout is negative, it blocks indefinitely.
// Caller must NOT hold the lock when calling this function.
func (g *Garland) waitForRunePosition(pos int64, timeout time.Duration) error {
	if pos < 0 {
		return ErrInvalidPosition
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Fast path
	if g.countComplete {
		if pos > g.totalRunes {
			return ErrInvalidPosition
		}
		return nil
	}
	if pos <= g.totalRunes {
		return nil
	}

	// Non-blocking mode
	if timeout == 0 {
		return ErrNotReady
	}

	// Set up timeout if needed
	var deadline time.Time
	var timer *time.Timer
	timedOut := false

	if timeout > 0 {
		deadline = time.Now().Add(timeout)
		timer = time.AfterFunc(timeout, func() {
			g.mu.Lock()
			timedOut = true
			g.streamCond.Broadcast()
			g.mu.Unlock()
		})
		defer timer.Stop()
	}

	// Blocking wait loop
	for !g.countComplete && pos > g.totalRunes {
		if timedOut {
			return ErrTimeout
		}
		if timeout > 0 && time.Now().After(deadline) {
			return ErrTimeout
		}
		g.streamCond.Wait()
	}

	// Check final state
	if g.countComplete && pos > g.totalRunes {
		return ErrInvalidPosition
	}
	return nil
}

// waitForLine blocks until the given line is available or timeout expires.
// If timeout is 0, it returns immediately with ErrNotReady if not available.
// If timeout is negative, it blocks indefinitely.
// Caller must NOT hold the lock when calling this function.
func (g *Garland) waitForLine(line int64, timeout time.Duration) error {
	if line < 0 {
		return ErrInvalidPosition
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Fast path
	if g.countComplete {
		if line > g.totalLines {
			return ErrInvalidPosition
		}
		return nil
	}
	if line <= g.totalLines {
		return nil
	}

	// Non-blocking mode
	if timeout == 0 {
		return ErrNotReady
	}

	// Set up timeout if needed
	var deadline time.Time
	var timer *time.Timer
	timedOut := false

	if timeout > 0 {
		deadline = time.Now().Add(timeout)
		timer = time.AfterFunc(timeout, func() {
			g.mu.Lock()
			timedOut = true
			g.streamCond.Broadcast()
			g.mu.Unlock()
		})
		defer timer.Stop()
	}

	// Blocking wait loop
	for !g.countComplete && line > g.totalLines {
		if timedOut {
			return ErrTimeout
		}
		if timeout > 0 && time.Now().After(deadline) {
			return ErrTimeout
		}
		g.streamCond.Wait()
	}

	// Check final state
	if g.countComplete && line > g.totalLines {
		return ErrInvalidPosition
	}
	return nil
}

// Address conversion functions

// ByteToRune converts a byte position to a rune position.
func (g *Garland) ByteToRune(bytePos int64) (int64, error) {
	if bytePos < 0 {
		return 0, ErrInvalidPosition
	}
	return g.byteToRuneInternal(bytePos)
}

// RuneToByte converts a rune position to a byte position.
func (g *Garland) RuneToByte(runePos int64) (int64, error) {
	if runePos < 0 {
		return 0, ErrInvalidPosition
	}
	return g.runeToByteInternal(runePos)
}

// LineRuneToByte converts a line:rune position to a byte position.
func (g *Garland) LineRuneToByte(line, runeInLine int64) (int64, error) {
	if line < 0 || runeInLine < 0 {
		return 0, ErrInvalidPosition
	}
	return g.lineRuneToByteInternal(line, runeInLine)
}

// ByteToLineRune converts a byte position to a line:rune position.
func (g *Garland) ByteToLineRune(bytePos int64) (line, runeInLine int64, err error) {
	if bytePos < 0 {
		return 0, 0, ErrInvalidPosition
	}
	return g.byteToLineRuneInternal(bytePos)
}

func (g *Garland) byteToRuneInternal(bytePos int64) (int64, error) {
	if bytePos == 0 {
		return 0, nil
	}

	result, err := g.findLeafByByte(bytePos)
	if err != nil {
		return 0, err
	}

	// Absolute rune position = leaf's rune start + rune offset within leaf
	return result.LeafRuneStart + result.RuneOffset, nil
}

// byteToRuneInternalUnlocked is the unlocked version for use when caller already holds the lock.
func (g *Garland) byteToRuneInternalUnlocked(bytePos int64) (int64, error) {
	if bytePos == 0 {
		return 0, nil
	}

	result, err := g.findLeafByByteUnlocked(bytePos)
	if err != nil {
		return 0, err
	}

	// Absolute rune position = leaf's rune start + rune offset within leaf
	return result.LeafRuneStart + result.RuneOffset, nil
}

func (g *Garland) runeToByteInternal(runePos int64) (int64, error) {
	if runePos == 0 {
		return 0, nil
	}

	result, err := g.findLeafByRune(runePos)
	if err != nil {
		return 0, err
	}

	// Absolute byte position = leaf's byte start + byte offset within leaf
	return result.LeafByteStart + result.ByteOffset, nil
}

func (g *Garland) byteToLineRuneInternal(bytePos int64) (int64, int64, error) {
	if bytePos == 0 {
		return 0, 0, nil
	}

	result, err := g.findLeafByByte(bytePos)
	if err != nil {
		return 0, 0, err
	}

	// Find which line within the leaf
	snap := result.Snapshot
	line := int64(0)
	lineRuneStart := int64(0)

	// Find the line that contains our byte offset
	for i := len(snap.lineStarts) - 1; i >= 0; i-- {
		if snap.lineStarts[i].ByteOffset <= result.ByteOffset {
			line = int64(i)
			lineRuneStart = snap.lineStarts[i].RuneOffset
			break
		}
	}

	// Calculate absolute line number
	absoluteLine := g.countLinesBeforeLeaf(result.LeafByteStart) + line

	// Calculate rune position within the line
	runeInLine := result.RuneOffset - lineRuneStart

	return absoluteLine, runeInLine, nil
}

// byteToLineRuneInternalUnlocked is the unlocked version for use when caller already holds the lock.
func (g *Garland) byteToLineRuneInternalUnlocked(bytePos int64) (int64, int64, error) {
	if bytePos == 0 {
		return 0, 0, nil
	}

	result, err := g.findLeafByByteUnlocked(bytePos)
	if err != nil {
		return 0, 0, err
	}

	// Find which line within the leaf
	snap := result.Snapshot
	line := int64(0)
	lineRuneStart := int64(0)

	// Find the line that contains our byte offset
	for i := len(snap.lineStarts) - 1; i >= 0; i-- {
		if snap.lineStarts[i].ByteOffset <= result.ByteOffset {
			line = int64(i)
			lineRuneStart = snap.lineStarts[i].RuneOffset
			break
		}
	}

	// Calculate absolute line number
	absoluteLine := g.countLinesBeforeLeaf(result.LeafByteStart) + line

	// Calculate rune position within the line
	runeInLine := result.RuneOffset - lineRuneStart

	return absoluteLine, runeInLine, nil
}

func (g *Garland) lineRuneToByteInternal(line, runeInLine int64) (int64, error) {
	result, err := g.findLeafByLine(line, runeInLine)
	if err != nil {
		return 0, err
	}

	return result.LeafResult.LeafByteStart + result.LeafResult.ByteOffset, nil
}

// seekByWordAt moves the cursor by n words.
// Positive n moves forward, negative moves backward.
// Returns the number of words actually moved.
func (g *Garland) seekByWordAt(c *Cursor, n int) (int, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if n == 0 {
		return 0, nil
	}

	// Read enough content around the cursor to find word boundaries
	// We need to read character by character to handle word boundaries correctly
	moved := 0

	if n > 0 {
		// Moving forward
		currentBytePos := c.bytePos
		for moved < n {
			// Find the next word boundary from currentBytePos
			nextWordStart, err := g.findNextWordBoundary(currentBytePos, true)
			if err != nil || nextWordStart == currentBytePos {
				// No more words or at end
				break
			}
			currentBytePos = nextWordStart
			moved++
		}
		if moved > 0 {
			runePos, _ := g.byteToRuneInternal(currentBytePos)
			line, lineRune, _ := g.byteToLineRuneInternal(currentBytePos)
			c.updatePosition(currentBytePos, runePos, line, lineRune)
		}
	} else {
		// Moving backward (n is negative)
		wordsToMove := -n
		currentBytePos := c.bytePos
		for moved < wordsToMove {
			// Find the previous word boundary from currentBytePos
			prevWordStart, err := g.findNextWordBoundary(currentBytePos, false)
			if err != nil || prevWordStart == currentBytePos {
				// No more words or at beginning
				break
			}
			currentBytePos = prevWordStart
			moved++
		}
		if moved > 0 {
			runePos, _ := g.byteToRuneInternal(currentBytePos)
			line, lineRune, _ := g.byteToLineRuneInternal(currentBytePos)
			c.updatePosition(currentBytePos, runePos, line, lineRune)
		}
	}

	return moved, nil
}

// findNextWordBoundary finds the byte position of the next/previous word boundary.
// forward=true finds next word start, forward=false finds previous word start.
func (g *Garland) findNextWordBoundary(fromByte int64, forward bool) (int64, error) {
	totalBytes := g.totalBytes

	if forward {
		if fromByte >= totalBytes {
			return fromByte, nil
		}

		// Skip current word (if in one), then skip whitespace, then find word start
		pos := fromByte
		inWord := false

		// First, determine if we're in a word and skip to its end
		for pos < totalBytes {
			r, size, err := g.runeAtByte(pos)
			if err != nil {
				break
			}
			if isWordChar(r) {
				inWord = true
				pos += int64(size)
			} else if inWord {
				// We've reached the end of current word
				break
			} else {
				pos += int64(size)
			}
		}

		// Now skip whitespace/non-word to find next word start
		for pos < totalBytes {
			r, size, err := g.runeAtByte(pos)
			if err != nil {
				break
			}
			if isWordChar(r) {
				// Found start of next word
				return pos, nil
			}
			pos += int64(size)
		}

		return pos, nil
	}

	// Moving backward
	if fromByte <= 0 {
		return 0, nil
	}

	pos := fromByte

	// First, move back to skip any whitespace/non-word chars before cursor
	for pos > 0 {
		r, size, err := g.runeBeforeByte(pos)
		if err != nil {
			break
		}
		if isWordChar(r) {
			break
		}
		pos -= int64(size)
	}

	// Now move back through the word to find its start
	for pos > 0 {
		r, size, err := g.runeBeforeByte(pos)
		if err != nil {
			break
		}
		if !isWordChar(r) {
			// Found start of word
			return pos, nil
		}
		pos -= int64(size)
	}

	return pos, nil
}

// runeAtByte returns the rune starting at the given byte position and its size.
func (g *Garland) runeAtByte(bytePos int64) (rune, int, error) {
	if bytePos >= g.totalBytes {
		return 0, 0, ErrInvalidPosition
	}

	// Read a small chunk to decode the rune (max 4 bytes for UTF-8)
	readLen := int64(4)
	if bytePos+readLen > g.totalBytes {
		readLen = g.totalBytes - bytePos
	}

	data, err := g.readBytesRangeInternal(bytePos, readLen)
	if err != nil {
		return 0, 0, err
	}

	if len(data) == 0 {
		return 0, 0, ErrInvalidPosition
	}

	r, size := decodeRune(data)
	return r, size, nil
}

// runeBeforeByte returns the rune ending at the given byte position and its size.
func (g *Garland) runeBeforeByte(bytePos int64) (rune, int, error) {
	if bytePos <= 0 {
		return 0, 0, ErrInvalidPosition
	}

	// Read up to 4 bytes before the position
	startPos := bytePos - 4
	if startPos < 0 {
		startPos = 0
	}

	data, err := g.readBytesRangeInternal(startPos, bytePos-startPos)
	if err != nil {
		return 0, 0, err
	}

	if len(data) == 0 {
		return 0, 0, ErrInvalidPosition
	}

	// Decode the last rune in the data
	r, size := decodeLastRune(data)
	return r, size, nil
}

// decodeRune decodes a single UTF-8 rune from the start of data.
func decodeRune(data []byte) (rune, int) {
	if len(data) == 0 {
		return 0, 0
	}

	// Simple UTF-8 decode
	b0 := data[0]
	if b0 < 0x80 {
		return rune(b0), 1
	}

	if b0 < 0xC0 {
		return rune(b0), 1 // Invalid, treat as single byte
	}

	if b0 < 0xE0 {
		if len(data) < 2 {
			return rune(b0), 1
		}
		return rune(b0&0x1F)<<6 | rune(data[1]&0x3F), 2
	}

	if b0 < 0xF0 {
		if len(data) < 3 {
			return rune(b0), 1
		}
		return rune(b0&0x0F)<<12 | rune(data[1]&0x3F)<<6 | rune(data[2]&0x3F), 3
	}

	if len(data) < 4 {
		return rune(b0), 1
	}
	return rune(b0&0x07)<<18 | rune(data[1]&0x3F)<<12 | rune(data[2]&0x3F)<<6 | rune(data[3]&0x3F), 4
}

// decodeLastRune decodes the last UTF-8 rune from data.
func decodeLastRune(data []byte) (rune, int) {
	if len(data) == 0 {
		return 0, 0
	}

	// Find the start of the last rune by looking for a non-continuation byte
	for i := len(data) - 1; i >= 0 && i >= len(data)-4; i-- {
		b := data[i]
		if b < 0x80 || b >= 0xC0 {
			// This is the start of a rune
			r, size := decodeRune(data[i:])
			return r, size
		}
	}

	// Fallback: treat last byte as single-byte rune
	return rune(data[len(data)-1]), 1
}

// seekLineEndAt moves the cursor to the end of its current line.
func (g *Garland) seekLineEndAt(c *Cursor) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	currentLine := c.line

	// Try to find the start of the next line
	nextLine := currentLine + 1
	nextLineBytePos, err := g.lineRuneToByteInternal(nextLine, 0)

	if err == nil && nextLineBytePos > 0 {
		// Next line exists - position just before the newline
		// The newline is at nextLineBytePos - 1 (assuming single-byte newline)
		endPos := nextLineBytePos - 1
		if endPos < 0 {
			endPos = 0
		}

		runePos, _ := g.byteToRuneInternal(endPos)
		_, lineRune, _ := g.byteToLineRuneInternal(endPos)
		c.updatePosition(endPos, runePos, currentLine, lineRune)
		return nil
	}

	// We're on the last line - go to EOF
	endPos := g.totalBytes
	runePos, _ := g.byteToRuneInternal(endPos)
	_, lineRune, _ := g.byteToLineRuneInternal(endPos)
	c.updatePosition(endPos, runePos, currentLine, lineRune)
	return nil
}

// countLinesBeforeLeaf counts the total lines in all leaves before the one starting at byteStart.
func (g *Garland) countLinesBeforeLeaf(byteStart int64) int64 {
	if byteStart == 0 {
		return 0
	}

	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return 0
	}

	return g.countLinesBeforeByteInternal(g.root, rootSnap, byteStart, 0)
}

func (g *Garland) countLinesBeforeByteInternal(node *Node, snap *NodeSnapshot, targetByte int64, currentByte int64) int64 {
	if snap.isLeaf {
		return 0
	}

	leftNode := g.nodeRegistry[snap.leftID]
	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)

	leftEnd := currentByte + leftSnap.byteCount

	if targetByte <= leftEnd {
		// Target is in left subtree
		return g.countLinesBeforeByteInternal(leftNode, leftSnap, targetByte, currentByte)
	}

	// Target is in right subtree; count all lines in left subtree
	rightNode := g.nodeRegistry[snap.rightID]
	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)

	return leftSnap.lineCount + g.countLinesBeforeByteInternal(rightNode, rightSnap, targetByte, leftEnd)
}

// Mutation operations

func (g *Garland) insertBytesAt(c *Cursor, pos int64, data []byte, decorations []RelativeDecoration, insertBefore bool) (ChangeResult, error) {
	if len(data) == 0 && len(decorations) == 0 {
		return ChangeResult{Fork: g.currentFork, Revision: g.currentRevision}, nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Validate position
	if pos < 0 || pos > g.totalBytes {
		return ChangeResult{}, ErrInvalidPosition
	}

	// Record cursor positions BEFORE any changes (for undo history)
	// Only if not in transaction (transactions record at TransactionStart)
	if g.transaction == nil {
		g.recordCursorPositionsInHistory()
	}

	// Perform the insertion by recursively rebuilding the tree
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return ChangeResult{}, ErrInvalidPosition
	}

	newRootID, err := g.insertInternal(g.root, rootSnap, pos, 0, data, decorations, insertBefore)
	if err != nil {
		return ChangeResult{}, err
	}

	// Update tree root
	g.root = g.nodeRegistry[newRootID]

	// Calculate deltas for counts
	insertedBytes := int64(len(data))
	insertedRunes := int64(len([]rune(string(data))))
	insertedLines := int64(0)
	for _, b := range data {
		if b == '\n' {
			insertedLines++
		}
	}

	// Update counts
	g.totalBytes += insertedBytes
	g.totalRunes += insertedRunes
	g.totalLines += insertedLines

	// Adjust cursors after insertion point
	for _, cursor := range g.cursors {
		if cursor != c && cursor.bytePos >= pos {
			cursor.adjustForMutation(pos, insertedBytes, insertedRunes, insertedLines)
		}
	}

	// Handle versioning
	return g.recordMutation(), nil
}

func (g *Garland) insertStringAt(c *Cursor, pos int64, data string, decorations []RelativeDecoration, insertBefore bool) (ChangeResult, error) {
	return g.insertBytesAt(c, pos, []byte(data), decorations, insertBefore)
}

func (g *Garland) deleteBytesAt(c *Cursor, pos int64, length int64, includeLineDecorations bool) ([]RelativeDecoration, ChangeResult, error) {
	if length <= 0 {
		return nil, ChangeResult{Fork: g.currentFork, Revision: g.currentRevision}, nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Validate position
	if pos < 0 || pos >= g.totalBytes {
		return nil, ChangeResult{}, ErrInvalidPosition
	}

	// Record cursor positions BEFORE any changes (for undo history)
	if g.transaction == nil {
		g.recordCursorPositionsInHistory()
	}

	// Clamp length to available data
	if pos+length > g.totalBytes {
		length = g.totalBytes - pos
	}

	// Read the content being deleted to calculate deltas
	deletedData, err := g.readBytesRangeInternal(pos, length)
	if err != nil {
		return nil, ChangeResult{}, err
	}

	// Calculate what we're deleting
	deletedBytes := int64(len(deletedData))
	deletedRunes := int64(len([]rune(string(deletedData))))
	deletedLines := int64(0)
	for _, b := range deletedData {
		if b == '\n' {
			deletedLines++
		}
	}

	// Perform the deletion
	deletedDecs, newRootID, err := g.deleteRange(pos, length)
	if err != nil {
		return nil, ChangeResult{}, err
	}

	// Update tree root
	g.root = g.nodeRegistry[newRootID]

	// Update counts
	g.totalBytes -= deletedBytes
	g.totalRunes -= deletedRunes
	g.totalLines -= deletedLines

	// Adjust cursors after deletion point
	for _, cursor := range g.cursors {
		if cursor != c {
			if cursor.bytePos > pos+length {
				// Cursor is after deleted range - shift back
				cursor.adjustForMutation(pos+length, -deletedBytes, -deletedRunes, -deletedLines)
			} else if cursor.bytePos > pos {
				// Cursor is within deleted range - move to deletion point
				cursor.bytePos = pos
				// Recalculate other coordinates (use unlocked versions since we hold the lock)
				cursor.runePos, _ = g.byteToRuneInternalUnlocked(pos)
				cursor.line, cursor.lineRune, _ = g.byteToLineRuneInternalUnlocked(pos)
			}
		}
	}

	// Convert absolute decorations to relative
	relDecs := make([]RelativeDecoration, len(deletedDecs))
	for i, d := range deletedDecs {
		relDecs[i] = RelativeDecoration{
			Key:      d.Key,
			Position: d.Position - pos,
		}
	}

	// Handle versioning
	result := g.recordMutation()
	return relDecs, result, nil
}

// overwriteBytesAt replaces bytes at a position with new data in a single atomic operation.
// This is more efficient than delete + insert for binary editing scenarios.
func (g *Garland) overwriteBytesAt(c *Cursor, pos int64, length int64, newData []byte) ([]RelativeDecoration, ChangeResult, error) {
	return g.overwriteBytesAtInternal(c, pos, length, newData, nil, false)
}

// overwriteBytesAtInternal is the internal implementation that supports additional options.
// - decorationsToAdd: decorations to add to the new content (relative to new content start)
// - insertBefore: if true, displaced decorations consolidate to end; if false, to start
// Returns the original decorations from the overwritten range with their original relative positions.
func (g *Garland) overwriteBytesAtInternal(c *Cursor, pos int64, length int64, newData []byte, decorationsToAdd []RelativeDecoration, insertBefore bool) ([]RelativeDecoration, ChangeResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Handle edge case: if length is 0 and newData is empty, nothing to do
	if length == 0 && len(newData) == 0 {
		return nil, ChangeResult{Fork: g.currentFork, Revision: g.currentRevision}, nil
	}

	// Validate position
	if pos < 0 || pos > g.totalBytes {
		return nil, ChangeResult{}, ErrInvalidPosition
	}

	// Record cursor positions BEFORE any changes (for undo history)
	if g.transaction == nil {
		g.recordCursorPositionsInHistory()
	}

	// Clamp length to available data
	if pos+length > g.totalBytes {
		length = g.totalBytes - pos
	}

	// Read the content being overwritten to calculate deltas and get decorations
	var deletedData []byte
	var deletedDecs []Decoration
	var err error
	var deleteRootID NodeID

	if length > 0 {
		deletedData, err = g.readBytesRangeInternal(pos, length)
		if err != nil {
			return nil, ChangeResult{}, err
		}

		// Perform the deletion portion
		deletedDecs, deleteRootID, err = g.deleteRange(pos, length)
		if err != nil {
			return nil, ChangeResult{}, err
		}

		// Update root to the post-deletion tree
		g.root = g.nodeRegistry[deleteRootID]
	}

	// Calculate deleted counts
	deletedBytes := int64(len(deletedData))
	deletedRunes := int64(len([]rune(string(deletedData))))
	deletedLines := int64(0)
	for _, b := range deletedData {
		if b == '\n' {
			deletedLines++
		}
	}

	// Build the decorations for the new content:
	// 1. Start with explicitly provided decorations
	// 2. Add consolidated displaced decorations from the overwritten range
	var allDecorations []RelativeDecoration

	// Add explicitly provided decorations first
	if len(decorationsToAdd) > 0 {
		allDecorations = append(allDecorations, decorationsToAdd...)
	}

	// Consolidate displaced decorations to start (position 0) or end (position len(newData))
	// based on insertBefore flag
	newDataLen := int64(len(newData))
	consolidatePos := int64(0)
	if insertBefore {
		consolidatePos = newDataLen
	}

	for _, d := range deletedDecs {
		allDecorations = append(allDecorations, RelativeDecoration{
			Key:      d.Key,
			Position: consolidatePos,
		})
	}

	// Perform the insertion portion at the same position using the updated tree
	if len(newData) > 0 {
		rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
		if rootSnap == nil {
			return nil, ChangeResult{}, ErrInternal
		}
		newRootID, err := g.insertInternal(g.root, rootSnap, pos, 0, newData, allDecorations, insertBefore)
		if err != nil {
			return nil, ChangeResult{}, err
		}
		g.root = g.nodeRegistry[newRootID]
	} else if len(allDecorations) > 0 {
		// No new data but we have decorations to add - they need to go somewhere
		// In this case, the decorations are effectively deleted (returned to caller)
		// since there's no content to attach them to
	}

	// Calculate inserted counts
	insertedBytes := int64(len(newData))
	insertedRunes := int64(len([]rune(string(newData))))
	insertedLines := int64(0)
	for _, b := range newData {
		if b == '\n' {
			insertedLines++
		}
	}

	// Update counts with net change
	g.totalBytes += insertedBytes - deletedBytes
	g.totalRunes += insertedRunes - deletedRunes
	g.totalLines += insertedLines - deletedLines

	// Adjust cursors
	// If the overwrite changes the byte length, cursors after the range need to shift
	netByteChange := insertedBytes - deletedBytes
	netRuneChange := insertedRunes - deletedRunes
	netLineChange := insertedLines - deletedLines

	for _, cursor := range g.cursors {
		if cursor != c {
			if cursor.bytePos >= pos+length {
				// Cursor is after overwritten range - shift by net change
				if netByteChange != 0 {
					cursor.adjustForMutation(pos+length, netByteChange, netRuneChange, netLineChange)
				}
			} else if cursor.bytePos > pos {
				// Cursor is within overwritten range - move to start of range
				cursor.bytePos = pos
				// Use unlocked versions since we hold the lock
				cursor.runePos, _ = g.byteToRuneInternalUnlocked(pos)
				cursor.line, cursor.lineRune, _ = g.byteToLineRuneInternalUnlocked(pos)
			}
		}
	}

	// Convert absolute decorations to relative (original positions before deletion)
	relDecs := make([]RelativeDecoration, len(deletedDecs))
	for i, d := range deletedDecs {
		relDecs[i] = RelativeDecoration{
			Key:      d.Key,
			Position: d.Position - pos,
		}
	}

	// Handle versioning
	result := g.recordMutation()
	return relDecs, result, nil
}

// MoveResult contains the result of a Move operation.
type MoveResult struct {
	ChangeResult
	DisplacedDecorations []RelativeDecoration // Decorations that were in the destination range (original positions)
}

// CopyResult contains the result of a Copy operation.
type CopyResult struct {
	ChangeResult
	DisplacedDecorations []RelativeDecoration // Decorations that were in the destination range (original positions)
}

// moveBytesAt moves a byte range to a new location.
// All addresses are interpreted as positions in the original document before any changes.
// Source and destination ranges cannot overlap for Move.
// Decorations in the source range move with the content.
// Decorations in the destination range are consolidated and returned.
func (g *Garland) moveBytesAt(c *Cursor, srcStart, srcEnd, dstStart, dstEnd int64, insertBefore bool) (MoveResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Validate positions
	if srcStart < 0 || srcEnd < srcStart || srcEnd > g.totalBytes {
		return MoveResult{}, ErrInvalidPosition
	}
	if dstStart < 0 || dstEnd < dstStart || dstEnd > g.totalBytes {
		return MoveResult{}, ErrInvalidPosition
	}

	srcLen := srcEnd - srcStart
	dstLen := dstEnd - dstStart

	// Check for overlap - source and destination cannot overlap for Move
	// Ranges overlap if: srcStart < dstEnd && dstStart < srcEnd
	if srcStart < dstEnd && dstStart < srcEnd {
		return MoveResult{}, ErrOverlappingRanges
	}

	// Handle edge case: moving zero bytes
	if srcLen == 0 && dstLen == 0 {
		return MoveResult{
			ChangeResult: ChangeResult{Fork: g.currentFork, Revision: g.currentRevision},
		}, nil
	}

	// Record cursor positions BEFORE any changes
	if g.transaction == nil {
		g.recordCursorPositionsInHistory()
	}

	// Read source data and decorations before any modifications
	var srcData []byte
	var srcDecs []Decoration
	var err error

	if srcLen > 0 {
		srcData, err = g.readBytesRangeInternal(srcStart, srcLen)
		if err != nil {
			return MoveResult{}, err
		}
		// Collect decorations in source range
		srcDecs = g.collectDecorationsInRange(srcStart, srcEnd)
	}

	// Convert source decorations to relative positions
	srcRelDecs := make([]RelativeDecoration, len(srcDecs))
	for i, d := range srcDecs {
		srcRelDecs[i] = RelativeDecoration{
			Key:      d.Key,
			Position: d.Position - srcStart,
		}
	}

	// Track cursors that are within the source range (to move them with content)
	cursorInSource := make(map[*Cursor]int64) // cursor -> relative position within source
	for _, cursor := range g.cursors {
		if cursor.bytePos >= srcStart && cursor.bytePos < srcEnd {
			cursorInSource[cursor] = cursor.bytePos - srcStart
		}
	}

	// Determine order of operations based on relative positions
	// to correctly handle address adjustments
	var dstDecs []Decoration
	var deleteRootID NodeID

	if srcStart < dstStart {
		// Source is before destination
		// 1. Delete destination range first (at original address)
		if dstLen > 0 {
			dstDecs, deleteRootID, err = g.deleteRange(dstStart, dstLen)
			if err != nil {
				return MoveResult{}, err
			}
			g.root = g.nodeRegistry[deleteRootID]
			g.totalBytes -= dstLen
			g.totalRunes -= int64(len([]rune(g.readBytesRangeInternalUnsafe(dstStart, dstLen))))
		}

		// 2. Delete source range (address unchanged since src < dst)
		if srcLen > 0 {
			_, deleteRootID, err = g.deleteRange(srcStart, srcLen)
			if err != nil {
				return MoveResult{}, err
			}
			g.root = g.nodeRegistry[deleteRootID]
			g.totalBytes -= srcLen
		}

		// 3. Insert source content at adjusted destination
		// Destination shifts left by srcLen (source was removed before it)
		// Also shifts left by dstLen (destination was removed)
		adjustedDst := dstStart - srcLen
		if len(srcData) > 0 {
			// Consolidate destination decorations
			allDecs := srcRelDecs
			consolidatePos := int64(0)
			if insertBefore {
				consolidatePos = int64(len(srcData))
			}
			for _, d := range dstDecs {
				allDecs = append(allDecs, RelativeDecoration{
					Key:      d.Key,
					Position: consolidatePos,
				})
			}

			rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
			newRootID, err := g.insertInternal(g.root, rootSnap, adjustedDst, 0, srcData, allDecs, insertBefore)
			if err != nil {
				return MoveResult{}, err
			}
			g.root = g.nodeRegistry[newRootID]
			g.totalBytes += int64(len(srcData))
		}
	} else {
		// Source is after destination (or at same position)
		// 1. Delete source range first (at original address)
		if srcLen > 0 {
			_, deleteRootID, err = g.deleteRange(srcStart, srcLen)
			if err != nil {
				return MoveResult{}, err
			}
			g.root = g.nodeRegistry[deleteRootID]
			g.totalBytes -= srcLen
		}

		// 2. Delete destination range (address unchanged since dst < src)
		if dstLen > 0 {
			dstDecs, deleteRootID, err = g.deleteRange(dstStart, dstLen)
			if err != nil {
				return MoveResult{}, err
			}
			g.root = g.nodeRegistry[deleteRootID]
			g.totalBytes -= dstLen
		}

		// 3. Insert source content at destination (no adjustment needed)
		if len(srcData) > 0 {
			// Consolidate destination decorations
			allDecs := srcRelDecs
			consolidatePos := int64(0)
			if insertBefore {
				consolidatePos = int64(len(srcData))
			}
			for _, d := range dstDecs {
				allDecs = append(allDecs, RelativeDecoration{
					Key:      d.Key,
					Position: consolidatePos,
				})
			}

			rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
			newRootID, err := g.insertInternal(g.root, rootSnap, dstStart, 0, srcData, allDecs, insertBefore)
			if err != nil {
				return MoveResult{}, err
			}
			g.root = g.nodeRegistry[newRootID]
			g.totalBytes += int64(len(srcData))
		}
	}

	// Update counts properly
	g.updateCountsFromRoot()

	// Calculate final destination position for cursor adjustment
	var finalDstStart int64
	if srcStart < dstStart {
		finalDstStart = dstStart - srcLen
	} else {
		finalDstStart = dstStart
	}

	// Adjust cursors
	for _, cursor := range g.cursors {
		if relPos, inSource := cursorInSource[cursor]; inSource {
			// Cursor was in source - move with content to destination
			cursor.bytePos = finalDstStart + relPos
			cursor.runePos, _ = g.byteToRuneInternalUnlocked(cursor.bytePos)
			cursor.line, cursor.lineRune, _ = g.byteToLineRuneInternalUnlocked(cursor.bytePos)
		} else {
			// Cursor was outside source - adjust based on position changes
			// This is complex due to multiple operations; recalculate based on final state
			if cursor.bytePos > g.totalBytes {
				cursor.bytePos = g.totalBytes
				cursor.runePos, _ = g.byteToRuneInternalUnlocked(cursor.bytePos)
				cursor.line, cursor.lineRune, _ = g.byteToLineRuneInternalUnlocked(cursor.bytePos)
			}
		}
	}

	// Convert destination decorations to relative (original positions)
	dstRelDecs := make([]RelativeDecoration, len(dstDecs))
	for i, d := range dstDecs {
		dstRelDecs[i] = RelativeDecoration{
			Key:      d.Key,
			Position: d.Position - dstStart,
		}
	}

	result := g.recordMutation()
	return MoveResult{
		ChangeResult:         result,
		DisplacedDecorations: dstRelDecs,
	}, nil
}

// copyBytesAt copies a byte range to a new location.
// All addresses are interpreted as positions in the original document before any changes.
// Source and destination ranges may overlap for Copy (source is snapshotted first).
// decorationsToAdd are added to the copied content (relative to copied content start).
// Decorations in the destination range are consolidated and returned.
func (g *Garland) copyBytesAt(c *Cursor, srcStart, srcEnd, dstStart, dstEnd int64, decorationsToAdd []RelativeDecoration, insertBefore bool) (CopyResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Validate positions
	if srcStart < 0 || srcEnd < srcStart || srcEnd > g.totalBytes {
		return CopyResult{}, ErrInvalidPosition
	}
	if dstStart < 0 || dstEnd < dstStart || dstEnd > g.totalBytes {
		return CopyResult{}, ErrInvalidPosition
	}

	srcLen := srcEnd - srcStart
	dstLen := dstEnd - dstStart

	// Handle edge case: copying zero bytes and deleting zero bytes
	if srcLen == 0 && dstLen == 0 {
		return CopyResult{
			ChangeResult: ChangeResult{Fork: g.currentFork, Revision: g.currentRevision},
		}, nil
	}

	// Record cursor positions BEFORE any changes
	if g.transaction == nil {
		g.recordCursorPositionsInHistory()
	}

	// Snapshot source data before any modifications (important for overlapping ranges)
	var srcData []byte
	var err error

	if srcLen > 0 {
		srcData, err = g.readBytesRangeInternal(srcStart, srcLen)
		if err != nil {
			return CopyResult{}, err
		}
	}

	// Delete destination range and capture decorations
	var dstDecs []Decoration
	var deleteRootID NodeID

	if dstLen > 0 {
		dstDecs, deleteRootID, err = g.deleteRange(dstStart, dstLen)
		if err != nil {
			return CopyResult{}, err
		}
		g.root = g.nodeRegistry[deleteRootID]
	}

	// Build decorations for the copied content:
	// 1. Explicitly provided decorations
	// 2. Consolidated destination decorations
	var allDecs []RelativeDecoration
	if len(decorationsToAdd) > 0 {
		allDecs = append(allDecs, decorationsToAdd...)
	}

	// Consolidate destination decorations
	consolidatePos := int64(0)
	if insertBefore {
		consolidatePos = int64(len(srcData))
	}
	for _, d := range dstDecs {
		allDecs = append(allDecs, RelativeDecoration{
			Key:      d.Key,
			Position: consolidatePos,
		})
	}

	// Insert copied content at destination
	if len(srcData) > 0 {
		rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
		if rootSnap == nil {
			return CopyResult{}, ErrInternal
		}
		newRootID, err := g.insertInternal(g.root, rootSnap, dstStart, 0, srcData, allDecs, insertBefore)
		if err != nil {
			return CopyResult{}, err
		}
		g.root = g.nodeRegistry[newRootID]
	}

	// Update counts
	g.updateCountsFromRoot()

	// Adjust cursors that were in or after the destination range
	netChange := int64(len(srcData)) - dstLen
	for _, cursor := range g.cursors {
		if cursor != c {
			if cursor.bytePos >= dstStart+dstLen {
				// After destination - shift by net change
				cursor.bytePos += netChange
				if cursor.bytePos < 0 {
					cursor.bytePos = 0
				}
				if cursor.bytePos > g.totalBytes {
					cursor.bytePos = g.totalBytes
				}
				cursor.runePos, _ = g.byteToRuneInternalUnlocked(cursor.bytePos)
				cursor.line, cursor.lineRune, _ = g.byteToLineRuneInternalUnlocked(cursor.bytePos)
			} else if cursor.bytePos > dstStart {
				// Within destination range - move to start
				cursor.bytePos = dstStart
				cursor.runePos, _ = g.byteToRuneInternalUnlocked(cursor.bytePos)
				cursor.line, cursor.lineRune, _ = g.byteToLineRuneInternalUnlocked(cursor.bytePos)
			}
		}
	}

	// Convert destination decorations to relative (original positions)
	dstRelDecs := make([]RelativeDecoration, len(dstDecs))
	for i, d := range dstDecs {
		dstRelDecs[i] = RelativeDecoration{
			Key:      d.Key,
			Position: d.Position - dstStart,
		}
	}

	result := g.recordMutation()
	return CopyResult{
		ChangeResult:         result,
		DisplacedDecorations: dstRelDecs,
	}, nil
}

// collectDecorationsInRange returns all decorations within the specified byte range.
func (g *Garland) collectDecorationsInRange(start, end int64) []Decoration {
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return nil
	}

	var result []Decoration
	g.collectDecorationsInRangeRecursive(g.root, rootSnap, start, end, 0, &result)
	return result
}

// collectDecorationsInRangeRecursive recursively collects decorations in a byte range.
func (g *Garland) collectDecorationsInRangeRecursive(node *Node, snap *NodeSnapshot, start, end, offset int64, result *[]Decoration) {
	if snap == nil {
		return
	}

	nodeEnd := offset + snap.byteCount

	// Skip if node doesn't overlap with range
	if nodeEnd <= start || offset >= end {
		return
	}

	if snap.isLeaf {
		// Collect decorations that fall within the range
		for _, d := range snap.decorations {
			absPos := offset + d.Position
			if absPos >= start && absPos < end {
				*result = append(*result, Decoration{
					Key:      d.Key,
					Position: absPos,
				})
			}
		}
		return
	}

	// Internal node - recurse
	if snap.leftID != 0 {
		leftNode := g.nodeRegistry[snap.leftID]
		if leftNode != nil {
			leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
			if leftSnap != nil {
				g.collectDecorationsInRangeRecursive(leftNode, leftSnap, start, end, offset, result)
				offset += leftSnap.byteCount
			}
		}
	}

	if snap.rightID != 0 {
		rightNode := g.nodeRegistry[snap.rightID]
		if rightNode != nil {
			rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)
			if rightSnap != nil {
				leftBytes := int64(0)
				if snap.leftID != 0 {
					leftNode := g.nodeRegistry[snap.leftID]
					if leftNode != nil {
						leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
						if leftSnap != nil {
							leftBytes = leftSnap.byteCount
						}
					}
				}
				g.collectDecorationsInRangeRecursive(rightNode, rightSnap, start, end, offset+leftBytes-snap.byteCount+rightSnap.byteCount, result)
			}
		}
	}
}

// readBytesRangeInternalUnsafe is a helper that returns empty slice on error (for count calculations).
func (g *Garland) readBytesRangeInternalUnsafe(pos, length int64) string {
	data, err := g.readBytesRangeInternal(pos, length)
	if err != nil {
		return ""
	}
	return string(data)
}

func (g *Garland) deleteRunesAt(c *Cursor, runePos int64, length int64, includeLineDecorations bool) ([]RelativeDecoration, ChangeResult, error) {
	if length <= 0 {
		return nil, ChangeResult{Fork: g.currentFork, Revision: g.currentRevision}, nil
	}

	// Convert rune positions to byte positions (need brief lock for this)
	g.mu.RLock()
	byteStart, err := g.runeToByteInternal(runePos)
	if err != nil {
		g.mu.RUnlock()
		return nil, ChangeResult{}, err
	}

	byteEnd, err := g.runeToByteInternal(runePos + length)
	if err != nil {
		// Clamp to EOF
		byteEnd = g.totalBytes
	}
	g.mu.RUnlock()

	// Now call deleteBytesAt which will handle its own locking
	return g.deleteBytesAt(c, byteStart, byteEnd-byteStart, includeLineDecorations)
}

func (g *Garland) truncateAt(c *Cursor, pos int64) (ChangeResult, error) {
	g.mu.RLock()
	totalBytes := g.totalBytes
	g.mu.RUnlock()

	// Validate position
	if pos < 0 || pos > totalBytes {
		return ChangeResult{}, ErrInvalidPosition
	}

	// Nothing to truncate if already at end
	if pos == totalBytes {
		return ChangeResult{Fork: g.currentFork, Revision: g.currentRevision}, nil
	}

	length := totalBytes - pos

	// Call deleteBytesAt which will handle its own locking
	_, result, err := g.deleteBytesAt(c, pos, length, false)
	return result, err
}

// recordMutation handles versioning after a mutation.
// If in a transaction, marks it as having mutations.
// Otherwise, creates a new revision.
// If not at HEAD revision, creates a new fork first.
func (g *Garland) recordMutation() ChangeResult {
	if g.transaction != nil {
		// In transaction - check if we need to fork
		if !g.isAtHead() && !g.transaction.hasMutations {
			// First mutation in this transaction while not at HEAD - create fork
			g.createForkFromCurrent()
			// Update pending revision (fork preserves revision numbers, so increment)
			g.transaction.pendingRevision = g.currentRevision + 1
		}
		// Mark as having mutations
		g.transaction.hasMutations = true
		return ChangeResult{Fork: g.currentFork, Revision: g.transaction.pendingRevision}
	}

	// Not in transaction - check if we need to fork first
	if !g.isAtHead() {
		g.createForkFromCurrent()
		// Fork preserves revision number, increment below will create next revision
	}

	// Create new revision
	g.currentRevision++
	if forkInfo, ok := g.forks[g.currentFork]; ok {
		if g.currentRevision > forkInfo.HighestRevision {
			forkInfo.HighestRevision = g.currentRevision
		}
	}

	// Store revision info (unnamed) with current root ID
	streamKnown := int64(-1) // -1 means streaming is complete
	if g.loader != nil && !g.loader.eofReached {
		streamKnown = g.loader.bytesLoaded
	}
	g.revisionInfo[ForkRevision{g.currentFork, g.currentRevision}] = &RevisionInfo{
		Revision:         g.currentRevision,
		Name:             "",
		HasChanges:       true,
		RootID:           g.root.id,
		StreamKnownBytes: streamKnown,
	}

	// Apply pending decoration cache updates with the correct revision
	g.applyPendingDecorationUpdates(g.currentFork, g.currentRevision)

	// Update cursor version tracking to new revision
	for _, cursor := range g.cursors {
		cursor.lastFork = g.currentFork
		cursor.lastRevision = g.currentRevision
	}

	// Check memory pressure and perform incremental maintenance if needed
	// Note: This is done without holding the lock, so we need to be careful
	go g.CheckMemoryPressure()

	return ChangeResult{Fork: g.currentFork, Revision: g.currentRevision}
}

// recordCursorPositionsInHistory records all cursor positions in their history maps.
// Called before creating a new revision so positions can be restored on undo.
func (g *Garland) recordCursorPositionsInHistory() {
	key := ForkRevision{g.currentFork, g.currentRevision}
	for _, cursor := range g.cursors {
		// Always update position - cursor may have moved since last record
		// This captures the position just before the mutation occurs
		cursor.positionHistory[key] = &CursorPosition{
			BytePos:  cursor.bytePos,
			RunePos:  cursor.runePos,
			Line:     cursor.line,
			LineRune: cursor.lineRune,
		}
	}
}

// createForkFromCurrent creates a new fork branching from the current fork/revision.
func (g *Garland) createForkFromCurrent() {
	g.nextForkID++
	newForkID := g.nextForkID

	// Create new fork info
	// The new fork inherits the revision number from the parent - this allows
	// UndoSeek to navigate to any revision from 0 to HighestRevision, including
	// revisions that logically came from the parent fork.
	g.forks[newForkID] = &ForkInfo{
		ID:              newForkID,
		ParentFork:      g.currentFork,
		ParentRevision:  g.currentRevision,
		HighestRevision: g.currentRevision, // Start with parent's revision
	}

	// Switch to the new fork, keeping the current revision number
	g.currentFork = newForkID
	// Keep currentRevision as-is - recordMutation will increment it

	// Update cursor tracking
	for _, cursor := range g.cursors {
		cursor.lastFork = newForkID
		cursor.lastRevision = g.currentRevision
	}
}

// Read operations

func (g *Garland) readBytesAt(pos int64, length int64) ([]byte, error) {
	if pos < 0 {
		return nil, ErrInvalidPosition
	}

	if length <= 0 {
		return nil, nil
	}

	// Try read with read lock first (fast path)
	g.mu.RLock()
	totalBytesForRevision := g.calculateTotalBytesUnlocked()

	if pos > totalBytesForRevision {
		g.mu.RUnlock()
		return nil, ErrInvalidPosition
	}

	// Clamp length to available data
	readLength := length
	if pos+readLength > totalBytesForRevision {
		readLength = totalBytesForRevision - pos
	}

	result, err := g.readBytesRangeInternal(pos, readLength)
	g.mu.RUnlock()

	// If data is not loaded (cold storage), try to thaw and retry
	if err == ErrDataNotLoaded {
		// Thaw only the byte range we need - RAM-safe for large files
		if thawErr := g.ThawRange(pos, pos+readLength); thawErr != nil {
			return nil, err // Return original error if thaw fails
		}

		// Retry with read lock
		g.mu.RLock()
		result, err = g.readBytesRangeInternal(pos, readLength)
		g.mu.RUnlock()
	}

	return result, err
}

// calculateTotalBytesUnlocked returns the total bytes for the current revision,
// including streaming remainder. Caller must hold at least read lock.
func (g *Garland) calculateTotalBytesUnlocked() int64 {
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return 0
	}
	treeBytes := rootSnap.byteCount

	// Check for streaming remainder
	// Only add remainder if current tree is NOT the streaming tree
	revInfo, hasRevInfo := g.revisionInfo[ForkRevision{g.currentFork, g.currentRevision}]
	if hasRevInfo && revInfo.StreamKnownBytes >= 0 && g.streamingRoot != nil && g.root != g.streamingRoot {
		streamSnap := g.streamingRoot.snapshotAt(0, 0)
		if streamSnap != nil {
			currentStreamBytes := streamSnap.byteCount
			if currentStreamBytes > revInfo.StreamKnownBytes {
				streamRemainderBytes := currentStreamBytes - revInfo.StreamKnownBytes
				treeBytes += streamRemainderBytes
			}
		}
	}

	return treeBytes
}

func (g *Garland) readStringAt(pos int64, length int64) (string, error) {
	if length <= 0 {
		return "", nil
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	if pos < 0 || pos > g.totalRunes {
		return "", ErrInvalidPosition
	}

	// Convert rune range to byte range
	byteStart, err := g.runeToByteInternal(pos)
	if err != nil {
		return "", err
	}

	byteEnd, err := g.runeToByteInternal(pos + length)
	if err != nil {
		// If end is past EOF, clamp to EOF
		byteEnd = g.totalBytes
	}

	data, err := g.readBytesRangeInternal(byteStart, byteEnd-byteStart)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func (g *Garland) readLineAt(line int64) (string, error) {
	if line < 0 {
		return "", ErrInvalidPosition
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	// Validate line number
	if line > g.totalLines {
		return "", ErrInvalidPosition
	}

	// Find start of line
	lineResult, err := g.findLeafByLine(line, 0)
	if err != nil {
		return "", err
	}

	lineStart := lineResult.LineByteStart

	// Find end of line (next newline or EOF)
	lineEnd := g.findLineEnd(lineStart)

	// Read the line content
	length := lineEnd - lineStart
	if length <= 0 {
		return "", nil
	}

	data, err := g.readBytesRangeInternal(lineStart, length)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// readBytesRangeInternal reads bytes from pos to pos+length.
// For revisions created during streaming, this includes the streaming remainder.
// Caller must hold at least read lock.
func (g *Garland) readBytesRangeInternal(pos int64, length int64) ([]byte, error) {
	if length <= 0 {
		return nil, nil
	}

	// Get revision info to check for streaming remainder
	revInfo, hasRevInfo := g.revisionInfo[ForkRevision{g.currentFork, g.currentRevision}]

	// Calculate tree byte count
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return nil, ErrInternal
	}
	treeBytes := rootSnap.byteCount

	// Calculate streaming remainder if applicable
	// Only add remainder if current tree is NOT the streaming tree
	streamRemainderStart := int64(-1) // -1 means no remainder
	streamRemainderBytes := int64(0)

	if hasRevInfo && revInfo.StreamKnownBytes >= 0 && g.streamingRoot != nil && g.root != g.streamingRoot {
		// This revision was created during streaming - it may have remainder
		streamSnap := g.streamingRoot.snapshotAt(0, 0)
		if streamSnap != nil {
			currentStreamBytes := streamSnap.byteCount
			if currentStreamBytes > revInfo.StreamKnownBytes {
				streamRemainderStart = revInfo.StreamKnownBytes
				streamRemainderBytes = currentStreamBytes - revInfo.StreamKnownBytes
			}
		}
	}

	totalBytes := treeBytes + streamRemainderBytes

	// Clamp length to available bytes
	if pos >= totalBytes {
		return nil, nil
	}
	if pos+length > totalBytes {
		length = totalBytes - pos
	}

	result := make([]byte, 0, length)
	remaining := length
	currentPos := pos

	// Read from tree portion
	for remaining > 0 && currentPos < treeBytes {
		leafResult, err := g.findLeafByByteUnlocked(currentPos)
		if err != nil {
			return nil, err
		}

		snap := leafResult.Snapshot

		// Check if data is loaded (may be in cold/warm storage)
		if snap.storageState != StorageMemory || snap.data == nil {
			return nil, ErrDataNotLoaded
		}

		// Calculate how much we can read from this leaf
		availableInLeaf := snap.byteCount - leafResult.ByteOffset
		toRead := remaining
		if toRead > availableInLeaf {
			toRead = availableInLeaf
		}
		// Don't read past tree boundary
		if currentPos+toRead > treeBytes {
			toRead = treeBytes - currentPos
		}

		// Copy data from leaf
		start := leafResult.ByteOffset
		end := start + toRead
		result = append(result, snap.data[start:end]...)

		remaining -= toRead
		currentPos += toRead
	}

	// Read from streaming remainder if needed
	if remaining > 0 && streamRemainderStart >= 0 {
		// currentPos is now >= treeBytes, convert to streaming position
		streamPos := streamRemainderStart + (currentPos - treeBytes)
		streamData, err := g.readFromStreamingTree(streamPos, remaining)
		if err != nil {
			return nil, err
		}
		result = append(result, streamData...)
	}

	return result, nil
}

// readFromStreamingTree reads bytes from the streamingRoot tree at the given position.
// Caller must hold at least read lock.
func (g *Garland) readFromStreamingTree(pos int64, length int64) ([]byte, error) {
	if g.streamingRoot == nil || length <= 0 {
		return nil, nil
	}

	result := make([]byte, 0, length)
	remaining := length
	currentPos := pos

	for remaining > 0 {
		leafResult, err := g.findLeafByByteInTree(g.streamingRoot, 0, 0, currentPos)
		if err != nil {
			return nil, err
		}
		if leafResult == nil {
			break // Past end of streaming tree
		}

		snap := leafResult.Snapshot

		// Check if data is loaded (may be in cold/warm storage)
		if snap.storageState != StorageMemory || snap.data == nil {
			return nil, ErrDataNotLoaded
		}

		// Calculate how much we can read from this leaf
		availableInLeaf := snap.byteCount - leafResult.ByteOffset
		toRead := remaining
		if toRead > availableInLeaf {
			toRead = availableInLeaf
		}

		// Copy data from leaf
		start := leafResult.ByteOffset
		end := start + toRead
		result = append(result, snap.data[start:end]...)

		remaining -= toRead
		currentPos += toRead
	}

	return result, nil
}

// findLeafByByteInTree finds the leaf containing the given byte position in a specific tree.
// Caller must hold at least read lock.
func (g *Garland) findLeafByByteInTree(root *Node, fork ForkID, revision RevisionID, pos int64) (*LeafSearchResult, error) {
	if root == nil {
		return nil, ErrInternal
	}

	node := root
	accumulatedBytes := int64(0)

	for {
		snap := node.snapshotAt(fork, revision)
		if snap == nil {
			return nil, ErrInternal
		}

		// Check if this is a leaf
		if snap.leftID == 0 {
			// Leaf node
			if pos-accumulatedBytes >= snap.byteCount {
				return nil, nil // Past end
			}
			return &LeafSearchResult{
				Node:       node,
				Snapshot:   snap,
				ByteOffset: pos - accumulatedBytes,
			}, nil
		}

		// Internal node - descend
		leftNode := g.nodeRegistry[snap.leftID]
		if leftNode == nil {
			return nil, ErrInternal
		}

		leftSnap := leftNode.snapshotAt(fork, revision)
		if leftSnap == nil {
			return nil, ErrInternal
		}

		if pos < accumulatedBytes+leftSnap.byteCount {
			// Position is in left subtree
			node = leftNode
		} else {
			// Position is in right subtree
			accumulatedBytes += leftSnap.byteCount
			rightNode := g.nodeRegistry[snap.rightID]
			if rightNode == nil {
				return nil, ErrInternal
			}
			node = rightNode
		}
	}
}

// findLineEnd finds the byte position of the end of the line (at newline or EOF).
// Caller must hold at least read lock.
func (g *Garland) findLineEnd(lineStart int64) int64 {
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return lineStart
	}

	currentPos := lineStart
	totalBytes := rootSnap.byteCount

	for currentPos < totalBytes {
		leafResult, err := g.findLeafByByteUnlocked(currentPos)
		if err != nil {
			return currentPos
		}

		snap := leafResult.Snapshot

		// Search for newline in this leaf starting from our offset
		for i := leafResult.ByteOffset; i < snap.byteCount; i++ {
			if snap.data[i] == '\n' {
				return currentPos + (i - leafResult.ByteOffset) + 1 // include the newline
			}
		}

		// Move to next leaf
		currentPos += snap.byteCount - leafResult.ByteOffset
	}

	return totalBytes
}

func formatGarlandID(id uint64) string {
	return "garland_" + string(rune('0'+id%10))
}

// Decorate adds, updates, or removes decorations at absolute positions.
// All changes are applied as a single revision.
// Pass nil Address in a DecorationEntry to delete that decoration.
func (g *Garland) Decorate(entries []DecorationEntry) (ChangeResult, error) {
	if len(entries) == 0 {
		return ChangeResult{Fork: g.currentFork, Revision: g.currentRevision}, nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Record cursor positions BEFORE any changes (for undo history)
	// Only if not in transaction (transactions record at TransactionStart)
	if g.transaction == nil {
		g.recordCursorPositionsInHistory()
	}

	// Separate deletions from additions/updates
	var deletions []string
	var additions []struct {
		key     string
		bytePos int64
	}

	for _, entry := range entries {
		if entry.Address == nil {
			// Deletion
			deletions = append(deletions, entry.Key)
		} else {
			// Addition/update - convert address to byte position
			bytePos, err := g.addressToByteUnlocked(entry.Address)
			if err != nil {
				return ChangeResult{}, err
			}
			additions = append(additions, struct {
				key     string
				bytePos int64
			}{entry.Key, bytePos})
		}
	}

	// Track whether any changes were made
	changed := false

	// Process deletions: use cache for single deletions, tree walk for batch
	if len(deletions) == 1 {
		// Single deletion: try cache-based direct removal first
		removed, err := g.removeDecorationViaCache(deletions[0])
		if err != nil {
			return ChangeResult{}, err
		}
		if removed {
			changed = true
		} else {
			// Cache miss - fall back to tree walk
			keySet := map[string]bool{deletions[0]: true}
			newRootID, didChange, err := g.removeDecorationsInternal(g.root, g.root.snapshotAt(g.currentFork, g.currentRevision), 0, keySet)
			if err != nil {
				return ChangeResult{}, err
			}
			if didChange {
				g.root = g.nodeRegistry[newRootID]
				changed = true
			}
		}
	} else if len(deletions) > 1 {
		// Batch deletions: use tree walk (single pass for all keys)
		keySet := make(map[string]bool)
		for _, key := range deletions {
			keySet[key] = true
		}
		newRootID, didChange, err := g.removeDecorationsInternal(g.root, g.root.snapshotAt(g.currentFork, g.currentRevision), 0, keySet)
		if err != nil {
			return ChangeResult{}, err
		}
		if didChange {
			g.root = g.nodeRegistry[newRootID]
			changed = true
		}
	}

	// Process additions/updates: group by leaf node for efficiency
	if len(additions) > 0 {
		// Group additions by their target leaf position
		for _, add := range additions {
			newRootID, err := g.addDecorationInternal(add.key, add.bytePos)
			if err != nil {
				return ChangeResult{}, err
			}
			g.root = g.nodeRegistry[newRootID]
			changed = true
		}
	}

	// Record the mutation only once for all changes
	if changed {
		return g.recordMutation(), nil
	}

	return ChangeResult{Fork: g.currentFork, Revision: g.currentRevision}, nil
}

// GetDecorationPosition returns the current position of a decoration by key.
// Uses registry-based O(1) existence check and cached location hints for fast lookup.
func (g *Garland) GetDecorationPosition(key string) (AbsoluteAddress, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// O(1) existence check: if not in registry, it was never created
	cacheEntry, exists := g.decorationCache[key]
	if !exists {
		return AbsoluteAddress{}, ErrDecorationNotFound
	}

	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return AbsoluteAddress{}, ErrDecorationNotFound
	}

	// During a transaction, always search the tree since decorations may have moved
	// as a side effect of inserts/deletes (cache doesn't track these movements)
	inTransaction := g.transaction != nil && g.transaction.hasMutations

	// Check if cached location is valid (same fork, not in transaction)
	if !inTransaction && cacheEntry.LastKnownFork == g.currentFork {
		// NodeID == 0 means "confirmed not present at this fork/revision"
		if cacheEntry.LastKnownNode == 0 && cacheEntry.LastKnownRev == g.currentRevision {
			return AbsoluteAddress{}, ErrDecorationNotFound
		}

		// Try cached location directly using O(1) history lookup
		// Avoid snapshotAt which is O(revisions) in worst case
		if cacheEntry.LastKnownNode != 0 {
			if node, ok := g.nodeRegistry[cacheEntry.LastKnownNode]; ok {
				cachedKey := ForkRevision{cacheEntry.LastKnownFork, cacheEntry.LastKnownRev}
				if snap, ok := node.history[cachedKey]; ok && snap != nil && snap.isLeaf {
					for _, d := range snap.decorations {
						if d.Key == key {
							// Cache hit! Update access time
							cacheEntry.LastAccess = time.Now()
							cacheEntry.Tier = CacheTierHot
							return ByteAddress(cacheEntry.LastKnownOffset + d.Position), nil
						}
					}
				}
			}
		}
	}

	// Cache miss or stale - need to search the tree
	// Use cached offset as hint for middle-out search
	bytePos, nodeID, nodeOffset, found := g.findDecorationWithHint(key, cacheEntry.LastKnownOffset)
	if !found {
		// Decoration not present at this revision - mark as confirmed absent
		cacheEntry.LastKnownFork = g.currentFork
		cacheEntry.LastKnownRev = g.currentRevision
		cacheEntry.LastKnownNode = 0 // 0 = confirmed not present
		cacheEntry.LastAccess = time.Now()
		return AbsoluteAddress{}, ErrDecorationNotFound
	}

	// Update cache with new location
	cacheEntry.LastKnownFork = g.currentFork
	cacheEntry.LastKnownRev = g.currentRevision
	cacheEntry.LastKnownNode = nodeID
	cacheEntry.LastKnownOffset = nodeOffset
	cacheEntry.Tier = CacheTierHot
	cacheEntry.LastAccess = time.Now()

	return ByteAddress(bytePos), nil
}

// findDecorationWithHint searches for a decoration using a position hint.
// Starts near the hint and expands outward (middle-out search).
// Returns absolute byte position, containing node ID, node's byte offset, and whether found.
func (g *Garland) findDecorationWithHint(key string, hintOffset int64) (int64, NodeID, int64, bool) {
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return 0, 0, 0, false
	}

	// Clamp hint to valid range
	if hintOffset < 0 {
		hintOffset = 0
	}
	if hintOffset > rootSnap.byteCount {
		hintOffset = rootSnap.byteCount
	}

	// Find the leaf containing the hint position
	hintLeaf, hintLeafOffset := g.findLeafAtOffset(hintOffset)
	if hintLeaf == nil {
		// Fall back to full tree search
		pos, found := g.findDecorationByKeyInternal(g.root, rootSnap, key, 0)
		if found {
			// Need to find which node contains this - do another search
			leaf, leafOffset := g.findLeafAtOffset(pos)
			if leaf != nil {
				return pos, leaf.id, leafOffset, true
			}
		}
		return 0, 0, 0, false
	}

	// Check hint leaf first
	hintSnap := hintLeaf.snapshotAt(g.currentFork, g.currentRevision)
	if hintSnap != nil {
		for _, d := range hintSnap.decorations {
			if d.Key == key {
				return hintLeafOffset + d.Position, hintLeaf.id, hintLeafOffset, true
			}
		}
	}

	// Middle-out search: alternate between left and right of hint
	// This is a simplification - we'll just do a full tree search for now
	// TODO: Implement true middle-out search for even better locality
	pos, found := g.findDecorationByKeyInternal(g.root, rootSnap, key, 0)
	if found {
		// Find containing node for cache update
		leaf, leafOffset := g.findLeafAtOffset(pos)
		if leaf != nil {
			return pos, leaf.id, leafOffset, true
		}
		return pos, 0, 0, true
	}
	return 0, 0, 0, false
}

// updateDecorationCacheForNode queues decoration cache updates to be applied when
// recordMutation is called. This ensures the cache is updated with the correct
// revision number (which isn't known until after the mutation completes).
func (g *Garland) updateDecorationCacheForNode(nodeID NodeID, nodeOffset int64, decorations []Decoration) {
	for _, d := range decorations {
		g.pendingDecorationUpdates = append(g.pendingDecorationUpdates, pendingDecorationUpdate{
			Key:    d.Key,
			NodeID: nodeID,
			Offset: nodeOffset,
		})
	}
}

// applyPendingDecorationUpdates applies queued cache updates with the given revision.
func (g *Garland) applyPendingDecorationUpdates(fork ForkID, rev RevisionID) {
	now := time.Now()
	for _, update := range g.pendingDecorationUpdates {
		g.decorationCache[update.Key] = &DecorationCacheEntry{
			LastKnownFork:   fork,
			LastKnownRev:    rev,
			LastKnownNode:   update.NodeID,
			LastKnownOffset: update.Offset,
			Tier:            CacheTierHot,
			LastAccess:      now,
		}
	}
	g.pendingDecorationUpdates = g.pendingDecorationUpdates[:0] // Clear slice, keep capacity

	// Process pending deletions - mark as "confirmed not present" at this revision
	for _, key := range g.pendingDecorationDeletes {
		if entry, exists := g.decorationCache[key]; exists {
			entry.LastKnownFork = fork
			entry.LastKnownRev = rev
			entry.LastKnownNode = 0 // 0 = confirmed not present
			entry.LastAccess = now
		}
	}
	g.pendingDecorationDeletes = g.pendingDecorationDeletes[:0] // Clear slice, keep capacity
}

// findLeafAtOffset finds the leaf node containing the given byte offset.
// Returns the leaf node and the byte offset at the start of that leaf.
func (g *Garland) findLeafAtOffset(offset int64) (*Node, int64) {
	node := g.root
	snap := node.snapshotAt(g.currentFork, g.currentRevision)
	if snap == nil {
		return nil, 0
	}

	currentOffset := int64(0)
	for !snap.isLeaf {
		leftNode := g.nodeRegistry[snap.leftID]
		leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
		if leftSnap == nil {
			return nil, 0
		}

		if offset < currentOffset+leftSnap.byteCount {
			// Go left
			node = leftNode
			snap = leftSnap
		} else {
			// Go right
			currentOffset += leftSnap.byteCount
			rightNode := g.nodeRegistry[snap.rightID]
			rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)
			if rightSnap == nil {
				return nil, 0
			}
			node = rightNode
			snap = rightSnap
		}
	}

	return node, currentOffset
}

// findDecorationByKeyInternal recursively searches for a decoration by key.
// Returns the absolute byte position and whether it was found.
func (g *Garland) findDecorationByKeyInternal(node *Node, snap *NodeSnapshot, key string, offset int64) (int64, bool) {
	if snap == nil {
		return 0, false
	}

	if snap.isLeaf {
		for _, d := range snap.decorations {
			if d.Key == key {
				return offset + d.Position, true
			}
		}
		return 0, false
	}

	// Internal node - search both children
	leftNode := g.nodeRegistry[snap.leftID]
	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)

	if pos, found := g.findDecorationByKeyInternal(leftNode, leftSnap, key, offset); found {
		return pos, true
	}

	rightNode := g.nodeRegistry[snap.rightID]
	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)

	return g.findDecorationByKeyInternal(rightNode, rightSnap, key, offset+leftSnap.byteCount)
}

// GetDecorationsInByteRange returns all decorations within [start, end).
func (g *Garland) GetDecorationsInByteRange(start, end int64) ([]DecorationEntry, error) {
	if start < 0 || end < start {
		return nil, ErrInvalidPosition
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	if start > g.totalBytes {
		return nil, ErrInvalidPosition
	}
	// Allow end up to totalBytes+1 to include EOF decorations
	if end > g.totalBytes+1 {
		end = g.totalBytes + 1
	}

	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return nil, nil
	}

	var result []DecorationEntry
	g.collectDecorationsInRangeInternal(g.root, rootSnap, start, end, 0, &result)
	return result, nil
}

// collectDecorationsInRangeInternal recursively collects decorations in the given byte range.
func (g *Garland) collectDecorationsInRangeInternal(node *Node, snap *NodeSnapshot, start, end, offset int64, result *[]DecorationEntry) {
	if snap == nil {
		return
	}

	nodeEnd := offset + snap.byteCount

	// Skip if this node is entirely outside the range
	// Use < for nodeEnd to include nodes that may have EOF decorations at nodeEnd
	if nodeEnd < start || offset >= end {
		return
	}

	if snap.isLeaf {
		for _, d := range snap.decorations {
			absPos := offset + d.Position
			if absPos >= start && absPos < end {
				addr := ByteAddress(absPos)
				*result = append(*result, DecorationEntry{
					Key:     d.Key,
					Address: &addr,
				})
			}
		}
		return
	}

	// Internal node - recurse into children
	leftNode := g.nodeRegistry[snap.leftID]
	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)

	g.collectDecorationsInRangeInternal(leftNode, leftSnap, start, end, offset, result)

	rightNode := g.nodeRegistry[snap.rightID]
	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)

	g.collectDecorationsInRangeInternal(rightNode, rightSnap, start, end, offset+leftSnap.byteCount, result)
}

// GetDecorationsOnLine returns all decorations on the specified line.
func (g *Garland) GetDecorationsOnLine(line int64) ([]DecorationEntry, error) {
	if line < 0 {
		return nil, ErrInvalidPosition
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	if line > g.totalLines {
		return nil, ErrInvalidPosition
	}

	// Find byte range for this line
	lineResult, err := g.findLeafByLineUnlocked(line, 0)
	if err != nil {
		return nil, err
	}
	lineStart := lineResult.LineByteStart

	// Find end of line (next newline or EOF)
	lineEnd := g.findLineEndUnlocked(lineStart)

	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return nil, nil
	}

	var result []DecorationEntry
	g.collectDecorationsInRangeInternal(g.root, rootSnap, lineStart, lineEnd, 0, &result)
	return result, nil
}

// findLineEndUnlocked finds the byte position of the end of the line.
// Caller must hold at least a read lock.
func (g *Garland) findLineEndUnlocked(lineStart int64) int64 {
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return lineStart
	}

	currentPos := lineStart
	totalBytes := rootSnap.byteCount

	for currentPos < totalBytes {
		leafResult, err := g.findLeafByByteUnlocked(currentPos)
		if err != nil {
			return currentPos
		}

		snap := leafResult.Snapshot

		// Search for newline in this leaf starting from our offset
		for i := leafResult.ByteOffset; i < snap.byteCount; i++ {
			if snap.data[i] == '\n' {
				return currentPos + (i - leafResult.ByteOffset) + 1 // include the newline
			}
		}

		// Move to next leaf
		currentPos += snap.byteCount - leafResult.ByteOffset
	}

	return totalBytes
}

// DumpDecorations writes all decorations to a file in INI-like format.
// If fs is nil, uses the Garland's source filesystem.
func (g *Garland) DumpDecorations(fs FileSystemInterface, path string) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return nil
	}

	// Collect all decorations
	var decorations []DecorationEntry
	g.collectDecorationsInRangeInternal(g.root, rootSnap, 0, g.totalBytes+1, 0, &decorations)

	// Build INI content
	var content string
	content = "[decorations]\n"
	for _, d := range decorations {
		if d.Address != nil {
			content += d.Key + "=" + formatInt64(d.Address.Byte) + "\n"
		}
	}

	// Use provided fs or default to sourceFS
	targetFS := fs
	if targetFS == nil {
		targetFS = g.sourceFS
	}

	// Write to file
	return targetFS.WriteFile(path, []byte(content))
}

// formatInt64 converts an int64 to a string.
func formatInt64(n int64) string {
	if n == 0 {
		return "0"
	}

	negative := n < 0
	if negative {
		n = -n
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// addressToByteUnlocked converts an AbsoluteAddress to a byte position.
// Caller must hold the lock.
func (g *Garland) addressToByteUnlocked(addr *AbsoluteAddress) (int64, error) {
	switch addr.Mode {
	case ByteMode:
		if addr.Byte < 0 || addr.Byte > g.totalBytes {
			return 0, ErrInvalidPosition
		}
		return addr.Byte, nil

	case RuneMode:
		if addr.Rune < 0 || addr.Rune > g.totalRunes {
			return 0, ErrInvalidPosition
		}
		return g.runeToByteUnlocked(addr.Rune)

	case LineRuneMode:
		if addr.Line < 0 || addr.Line > g.totalLines {
			return 0, ErrInvalidPosition
		}
		return g.lineRuneToByteUnlocked(addr.Line, addr.LineRune)

	default:
		return 0, ErrInvalidPosition
	}
}

// runeToByteUnlocked converts a rune position to a byte position.
// Caller must hold the lock.
func (g *Garland) runeToByteUnlocked(runePos int64) (int64, error) {
	if runePos == 0 {
		return 0, nil
	}

	result, err := g.findLeafByRuneUnlocked(runePos)
	if err != nil {
		return 0, err
	}

	// Absolute byte position = leaf's byte start + byte offset within leaf
	return result.LeafByteStart + result.ByteOffset, nil
}

// lineRuneToByteUnlocked converts a line:rune position to a byte position.
// Caller must hold the lock.
func (g *Garland) lineRuneToByteUnlocked(line, runeInLine int64) (int64, error) {
	result, err := g.findLeafByLineUnlocked(line, runeInLine)
	if err != nil {
		return 0, err
	}

	return result.LeafResult.LeafByteStart + result.LeafResult.ByteOffset, nil
}

// removeDecorationViaCache removes a decoration using only the cache hint.
// Returns true if the decoration was found and removed, false if cache miss (caller should use fallback).
// This is O(log n) when cache hits, and returns false when cache misses (no tree walk).
func (g *Garland) removeDecorationViaCache(key string) (bool, error) {
	// Check cache for hint
	cacheEntry, exists := g.decorationCache[key]
	if !exists || cacheEntry.LastKnownNode == 0 {
		return false, nil // No cache entry or marked as not present
	}

	// Check if cache is for current fork (different fork = definitely stale)
	if cacheEntry.LastKnownFork != g.currentFork {
		return false, nil // Wrong fork, need fallback
	}

	// Try to access the cached node directly
	node, ok := g.nodeRegistry[cacheEntry.LastKnownNode]
	if !ok {
		return false, nil // Node doesn't exist, need fallback
	}

	// Fast path: Check if node has a snapshot at the cached revision (O(1) lookup)
	// Avoid calling snapshotAt which is O(revisions) in worst case
	cachedKey := ForkRevision{cacheEntry.LastKnownFork, cacheEntry.LastKnownRev}
	snap, ok := node.history[cachedKey]
	if !ok || snap == nil || !snap.isLeaf {
		return false, nil // Node wasn't valid at cached revision, need fallback
	}

	// Look for the decoration in this leaf
	var newDecs []Decoration
	removed := false
	for _, d := range snap.decorations {
		if d.Key == key {
			removed = true
		} else {
			newDecs = append(newDecs, d)
		}
	}

	if !removed {
		return false, nil // Decoration not in this leaf, need fallback
	}

	// Create new leaf with filtered decorations
	g.nextNodeID++
	newLeaf := newNode(g.nextNodeID, g)
	g.nodeRegistry[newLeaf.id] = newLeaf
	newSnap := createLeafSnapshot(snap.data, newDecs, snap.originalFileOffset)
	newLeaf.setSnapshot(g.currentFork, g.currentRevision, newSnap)

	// Queue cache update to mark as deleted
	g.pendingDecorationDeletes = append(g.pendingDecorationDeletes, key)

	// Rebuild the path from this leaf to root
	leafResult := &LeafSearchResult{
		LeafByteStart: cacheEntry.LastKnownOffset,
	}
	newRootID, err := g.rebuildFromLeaf(leafResult, newLeaf.id)
	if err != nil {
		return false, err
	}

	g.root = g.nodeRegistry[newRootID]
	return true, nil
}

// removeDecorationDirect removes a single decoration by key using targeted lookup.
// Returns new root ID and whether the decoration was found and removed.
func (g *Garland) removeDecorationDirect(key string) (NodeID, bool, error) {
	// Use cache hint if available
	var hintOffset int64
	if cacheEntry, exists := g.decorationCache[key]; exists {
		hintOffset = cacheEntry.LastKnownOffset
	}

	// Find the decoration's location
	_, nodeID, nodeOffset, found := g.findDecorationWithHint(key, hintOffset)
	if !found {
		return g.root.id, false, nil
	}

	// Get the leaf node and its snapshot
	node, ok := g.nodeRegistry[nodeID]
	if !ok {
		return g.root.id, false, nil
	}
	snap := node.snapshotAt(g.currentFork, g.currentRevision)
	if snap == nil || !snap.isLeaf {
		return g.root.id, false, nil
	}

	// Create new decorations list without the deleted key
	var newDecs []Decoration
	removed := false
	for _, d := range snap.decorations {
		if d.Key == key {
			removed = true
		} else {
			newDecs = append(newDecs, d)
		}
	}

	if !removed {
		return g.root.id, false, nil
	}

	// Create new leaf with filtered decorations
	g.nextNodeID++
	newLeaf := newNode(g.nextNodeID, g)
	g.nodeRegistry[newLeaf.id] = newLeaf
	newSnap := createLeafSnapshot(snap.data, newDecs, snap.originalFileOffset)
	newLeaf.setSnapshot(g.currentFork, g.currentRevision, newSnap)

	// Queue cache removal
	g.pendingDecorationDeletes = append(g.pendingDecorationDeletes, key)

	// Rebuild the path from this leaf to root
	leafResult := &LeafSearchResult{
		LeafByteStart: nodeOffset,
	}
	newRootID, err := g.rebuildFromLeaf(leafResult, newLeaf.id)
	if err != nil {
		return 0, false, err
	}

	return newRootID, true, nil
}

// removeDecorationsInternal recursively walks the tree and removes decorations matching the given keys.
// Returns the new node ID and whether any changes were made.
// DEPRECATED: This is slow O(n) - use removeDecorationDirect for targeted removal.
func (g *Garland) removeDecorationsInternal(node *Node, snap *NodeSnapshot, offset int64, keys map[string]bool) (NodeID, bool, error) {
	if snap == nil {
		return node.id, false, nil
	}

	if snap.isLeaf {
		// Check if this leaf has any decorations to remove
		var newDecs []Decoration
		changed := false
		for _, d := range snap.decorations {
			if keys[d.Key] {
				changed = true
				// Skip this decoration (delete it)
			} else {
				newDecs = append(newDecs, d)
			}
		}

		if !changed {
			return node.id, false, nil
		}

		// Create new leaf with filtered decorations
		g.nextNodeID++
		newNode := newNode(g.nextNodeID, g)
		g.nodeRegistry[newNode.id] = newNode
		newSnap := createLeafSnapshot(snap.data, newDecs, snap.originalFileOffset)
		newNode.setSnapshot(g.currentFork, g.currentRevision, newSnap)
		return newNode.id, true, nil
	}

	// Internal node - recurse
	leftNode := g.nodeRegistry[snap.leftID]
	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
	rightNode := g.nodeRegistry[snap.rightID]
	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)

	newLeftID, leftChanged, err := g.removeDecorationsInternal(leftNode, leftSnap, offset, keys)
	if err != nil {
		return 0, false, err
	}

	newRightID, rightChanged, err := g.removeDecorationsInternal(rightNode, rightSnap, offset+leftSnap.byteCount, keys)
	if err != nil {
		return 0, false, err
	}

	if !leftChanged && !rightChanged {
		return node.id, false, nil
	}

	// Rebuild internal node with new children
	newID, err := g.concatenate(newLeftID, newRightID)
	if err != nil {
		return 0, false, err
	}
	return newID, true, nil
}

// addDecorationInternal adds a decoration at the given byte position.
// Returns the new root node ID.
func (g *Garland) addDecorationInternal(key string, bytePos int64) (NodeID, error) {
	// Find the leaf containing this position
	leafResult, err := g.findLeafByByteUnlocked(bytePos)
	if err != nil {
		return 0, err
	}

	// Create new decoration with position relative to the leaf
	newDec := Decoration{
		Key:      key,
		Position: leafResult.ByteOffset,
	}

	// Build new decorations list - update existing or add new
	snap := leafResult.Snapshot
	var newDecs []Decoration
	found := false
	for _, d := range snap.decorations {
		if d.Key == key {
			// Update existing decoration
			newDecs = append(newDecs, newDec)
			found = true
		} else {
			newDecs = append(newDecs, d)
		}
	}
	if !found {
		newDecs = append(newDecs, newDec)
	}

	// Create new leaf with updated decorations
	g.nextNodeID++
	newLeaf := newNode(g.nextNodeID, g)
	g.nodeRegistry[newLeaf.id] = newLeaf
	newSnap := createLeafSnapshot(snap.data, newDecs, snap.originalFileOffset)
	newLeaf.setSnapshot(g.currentFork, g.currentRevision, newSnap)

	// Queue cache update to be applied when recordMutation is called
	// Note: Offset is the absolute byte position where the leaf starts (LeafByteStart),
	// not the relative position within the leaf (ByteOffset)
	g.pendingDecorationUpdates = append(g.pendingDecorationUpdates, pendingDecorationUpdate{
		Key:    key,
		NodeID: newLeaf.id,
		Offset: leafResult.LeafByteStart,
	})

	// Rebuild the tree from this leaf up to the root
	return g.rebuildFromLeaf(leafResult, newLeaf.id)
}

// rebuildFromLeaf rebuilds the tree after a leaf has been replaced.
// Takes the original leaf search result and the new leaf's ID.
func (g *Garland) rebuildFromLeaf(leafResult *LeafSearchResult, newLeafID NodeID) (NodeID, error) {
	// Walk up from the leaf to the root, rebuilding internal nodes
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	if rootSnap == nil {
		return 0, ErrInvalidPosition
	}

	return g.rebuildFromLeafInternal(g.root, rootSnap, leafResult.LeafByteStart, 0, newLeafID)
}

// rebuildFromLeafInternal recursively rebuilds the tree path to a replaced leaf.
func (g *Garland) rebuildFromLeafInternal(node *Node, snap *NodeSnapshot, targetByteStart, offset int64, newLeafID NodeID) (NodeID, error) {
	if snap.isLeaf {
		// This is the target leaf - return the replacement
		return newLeafID, nil
	}

	leftNode := g.nodeRegistry[snap.leftID]
	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
	leftEnd := offset + leftSnap.byteCount

	if targetByteStart < leftEnd {
		// Target is in left subtree
		newLeftID, err := g.rebuildFromLeafInternal(leftNode, leftSnap, targetByteStart, offset, newLeafID)
		if err != nil {
			return 0, err
		}
		return g.concatenate(newLeftID, snap.rightID)
	}

	// Target is in right subtree
	rightNode := g.nodeRegistry[snap.rightID]
	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)

	newRightID, err := g.rebuildFromLeafInternal(rightNode, rightSnap, targetByteStart, leftEnd, newLeafID)
	if err != nil {
		return 0, err
	}
	return g.concatenate(snap.leftID, newRightID)
}

// TreeNodeInfo contains information about a single node in the tree.
type TreeNodeInfo struct {
	NodeID       NodeID
	IsLeaf       bool
	ByteCount    int64
	RuneCount    int64
	LineCount    int64
	Storage      StorageState
	DataPreview  string // First 32 chars of leaf data (for leaves only)
	LeftChildID  NodeID // For internal nodes
	RightChildID NodeID // For internal nodes
	Children     []*TreeNodeInfo
}

// GetTreeInfo returns a snapshot of the current tree structure for visualization.
func (g *Garland) GetTreeInfo() *TreeNodeInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.root == nil {
		return nil
	}

	return g.buildTreeInfo(g.root, g.currentFork, g.currentRevision)
}

// buildTreeInfo recursively builds a TreeNodeInfo from a node.
func (g *Garland) buildTreeInfo(node *Node, fork ForkID, rev RevisionID) *TreeNodeInfo {
	if node == nil {
		return nil
	}

	snap := node.snapshotAt(fork, rev)
	if snap == nil {
		return nil
	}

	info := &TreeNodeInfo{
		NodeID:    node.id,
		IsLeaf:    snap.isLeaf,
		ByteCount: snap.byteCount,
		RuneCount: snap.runeCount,
		LineCount: snap.lineCount,
		Storage:   snap.storageState,
	}

	if snap.isLeaf {
		// Create data preview (first 32 chars, escaped)
		if len(snap.data) > 0 {
			preview := string(snap.data)
			if len(preview) > 32 {
				preview = preview[:32] + "..."
			}
			// Escape special characters for display
			preview = escapeForPreview(preview)
			info.DataPreview = preview
		}
	} else {
		// Internal node - recurse into children
		info.LeftChildID = snap.leftID
		info.RightChildID = snap.rightID

		if snap.leftID != 0 {
			if leftNode := g.nodeRegistry[snap.leftID]; leftNode != nil {
				info.Children = append(info.Children, g.buildTreeInfo(leftNode, fork, rev))
			}
		}
		if snap.rightID != 0 {
			if rightNode := g.nodeRegistry[snap.rightID]; rightNode != nil {
				info.Children = append(info.Children, g.buildTreeInfo(rightNode, fork, rev))
			}
		}
	}

	return info
}

// escapeForPreview escapes special characters for display in tree output.
func escapeForPreview(s string) string {
	var result []byte
	for _, r := range s {
		switch r {
		case '\n':
			result = append(result, '\\', 'n')
		case '\r':
			result = append(result, '\\', 'r')
		case '\t':
			result = append(result, '\\', 't')
		case '\\':
			result = append(result, '\\', '\\')
		default:
			if r < 32 || r == 127 {
				// Control character - show as \xNN
				result = append(result, '\\', 'x')
				hex := "0123456789abcdef"
				result = append(result, hex[(r>>4)&0xf], hex[r&0xf])
			} else {
				result = append(result, string(r)...)
			}
		}
	}
	return string(result)
}

// SnapshotStats contains statistics about node snapshots.
type SnapshotStats struct {
	TotalSnapshots int                  // Total snapshots across all nodes
	ByFork         map[ForkID]int       // Snapshots per fork
	ByForkRevision map[ForkRevision]int // Snapshots per fork/revision
}

// GetSnapshotStats returns statistics about node snapshots.
// Useful for testing and diagnostics to verify garbage collection.
func (g *Garland) GetSnapshotStats() SnapshotStats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stats := SnapshotStats{
		ByFork:         make(map[ForkID]int),
		ByForkRevision: make(map[ForkRevision]int),
	}

	for _, node := range g.nodeRegistry {
		for forkRev := range node.history {
			stats.TotalSnapshots++
			stats.ByFork[forkRev.Fork]++
			stats.ByForkRevision[forkRev]++
		}
	}

	return stats
}

// LoadDecorations loads decorations from a file using the INI format.
// Unknown sections are ignored for future compatibility.
// Comments are lines starting with ';' or '# ' (hash followed by space).
// End-of-line comments start with whitespace followed by ';' or '#'.
func (g *Garland) LoadDecorations(fs FileSystemInterface, path string) error {
	if fs == nil {
		fs = g.sourceFS
	}
	if fs == nil {
		return ErrNoDataSource
	}

	data, err := fs.ReadFile(path)
	if err != nil {
		return err
	}

	return g.LoadDecorationsFromString(string(data))
}

// LoadDecorationsFromString loads decorations from an INI-formatted string.
// Unknown sections are ignored for future compatibility.
// Comments are lines starting with ';' or '# ' (hash followed by space).
// End-of-line comments start with whitespace followed by ';' or '#'.
func (g *Garland) LoadDecorationsFromString(content string) error {
	entries, err := parseDecorationINI(content)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		return nil
	}

	_, err = g.Decorate(entries)
	return err
}

// parseDecorationINI parses INI format decoration content.
// Returns decoration entries from the [decorations] section.
// Unknown sections are silently ignored for forward compatibility.
func parseDecorationINI(content string) ([]DecorationEntry, error) {
	var entries []DecorationEntry
	inDecorationsSection := false

	lines := splitLines(content)
	for _, line := range lines {
		// Remove end-of-line comments (whitespace followed by ; or #)
		line = stripEndOfLineComment(line)

		// Trim whitespace
		line = trimWhitespace(line)

		// Skip empty lines
		if len(line) == 0 {
			continue
		}

		// Check for full-line comments
		if isFullLineComment(line) {
			continue
		}

		// Check for section header
		if line[0] == '[' {
			sectionName := parseSectionHeader(line)
			inDecorationsSection = (sectionName == "decorations")
			continue
		}

		// Parse key=value if in decorations section
		if inDecorationsSection {
			key, value, ok := parseKeyValue(line)
			if ok {
				bytePos, err := parseInt64(value)
				if err != nil {
					// Skip malformed entries silently for robustness
					continue
				}
				addr := ByteAddress(bytePos)
				entries = append(entries, DecorationEntry{
					Key:     key,
					Address: &addr,
				})
			}
		}
		// Unknown sections are silently ignored
	}

	return entries, nil
}

// splitLines splits content into lines, handling various line endings.
func splitLines(content string) []string {
	var lines []string
	var current []byte

	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, string(current))
			current = current[:0]
		} else if content[i] == '\r' {
			lines = append(lines, string(current))
			current = current[:0]
			// Skip following \n if present (CRLF)
			if i+1 < len(content) && content[i+1] == '\n' {
				i++
			}
		} else {
			current = append(current, content[i])
		}
	}
	// Don't forget the last line if it doesn't end with newline
	if len(current) > 0 {
		lines = append(lines, string(current))
	}

	return lines
}

// stripEndOfLineComment removes end-of-line comments.
// End-of-line comments start with whitespace followed by ';' or '#'.
func stripEndOfLineComment(line string) string {
	for i := 0; i < len(line); i++ {
		// Look for whitespace
		if line[i] == ' ' || line[i] == '\t' {
			// Check if followed by comment character
			for j := i + 1; j < len(line); j++ {
				if line[j] == ' ' || line[j] == '\t' {
					continue
				}
				if line[j] == ';' || line[j] == '#' {
					return line[:i]
				}
				break
			}
		}
	}
	return line
}

// isFullLineComment checks if a line is a full-line comment.
// Comments start with ';' or '# ' (hash followed by space).
func isFullLineComment(line string) bool {
	if len(line) == 0 {
		return false
	}
	// Semicolon comments
	if line[0] == ';' {
		return true
	}
	// Hash comments require a space after (to allow #fragment bookmarks)
	if line[0] == '#' && len(line) > 1 && line[1] == ' ' {
		return true
	}
	return false
}

// parseSectionHeader extracts the section name from a line like "[section]".
func parseSectionHeader(line string) string {
	if len(line) < 2 || line[0] != '[' {
		return ""
	}
	end := -1
	for i := 1; i < len(line); i++ {
		if line[i] == ']' {
			end = i
			break
		}
	}
	if end == -1 {
		return ""
	}
	return trimWhitespace(line[1:end])
}

// parseKeyValue parses a "key=value" line.
func parseKeyValue(line string) (key, value string, ok bool) {
	eqIndex := -1
	for i := 0; i < len(line); i++ {
		if line[i] == '=' {
			eqIndex = i
			break
		}
	}
	if eqIndex == -1 {
		return "", "", false
	}
	key = trimWhitespace(line[:eqIndex])
	value = trimWhitespace(line[eqIndex+1:])
	return key, value, true
}

// trimWhitespace removes leading and trailing whitespace.
func trimWhitespace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// parseInt64 parses a string as int64.
func parseInt64(s string) (int64, error) {
	if len(s) == 0 {
		return 0, ErrInvalidPosition
	}

	negative := false
	start := 0
	if s[0] == '-' {
		negative = true
		start = 1
	} else if s[0] == '+' {
		start = 1
	}

	if start >= len(s) {
		return 0, ErrInvalidPosition
	}

	var result int64
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrInvalidPosition
		}
		result = result*10 + int64(s[i]-'0')
	}

	if negative {
		result = -result
	}
	return result, nil
}
