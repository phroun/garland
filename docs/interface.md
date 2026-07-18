# Garland Interface Reference

This document defines the complete user-facing API for the Garland library.

## Concurrency Contract

Any goroutine may call any public API concurrently. All operations are
linearized through the buffer's internal lock: pure metadata getters
(counts, positions, fork/revision, status, IntegrityEvents,
MemoryPressure) run in parallel under a read lock; everything that can
load storage tiers or mutate (edits, seeks, reads that may thaw,
searches, decorations) serializes under the write lock - at sub-
millisecond per operation this costs nothing measurable. Each Cursor
may be used by ONE goroutine at a time (edits from other goroutines
still adjust it safely). The long operation - save - runs without the
lock via SaveOptions.Concurrent. Position errors (ErrInvalidPosition
etc.) are normal when the buffer shrinks under a racing reader; the
caller coordinates its own read-modify-write sequences. Cold-storage
block writes are atomic (write + rename), so concurrent block reads
never tear.

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

    // Convenience / directory operations
    WriteFile(name string, data []byte) error
    ReadFile(name string) ([]byte, error)
    MkdirAll(path string) error
    Remove(name string) error
    Rmdir(path string) error
    Rename(oldpath, newpath string) error // atomic replace (cold storage relies on it)

    // Metadata hooks (may return ErrNotSupported). Stat reports a
    // missing file as FileMetadata{Exists:false} with a NIL error;
    // errors are for real failures. A VFS without metadata support
    // still works - Garland then tracks whatever the app volunteers
    // via ReportSourceMetadata.
    Stat(name string) (FileMetadata, error)
    DeviceInfo(name string) (DeviceInfo, error)
}

// FileMetadata is what Garland tracks per source file to detect
// external modification (captured at EVERY file open - memory, warm,
// or cold mode - and re-baselined at each save/adoption).
type FileMetadata struct {
    Exists   bool
    Size     int64
    ModTime  time.Time
    Identity string // storage-object identity ("dev:ino" locally); "" = unknown
}

// DeviceInfo describes the device/volume behind a path, for free-space
// warnings and same-device recognition (removable media vs. working drive).
type DeviceInfo struct {
    DeviceID   string // "" = unknown
    FreeBytes  int64  // -1 = unknown
    TotalBytes int64  // -1 = unknown
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

    // UseEmacsLocks (opt-in, file sources only): maintain an
    // emacs-compatible ".#<name>" lock file while the buffer holds
    // unsaved modifications. See "Source Metadata & Consistency".
    UseEmacsLocks bool

    // LockOwner overrides the identity written inside the lock file
    // (default: environment-derived "user@host.pid"; follow that form
    // for emacs interoperability). Single line, trimmed.
    LockOwner string
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

// Save overwrites the original file IN PLACE (no temp copy; the file
// shrinks only as the final step) and preserves undo history. Warm
// storage survives, re-homed to the new layout.
func (g *Garland) Save() (SaveReport, error)

// SaveWith is Save with explicit options (e.g. PreserveHistory=false).
func (g *Garland) SaveWith(opts SaveOptions) (SaveReport, error)

// SaveAs streams the document to a new location (leaf-by-leaf, no
// full-buffer materialization). Warm storage remains untouched.
// Saving onto the original path routes through the in-place engine.
// A nil fs resolves like Save/SaveWith - the buffer's source
// filesystem, else the library default (local disk) - so a host can
// stream a save-as without hand-rolling a FileSystemInterface.
// Equivalent to SaveAsWith(fs, name, SaveAsOptions{}).
func (g *Garland) SaveAs(fs FileSystemInterface, name string) (SaveReport, error)

// SaveAsWith adds control over whether the destination becomes the
// buffer's new source. AdoptAsSource=true: warm references re-home
// onto the new file, change detection re-baselines against it, future
// Save calls write there ("the file lives here now"). false: export /
// removable-media case - the buffer keeps working from its original
// source. PreserveHistory (adoption only) migrates old-source-backed
// undo history off the abandoned source first.
func (g *Garland) SaveAsWith(fs FileSystemInterface, name string, opts SaveAsOptions) (SaveReport, error)

type SaveAsOptions struct {
    AdoptAsSource   bool
    PreserveHistory bool
}

// NewLocalFileSystem returns a FileSystemInterface backed by the real
// OS filesystem (the default Garland uses). For hosts that want to
// pass an explicit fs to SaveAs, or wrap/delegate to local disk when
// building a custom FileSystemInterface.
func NewLocalFileSystem() FileSystemInterface

// DefaultFS returns the filesystem this library uses for local-disk
// operations.
func (lib *Library) DefaultFS() FileSystemInterface

// SaveOptions configures Save behavior.
type SaveOptions struct {
    // PreserveHistory migrates warm-backed undo history that the
    // rewrite would overwrite into cold storage first. When false,
    // overwritten history becomes placeholders on access (amputated,
    // never silently corrupted).
    PreserveHistory bool

    // Concurrent (opt-in) runs the rewrite WITHOUT holding the buffer
    // lock: the app may keep reading/editing on its op goroutine while
    // the save writes (only Prune, DeleteFork, Rebase, Close, and
    // other saves wait). Cost: displaced warm spans are EVACUATED
    // first (cold storage if available, else memory); with no cold
    // backend and a hard memory limit the save transparently falls
    // back to the locked zero-copy path.
    Concurrent bool
}

// MemoryPressure reports the buffer's memory standing - the signal
// for an app-side "hot-write mode": when SaveableBytes dominates and
// ResidentBytes approaches the hard limit, saving is the only relief
// ("to keep editing, I need to save before I run out of RAM").
func (g *Garland) MemoryPressure() MemoryPressureInfo

type MemoryPressureInfo struct {
    ResidentBytes  int64 // leaf bytes currently in memory
    EvictableBytes int64 // chillable to warm/cold right now
    SaveableBytes  int64 // only a Save can make these evictable
    SoftLimitBytes int64 // configured limits (0 = none)
    HardLimitBytes int64
}

// A save NEVER refuses because data was lost to a storage failure:
// lost blocks are written as visible, exact-size scars and reported
// back so the app decides how to deal with them.
type SaveReport struct {
    Scars      []ScarWarning    // empty on a clean save
    Integrity  []IntegrityEvent // block-level events since the last save
    Concurrent bool             // whether the save actually ran lock-free
}

type ScarWarning struct {
    Offset   int64  // byte offset of the scarred block in the saved content
    Length   int64  // byte count of the lost block
    Marker   string // human-readable marker text written into the file
    Appended bool   // marker did not fit in the block; appended at EOF
    Reason   string // why the data was lost, captured at discovery time
}

// Block-level integrity forensics: when a warm block's bytes on disk
// stop matching expectations, the mismatch is triaged before being
// declared a loss - slide (external insert/delete shifted the block;
// re-homed), swap (external move; backing offsets exchanged), soft
// adopt (surrounding blocks verify, so the file's bytes are adopted as
// a deliberate external edit - a future save preserves them), or hard
// loss (placeholder, scarred on save). Every outcome is recorded at
// the moment of discovery.
type IntegrityKind int

const (
    IntegrityBlockSlid             IntegrityKind = iota // recovered: re-homed
    IntegrityBlockSwapped                               // recovered: external move
    IntegrityBlockAdopted                               // soft: external edit adopted
    IntegrityBlockAdoptedDuplicate                      // soft: adopted; duplicates another block (move/copy suspected)
    IntegrityBlockLost                                  // hard: placeholder, will scar
    IntegrityDecorationsLost                            // marks lost on thaw; content intact
)

// Decoration keys are IDENTIFIERS (RULING): non-empty ASCII letters,
// digits, '_', '.', '#', '-' only. Every write-side API rejects other
// characters with ErrInvalidDecorationKey, which keeps the cold-storage
// .dec encoding framing-safe by construction. Failure to restore a
// block's decorations on thaw (side block missing / hash mismatch /
// corrupt encoding) is reported as an IntegrityDecorationsLost event -
// content thaws fine, marks never vanish silently.
func ValidDecorationKey(key string) bool

type IntegrityEvent struct {
    Kind         IntegrityKind
    BufferOffset int64  // block's buffer offset at discovery (-1 unknown)
    FileOffset   int64  // backing position in the source file
    Length       int64
    Detail       string // specifics: shift distance, duplicate location, cause
}

// IntegrityEvents peeks at events accumulated since the last
// successful save; the save itself drains them into SaveReport.Integrity.
func (g *Garland) IntegrityEvents() []IntegrityEvent

// Rebase: deliberate reconciliation against a file ("file changed on
// disk - reload or keep your version?" -> rebase takes the FILE as
// the new base). Blocks are anchor-matched by hash (piecewise shifts
// followed), keeping identity/decorations/warm backing for matched
// content; placeholders whose bytes still exist in the file heal;
// everything else (external edits, resized blocks - kind
// IntegrityBlockResized points here - and unsaved local edits: the
// disk wins) is adopted and reported. One recorded mutation:
// UndoSeek(report.PreviousRevision) is "keep your version" after the
// fact. Source tracking is re-baselined - a fresh starting point.
func (g *Garland) RebaseOnSource() (RebaseReport, error)

// RebaseOn does the same against a DIFFERENT file, which becomes the
// buffer's source (path, handle, warm backing all switch).
func (g *Garland) RebaseOnFile(fs FileSystemInterface, name string) (RebaseReport, error)

type RebaseReport struct {
    Adopted          []RebaseRegion // regions taken from the file
    BytesKept        int64
    BytesAdopted     int64
    BlocksKept       int
    BlocksHealed     int   // lost placeholders recovered from the file
    OldSize, NewSize int64
    NoChange         bool  // already identical; nothing recorded
    PreviousRevision RevisionID // undo target for "keep your version"
}

type RebaseRegion struct {
    Offset int64 // in the new buffer (== file offset)
    Length int64
}
```

---

## Source Metadata & Consistency

Every file open captures the source's metadata (size, mtime, identity)
through the filesystem hook, whatever the loading style. The app can
ask at any time whether another program touched the file - before a
save, on window focus, on a timer - and drive its UI from the answer
(save silently / prompt to overwrite / fork to a copy / merge /
abandon and reload).

```go
// Pull style: stat through the hook (VFS-aware), classify, report.
// With a metadata-less hook it answers from the last volunteered
// observation. Re-probes the emacs lock file when locks are enabled.
func (g *Garland) SourceConsistency() (SourceConsistencyReport, error)

// No I/O: answer from tracked state only (safe on a paint path).
func (g *Garland) SourceConsistencyCached() SourceConsistencyReport

// Push style: the app volunteers fresh facts (its own watcher, a sync
// client, a VFS event). Recorded exactly as a stat result would be.
// With no baseline yet, the first observation BECOMES the baseline.
func (g *Garland) ReportSourceMetadata(meta FileMetadata) SourceConsistencyState

type SourceConsistencyState int

const (
    ConsistencyUntracked SourceConsistencyState = iota // no baseline available
    ConsistencyClean
    ConsistencyAppended  // grew; existing content may be intact
    ConsistencyModified  // same size, mtime changed
    ConsistencyTruncated
    ConsistencyReplaced  // path re-bound to a different storage object
    ConsistencyMissing
)

type SourceConsistencyReport struct {
    State      SourceConsistencyState
    Baseline   FileMetadata // as of last agreement (open/save/adoption)
    Observed   FileMetadata // most recent observation
    ObservedAt time.Time
    LockedBy   string // foreign emacs-lock owner, "" when none known
}
```

### Save history & revert

Every successful save records a SavePoint: path, metadata as written,
and the fork/revision the save captured. This anchors "revert to last
saved version" as a pure history seek, and source recovery below.

```go
func (g *Garland) SaveHistory() []SavePoint       // oldest first, bounded (8)
func (g *Garland) LastSave() (SavePoint, bool)
func (g *Garland) RevertToLastSave() error        // ForkSeek+UndoSeek to the save point;
                                                  // abandoned edits stay reachable as redo
                                                  // (prune from the save point to discard)

type SavePoint struct {
    Fork            ForkID
    Revision        RevisionID
    Path            string
    Meta            FileMetadata // observed right after the save
    SavedAt         time.Time
    AdoptedAsSource bool
}
```

### Source switching & recovery

```go
// AdoptWarmSource switches the warm-storage backing to another file
// believed to hold exactly the current content - with the cheapest
// sufficient check. Current leaves re-home onto the new file; history
// backed by the old source migrates off it (unreadable blocks are
// marked lost with a reason - the old source may be corrupt, which is
// WHY it is being abandoned); change detection re-baselines.
func (g *Garland) AdoptWarmSource(fs FileSystemInterface, name string, level VerifyLevel) error

type VerifyLevel int

const (
    VerifyMetadata VerifyLevel = iota // swift: a SavePoint at the current
                                      // fork/revision for that path, with
                                      // matching file metadata
    VerifySample                      // + hash-check a bounded sample of spans
    VerifyFull                        // + hash-check every span
)

// TryRecoverSource walks save history newest-first and adopts the
// first alternate location that verifies - automatic recovery when
// the current source becomes corrupt or unreachable.
func (g *Garland) TryRecoverSource(level VerifyLevel) (SavePoint, error) // ErrNoRecoverySource
```

### Emacs-compatible file locks (opt-in)

With `FileOptions.UseEmacsLocks`, Garland maintains an emacs-style
`.#<name>` lock file next to the source for exactly as long as the
buffer holds unsaved modifications: acquired on the first mutation
past a clean point, released on save / revert-to-saved / undo onto
the saved revision / Close. The lock is a regular file containing
"user@host.pid" (identity overridable via FileOptions.LockOwner),
written through the filesystem hook (VFS-portable; emacs reads this
form as well as its symlink form). A foreign lock is NEVER clobbered -
it is recorded and reported; the app decides.

```go
func (g *Garland) HoldsSourceLock() bool
func (g *Garland) SourceLockOwner() (owner string, foreign bool) // last observed
func (g *Garland) BreakSourceLock() error // steal deliberately; re-acquires if dirty
```

### Device information

```go
// Free space / device identity for a path - warn before a save that
// may not fit, or recognize removable media. Nil fs = library default.
func (lib *Library) DeviceInfoFor(fs FileSystemInterface, name string) (DeviceInfo, error)
func (g *Garland) SourceDeviceInfo() (DeviceInfo, error)
```

### Pre-session backups

The app names a backup location per garland; on the FIRST mutation a
background thread streams the source file's pre-session content there,
so the backup is already in place before Save is pressed. A save that
races ahead of the background copy performs it inline first - the
backup ALWAYS holds pre-overwrite content. Only an in-place save of
the protected file commits the backup (keeps it past Close); merely
viewing never creates one, and an uncommitted backup is removed at
Close, so browsing files does not accumulate backup storage.

```go
// Nil fs = library default. Empty dir disables (removing an
// uncommitted backup). Configuring on an already-dirty buffer
// captures immediately. One capture per configuration.
func (g *Garland) SetBackupLocation(fs FileSystemInterface, dir string, opts BackupOptions) error

type BackupOptions struct {
    Name string // backup filename; default "<source basename>~"
}

// Cheap status query (safe on a paint path).
func (g *Garland) BackupInfo() BackupInfo

type BackupInfo struct {
    State   BackupState
    Path    string // destination backup file
    Subject string // the source file it captures
    Bytes   int64
    Err     string // when State == BackupFailed
}

type BackupState int

const (
    BackupDisabled  BackupState = iota
    BackupArmed     // configured; no modification yet
    BackupPending   // copy armed by a mutation, not finished
    BackupReady     // in place at the destination
    BackupCommitted // a save overwrote the subject; kept past Close
    BackupFailed    // copy failed; saves proceed regardless
)
```

---

## Cursors

```go
// NewCursor creates a new cursor at position 0.
func (g *Garland) NewCursor() *Cursor            // tracked through undo/redo
func (g *Garland) NewEphemeralCursor() *Cursor  // no per-revision history

// Cursor history: a tracked cursor records its position per revision and
// is restored on undo/redo/fork navigation. An ephemeral cursor (paint
// caret, maintenance scan, transient search) still adjusts to edits but
// keeps no history and is never teleported to a historical position -
// toggle with SetTracksHistory / query with TracksHistory.
func (c *Cursor) SetTracksHistory(track bool)
func (c *Cursor) TracksHistory() bool

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
// A runeInLine past the line's rune count migrates forward into the
// following line(s), bounded at end-of-buffer (the final line past a
// trailing newline is reachable). LinePos() afterward reports the
// ACTUAL resolved line:rune, always consistent with BytePos().
func (c *Cursor) SeekLine(line, runeInLine int64) error

// SeekByWord moves by n words (negative = backward) using
// WordStyleSimple; returns how many words were actually moved.
func (c *Cursor) SeekByWord(n int) (int, error)

// SeekByWordStyle selects the word semantics per call.
func (c *Cursor) SeekByWordStyle(n int, style WordStyle) (int, error)

type WordStyle int

const (
    // WordStyleSimple: words are runs of letters/digits/underscore;
    // punctuation and whitespace are separators.
    WordStyleSimple WordStyle = iota
    // WordStyleVi: like vi's w/b - punctuation runs are words of
    // their own; only whitespace separates.
    WordStyleVi
)

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
// Returns decorations from the deleted range AS A REPORT: marks are
// never deleted with a range - they collapse to the deletion point
// and survive. The caller decides which reported marks to remove
// explicitly (Decorate with a nil Address).
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

// Undo coalescing (opt-in; off by default - every mutation is its own
// revision). While enabled, a run of adjacent inserts (typing: each
// insert landing at the beginning or end of the chunk the run built)
// or adjacent deletes (forward-delete repeating at one caret, or
// backspace walking left into it) AMENDS the current revision instead
// of minting one per keystroke - typing a word is ONE undo step.
// Runs end at a HARD EDGE: Bake(); an edit arriving more than
// autoBakeTime after the previous one (0 disables time-based baking);
// any non-continuation (different kind, non-adjacent or interior
// position, any other mutation type); UndoSeek/ForkSeek; a successful
// save (the save point pins its revision); TransactionStart. Runs may
// freely exist within a bigger pending transaction - they simply
// dissolve into it (the transaction is already one revision).
func (g *Garland) SetUndoCoalescing(enabled bool, autoBakeTime time.Duration)
func (g *Garland) UndoCoalescing() (enabled bool, autoBakeTime time.Duration)

// Bake forces a hard edge: the current run is finalized and the next
// edit starts a fresh history entry no matter how adjacent. Safe to
// call at any time.
func (g *Garland) Bake()

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
// TransactionStart begins a new transaction with an optional descriptive name.
// All mutations until commit/rollback are bundled into one pending revision.
// Transactions can be nested; the outermost commit creates the revision.
// The name is associated with the resulting revision for undo history display.
// UndoSeek and ForkSeek are not allowed while a transaction is pending.
func (g *Garland) TransactionStart(name string) error

// TransactionCommit commits the current transaction.
// For nested transactions, only the outermost commit creates a revision.
// A new revision is ALWAYS created, even if no mutations occurred.
// This allows external state to be synchronized with garland revisions.
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

### Revision History

```go
// RevisionInfo contains metadata about a revision.
type RevisionInfo struct {
    Revision    RevisionID
    Name        string    // from TransactionStart, empty if unnamed
    HasChanges  bool      // true if revision contains actual mutations
}

// GetRevisionInfo returns information about a specific revision.
func (g *Garland) GetRevisionInfo(revision RevisionID) (*RevisionInfo, error)

// GetRevisionRange returns info for revisions in [start, end] inclusive.
// Useful for displaying undo history.
func (g *Garland) GetRevisionRange(start, end RevisionID) ([]RevisionInfo, error)
```

### Transaction Behavior

**Nesting Rules:**
- `TransactionStart` increments the nesting depth
- `TransactionCommit` decrements depth; revision created at outermost commit
- `TransactionRollback` poisons the transaction and decrements depth
- Only the outermost transaction's name is used for the revision

**Empty Transactions:**
- Committing a transaction ALWAYS creates a new revision, even with no mutations
- `RevisionInfo.HasChanges` indicates whether actual changes were made
- This allows external application state to sync with revision numbers

**Poisoned Transactions:**
- Once any inner `TransactionRollback` is called, the transaction is poisoned
- All subsequent `TransactionCommit` calls at any depth will rollback instead
- The entire outer transaction's changes are discarded (no revision created)

**Restrictions During Transactions:**
- `UndoSeek` returns `ErrTransactionPending`
- `ForkSeek` returns `ErrTransactionPending`
- Optimized region `CommitSnapshot` is deferred until transaction commit

**Example Usage:**
```go
g.TransactionStart("Replace header text")

cursor.SeekByte(100)
cursor.DeleteBytes(50, false)  // no revision yet

cursor.SeekByte(200)
cursor.InsertString("replacement", nil, true)  // still no revision

result, err := g.TransactionCommit()  // single revision for both operations
// result.Revision is the new revision number

// Later, display undo history:
info, _ := g.GetRevisionInfo(result.Revision)
fmt.Println(info.Name)  // "Replace header text"
```

**Empty Transaction Example:**
```go
// Sync external state with garland revision
g.TransactionStart("Update cursor color to blue")
// No garland mutations, just external state change
result, _ := g.TransactionCommit()  // still creates a revision

info, _ := g.GetRevisionInfo(result.Revision)
// info.HasChanges == false
// info.Name == "Update cursor color to blue"
```

**Nested Example:**
```go
g.TransactionStart("Refactor function")  // depth 1, this name is used
cursor.InsertString("outer", nil, true)

    g.TransactionStart("inner operation")  // depth 2, name ignored
    cursor.InsertString("inner", nil, true)

    if somethingWentWrong {
        g.TransactionRollback()  // poisons entire transaction
    } else {
        g.TransactionCommit()  // depth back to 1, no revision yet
    }

result, err := g.TransactionCommit()  // if poisoned: rolls back, err = ErrTransactionPoisoned
                                       // if not poisoned: creates revision named "Refactor function"
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
