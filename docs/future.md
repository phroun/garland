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

### 2. Decoration Loading from Files

**Location:** `garland.go` line ~2073

The `loadInitialDecorations()` function exists but isn't fully wired to load decorations from files.

**Current behavior:** Decorations must be programmatically added after opening a file.

**Desired behavior:** Support loading decorations from a companion file (e.g., `.decorations` or embedded format) when opening a file.

## Missing Features

These are features that don't exist yet but would enhance the library:

### Navigation

#### Word Navigation
- Move cursor by word boundaries
- Word selection
- Word deletion (delete word forward/backward)

#### Bookmarks
- Named bookmarks separate from decorations
- Jump to bookmark by name
- Bookmark list/management

### Selection Ranges

The current model uses point cursors only. Selection ranges would enable:
- Visual selection of text regions
- Cut/copy/paste operations on selections
- Multi-cursor editing with selections
- Block/column selection mode

**Implementation approach:**
- Add `SelectionStart` and `SelectionEnd` to Cursor
- Selection-aware operations (delete selection, replace selection)
- Consider anchor/head model vs start/end model

### Diff Between Revisions

- Show differences between any two revisions
- Show differences between forks
- Line-by-line diff output
- Unified diff format export

### History Management

#### Revision Pruning/Garbage Collection
- Limit maximum revision history depth
- Prune old revisions to save memory
- Configurable retention policies
- Squash multiple revisions into one

#### History Compression
- Compress old revision data
- Delta encoding between revisions
- On-demand decompression

### Performance Enhancements

#### Memory Limits
- Maximum RAM usage threshold
- Auto-chill when approaching limit
- LRU eviction for rarely-accessed nodes

#### Incremental Loading Limits
- Progressive loading for huge files
- Viewport-based loading (load visible portion first)
- Background loading of remainder

#### Caching Improvements
- LRU cache for thawed nodes
- Read-ahead caching for sequential access
- Cache statistics and tuning

### Advanced Editing

#### Macros
- Record keystroke sequences
- Playback recorded macros
- Save/load macro definitions

#### Auto-complete
- Completion suggestions
- Word completion from document
- Custom completion providers

#### Plugins/Extensions
- Plugin architecture
- Event hooks for operations
- Custom command registration

### Integration Features

#### Clipboard Support
- System clipboard integration
- Multiple clipboard registers (vim-style)
- Clipboard history

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

## Priority Recommendations

### High Priority (Core Functionality)
1. Selection ranges - fundamental for practical text editing
2. Word navigation - common editing operation

### Medium Priority (Quality of Life)
3. File change detection - safety feature
4. Revision pruning - memory management for long sessions
5. Diff between revisions - debugging/comparison feature

### Lower Priority (Nice to Have)
6. Clipboard support - platform-specific complexity
7. Macros - power user feature (note: separate macro language already exists)
