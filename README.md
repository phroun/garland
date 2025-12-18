# Garland

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Garland:  A rope-based text buffer library for Go, designed for text editors and applications requiring
efficient document manipulation with version control.

*If you use this, please support me on ko-fi:  [https://ko-fi.com/jeffday](https://ko-fi.com/F2F61JR2B4)*

[![ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/F2F61JR2B4)

## Features

- **Rope data structure**: O(log n) insert and delete operations using a balanced binary tree
- **Multiple addressing modes**: Position by byte, rune (Unicode code point), or line:rune
- **Version control**: Full undo/redo with branching support via forks and revisions
- **Three-tier storage**: Automatic management across memory, warm (original file), and cold (external) storage
- **Decorations**: Named position markers that track through edits
- **Multiple cursors**: Independent cursor positions with automatic adjustment on edits
- **Search and replace**: Literal and regex-based search with case sensitivity and whole-word options
- **Lazy loading**: Stream large files with configurable ready thresholds
- **Transactions**: Group operations into atomic revisions with rollback support
- **Memory management**: Configurable soft/hard limits with LRU eviction to cold storage
- **File change detection**: Monitor and handle external modifications to source files

## Installation

```bash
go get github.com/phroun/garland
```

Requires Go 1.24 or later.

## Usage

### Basic Example

```go
package main

import (
    "fmt"
    "github.com/phroun/garland"
)

func main() {
    // Initialize the library
    lib, err := garland.Init(garland.LibraryOptions{})
    if err != nil {
        panic(err)
    }

    // Create a new document
    g, err := lib.Open(garland.FileOptions{
        DataString: "Hello, World!",
    })
    if err != nil {
        panic(err)
    }
    defer g.Close()

    // Create a cursor and edit
    cursor := g.NewCursor()
    cursor.SeekByte(7)
    cursor.DeleteBytes(5, false)
    cursor.InsertString("Garland", nil, true)

    // Read the result
    cursor.SeekByte(0)
    content, _ := cursor.ReadBytes(g.ByteCount().Value)
    fmt.Println(string(content)) // "Hello, Garland!"
}
```

### Opening Files

```go
// From file path
g, err := lib.Open(garland.FileOptions{
    FilePath: "document.txt",
})

// From byte slice
g, err := lib.Open(garland.FileOptions{
    DataBytes: []byte("content"),
})

// From string
g, err := lib.Open(garland.FileOptions{
    DataString: "content",
})
```

### Transactions

```go
g.TransactionStart("Replace header")
cursor.SeekByte(0)
cursor.DeleteBytes(100, false)
cursor.InsertString("New Header\n", nil, true)
result, err := g.TransactionCommit()
// result.Revision contains the new revision number
```

### Undo/Redo with Forks

```go
// Undo to a previous revision
g.UndoSeek(5)

// Editing after undo creates a new fork automatically
cursor.InsertString("alternative content", nil, true)

// Switch between forks
g.ForkSeek(1, g.GetForkInfo(1).HighestRevision)
```

### Decorations

```go
// Add a decoration at cursor position
g.Decorate("bookmark", garland.AbsoluteAddress{
    Mode: garland.ByteMode,
    Byte: cursor.BytePos(),
})

// Query decoration position (updates as content changes)
pos, err := g.GetDecorationPosition("bookmark")

// Get decorations in a range
decorations := g.GetDecorationsInByteRange(0, 1000)
```

### Search and Replace

```go
// Find first occurrence
result, err := cursor.FindString("needle", garland.SearchOptions{
    CaseSensitive: false,
    WholeWord:     true,
})

// Replace all occurrences
count, err := cursor.ReplaceStringAll("old", "new", garland.SearchOptions{})

// Regex search
result, err := cursor.FindRegex(`\d{4}-\d{2}-\d{2}`, garland.SearchOptions{})
```

### Storage Tiers

```go
// Configure cold storage
lib, err := garland.Init(garland.LibraryOptions{
    ColdStoragePath: "/path/to/cold-storage",
})

// Use all storage tiers (memory + warm + cold)
g, err := lib.Open(garland.FileOptions{
    FilePath:     "large-file.txt",
    LoadingStyle: garland.AllStorage,
})

// Move inactive data to cold storage
g.Chill(garland.ChillInactiveForks)
```

### Memory Management

```go
lib, err := garland.Init(garland.LibraryOptions{
    MemorySoftLimit: 100 * 1024 * 1024, // 100 MB target
    MemoryHardLimit: 200 * 1024 * 1024, // 200 MB maximum
})

// Query memory usage
stats := g.MemoryUsage()
```

## Architecture

Garland uses a rope data structure implemented as a balanced binary tree:

- **Leaf nodes** store text data (default max 128KB per node)
- **Internal nodes** aggregate metrics (byte count, rune count, line count)
- **Structural sharing** enables memory-efficient versioning via copy-on-write

Each edit creates a new revision. Revisions are organized into forks, which branch when editing from a non-HEAD revision. This provides full undo history with the ability to explore alternative edit paths.

## Storage Tiers

| Tier | Description | Use Case |
|------|-------------|----------|
| Memory | Data held in RAM | Active editing |
| Warm | Original file on disk, verified by checksum | Large files, read-heavy workloads |
| Cold | Library-managed external storage | Undo history, inactive forks |

Transitions between tiers are automatic and transparent to the application.

## REPL

An interactive REPL is included for testing and exploration:

```bash
go run ./cmd/garland-repl
```

Type `help` for available commands.

## Testing

```bash
go test ./...
```

Tests are located alongside source files following Go conventions.

## License

MIT License. See [LICENSE](LICENSE) for details.
