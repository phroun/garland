# Garland Interface Reference

This document defines the complete user-facing API for the Garland library.

## Overview

Garland is a rope-based data structure for efficient text/binary editing with:
- Multiple storage tiers (memory, warm/original file, cold storage)
- Full undo/redo with fork support
- Decorations (annotations) attached to byte positions
- Multiple cursor support
- Lazy loading with configurable thresholds
- Three addressing modes (bytes, runes, line:rune pairs)

---

## Library Initialization

```go
package garland

// Init initializes the garland library with cold storage options.
// Cold storage is shared across all files opened through this library instance.
func Init(options LibraryOptions) (*Library, error)

type LibraryOptions struct {
    // ColdStoragePath is a filesystem path for cold storage.
    // Either this or ColdStorageBackend must be provided (or both).
    ColdStoragePath string

    // ColdStorageBackend is a custom cold storage implementation.
    ColdStorageBackend ColdStorageInterface
}

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
```

---

## File System Abstraction

```go
// FileSystemInterface abstracts file operations for custom protocols.
// The library provides a default implementation for local files.
type FileSystemInterface interface {
    // Required methods
    Open(name string, mode OpenMode) (FileHandle, error)
    SeekByte(handle FileHandle, pos int64) error
    ReadBytes(handle FileHandle, length int) ([]byte, error)
    IsEOF(handle FileHandle) bool
    Close(handle FileHandle) error

    // Optional methods (may return ErrNotSupported)
    HasChanged(handle FileHandle) (bool, error)
    FileSize(handle FileHandle) (int64, error)
    BlockChecksum(handle FileHandle, start, length int64) ([]byte, error)
    WriteBytes(handle FileHandle, data []byte) error
    Truncate(handle FileHandle, size int64) error
}

type OpenMode int

const (
    OpenModeRead OpenMode = iota
    OpenModeWrite
    OpenModeReadWrite
)

type FileHandle interface{}
```

---

## File Operations

```go
// Open creates or loads a Garland from various sources.
func (lib *Library) Open(options FileOptions) (*Garland, error)

type FileOptions struct {
    // Loading style determines storage tier availability
    LoadingStyle LoadingStyle

    // Data source (exactly one must be provided)
    FilePath    string               // load from file path using default FS
    FileSystem  FileSystemInterface  // custom file system + path
    DataBytes   []byte               // literal byte content
    DataString  string               // literal string content
    DataChannel chan []byte          // streaming input

    // Initial decorations (optional, at most one)
    Decorations      []DecorationEntry  // literal list
    DecorationChan   chan DecorationEntry
    DecorationPath   string             // load from dump file
    DecorationString string             // parse from dump format

    // Ready thresholds - ALL specified (non-zero) must be met
    // Measured from beginning of file at initial load
    ReadyLines int64
    ReadyBytes int64
    ReadyRunes int64
    ReadyAll   bool // only ready when entire file processed

    // Lazy read-ahead - ALL specified (non-zero) must be met
    // Measured from highest seek position after any seek
    ReadAheadLines int64
    ReadAheadBytes int64
    ReadAheadRunes int64
    ReadAheadAll   bool // read entire file as soon as performant
}

type LoadingStyle int

const (
    // AllStorage allows memory, warm (original file), and cold storage.
    // Warm storage requires original file to be unchanged.
    AllStorage LoadingStyle = iota

    // ColdAndMemory prevents warm storage, only memory and cold.
    ColdAndMemory

    // MemoryOnly keeps everything in memory, no external storage.
    MemoryOnly
)

// Close releases resources associated with the Garland.
func (g *Garland) Close() error

// Save overwrites the original file.
// Caller asserts that this replaces any warm storage source.
func (g *Garland) Save() error

// SaveAs writes to a new location. Warm storage remains untouched.
func (g *Garland) SaveAs(fs FileSystemInterface, name string) error
```

---

## Cursors

```go
// NewCursor creates a new cursor at position 0.
func (g *Garland) NewCursor() *Cursor

// RemoveCursor removes a cursor from the Garland.
func (g *Garland) RemoveCursor(c *Cursor) error

// Cursor represents a position within a Garland with its own ready state.
type Cursor struct {
    // ... internal fields
}

// Movement methods - block until position is available

// SeekByte moves cursor to an absolute byte position.
func (c *Cursor) SeekByte(pos int64) error

// SeekRune moves cursor to an absolute rune position.
func (c *Cursor) SeekRune(pos int64) error

// SeekLine moves cursor to a line and rune-within-line position.
// Line and rune are both 0-indexed. Newline is the last character of its line.
func (c *Cursor) SeekLine(line, runeInLine int64) error

// Position queries

// BytePos returns the cursor's absolute byte position.
func (c *Cursor) BytePos() int64

// RunePos returns the cursor's absolute rune position.
func (c *Cursor) RunePos() int64

// LinePos returns the cursor's line number and rune position within that line.
func (c *Cursor) LinePos() (line, runeInLine int64)

// Ready state

// IsReady returns true if the read-ahead threshold has been met
// relative to this cursor's position.
func (c *Cursor) IsReady() bool

// WaitReady blocks until the cursor becomes ready.
func (c *Cursor) WaitReady() error
```

---

## Content Operations

All content operations occur at the cursor's current position.

```go
// ChangeResult contains version information after a mutation.
type ChangeResult struct {
    Fork     ForkID
    Revision RevisionID
}

// RelativeDecoration specifies a decoration relative to an insert point.
// Position semantics:
//   < 0      : attach after the newline preceding the insert point
//   0 to Len : attach to the byte/rune at that offset in inserted content
//   Len + 1  : attach before the newline following the insert point
//   > Len + 1: attach after the newline following the insert point
type RelativeDecoration struct {
    Key      string
    Position int64
}
```

### Insert Operations

```go
// InsertBytes inserts raw bytes at the cursor position.
// If insertBefore is true, insertion occurs before any existing
// cursors/decorations at this position; otherwise after.
func (c *Cursor) InsertBytes(data []byte, decorations []RelativeDecoration,
    insertBefore bool) (ChangeResult, error)

// InsertString inserts a string at the cursor position.
// Relative decoration positions are measured in runes.
func (c *Cursor) InsertString(data string, decorations []RelativeDecoration,
    insertBefore bool) (ChangeResult, error)
```

### Delete Operations

```go
// DeleteBytes deletes `length` bytes starting at cursor position.
// Returns decorations from the deleted range.
// If includeLineDecorations is true, also returns (but does not move)
// decorations from partially affected lines.
func (c *Cursor) DeleteBytes(length int64, includeLineDecorations bool) (
    []RelativeDecoration, ChangeResult, error)

// DeleteRunes deletes `length` runes starting at cursor position.
func (c *Cursor) DeleteRunes(length int64, includeLineDecorations bool) (
    []RelativeDecoration, ChangeResult, error)

// TruncateToEOF deletes everything from cursor position to end of file.
func (c *Cursor) TruncateToEOF() (ChangeResult, error)
```

### Read Operations

```go
// ReadBytes reads `length` bytes starting at cursor position.
func (c *Cursor) ReadBytes(length int64) ([]byte, error)

// ReadString reads `length` runes starting at cursor position as a string.
func (c *Cursor) ReadString(length int64) (string, error)

// ReadLine reads the entire line the cursor is on.
func (c *Cursor) ReadLine() (string, error)
```

---

## Decorations

Decorations are named markers attached to byte positions.

```go
// DecorationEntry represents a decoration with its position.
type DecorationEntry struct {
    Key     string
    Address *AbsoluteAddress // nil to delete the decoration
}

// AbsoluteAddress specifies a position using one of three addressing modes.
type AbsoluteAddress struct {
    Mode AddressMode

    // For ByteMode
    Byte int64

    // For RuneMode
    Rune int64

    // For LineRuneMode
    Line     int64
    LineRune int64
}

type AddressMode int

const (
    ByteMode     AddressMode = iota // absolute byte position
    RuneMode                        // absolute rune position
    LineRuneMode                    // line and rune within line
)

// Decorate adds, updates, or removes decorations.
// Pass nil Address in a DecorationEntry to delete that decoration.
func (g *Garland) Decorate(entries []DecorationEntry) (ChangeResult, error)

// GetDecorationPosition returns the current position of a decoration.
func (g *Garland) GetDecorationPosition(key string) (AbsoluteAddress, error)

// GetDecorationsInByteRange returns all decorations within [start, end).
func (g *Garland) GetDecorationsInByteRange(start, end int64) ([]DecorationEntry, error)

// GetDecorationsOnLine returns all decorations on the specified line.
func (g *Garland) GetDecorationsOnLine(line int64) ([]DecorationEntry, error)

// DumpDecorations writes all decorations to a file in INI-like format.
func (g *Garland) DumpDecorations(path string) error
```

---

## Versioning (Undo/Redo with Forks)

```go
type ForkID uint64
type RevisionID uint64

// CurrentFork returns the current fork ID.
func (g *Garland) CurrentFork() ForkID

// CurrentRevision returns the current revision number within the current fork.
func (g *Garland) CurrentRevision() RevisionID

// UndoSeek navigates to a specific revision within the current fork.
// Cannot seek forward past the highest revision in this fork.
// Seeking backwards then making a change creates a new fork.
func (g *Garland) UndoSeek(revision RevisionID) error

// ForkSeek switches to a different fork.
// Retains current revision if it exists in both forks,
// otherwise retreats to the last common revision.
func (g *Garland) ForkSeek(fork ForkID) error

// GetForkInfo returns information about a specific fork.
func (g *Garland) GetForkInfo(fork ForkID) (*ForkInfo, error)

type ForkInfo struct {
    ID              ForkID
    ParentFork      ForkID
    HighestRevision RevisionID
}

// ForkDivergence describes where a fork split occurred.
type ForkDivergence struct {
    Fork          ForkID
    DivergenceRev RevisionID          // revision at which split occurred
    Direction     DivergenceDirection // relationship to current fork
}

type DivergenceDirection int

const (
    // BranchedFrom means current fork split off from this fork
    BranchedFrom DivergenceDirection = iota
    // BranchedInto means this fork split off from current fork
    BranchedInto
)

// FindForksBetween returns all fork divergence points between two revisions
// from the perspective of the current fork.
func (g *Garland) FindForksBetween(revisionFirst, revisionLast RevisionID) (
    []ForkDivergence, error)
```

---

## Transactions

Transactions group multiple operations into a single revision.

```go
// TransactionStart begins a new transaction.
// All mutations until commit/rollback are bundled into one pending revision.
// Transactions can be nested; the outermost commit creates the revision.
// UndoSeek and ForkSeek are not allowed while a transaction is pending.
func (g *Garland) TransactionStart() error

// TransactionCommit commits the current transaction.
// For nested transactions, only the outermost commit creates a revision.
// Returns ErrTransactionPoisoned if any inner transaction was rolled back.
func (g *Garland) TransactionCommit() (ChangeResult, error)

// TransactionRollback discards all changes in the current transaction.
// For nested transactions, this "poisons" all outer transactions,
// causing their commits to automatically rollback.
func (g *Garland) TransactionRollback() error

// TransactionDepth returns the current nesting depth (0 = no active transaction).
func (g *Garland) TransactionDepth() int

// InTransaction returns true if any transaction is active.
func (g *Garland) InTransaction() bool
```

### Transaction Behavior

**Nesting Rules:**
- `TransactionStart` increments the nesting depth
- `TransactionCommit` decrements the nesting depth; revision created only at depth 0→0
- `TransactionRollback` poisons the transaction and decrements depth

**Poisoned Transactions:**
- Once any inner `TransactionRollback` is called, the transaction is poisoned
- All subsequent `TransactionCommit` calls at any depth will rollback instead
- The entire outer transaction's changes are discarded

**Restrictions During Transactions:**
- `UndoSeek` returns `ErrTransactionPending`
- `ForkSeek` returns `ErrTransactionPending`
- Optimized region `CommitSnapshot` is deferred until transaction commit

**Example Usage:**
```go
g.TransactionStart()

cursor.SeekByte(100)
cursor.DeleteBytes(50, false)  // no revision yet

cursor.SeekByte(200)
cursor.InsertString("replacement", nil, true)  // still no revision

result, err := g.TransactionCommit()  // single revision for both operations
// result.Revision is the new revision number
```

**Nested Example:**
```go
g.TransactionStart()  // depth 1
cursor.InsertString("outer", nil, true)

    g.TransactionStart()  // depth 2
    cursor.InsertString("inner", nil, true)

    if somethingWentWrong {
        g.TransactionRollback()  // poisons entire transaction
    } else {
        g.TransactionCommit()  // depth back to 1, no revision yet
    }

result, err := g.TransactionCommit()  // if poisoned: rolls back, err = ErrTransactionPoisoned
                                       // if not poisoned: creates single revision
```

---

## Counts and Status

```go
// CountResult contains a count and whether it is complete.
type CountResult struct {
    Value    int64
    Complete bool // true if EOF has been reached
}

// ByteCount returns total bytes (or known bytes if still loading).
func (g *Garland) ByteCount() CountResult

// RuneCount returns total runes (or known runes if still loading).
func (g *Garland) RuneCount() CountResult

// LineCount returns total newlines (or known newlines if still loading).
func (g *Garland) LineCount() CountResult

// IsComplete returns true if EOF has been reached during loading.
func (g *Garland) IsComplete() bool

// IsReady returns true if initial ready threshold has been met.
func (g *Garland) IsReady() bool
```

---

## Address Conversion

```go
// ByteToRune converts a byte position to a rune position.
func (g *Garland) ByteToRune(bytePos int64) (int64, error)

// RuneToByte converts a rune position to a byte position.
func (g *Garland) RuneToByte(runePos int64) (int64, error)

// LineRuneToByte converts a line:rune position to a byte position.
func (g *Garland) LineRuneToByte(line, runeInLine int64) (int64, error)

// ByteToLineRune converts a byte position to a line:rune position.
func (g *Garland) ByteToLineRune(bytePos int64) (line, runeInLine int64, err error)
```

---

## Optimized Regions

Optimized regions provide high-performance editing for hot zones.

```go
// OptimizedRegion is implemented by high-performance editing zones.
type OptimizedRegion interface {
    // Counts
    ByteCount() int64
    RuneCount() int64
    LineCount() int64

    // Operations (offset is relative to region start)
    InsertBytes(offset int64, data []byte, decorations []RelativeDecoration,
        insertBefore bool) error
    DeleteBytes(offset, length int64) ([]RelativeDecoration, error)
    ReadBytes(offset, length int64) ([]byte, error)

    // Versioning
    CommitSnapshot() (RevisionID, error) // called before UndoSeek
    RevertTo(revision RevisionID) error

    // Dissolution back to tree structure
    Dissolve() (data []byte, decorations []Decoration, err error)
}

// OptimizedRegionHandle provides access to an active optimized region.
type OptimizedRegionHandle struct {
    // ... internal fields
}

// StartByte returns the starting byte position of the region.
func (h *OptimizedRegionHandle) StartByte() int64

// EndByte returns the ending byte position of the region (exclusive).
func (h *OptimizedRegionHandle) EndByte() int64

// Region returns the underlying OptimizedRegion implementation.
func (h *OptimizedRegionHandle) Region() OptimizedRegion

// CreateOptimizedRegion creates a new optimized region.
// Returns error if the range overlaps an existing region.
func (g *Garland) CreateOptimizedRegion(start, length int64) (
    *OptimizedRegionHandle, error)

// CreateOptimizedRegionWith creates a region with a custom implementation.
func (g *Garland) CreateOptimizedRegionWith(start, length int64,
    factory func(data []byte, decorations []Decoration) OptimizedRegion) (
    *OptimizedRegionHandle, error)

// GetOptimizedRegionAt returns the region containing a position, if any.
func (g *Garland) GetOptimizedRegionAt(pos int64) (*OptimizedRegionHandle, bool)

// DissolveOptimizedRegion converts a region back to normal tree structure.
func (g *Garland) DissolveOptimizedRegion(region *OptimizedRegionHandle) (
    ChangeResult, error)
```

---

## Errors

```go
var (
    // Position errors
    ErrNotReady        = errors.New("position not yet available")
    ErrInvalidPosition = errors.New("position out of bounds")

    // Decoration errors
    ErrDecorationNotFound = errors.New("decoration not found")

    // Versioning errors
    ErrForkNotFound     = errors.New("fork not found")
    ErrRevisionNotFound = errors.New("revision not found")

    // Storage errors
    ErrColdStorageFailure  = errors.New("cold storage operation failed")
    ErrWarmStorageMismatch = errors.New("warm storage checksum mismatch")
    ErrReadOnly            = errors.New("region is read-only due to storage failure")

    // File system errors
    ErrNotSupported = errors.New("operation not supported")

    // Region errors
    ErrRegionOverlap = errors.New("optimized regions cannot overlap")

    // Transaction errors
    ErrTransactionPending  = errors.New("operation not allowed during transaction")
    ErrTransactionPoisoned = errors.New("transaction was poisoned by inner rollback")
    ErrNoTransaction       = errors.New("no active transaction")
)
```

---

## Decoration Dump File Format

The decoration dump file uses an INI-like format:

```ini
[decorations]
bookmark-1=4521
error-marker=8930
cursor-main=12050
```

Positions are stored as absolute byte addresses.
