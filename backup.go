package garland

import (
	"io"
	"path/filepath"
)

// backup.go - per-garland pre-session backups.
//
// DESIGN: the app names a backup location (per garland, through the
// filesystem abstraction so virtualized filesystems participate). On
// the FIRST mutation past a clean point, a background thread streams a
// copy of the source file - the content as it stood when this editing
// session began - to that location, so the backup is already in place
// BEFORE Save is pressed. Merely viewing a file never creates a
// backup, and a backup that turns out never to be needed (no save ever
// overwrote the source) is removed at Close, so browsing files does
// not accumulate backup storage.
//
// GUARANTEES:
//   - The backup ALWAYS holds pre-overwrite content. The copy runs
//     under saveMu, so an in-place save cannot rewrite the source
//     while the backup streams; and a save that wins the race before
//     the background thread even started performs the copy inline
//     first (ensureBackupBeforeSave), closing the gap.
//   - The copy is atomic at the destination: streamed to "<name>.tmp"
//     and renamed into place, so a torn backup is never visible.
//   - COMMIT rule: only an in-place save of the file the backup
//     protects commits it (keeps it past Close). A SaveAs to another
//     path - export, removable media, adopt-elsewhere - leaves the
//     original file intact, so its backup stays uncommitted and is
//     cleaned up if never needed.
//   - One backup per configuration: the FIRST dirty transition after
//     SetBackupLocation captures the baseline; later saves in the same
//     session do not re-copy (the pre-session content is what a backup
//     is for). Reconfigure to start a fresh capture.

// BackupState describes where the backup machinery stands.
type BackupState int

const (
	// BackupDisabled: no backup location configured.
	BackupDisabled BackupState = iota

	// BackupArmed: configured; no modification seen yet, nothing
	// copied (viewing a file stays in this state forever).
	BackupArmed

	// BackupPending: a modification armed the copy; it has not
	// finished yet.
	BackupPending

	// BackupReady: the backup is in place at the destination.
	BackupReady

	// BackupCommitted: a save overwrote the protected file; the backup
	// is kept (survives Close).
	BackupCommitted

	// BackupFailed: the copy failed (see BackupInfo.Err). Saves are
	// never blocked by a failed backup - the app decides what to do.
	BackupFailed
)

// String returns a human-readable description of the state.
func (s BackupState) String() string {
	switch s {
	case BackupDisabled:
		return "disabled"
	case BackupArmed:
		return "armed"
	case BackupPending:
		return "pending"
	case BackupReady:
		return "ready"
	case BackupCommitted:
		return "committed"
	case BackupFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// BackupOptions configures SetBackupLocation.
type BackupOptions struct {
	// Name overrides the backup filename within the backup directory.
	// Default: "<source basename>~" (the emacs backup convention).
	// NOTE: never use the ".#<name>" form - that is the lock-file
	// namespace.
	Name string
}

// BackupInfo reports the backup machinery's current standing.
type BackupInfo struct {
	State   BackupState
	Path    string // destination backup file (once armed and resolved)
	Subject string // the source file the backup captures
	Bytes   int64  // bytes copied so far / in the finished backup
	Err     string // failure detail when State == BackupFailed
}

// backupState is the per-garland backup bookkeeping (nil = disabled).
type backupState struct {
	fs   FileSystemInterface
	dir  string
	name string // filename override, "" = "<base>~"

	wanted    bool   // a mutation armed the copy
	attempted bool   // the copy started (exactly once)
	finished  bool   // the copy finished (see err)
	committed bool   // a save overwrote the subject; backup is kept
	err       error  // copy failure, nil on success
	bytes     int64  // bytes written
	subject   string // source path captured
	path      string // destination path
}

// SetBackupLocation configures (or, with an empty dir, disables)
// pre-session backups for this garland. The directory may live on any
// filesystem (nil fs = library default / local disk). Configuring
// replaces any previous configuration; an uncommitted previous backup
// is removed. If the buffer already holds unsaved modifications, the
// capture arms immediately (the source file still holds the
// pre-session content - nothing has overwritten it yet).
// Returns ErrNoDataSource when the buffer has no source file (there
// is nothing on disk to protect).
func (g *Garland) SetBackupLocation(fs FileSystemInterface, dir string, opts BackupOptions) error {
	// Serialize against saves and an in-flight backup copy.
	g.saveMu.Lock()
	defer g.saveMu.Unlock()

	g.mu.Lock()
	defer g.mu.Unlock()

	// Replacing or disabling: a previous uncommitted backup is not
	// needed (its subject file has not been overwritten).
	g.cleanupBackupLocked()

	if dir == "" {
		return nil
	}
	if g.sourcePath == "" {
		return ErrNoDataSource
	}
	if fs == nil {
		fs = g.lib.defaultFS
	}
	g.backup = &backupState{fs: fs, dir: dir, name: opts.Name}

	// Configured after edits: the source still holds pre-session
	// content, capture it now.
	if g.bufferDirtyLocked() {
		g.backup.wanted = true
		go g.backupWorker()
	}
	return nil
}

// BackupInfo reports the backup machinery's current standing. Cheap;
// safe to call from a status-bar paint path.
func (g *Garland) BackupInfo() BackupInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()

	bs := g.backup
	if bs == nil {
		return BackupInfo{State: BackupDisabled}
	}
	info := BackupInfo{Path: bs.path, Subject: bs.subject, Bytes: bs.bytes}
	switch {
	case !bs.wanted:
		info.State = BackupArmed
	case !bs.finished:
		info.State = BackupPending
	case bs.err != nil:
		info.State = BackupFailed
		info.Err = bs.err.Error()
	case bs.committed:
		info.State = BackupCommitted
	default:
		info.State = BackupReady
	}
	return info
}

// bufferDirtyLocked reports whether the buffer holds modifications the
// source file does not: the current coordinates differ from the last
// save point (or from the open state when nothing was ever saved).
// Caller must hold at least the read lock.
func (g *Garland) bufferDirtyLocked() bool {
	if n := len(g.saveHistory); n > 0 {
		sp := g.saveHistory[n-1]
		return sp.Fork != g.currentFork || sp.Revision != g.currentRevision
	}
	return g.currentFork != 0 || g.currentRevision != 0
}

// backupMutatedLocked is the recordMutation hook: the first mutation
// arms the background copy. Nil-check plus one bool when idle. Caller
// must hold the write lock.
func (g *Garland) backupMutatedLocked() {
	bs := g.backup
	if bs == nil || bs.wanted {
		return
	}
	bs.wanted = true
	go g.backupWorker()
}

// backupWorker is the background thread: it queues behind saves (and
// is queued behind BY saves), then runs the one-shot copy.
func (g *Garland) backupWorker() {
	g.saveMu.Lock()
	defer g.saveMu.Unlock()
	g.ensureBackupBeforeSave()
}

// ensureBackupBeforeSave runs the pre-session copy if it is armed and
// has not run yet - the single body shared by the background worker
// and the save-side inline guarantee. Exactly one caller performs the
// copy (attempted flips under the lock); everyone else no-ops.
// Caller must hold saveMu and must NOT hold g.mu.
func (g *Garland) ensureBackupBeforeSave() {
	g.mu.Lock()
	bs := g.backup
	if bs == nil || !bs.wanted || bs.attempted {
		g.mu.Unlock()
		return
	}
	bs.attempted = true
	srcPath := g.sourcePath
	srcFS := g.sourceFS
	if srcFS == nil {
		srcFS = g.lib.defaultFS
	}
	base := bs.name
	if base == "" {
		base = filepath.Base(srcPath) + "~"
	}
	bs.subject = srcPath
	bs.path = filepath.Join(bs.dir, base)
	destFS, destDir, destPath := bs.fs, bs.dir, bs.path
	g.mu.Unlock()

	n, err := backupCopyFile(srcFS, srcPath, destFS, destDir, destPath)

	g.mu.Lock()
	bs.bytes = n
	bs.err = err
	bs.finished = true
	g.mu.Unlock()
}

// backupCopyFile streams srcPath to destPath in chunks, atomically
// (write "<dest>.tmp", rename into place). Runs with NO garland lock
// held; safety against a concurrent save rewriting the source comes
// from the caller holding saveMu.
func backupCopyFile(srcFS FileSystemInterface, srcPath string,
	destFS FileSystemInterface, destDir, destPath string) (int64, error) {

	if err := destFS.MkdirAll(destDir); err != nil {
		return 0, err
	}
	src, err := srcFS.Open(srcPath, OpenModeRead)
	if err != nil {
		return 0, err
	}
	defer srcFS.Close(src)
	if err := srcFS.SeekByte(src, 0); err != nil {
		return 0, err
	}

	tmp := destPath + ".tmp"
	dst, err := destFS.Open(tmp, OpenModeWrite)
	if err != nil {
		return 0, err
	}
	fail := func(n int64, err error) (int64, error) {
		destFS.Close(dst)
		_ = destFS.Remove(tmp)
		return n, err
	}

	var n int64
	const chunk = 128 << 10
	for {
		data, rerr := srcFS.ReadBytes(src, chunk)
		if len(data) > 0 {
			if werr := destFS.WriteBytes(dst, data); werr != nil {
				return fail(n, werr)
			}
			n += int64(len(data))
		}
		if rerr == io.EOF || srcFS.IsEOF(src) {
			break
		}
		if rerr != nil {
			return fail(n, rerr)
		}
		if len(data) == 0 {
			break // defensive: no progress and no EOF signal
		}
	}
	if err := destFS.Close(dst); err != nil {
		_ = destFS.Remove(tmp)
		return n, err
	}
	if err := destFS.Rename(tmp, destPath); err != nil {
		_ = destFS.Remove(tmp)
		return n, err
	}
	return n, nil
}

// commitBackupLocked marks the backup as needed-for-real: an in-place
// save is overwriting the file it protects, so it must survive Close.
// A save of a DIFFERENT path (SaveAs export, adopted elsewhere) leaves
// the subject intact and does not commit. Caller must hold the write
// lock.
func (g *Garland) commitBackupLocked() {
	bs := g.backup
	if bs == nil || !bs.finished || bs.err != nil || bs.committed {
		return
	}
	if bs.subject != g.sourcePath {
		return
	}
	bs.committed = true
}

// cleanupBackupLocked removes an uncommitted backup (its subject was
// never overwritten - it is not needed) and drops the configuration.
// Committed backups are left in place. Caller must hold saveMu (so no
// copy is in flight) and the write lock.
func (g *Garland) cleanupBackupLocked() {
	bs := g.backup
	if bs == nil {
		return
	}
	if bs.attempted && !bs.committed && bs.path != "" {
		_ = bs.fs.Remove(bs.path)
		_ = bs.fs.Remove(bs.path + ".tmp")
	}
	g.backup = nil
}
