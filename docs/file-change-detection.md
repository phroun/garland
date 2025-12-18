# File Change Detection

This document describes Garland's source file change detection system, which monitors external modifications to source files and manages warm storage trust accordingly.

## Overview

When Garland opens a file, it captures the file's metadata (size, modification time, inode). The library can then detect when the source file has been modified externally, distinguish between different types of changes, and manage warm storage trust to prevent data corruption.

## Design Goals

1. **Lazy verification** - Don't checksum the entire file on every change detection
2. **Protect against data loss** - Verify warm storage before LRU eviction when changes are suspected
3. **Support append-only files** - Efficiently handle log files and streaming data
4. **User awareness** - Notify applications of changes even when not using warm storage

## Change Types

| Type | Detection | Description |
|------|-----------|-------------|
| `SourceUnchanged` | Size and mtime unchanged | No modification detected |
| `SourceAppended` | Size grew | File grew, existing content may be intact |
| `SourceModified` | Mtime changed (size same) | Content potentially altered |
| `SourceTruncated` | Size shrunk | File was shortened |
| `SourceReplaced` | Inode changed | File deleted and recreated |
| `SourceDeleted` | File not found | File no longer exists |

## Warm Storage Trust Levels

Each warm storage block is tracked with a trust level that determines how it's handled during reads and LRU eviction.

| Trust Level | Meaning | LRU to Warm | Read Verification |
|-------------|---------|-------------|-------------------|
| `WarmTrustFull` | No changes ever detected | Yes | Optional (configurable) |
| `WarmTrustVerified` | Block verified since last change | Yes | Optional (configurable) |
| `WarmTrustStale` | Block not verified since change | Verify first | Required |
| `WarmTrustSuspended` | User notified, awaiting response | No (cold only) | Required |

## Core Data Structures

### Source State

```go
type sourceState struct {
    // Original file metadata captured at open time
    originalMtime time.Time
    originalSize  int64
    originalInode uint64

    // Change tracking
    changeCounter  uint64    // Incremented on any detected change
    lastChangeTime time.Time

    // User notification state
    status               SourceChangeStatus
    userNotifiedPending  bool
    appendAvailableBytes int64

    // Policy settings
    appendPolicy AppendPolicy
    verifyOnRead bool  // Default: true
}
```

### Per-Block Verification State

```go
type warmVerificationState struct {
    verifiedAtCounter uint64    // changeCounter when last verified
    verifiedTime      time.Time
}
```

## Detection Flow

```
                         stat file
                             │
              ┌──────────────┼──────────────┐
              │              │              │
              ▼              ▼              ▼
         size grew     mtime changed    unchanged
              │         (size same)          │
              │              │               │
              ▼              ▼               ▼
      verify boundary   mark suspect     (no action)
         block only          │
              │              │
         ┌────┴────┐         │
         │         │         │
         ▼         ▼         │
      matches    fails       │
         │         │         │
         ▼         ▼         ▼
    AppendAvail  SuspectChange
         │              │
         │              │  (lazy - on next warm read)
         ▼              ▼
    notify app    checksum verified
    per policy          │
                   ┌────┴────┐
                   │         │
                   ▼         ▼
                passes    fails
                   │         │
                   ▼         ▼
              (continue)  Modified
                              │
                              ▼
                         notify app
```

## LRU Eviction Decision Flow

```
              ┌─────────────────┐
              │ LRU Evict Block │
              └────────┬────────┘
                       │
                       ▼
              ┌─────────────────┐
              │ Has Warm Storage│
              │ (originalOffset │
              │    >= 0)?       │
              └────────┬────────┘
                      ╱ ╲
                 yes ╱   ╲ no
                    ╱     ╲
                   ▼       ▼
          ┌────────────┐  ┌──────────────┐
          │ Get Trust  │  │ Use Cold     │
          │ Level      │  │ Storage      │
          └─────┬──────┘  └──────────────┘
                │
       ┌────────┼────────┬────────┐
       ▼        ▼        ▼        ▼
     Full   Verified   Stale  Suspended
       │        │        │        │
       ▼        ▼        ▼        ▼
    ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐
    │Evict │ │Evict │ │Verify│ │Cold  │
    │Warm  │ │Warm  │ │First │ │Only  │
    └──────┘ └──────┘ └──┬───┘ └──────┘
                         │
                    ┌────┴────┐
                    │         │
                    ▼         ▼
                 passes    fails
                    │         │
                    ▼         ▼
                ┌──────┐  ┌──────┐
                │Evict │  │Cold  │
                │Warm  │  │Only  │
                └──────┘  └──────┘
```

## Warm Storage Read Decision

```
              ┌─────────────────┐
              │ Read Warm Block │
              └────────┬────────┘
                       │
                       ▼
              ┌─────────────────┐
              │ Get Trust Level │
              └────────┬────────┘
                       │
       ┌───────────────┼───────────────┐
       ▼               ▼               ▼
   Full/Verified     Stale        Suspended
       │               │               │
       ▼               ▼               ▼
  ┌──────────┐   ┌──────────┐   ┌──────────┐
  │ Optional │   │ Required │   │ Required │
  │ Verify   │   │ Verify   │   │ Verify   │
  │(config)  │   │          │   │          │
  └────┬─────┘   └────┬─────┘   └────┬─────┘
       │              │              │
       ▼              ▼              ▼
    Read data,   Read & verify,  Read & verify,
    update LRU   update tracking notify on fail
```

## Append Handling

### Append Policy

```go
type AppendPolicy int

const (
    AppendPolicyAsk        // Notify app, wait for decision
    AppendPolicyIgnore     // Ignore this append, ask again next time
    AppendPolicyNever      // Ignore all future appends
    AppendPolicyOnce       // Load this append, ask again next time
    AppendPolicyContinuous // Tail mode - auto-load appends
)
```

### Append Detection Process

1. **Detect size increase** via `CheckSourceMetadata()`
2. **Verify boundary block** - only the last block before the original EOF
3. **If boundary intact** → `SourceAppended` status, content is safe
4. **If boundary fails** → Treat as `SourceModified`

This allows efficient append detection without checksumming the entire file.

## API Reference

### Change Detection

```go
// Cheap metadata check - just stat(), no content read
func (g *Garland) CheckSourceMetadata() (SourceChangeInfo, error)

// Current status
func (g *Garland) SourceStatus() SourceChangeStatus

// Source file path
func (g *Garland) SourcePath() string
```

### Append Handling

```go
// Verify boundary block for append detection
func (g *Garland) VerifyBoundaryForAppend() error

// Load appended content (after boundary verification)
func (g *Garland) LoadAppendedContent() (bytesLoaded int64, err error)

// Set append policy
func (g *Garland) SetAppendPolicy(policy AppendPolicy)
```

### Configuration

```go
// Set whether warm reads verify checksums (default: true)
func (g *Garland) SetVerifyOnRead(enabled bool)

// Set handler for change notifications
func (g *Garland) SetSourceChangeHandler(handler SourceChangeHandler)
```

### File Watching

```go
// Enable periodic source file monitoring
func (g *Garland) EnableSourceWatch(interval time.Duration)

// Disable monitoring
func (g *Garland) DisableSourceWatch()
```

### User Acknowledgment

```go
// Acknowledge a detected change
// reload=true: refresh source info (user wants to reload)
// reload=false: keep our version, reset trust
func (g *Garland) AcknowledgeSourceChange(reload bool) error

// Update stored metadata after a save
func (g *Garland) RefreshSourceInfo() error
```

## Usage Examples

### Basic Change Detection

```go
g, _ := lib.Open(FileOptions{FilePath: "myfile.txt"})
defer g.Close()

// Periodically check for changes
info, err := g.CheckSourceMetadata()
if err != nil {
    log.Fatal(err)
}

switch info.Type {
case SourceUnchanged:
    // No action needed
case SourceAppended:
    // File grew - verify and optionally load
    if err := g.VerifyBoundaryForAppend(); err == nil {
        bytesLoaded, _ := g.LoadAppendedContent()
        log.Printf("Loaded %d appended bytes", bytesLoaded)
    }
case SourceModified:
    // Warn user about external modification
    log.Println("Warning: file modified externally")
case SourceDeleted:
    log.Println("Warning: file was deleted")
}
```

### Continuous Append Mode (Log Tailing)

```go
g, _ := lib.Open(FileOptions{FilePath: "/var/log/app.log"})
defer g.Close()

// Set up continuous append mode
g.SetAppendPolicy(AppendPolicyContinuous)

// Set up notification handler
g.SetSourceChangeHandler(func(g *Garland, status SourceChangeStatus, info SourceChangeInfo) {
    if status == SourceStatusAppendAvailable {
        log.Printf("Loaded %d new bytes", info.AppendedBytes)
    }
})

// Enable watching
g.EnableSourceWatch(1 * time.Second)
defer g.DisableSourceWatch()

// ... application runs ...
```

### Handling User Decisions

```go
g.SetSourceChangeHandler(func(g *Garland, status SourceChangeStatus, info SourceChangeInfo) {
    switch status {
    case SourceStatusModified:
        // Ask user what to do
        if userWantsReload() {
            g.AcknowledgeSourceChange(true)
            // Reload the file...
        } else {
            g.AcknowledgeSourceChange(false)
            // Keep our version, restore warm trust
        }
    }
})
```

## Integration with Save Operations

After saving a file, call `RefreshSourceInfo()` to update the baseline:

```go
err := g.Save()
if err != nil {
    return err
}

// Update our baseline to match the saved file
err = g.RefreshSourceInfo()
```

## Performance Considerations

1. **Metadata checks are cheap** - `CheckSourceMetadata()` only stats the file
2. **Boundary verification is one block** - Only reads the last block before EOF
3. **Full verification is lazy** - Happens on warm reads, not proactively
4. **Configurable verification** - Can disable read verification for trusted files

## Behavior Matrix

| Scenario | HasWarm | Trust | LRU Evict | Read | Notes |
|----------|---------|-------|-----------|------|-------|
| File unchanged | Yes | Full | → Warm | No verify | Normal operation |
| File unchanged | Yes | Full | → Warm | Verify (cfg) | With verifyOnRead=true |
| File appended | Yes | Stale | Verify first | Verify | Until boundary verified |
| File modified | Yes | Stale | Verify first | Verify | Until user responds |
| User pending | Yes | Suspended | → Cold only | Verify | Protect against loss |
| No warm available | No | N/A | → Cold | N/A | Cold storage only |

## Related Documentation

- [Architecture](architecture.md) - Overall system design
- [Tree Operations](tree-operations.md) - Node splitting and merging
- [Fork Revisions](fork-revisions.md) - Version management
