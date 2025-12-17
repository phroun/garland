# Future Work

This document outlines incomplete features and potential enhancements for the Garland text editor library.

## Incomplete Features

These features have partial implementations that should be completed:

### 1. File Change Detection

**Location:** `storage.go` line ~127

The `HasChanged()` method on `localFileSystem` returns `ErrNotSupported`.

**Current behavior:** Cannot detect if the source file was modified externally.

**Desired behavior:** Compare file mtime and size against cached values to detect external modifications.

**Implementation notes:**
- Store mtime and size when file is opened
- Compare against current values in `HasChanged()`
- Consider inode checking on Unix systems

## Missing Features

These are features that don't exist yet but would enhance the library:

### Navigation

#### Word Selection and Deletion
- Word selection (currently only navigation is implemented)
- Word deletion (delete word forward/backward)

### Diff Between Revisions

- Show differences between any two revisions
- Show differences between forks
- Line-by-line diff output
- Unified diff format export

### History Management

#### History Compression
- Compress old revision data
- Delta encoding between revisions
- On-demand decompression

### Performance Enhancements

#### Incremental Loading Limits
- Progressive loading for huge files
- Viewport-based loading (load visible portion first)
- Background loading of remainder

#### Caching Improvements
- Read-ahead caching for sequential access
- Cache statistics and tuning

### Advanced Editing

#### Plugins/Extensions
- Plugin architecture
- Event hooks for operations
- Custom command registration

### Integration Features

#### File Watching
- Detect external file modifications
- Auto-reload option
- Conflict resolution

### Display/Rendering Support

These are application-level concerns but the library could provide support:

#### Line Number Metadata
- Efficient line number lookups
- Line number caching

#### Soft Wrap Support
- Track display lines vs logical lines
- Wrap position calculations

#### Syntax Highlighting Support
- Token range annotations
- Incremental re-highlighting hints

## REPL Enhancements

Minor improvements for the REPL demo application:

- Command history (up/down arrows)
- Tab completion for commands
- Scripting mode (read commands from file)
- Output redirection

## Recently Completed Features

The following features have been implemented:

- **Lazy loading blocking** - `waitFor*` functions now block with timeout support; `Is*Ready` check methods for non-blocking guards
- **Find/Search** - String and regex search with case sensitivity, whole word matching, forward/backward
- **Find and Replace** - Single, all, and count-limited replacement with regex capture group expansion
- **Incremental Memory Management** - Soft/hard memory limits with LRU-based auto-chilling; single background worker across all open files; budget-limited per-tick operations for smooth user experience
- **Incremental Tree Rebalancing** - Path-based rebalancing after mutations with configurable budget; `ForceRebalance()` for full tree rebuild when needed; `NeedsRebalancing()` check
- **Word Navigation** - `SeekByWord(n)` for bidirectional word navigation; positive n moves forward, negative n moves backward; positions at start of next/previous word
- **Line Navigation** - `SeekLineStart()` and `SeekLineEnd()` for navigating to beginning/end of current line
- **Decoration Loading from Files** - `LoadDecorations(fs, path)` and `LoadDecorationsFromString(content)` for loading decorations from INI-format files with comment support
- **Revision Pruning** - `Prune(keepFromRevision)` for per-fork history pruning; shared revisions only deleted when all dependent forks have pruned past them; cursor history cleaned up automatically
- **Fork Deletion** - `DeleteFork(fork)` for soft-deleting forks; keeps shared data for child forks; prevents switching to deleted forks

## Handled Elsewhere

These features are addressed through other mechanisms:

- **Selection Ranges** - Handled via decorations; applications can use decoration pairs to mark selection start/end positions
- **Macros** - A separate macro language has been developed independently of this library
- **Bookmarks** - Use decorations with a naming convention (e.g., `bookmark:name`)

## Out of Scope

These are application-layer concerns, not appropriate for a backend text buffer library:

- **Clipboard Support** - System clipboard integration is platform-specific and belongs in the host application
- **Auto-complete** - Completion UI/UX belongs in the application; the library already provides search and word navigation primitives that applications can build on

## Priority Recommendations

### High Priority (Core Functionality)
1. File change detection - safety feature

### Medium Priority (Quality of Life)
2. Diff between revisions - debugging/comparison feature
