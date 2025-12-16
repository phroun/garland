# Garland Implementation Plan

This document outlines the step-by-step implementation plan for the Garland library.

## Phase 1: Foundation

### 1.1 Project Setup
- [x] Initialize Go module
- [ ] Create package structure
- [ ] Set up basic error types
- [ ] Create type definitions for IDs (NodeID, ForkID, RevisionID)

```
garland/
├── garland.go         # Library, Garland types, Open/Close
├── node.go            # Node, NodeSnapshot types
├── cursor.go          # Cursor type
├── decoration.go      # Decoration types and cache
├── storage.go         # Storage interfaces and implementations
├── versioning.go      # Fork/revision management
├── transaction.go     # Transaction state and operations
├── address.go         # Address modes and conversion
├── loader.go          # Background loading
├── region.go          # Optimized regions
├── errors.go          # Error definitions
└── garland_test.go    # Tests
```

### 1.2 Core Types
- [ ] Define all ID types (NodeID, ForkID, RevisionID)
- [ ] Define ForkRevision composite key
- [ ] Define StorageState enum
- [ ] Define AddressMode enum
- [ ] Define LoadingStyle enum
- [ ] Define all error variables

### 1.3 Node Structure (Memory Only)
- [ ] Implement Node with history map
- [ ] Implement NodeSnapshot for leaves
- [ ] Implement NodeSnapshot for internal nodes
- [ ] Implement `snapshotAt(fork, revision)` lookup
- [ ] Implement LineStart tracking
- [ ] Implement weight calculation (bytes, runes, lines)

**Milestone 1**: Can create nodes with version history, look up snapshots.

---

## Phase 2: Basic Tree Operations

### 2.1 Tree Navigation
- [ ] Implement `findLeafByByte(pos)` - navigate to leaf by byte position
- [ ] Implement `findLeafByRune(pos)` - navigate to leaf by rune position
- [ ] Implement `findLeafByLine(line, rune)` - navigate by line:rune
- [ ] Implement leaf iteration (next/previous leaf)

### 2.2 Split and Concatenate
- [ ] Implement `split(leaf, pos)` - split leaf at byte position
- [ ] Implement `concatenate(left, right)` - join two nodes
- [ ] Handle decoration partitioning on split
- [ ] Handle weight recomputation on concatenate
- [ ] Ensure UTF-8 safety (don't split mid-character)

### 2.3 Tree Rebalancing
- [ ] Implement basic tree construction from leaves
- [ ] Implement rebalancing after insert/delete
- [ ] Keep tree reasonably balanced (not necessarily AVL-strict)

**Milestone 2**: Can build trees, navigate by all address modes, split/join nodes.

---

## Phase 3: Garland and Cursor Basics

### 3.1 Library Initialization
- [ ] Implement `Init(LibraryOptions)`
- [ ] Implement basic cold storage interface
- [ ] Implement default local file system interface

### 3.2 Garland Creation
- [ ] Implement `Open(FileOptions)` for byte/string sources
- [ ] Create root node structure (content left, EOF right)
- [ ] Initialize node registry
- [ ] Initialize fork 0, revision 0

### 3.3 Cursor Implementation
- [ ] Implement `NewCursor()` at position 0
- [ ] Implement `RemoveCursor()`
- [ ] Implement `SeekByte()`, `SeekRune()`, `SeekLine()`
- [ ] Implement position queries (`BytePos`, `RunePos`, `LinePos`)
- [ ] Track cursor position in all three coordinate systems

### 3.4 Basic Read Operations
- [ ] Implement `ReadBytes(length)`
- [ ] Implement `ReadString(length)`
- [ ] Implement `ReadLine()`

**Milestone 3**: Can create Garland from string/bytes, create cursors, seek, read.

---

## Phase 4: Mutations and Versioning

### 4.1 Insert Operations
- [ ] Implement `InsertBytes()` at cursor position
- [ ] Implement `InsertString()` at cursor position
- [ ] Handle `insertBefore` parameter
- [ ] Handle relative decorations
- [ ] Update all cursor positions after insert
- [ ] Increment revision on each insert

### 4.2 Delete Operations
- [ ] Implement `DeleteBytes(length)`
- [ ] Implement `DeleteRunes(length)`
- [ ] Implement `TruncateToEOF()`
- [ ] Return deleted decorations
- [ ] Handle `includeLineDecorations` parameter
- [ ] Update all cursor positions after delete
- [ ] Increment revision on each delete

### 4.3 Fork Management
- [ ] Implement fork creation on change after UndoSeek
- [ ] Track ForkInfo (parent, highest revision)
- [ ] Implement `CurrentFork()`, `CurrentRevision()`

### 4.4 UndoSeek
- [ ] Implement `UndoSeek(revision)` within current fork
- [ ] Update all cursors on UndoSeek
- [ ] Validate revision exists in fork

### 4.5 ForkSeek
- [ ] Implement `ForkSeek(fork)`
- [ ] Find common ancestor revision
- [ ] Handle revision retention or retreat

### 4.6 FindForksBetween
- [ ] Implement divergence point discovery
- [ ] Return list of ForkDivergence

### 4.7 Transactions
- [ ] Implement `TransactionState` structure with name field
- [ ] Implement `TransactionStart(name)` with nesting support
- [ ] Implement `TransactionCommit()` with poison detection
- [ ] Implement `TransactionRollback()` with poison propagation
- [ ] Implement `TransactionDepth()` and `InTransaction()`
- [ ] Block `UndoSeek`/`ForkSeek` during transactions
- [ ] Snapshot cursor positions at transaction start
- [ ] Restore cursor positions on rollback
- [ ] Always create revision on commit (even for empty transactions)
- [ ] Implement `RevisionInfo` structure and storage
- [ ] Implement `GetRevisionInfo(revision)`
- [ ] Implement `GetRevisionRange(start, end)`

**Milestone 4**: Full mutation support with undo/redo, forking, and transactions.

---

## Phase 5: Decorations

### 5.1 Decoration Storage
- [ ] Store decorations in leaf nodes
- [ ] Implement decoration CRUD within nodes
- [ ] Maintain sorted order by position

### 5.2 Decoration Cache
- [ ] Implement cache structure
- [ ] Implement cache lookup (hint only)
- [ ] Implement cache update

### 5.3 Decoration Search
- [ ] Implement outward search from cache hint
- [ ] Search until found or exhausted
- [ ] Update cache on find

### 5.4 Decoration API
- [ ] Implement `Decorate(entries)` - add/update/remove
- [ ] Implement `GetDecorationPosition(key)`
- [ ] Implement `GetDecorationsInByteRange(start, end)`
- [ ] Implement `GetDecorationsOnLine(line)`
- [ ] Implement `DumpDecorations(path)`

### 5.5 Decoration Loading
- [ ] Parse decoration dump file format
- [ ] Support DecorationPath in FileOptions
- [ ] Support DecorationString in FileOptions

**Milestone 5**: Full decoration support with persistence.

---

## Phase 6: Storage Tiers

### 6.1 Cold Storage Backend
- [ ] Implement file-based cold storage
- [ ] Implement custom backend support
- [ ] Add checksum computation and verification

### 6.2 Storage Transitions
- [ ] Implement Memory → Cold transition
- [ ] Implement Cold → Memory transition
- [ ] Handle cold storage failures (placeholder nodes)

### 6.3 Warm Storage
- [ ] Track originalFileOffset in leaves
- [ ] Implement Memory → Warm transition (with checksum)
- [ ] Implement Warm → Memory transition
- [ ] Handle warm storage mismatch (fallback to cold or placeholder)

### 6.4 Decoration Cold Storage
- [ ] Store decorations separately from data
- [ ] Implement decoration cold storage transitions

**Milestone 6**: Full storage tier support.

---

## Phase 7: File System Abstraction

### 7.1 Default Implementation
- [ ] Implement local file FileSystemInterface
- [ ] Implement all required methods
- [ ] Implement optional methods

### 7.2 File Loading
- [ ] Implement `Open()` with FilePath source
- [ ] Implement `Open()` with custom FileSystem
- [ ] Handle file reading and initial tree construction

### 7.3 File Saving
- [ ] Implement `Save()` - overwrite original
- [ ] Implement `SaveAs()` - write to new location
- [ ] Handle warm storage invalidation on Save

**Milestone 7**: Can load and save files.

---

## Phase 8: Lazy Loading

### 8.1 Background Loader
- [ ] Implement Loader goroutine
- [ ] Process input source incrementally
- [ ] Build tree nodes as data arrives
- [ ] Track bytes/runes/lines loaded

### 8.2 Ready Thresholds
- [ ] Implement initial ready threshold checking
- [ ] Implement read-ahead threshold checking
- [ ] Implement `IsReady()` at Garland level

### 8.3 Cursor Ready State
- [ ] Block cursor seeks until position available
- [ ] Implement `IsReady()` per cursor
- [ ] Implement `WaitReady()` blocking call

### 8.4 Streaming Input
- [ ] Implement `Open()` with DataChannel source
- [ ] Handle channel close as EOF
- [ ] Support continuous appending (log files)

### 8.5 Count Tracking
- [ ] Implement `ByteCount()`, `RuneCount()`, `LineCount()`
- [ ] Track `Complete` flag
- [ ] Update counts as loading progresses

**Milestone 8**: Lazy loading with ready state tracking.

---

## Phase 9: Address Conversion

### 9.1 Conversion Functions
- [ ] Implement `ByteToRune()`
- [ ] Implement `RuneToByte()`
- [ ] Implement `LineRuneToByte()`
- [ ] Implement `ByteToLineRune()`

### 9.2 AbsoluteAddress Resolution
- [ ] Convert any AbsoluteAddress to byte position
- [ ] Use in decoration positioning

**Milestone 9**: Full address mode support.

---

## Phase 10: Optimized Regions

### 10.1 Region Interface
- [ ] Define OptimizedRegion interface
- [ ] Define OptimizedRegionHandle

### 10.2 Region Management
- [ ] Implement `CreateOptimizedRegion()`
- [ ] Implement `CreateOptimizedRegionWith()` (custom factory)
- [ ] Implement `GetOptimizedRegionAt()`
- [ ] Implement `DissolveOptimizedRegion()`
- [ ] Detect and prevent region overlap

### 10.3 Operation Forwarding
- [ ] Detect when mutations touch regions
- [ ] Forward partial operations to region interface
- [ ] Query regions for updated counts

### 10.4 Region Versioning
- [ ] Call `CommitSnapshot()` before UndoSeek
- [ ] Call `RevertTo()` when seeking to past revision
- [ ] Handle region dissolution on version change

### 10.5 Default Region Implementation
- [ ] Implement simple mutable buffer region
- [ ] Implement snapshot/revert for versioning

**Milestone 10**: Optimized region support.

---

## Phase 11: Cursor History

### 11.1 Position History
- [ ] Track cursor position per (fork, revision)
- [ ] Lazy recording (only when cursor moves after version change)

### 11.2 History on UndoSeek
- [ ] Restore cursor positions when seeking to past revision
- [ ] Handle cursors that didn't exist at target revision

**Milestone 11**: Full cursor history support.

---

## Phase 12: Polish and Testing

### 12.1 Comprehensive Tests
- [ ] Unit tests for all node operations
- [ ] Unit tests for tree navigation
- [ ] Unit tests for mutations
- [ ] Unit tests for versioning
- [ ] Unit tests for transactions (nesting, poison, rollback)
- [ ] Unit tests for decorations
- [ ] Unit tests for storage tiers
- [ ] Unit tests for lazy loading
- [ ] Integration tests for common workflows

### 12.2 Edge Cases
- [ ] Empty file handling
- [ ] Single-byte file handling
- [ ] Very large file handling
- [ ] Binary data handling
- [ ] Malformed UTF-8 handling

### 12.3 Performance Testing
- [ ] Benchmark tree operations
- [ ] Benchmark large file loading
- [ ] Benchmark many cursors
- [ ] Memory usage profiling

### 12.4 Documentation
- [ ] Godoc comments on all public types/methods
- [ ] Usage examples
- [ ] Error handling guide

**Milestone 12**: Production-ready library.

---

## Implementation Order Rationale

1. **Foundation first**: Types and basic structures before behavior
2. **Memory-only first**: Simplify by ignoring storage tiers initially
3. **Read before write**: Navigation and reading before mutations
4. **Single version first**: Basic operations before versioning complexity
5. **Decorations after mutations**: Decorations depend on stable mutation semantics
6. **Storage tiers late**: Most complex, needs stable core first
7. **Lazy loading late**: Requires stable synchronous operations first
8. **Optimized regions last**: Advanced optimization, needs everything else working

---

## Dependencies Between Components

```
errors.go          ← (none)
address.go         ← errors
node.go            ← errors, address
cursor.go          ← errors, node
decoration.go      ← errors, node
versioning.go      ← errors, node
transaction.go     ← errors, node, cursor
storage.go         ← errors, node
garland.go         ← all above
loader.go          ← garland, node, storage
region.go          ← garland, node, decoration
```

---

## Estimated Complexity by Phase

| Phase | Complexity | Notes |
|-------|------------|-------|
| 1. Foundation | Low | Boilerplate, type definitions |
| 2. Tree Operations | Medium | Core algorithms, careful with UTF-8 |
| 3. Garland/Cursor | Medium | Integration of components |
| 4. Mutations/Versioning | High | Most complex logic, many edge cases |
| 5. Decorations | Medium | Search algorithm needs care |
| 6. Storage Tiers | High | Error handling, checksums, transitions |
| 7. File System | Low-Medium | Straightforward I/O |
| 8. Lazy Loading | High | Concurrency, synchronization |
| 9. Address Conversion | Low | Straightforward calculations |
| 10. Optimized Regions | Medium-High | Interface design, operation forwarding |
| 11. Cursor History | Low-Medium | Builds on existing versioning |
| 12. Polish/Testing | Medium | Comprehensive coverage takes time |

---

## Getting Started

Begin with Phase 1 by creating the package structure and type definitions:

```bash
mkdir -p garland
cd garland
go mod init garland
```

Then create the initial files with type stubs and build incrementally.
