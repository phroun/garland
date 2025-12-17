package garland

import (
	"sync"
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

// ColdStorageInterface allows custom cold storage implementations.
type ColdStorageInterface interface {
	// Set stores data for a block within a folder.
	// Folder names are unique per loaded file.
	Set(folder, block string, data []byte) error

	// Get retrieves data for a block within a folder.
	Get(folder, block string) ([]byte, error)

	// Delete removes a block from a folder.
	Delete(folder, block string) error
}

// LibraryOptions configures the garland library.
type LibraryOptions struct {
	// ColdStoragePath is a filesystem path for cold storage.
	// Either this or ColdStorageBackend must be provided (or both).
	ColdStoragePath string

	// ColdStorageBackend is a custom cold storage implementation.
	ColdStorageBackend ColdStorageInterface
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
}

// Init initializes the garland library with cold storage options.
// Cold storage is shared across all files opened through this library instance.
func Init(options LibraryOptions) (*Library, error) {
	lib := &Library{
		coldStoragePath:    options.ColdStoragePath,
		coldStorageBackend: options.ColdStorageBackend,
		activeGarlands:     make(map[string]*Garland),
		defaultFS:          &localFileSystem{},
	}

	// If a path was provided but no backend, create a file-based backend
	if options.ColdStoragePath != "" && options.ColdStorageBackend == nil {
		lib.coldStorageBackend = newFileColdStorage(options.ColdStoragePath)
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

// DecorationCacheEntry is a hint for finding a decoration quickly.
type DecorationCacheEntry struct {
	NodeID         NodeID
	RelativeOffset int64
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

	// Tree structure
	root         *Node
	eofNode      *Node              // special node for EOF decorations
	nodeRegistry map[NodeID]*Node   // all nodes
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

	// Decoration cache (hints only)
	decorationCache map[string]*DecorationCacheEntry

	// Loading state
	loader         *Loader
	highestSeekPos int64
	mu             sync.RWMutex

	// Counts (may be incomplete during loading)
	totalBytes    int64
	totalRunes    int64
	totalLines    int64
	countComplete bool

	// File system for warm storage
	sourceFS     FileSystemInterface
	sourceHandle FileHandle

	// Optimized regions
	optimizedRegions []*OptimizedRegionHandle

	// Transaction state
	transaction *TransactionState
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

		nodeRegistry:            make(map[NodeID]*Node),
		nextNodeID:              1,
		internalNodesByChildren: make(map[[2]NodeID]NodeID),
		forks:                   make(map[ForkID]*ForkInfo),
		revisionInfo:            make(map[ForkRevision]*RevisionInfo),
		cursors:                 make([]*Cursor, 0),
		decorationCache:         make(map[string]*DecorationCacheEntry),
	}

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

	case options.DataChannel != nil:
		// Start async loading
		g.startChannelLoader(options.DataChannel)
		initialData = nil
	}

	// Build initial tree structure
	if initialData != nil {
		g.buildInitialTree(initialData)
	} else {
		// Create empty tree for async loading
		g.buildEmptyTree()
	}

	// Load initial decorations if provided
	if err := g.loadInitialDecorations(options); err != nil {
		return nil, err
	}

	// Register with library
	lib.mu.Lock()
	lib.activeGarlands[g.id] = g
	lib.mu.Unlock()

	return g, nil
}

// Close releases resources associated with the Garland.
func (g *Garland) Close() error {
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
func (g *Garland) ByteCount() CountResult {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return CountResult{
		Value:    g.totalBytes,
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
		g.rollbackToPreTransaction()
		g.transaction = nil
		return ChangeResult{}, ErrTransactionPoisoned
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
	g.revisionInfo[ForkRevision{g.currentFork, g.currentRevision}] = &RevisionInfo{
		Revision:   g.currentRevision,
		Name:       g.transaction.name,
		HasChanges: g.transaction.hasMutations,
		RootID:     g.root.id,
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
		// Outermost level: perform actual rollback
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

		// Walk back through revisions in this fork (handle uint64 underflow safely)
		if currentRev > 0 {
			currentRev--
			continue
		}

		// Reached revision 0 in this fork with no match - check parent fork
		forkInfo, ok := g.forks[currentFork]
		if !ok {
			return nil
		}

		// If this is the root fork (fork 0) or parent is itself, we're done
		if forkInfo.ParentFork == currentFork {
			return nil
		}

		// Move to parent fork at the point where this fork diverged
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
	// TODO: Implement file loading
	return nil, ErrNotSupported
}

func (g *Garland) startChannelLoader(ch chan []byte) {
	// TODO: Implement channel loading
}

func (g *Garland) buildInitialTree(data []byte) {
	// Create root node with all data
	g.nextNodeID++
	contentNode := newNode(g.nextNodeID, g)
	g.nodeRegistry[contentNode.id] = contentNode

	snap := createLeafSnapshot(data, nil, 0)
	contentNode.setSnapshot(0, 0, snap)

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

	rootSnap := createInternalSnapshot(contentNode.id, g.eofNode.id, snap, eofSnap)
	g.root.setSnapshot(0, 0, rootSnap)

	// Register the root structure for reuse
	g.internalNodesByChildren[[2]NodeID{contentNode.id, g.eofNode.id}] = g.root.id

	// Update counts
	g.totalBytes = snap.byteCount
	g.totalRunes = snap.runeCount
	g.totalLines = snap.lineCount
	g.countComplete = true

	// Record initial revision (revision 0 with the initial tree)
	g.revisionInfo[ForkRevision{0, 0}] = &RevisionInfo{
		Revision:   0,
		Name:       "(initial)",
		HasChanges: false,
		RootID:     g.root.id,
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

func (g *Garland) waitForBytePosition(pos int64) error {
	if pos < 0 {
		return ErrInvalidPosition
	}
	// TODO: Implement blocking wait for lazy loading
	if !g.countComplete && pos > g.totalBytes {
		return ErrNotReady
	}
	// After loading is complete, validate position
	if g.countComplete && pos > g.totalBytes {
		return ErrInvalidPosition
	}
	return nil
}

func (g *Garland) waitForRunePosition(pos int64) error {
	if pos < 0 {
		return ErrInvalidPosition
	}
	// TODO: Implement blocking wait for lazy loading
	if !g.countComplete && pos > g.totalRunes {
		return ErrNotReady
	}
	if g.countComplete && pos > g.totalRunes {
		return ErrInvalidPosition
	}
	return nil
}

func (g *Garland) waitForLine(line int64) error {
	if line < 0 {
		return ErrInvalidPosition
	}
	// TODO: Implement blocking wait for lazy loading
	if !g.countComplete && line > g.totalLines {
		return ErrNotReady
	}
	if g.countComplete && line > g.totalLines {
		return ErrInvalidPosition
	}
	return nil
}

// Address conversion functions

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

func (g *Garland) lineRuneToByteInternal(line, runeInLine int64) (int64, error) {
	result, err := g.findLeafByLine(line, runeInLine)
	if err != nil {
		return 0, err
	}

	return result.LeafResult.LeafByteStart + result.LeafResult.ByteOffset, nil
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
	if len(data) == 0 {
		return ChangeResult{Fork: g.currentFork, Revision: g.currentRevision}, nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Validate position
	if pos < 0 || pos > g.totalBytes {
		return ChangeResult{}, ErrInvalidPosition
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
				// Recalculate other coordinates
				cursor.runePos, _ = g.byteToRuneInternal(pos)
				cursor.line, cursor.lineRune, _ = g.byteToLineRuneInternal(pos)
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
			// Update pending revision to 1 (first revision in new fork)
			g.transaction.pendingRevision = 1
		}
		// Mark as having mutations
		g.transaction.hasMutations = true
		return ChangeResult{Fork: g.currentFork, Revision: g.transaction.pendingRevision}
	}

	// Not in transaction - check if we need to fork first
	if !g.isAtHead() {
		g.createForkFromCurrent()
		// After forking, we're at revision 0 of new fork, increment to 1
	}

	// Create new revision
	g.currentRevision++
	if forkInfo, ok := g.forks[g.currentFork]; ok {
		if g.currentRevision > forkInfo.HighestRevision {
			forkInfo.HighestRevision = g.currentRevision
		}
	}

	// Store revision info (unnamed) with current root ID
	g.revisionInfo[ForkRevision{g.currentFork, g.currentRevision}] = &RevisionInfo{
		Revision:   g.currentRevision,
		Name:       "",
		HasChanges: true,
		RootID:     g.root.id,
	}

	return ChangeResult{Fork: g.currentFork, Revision: g.currentRevision}
}

// createForkFromCurrent creates a new fork branching from the current fork/revision.
func (g *Garland) createForkFromCurrent() {
	g.nextForkID++
	newForkID := g.nextForkID

	// Create new fork info
	g.forks[newForkID] = &ForkInfo{
		ID:              newForkID,
		ParentFork:      g.currentFork,
		ParentRevision:  g.currentRevision,
		HighestRevision: 0, // will be incremented by recordMutation
	}

	// Switch to the new fork
	g.currentFork = newForkID
	g.currentRevision = 0

	// Update cursor tracking
	for _, cursor := range g.cursors {
		cursor.lastFork = newForkID
		cursor.lastRevision = 0
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

	g.mu.RLock()
	defer g.mu.RUnlock()

	if pos > g.totalBytes {
		return nil, ErrInvalidPosition
	}

	// Clamp length to available data
	if pos+length > g.totalBytes {
		length = g.totalBytes - pos
	}

	return g.readBytesRangeInternal(pos, length)
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
// Caller must hold at least read lock.
func (g *Garland) readBytesRangeInternal(pos int64, length int64) ([]byte, error) {
	if length <= 0 {
		return nil, nil
	}

	result := make([]byte, 0, length)
	remaining := length
	currentPos := pos

	for remaining > 0 {
		leafResult, err := g.findLeafByByteUnlocked(currentPos)
		if err != nil {
			return nil, err
		}

		snap := leafResult.Snapshot

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

	// Process deletions first: find and remove decorations by key
	if len(deletions) > 0 {
		keySet := make(map[string]bool)
		for _, key := range deletions {
			keySet[key] = true
		}

		// Walk all leaves and remove matching decorations
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

// removeDecorationsInternal recursively walks the tree and removes decorations matching the given keys.
// Returns the new node ID and whether any changes were made.
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
