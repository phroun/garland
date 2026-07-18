# Backup Machinery (deferred - design notes)

Requested 2026-07-18; deliberately saved for a future pass. Captured
here so the requirements survive until then.

## Requirements (from the ruling)

- **Per-garland backup location, chosen by the app.** The library user
  specifies where backups go (via the same FileSystemInterface
  abstraction, so virtualized filesystems participate).
- **Background streaming, ahead of the save.** The backup streams to
  its destination on a background thread while the user works, so that
  by the time Save is pressed the backup is *already in place* - the
  save never waits on a backup copy.
- **No accumulation from mere viewing.** If no save ever occurs and
  the backup is not needed, it is removed - opening a file to read it
  must not leave backup files behind.

## Design sketch (to validate when implemented)

- Surface: `SetBackupLocation(fs FileSystemInterface, dir string, opts
  BackupOptions)` per garland; empty dir disables.
- Trigger: first mutation past a clean point (same signal the emacs
  lock uses) starts a background stream of the *baseline* content
  (the pre-edit file) to the backup location - classic "backup of what
  the file looked like before this editing session".
- The streamer can source from warm storage while the source file is
  still clean-adjacent; integrate with saveMu so an in-flight save and
  the backup stream never fight over handles.
- Cleanup: on Close (and on revert-to-clean with no save having
  happened), remove the backup if this session never saved. A save
  "commits" the backup (kept per app policy: single, numbered, etc.).
- Save-point integration: record backup paths as additional SavePoint
  entries so TryRecoverSource can explore them too.
- Free-space integration: consult DeviceInfo on the backup device
  before streaming; report (not fail) when the backup cannot fit.

## Interactions to watch

- Concurrent save (saveInFlight) vs. background backup stream - both
  read source/warm data; coordinate via saveMu or read-only handles.
- MemoryPressure: the backup stream must not force thaws that blow the
  memory budget; stream from file/cold directly where possible.
- Emacs locks: backup files must NOT be named `.#<name>` (that is the
  lock namespace); use emacs backup conventions (`name~`) or app-chosen
  naming in the backup dir.
