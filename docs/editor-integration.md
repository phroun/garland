# Editor Integration Checklist: Source Safety Cluster

The one-page map of what an editor built on garland needs to wire up
for external-change detection, safe saving, revert, recovery, locks,
and backups. Garland does the heavy lifting; these are the app-side
touchpoints. Full signatures: `interface.md`.

## 1. On open

- Pass a custom `FileSystem` in `FileOptions` if the file is
  virtualized. A custom FS must implement `Stat` and `DeviceInfo`
  (or return `ErrNotSupported` and volunteer metadata, see §2).
- `UseEmacsLocks: true` if you want emacs-interoperable locking (§6).
- `g.SetBackupLocation(nil, backupDir, BackupOptions{})` right after
  open (§7). Nothing else to do for backups - they are automatic.

Metadata (size/mtime/identity) is captured automatically at every
file open, in every loading style.

## 2. Detecting external modification

- **Pull**: `g.SourceConsistency()` before saving, on window focus, or
  on a timer. `g.SourceConsistencyCached()` is I/O-free for status
  bars. (Or use the existing `EnableSourceWatch(interval)` +
  `SetSourceChangeHandler` push notifications.)
- **Volunteer**: `g.ReportSourceMetadata(meta)` whenever the app
  learns facts garland can't observe (own watcher, sync client, VFS
  event). On a Stat-less VFS the first report becomes the baseline.
- Drive the UI from `SourceConsistencyReport.State`:
  - `Clean` -> save silently.
  - `Modified` / `Truncated` / `Replaced` -> prompt: **overwrite**
    (just `Save()`), **fork away to a copy** (`SaveAsWith` to a new
    path, `AdoptAsSource: true`), **merge / abandon-and-reload**
    (`RebaseOnSource()` - takes the disk as the new base; undo to
    `report.PreviousRevision` is "keep mine").
  - `Appended` (tail-follow) -> `VerifyBoundaryForAppend()` +
    `LoadAppendedContent()`.
  - `Missing` -> prompt to re-save or relocate (§5).

## 3. Saving

- Optionally warn on space first: `g.SourceDeviceInfo()` /
  `lib.DeviceInfoFor(fs, path)` (`FreeBytes` vs. `g.ByteCount()`).
- `g.Save()` / `g.SaveWith(...)`. **Always surface
  `SaveReport.Scars`** (data lost to storage failure, written as
  visible scars) and review `SaveReport.Integrity`.
- Save-As dialog needs an **adopt checkbox**:
  `g.SaveAsWith(fs, path, SaveAsOptions{AdoptAsSource: ..., PreserveHistory: true})`.
  Adopt = "the file lives here now" (source & warm storage move).
  Non-adopt = export / removable media about to be ejected.

## 4. Revert to last saved version

- Menu action -> `g.RevertToLastSave()`. It is a pure history seek:
  redo still reaches the abandoned edits; `Prune` from the save point
  to discard them for real. `g.SaveHistory()` / `g.LastSave()` for
  display.

## 5. Recovery when the source goes bad

- Corrupt/unreachable source -> `g.TryRecoverSource(VerifyFull)`
  walks known save locations and adopts one that verifies; show the
  returned `SavePoint.Path` to the user. `ErrNoRecoverySource` = none
  worked.
- Manual switch to a known-identical file:
  `g.AdoptWarmSource(fs, path, level)` with `VerifyMetadata` (swift),
  `VerifySample`, or `VerifyFull`.

## 6. Locks (only if `UseEmacsLocks` was set)

- Status display: `g.HoldsSourceLock()`, `g.SourceLockOwner()`, and
  `LockedBy` in the consistency report.
- Foreign lock seen -> warn; offer `g.BreakSourceLock()` to steal.
- Acquire/release is automatic (first edit / save / revert / undo
  onto the saved revision / Close).

## 7. Backups (automatic once configured)

- After `SetBackupLocation`: first edit streams a pre-session copy in
  the background (guaranteed in place before any save overwrites the
  file); an in-place save commits it; viewing or editing-without-
  saving leaves nothing behind. `g.BackupInfo()` for a status line;
  `BackupFailed` never blocks saves - decide whether to warn.

## 8. Memory pressure (app-side hot-write mode)

- `g.MemoryPressure()`: when `SaveableBytes` dominates and
  `ResidentBytes` nears the hard limit, prompt/trigger a save -
  "to keep editing, save before RAM runs out".
