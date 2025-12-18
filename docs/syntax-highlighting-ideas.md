# Syntax Highlighting Support

This document explores how Garland could provide syntax highlighting assistance to editors, helping them understand the syntactic context of any character or range.

## Goals

1. **Query syntax context** - Ask "what is the syntax class at byte/rune position X?"
2. **Efficient incremental updates** - Re-highlight only affected regions after edits
3. **Leverage existing formats** - Don't reinvent syntax definitions
4. **Minimal coupling** - Syntax support is optional and doesn't affect core operations

## Existing Syntax Definition Formats

### TextMate Grammars

**Files:** `.tmLanguage` (plist), `.tmLanguage.json`, `.tmlanguage.yaml`

**Used by:** VS Code, Sublime Text, Atom, many editors

**Pros:**
- Largest ecosystem of grammars
- Well-documented format
- JSON version is easy to parse

**Cons:**
- Regex-based, can be slow
- Stateful (requires tracking scope stack)
- Not designed for incremental parsing
- Complex nested scope rules

### Tree-sitter

**Files:** `grammar.js` compiled to `parser.c` / shared libraries

**Used by:** Neovim, Zed, Helix, GitHub (for code navigation), Emacs (via tree-sitter module)

**Pros:**
- True incremental parsing (re-parse only changed portions)
- Produces concrete syntax tree (AST)
- Very fast (written in C)
- Go bindings available (go-tree-sitter)
- Growing grammar library (100+ languages)
- Can query tree with S-expression patterns

**Cons:**
- Grammars must be compiled to C
- Requires bundling or dynamic loading of parser libraries
- More complex integration

### Vim Syntax Files

**Files:** `syntax/*.vim`

**Cons:**
- Vim-specific format
- Difficult to parse outside Vim
- Not recommended for new implementations

### Recommendation: Tree-sitter

Tree-sitter is the clear choice for modern syntax highlighting:

1. **Incremental by design** - Perfectly matches our edit-heavy use case
2. **AST-based** - Can answer structural questions, not just coloring
3. **Go bindings exist** - github.com/smacker/go-tree-sitter
4. **Industry momentum** - Major editors are adopting it

## Proposed Architecture

### Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        Garland                                   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                     Rope Tree                              │   │
│  │   (bytes, runes, lines, decorations)                      │   │
│  └──────────────────────────────────────────────────────────┘   │
│                              │                                   │
│                              │ content access                    │
│                              ▼                                   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                  Syntax Layer (optional)                   │   │
│  │                                                            │   │
│  │  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐   │   │
│  │  │ Tree-sitter │    │   Parser    │    │  Highlight  │   │   │
│  │  │   Parser    │◄───│   State     │◄───│   Query     │   │   │
│  │  └─────────────┘    └─────────────┘    └─────────────┘   │   │
│  │                                                            │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
                    Application (editor)
```

### Core Components

#### 1. SyntaxProvider Interface

```go
// SyntaxProvider parses content and answers syntax queries.
type SyntaxProvider interface {
    // Language returns the language name (e.g., "go", "python").
    Language() string

    // Parse performs initial full parse of content.
    Parse(content []byte) error

    // Update incrementally updates after an edit.
    // startByte/endByte are the old range, newEndByte is the new end.
    Update(content []byte, startByte, endByte, newEndByte int) error

    // ScopeAt returns the syntax scope(s) at a byte position.
    // Returns hierarchical scopes like ["source.go", "entity.name.function"]
    ScopeAt(bytePos int) []string

    // ScopesInRange returns all scope spans in a byte range.
    ScopesInRange(startByte, endByte int) []ScopeSpan

    // NodeAt returns the AST node at a position (Tree-sitter specific).
    NodeAt(bytePos int) SyntaxNode
}

// ScopeSpan represents a range with a syntax scope.
type ScopeSpan struct {
    StartByte int
    EndByte   int
    Scope     string  // e.g., "keyword.control", "string.quoted"
}

// SyntaxNode represents a node in the syntax tree.
type SyntaxNode struct {
    Type      string  // e.g., "function_declaration", "string_literal"
    StartByte int
    EndByte   int
    Children  []SyntaxNode
}
```

#### 2. Tree-sitter Implementation

```go
// TreeSitterProvider implements SyntaxProvider using Tree-sitter.
type TreeSitterProvider struct {
    parser *sitter.Parser
    tree   *sitter.Tree
    lang   *sitter.Language
    query  *sitter.Query  // Highlight query
}

// NewTreeSitterProvider creates a provider for a language.
func NewTreeSitterProvider(language string) (*TreeSitterProvider, error)
```

#### 3. Garland Integration

```go
// Garland additions
type Garland struct {
    // ... existing fields ...

    syntaxProvider SyntaxProvider
}

// EnableSyntax attaches a syntax provider to this Garland.
func (g *Garland) EnableSyntax(provider SyntaxProvider) error

// DisableSyntax removes syntax support.
func (g *Garland) DisableSyntax()

// SyntaxAt returns the syntax scope at a position.
func (g *Garland) SyntaxAt(bytePos int) ([]string, error)

// SyntaxInRange returns syntax spans for a byte range.
func (g *Garland) SyntaxInRange(startByte, endByte int) ([]ScopeSpan, error)

// HasSyntax returns true if syntax highlighting is enabled.
func (g *Garland) HasSyntax() bool
```

### Edit Integration

When content changes, we need to update the syntax tree:

```go
// After any mutation (insert, delete):
func (g *Garland) notifySyntaxChange(startByte, oldEndByte, newEndByte int) {
    if g.syntaxProvider == nil {
        return
    }

    // Get current content (or relevant portion)
    content := g.extractBytes(0, g.totalBytes)

    err := g.syntaxProvider.Update(content, startByte, oldEndByte, newEndByte)
    if err != nil {
        // Log error, potentially disable syntax
    }
}
```

### Optimization: Viewport-Based Parsing

For large files, we may not want to parse the entire file:

```go
type SyntaxOptions struct {
    // MaxParseBytes limits initial parse size.
    // Content beyond this is parsed on-demand.
    MaxParseBytes int

    // ParseAround specifies the context to parse around edits.
    ParseAround int
}
```

### Highlight Queries

Tree-sitter uses highlight queries to map AST nodes to scopes:

```scheme
; highlights.scm for Go

(package_identifier) @keyword
(type_identifier) @type
(function_declaration name: (identifier) @function)
(call_expression function: (identifier) @function.call)
(comment) @comment
(string_literal) @string
(number_literal) @number
```

We can bundle common highlight queries or allow users to provide custom ones.

## Integration with Decorations

We could optionally store syntax scopes as decorations:

```go
// SyntaxToDecorations converts syntax spans to decorations.
// Useful for editors that want unified decoration handling.
func (g *Garland) SyntaxToDecorations(startByte, endByte int) []DecorationEntry
```

However, this may be inefficient for large files. Direct queries are usually better.

## Scope Naming Convention

Follow TextMate conventions for compatibility:

| Scope | Meaning |
|-------|---------|
| `comment` | Comments |
| `constant` | Constants |
| `constant.numeric` | Numbers |
| `constant.language` | true, false, nil |
| `entity.name.function` | Function names |
| `entity.name.type` | Type names |
| `keyword` | Keywords |
| `keyword.control` | if, else, for, etc. |
| `keyword.operator` | and, or, not |
| `string` | String literals |
| `string.quoted.double` | Double-quoted strings |
| `variable` | Variables |
| `variable.parameter` | Function parameters |

## API Usage Examples

### Basic Usage

```go
g, _ := lib.Open(FileOptions{FilePath: "main.go"})
defer g.Close()

// Enable syntax for Go
provider, _ := garland.NewTreeSitterProvider("go")
g.EnableSyntax(provider)

// Query syntax at a position
scopes, _ := g.SyntaxAt(100)
// scopes = ["source.go", "meta.function", "entity.name.function"]

// Get highlighted spans for rendering
spans, _ := g.SyntaxInRange(0, 500)
for _, span := range spans {
    fmt.Printf("%d-%d: %s\n", span.StartByte, span.EndByte, span.Scope)
}
```

### With Cursor

```go
cursor := g.NewCursor()
cursor.SeekLine(10, 0)

// What's the syntax context here?
scopes, _ := g.SyntaxAt(cursor.BytePos())
if contains(scopes, "comment") {
    // Cursor is inside a comment
}
```

### Structural Queries

```go
// Find the function containing the cursor
node := g.SyntaxNodeAt(cursor.BytePos())
for node.Type != "function_declaration" && node.Parent != nil {
    node = node.Parent
}
if node.Type == "function_declaration" {
    fmt.Println("In function:", node.ChildByFieldName("name").Text())
}
```

## Dependencies

### Go Tree-sitter Bindings

```go
import (
    sitter "github.com/smacker/go-tree-sitter"
    "github.com/smacker/go-tree-sitter/golang"
    "github.com/smacker/go-tree-sitter/python"
    // ... other languages
)
```

### Language Parsers

Each language needs a compiled Tree-sitter parser. Options:

1. **Bundled** - Include popular languages (Go, Python, JavaScript, etc.)
2. **Dynamic loading** - Load .so/.dylib files at runtime
3. **User-provided** - Let applications provide their own parsers

## Implementation Phases

### Phase 1: Core Infrastructure
- [ ] Define `SyntaxProvider` interface
- [ ] Create `TreeSitterProvider` implementation
- [ ] Add `EnableSyntax()` / `DisableSyntax()` to Garland
- [ ] Implement `SyntaxAt()` and `SyntaxInRange()`

### Phase 2: Edit Integration
- [ ] Hook into insert/delete to call `Update()`
- [ ] Handle incremental updates efficiently
- [ ] Add tests for syntax after edits

### Phase 3: Language Support
- [ ] Bundle parsers for common languages (Go, Python, JavaScript, TypeScript, Rust, C, C++)
- [ ] Include highlight queries for bundled languages
- [ ] Document how to add custom languages

### Phase 4: Advanced Features
- [ ] Structural navigation (next/prev function, etc.)
- [ ] Code folding hints from syntax tree
- [ ] Indent level inference
- [ ] Language-aware word boundaries

## Performance Considerations

1. **Lazy parsing** - Don't parse until syntax is first queried
2. **Incremental updates** - Tree-sitter handles this natively
3. **Viewport optimization** - For huge files, only parse visible region + buffer
4. **Background parsing** - Long parses could run in a goroutine
5. **Cache invalidation** - Track which ranges need re-query after edits

## Alternatives Considered

### Regex-based (like TextMate)

We could implement TextMate grammar parsing, but:
- Requires maintaining scope stack state
- No incremental parsing
- Slower for large files
- More complex implementation

### External LSP Integration

Language Server Protocol could provide semantic tokens:
- Better semantic accuracy
- But requires external process
- Higher latency
- More complex setup

Tree-sitter provides a good balance of accuracy, speed, and self-containment.

## Open Questions

1. **Parser distribution** - Bundle vs dynamic loading?
2. **Memory overhead** - Syntax tree can be large; should we support disposing it?
3. **Thread safety** - How to handle syntax queries during edits?
4. **Error recovery** - How to handle parse errors in user code?

## Related Documentation

- [Architecture](architecture.md) - Overall system design
- [Tree Operations](tree-operations.md) - How the rope tree works
- [Decorations](decorations.md) - Existing annotation system
