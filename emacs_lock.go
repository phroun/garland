package garland

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// emacs_lock.go - opt-in emacs-compatible file locking.
//
// When FileOptions.UseEmacsLocks is enabled, Garland maintains an
// emacs-style lock file next to the source (".#<name>") for exactly as
// long as the buffer holds unsaved CONTENT modifications - the same
// protocol emacs, and tools interoperating with it, use to warn two
// editors off the same file:
//
//   - Open NEVER creates a lock file: a buffer opened for viewing
//     (reads, marks, no edits) never locks. The lock appears on the
//     first CONTENT mutation past a clean point and disappears when
//     buffer and file agree again (save, revert to the saved content,
//     close).
//   - Decoration-only mutations (marks, bookmarks) mint revisions for
//     undo/redo like any other edit, but never engage the lock and
//     never disturb the clean point: decorations do not serialize into
//     the source on save, so they can never BE unsaved changes. This
//     matches emacs, where setting the mark or a bookmark does not
//     modify the buffer and never locks.
//   - The lock is a REGULAR file containing "user@host.pid", written
//     through the filesystem hook so virtualized filesystems
//     participate too. (Emacs itself prefers a dangling symlink on
//     unix but reads the regular-file form as well; symlinks are not
//     expressible through FileSystemInterface.)
//   - A lock owned by someone else is NEVER clobbered: Garland records
//     the foreign owner (SourceLockOwner, SourceConsistencyReport.
//     LockedBy) and continues without the lock; the app decides
//     whether to warn, wait, or BreakSourceLock.

// emacsLockState tracks the lock lifecycle for one garland.
type emacsLockState struct {
	enabled bool
	held    bool   // our lock file is currently on disk
	ourInfo string // "user@host.pid" - what we write and recognize as ours

	// foreignOwner, when non-empty, is the content of a lock file owned
	// by someone else; acquisition is suspended until it clears
	// (observed gone on a later probe, or BreakSourceLock).
	foreignOwner string

	// cleanContentSeq names the CONTENT state that matches the source
	// file (open baseline, then each successful save). Landing on any
	// revision carrying this content sequence releases the lock;
	// anything else wants it. Comparing content sequences instead of
	// (fork, revision) identity means decoration-only revisions can sit
	// between the clean revision and the current one without poisoning
	// the clean point: undoing all content edits still releases.
	cleanContentSeq uint64
	haveClean       bool
}

// emacsLockOwnerInfo builds our lock-owner string.
func emacsLockOwnerInfo() string {
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	if user == "" {
		user = "unknown"
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s@%s.%d", user, host, os.Getpid())
}

// emacsLockPath names the lock file for a source path: ".#<base>" in
// the same directory.
func emacsLockPath(sourcePath string) string {
	return filepath.Join(filepath.Dir(sourcePath), ".#"+filepath.Base(sourcePath))
}

// initEmacsLock enables lock tracking for this garland. Called from
// Open when FileOptions.UseEmacsLocks is set with a file source.
// owner overrides the identity written inside the lock file
// (FileOptions.LockOwner); empty falls back to the environment-derived
// "user@host.pid". Caller must hold the write lock.
func (g *Garland) initEmacsLockLocked(owner string) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = emacsLockOwnerInfo()
	}
	g.emacsLock = &emacsLockState{
		enabled:         true,
		ourInfo:         owner,
		cleanContentSeq: g.contentSeq,
		haveClean:       true,
	}
	g.probeEmacsLockLocked()
}

// lockFSLocked resolves the filesystem the lock file lives on.
func (g *Garland) lockFSLocked() FileSystemInterface {
	fs := g.sourceFS
	if fs == nil && g.lib != nil {
		fs = g.lib.defaultFS
	}
	return fs
}

// probeEmacsLockLocked refreshes foreign-lock knowledge from disk: a
// foreign lock that appeared is recorded, one that vanished is
// cleared (re-enabling our own acquisition on the next mutation).
// Caller must hold the write lock. No-op when locks are disabled.
func (g *Garland) probeEmacsLockLocked() {
	el := g.emacsLock
	if el == nil || !el.enabled || g.sourcePath == "" {
		return
	}
	fs := g.lockFSLocked()
	if fs == nil {
		return
	}
	data, err := fs.ReadFile(emacsLockPath(g.sourcePath))
	if err != nil {
		// No lock file (or unreadable): no foreign owner.
		el.foreignOwner = ""
		if el.held {
			// Our lock vanished underneath us (external cleanup).
			el.held = false
		}
		return
	}
	owner := strings.TrimSpace(string(data))
	if owner == el.ourInfo {
		el.foreignOwner = ""
		el.held = true
		return
	}
	el.foreignOwner = owner
	el.held = false
}

// acquireEmacsLockLocked writes our lock file, unless a foreign lock
// exists (recorded, never clobbered). Caller must hold the write lock.
func (g *Garland) acquireEmacsLockLocked() {
	el := g.emacsLock
	if el == nil || !el.enabled || el.held || g.sourcePath == "" {
		return
	}
	fs := g.lockFSLocked()
	if fs == nil {
		return
	}
	lockPath := emacsLockPath(g.sourcePath)
	if data, err := fs.ReadFile(lockPath); err == nil {
		owner := strings.TrimSpace(string(data))
		if owner != el.ourInfo {
			el.foreignOwner = owner
			return
		}
	}
	if err := fs.WriteFile(lockPath, []byte(el.ourInfo)); err == nil {
		el.held = true
		el.foreignOwner = ""
	}
}

// releaseEmacsLockLocked removes our lock file if we hold it. Caller
// must hold the write lock.
func (g *Garland) releaseEmacsLockLocked() {
	el := g.emacsLock
	if el == nil || !el.held {
		return
	}
	fs := g.lockFSLocked()
	if fs != nil && g.sourcePath != "" {
		_ = fs.Remove(emacsLockPath(g.sourcePath))
	}
	el.held = false
}

// emacsLockMutatedLocked is the recordMutation hook, fired for CONTENT
// mutations only (decoration-only changes never reach it): the buffer
// just diverged from (or further from) the source - make sure the lock
// is held. Cheap when already held or blocked by a foreign lock. Caller
// must hold the write lock.
func (g *Garland) emacsLockMutatedLocked() {
	el := g.emacsLock
	if el == nil || !el.enabled || el.held || el.foreignOwner != "" {
		return
	}
	g.acquireEmacsLockLocked()
}

// emacsLockSavedLocked is the successful-save hook: buffer and source
// agree at the current content state - that becomes the clean point
// and the lock is released. Caller must hold the write lock.
func (g *Garland) emacsLockSavedLocked() {
	el := g.emacsLock
	if el == nil || !el.enabled {
		return
	}
	el.cleanContentSeq = g.contentSeq
	el.haveClean = true
	g.releaseEmacsLockLocked()
}

// syncEmacsLockAfterSeekLocked is the history-seek hook (UndoSeek,
// ForkSeek, transaction rollback): landing on the clean CONTENT state
// releases the lock; landing anywhere else wants it - matching emacs,
// where undoing back to the saved state marks the buffer unmodified.
// The comparison is by content sequence, so decoration-only revisions
// between the clean revision and the landing point don't keep the lock
// alive. Caller must hold the write lock.
func (g *Garland) syncEmacsLockAfterSeekLocked() {
	el := g.emacsLock
	if el == nil || !el.enabled {
		return
	}
	if el.haveClean && g.contentSeq == el.cleanContentSeq {
		g.releaseEmacsLockLocked()
		return
	}
	if !el.held && el.foreignOwner == "" {
		g.acquireEmacsLockLocked()
	}
}

// SourceLockOwner reports the owner string of a foreign emacs-style
// lock on the source file ("user@host.pid"), as last observed. Returns
// ("", false) when no foreign lock is known - including when locks are
// disabled. SourceConsistency re-probes the lock file; this call does
// not touch the filesystem.
func (g *Garland) SourceLockOwner() (string, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.emacsLock == nil || g.emacsLock.foreignOwner == "" {
		return "", false
	}
	return g.emacsLock.foreignOwner, true
}

// HoldsSourceLock reports whether this garland currently holds the
// emacs-style lock on its source file.
func (g *Garland) HoldsSourceLock() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.emacsLock != nil && g.emacsLock.held
}

// BreakSourceLock force-removes a foreign emacs-style lock file on the
// source - the "steal the lock" choice after warning the user - and,
// when the buffer holds unsaved modifications, immediately acquires the
// lock for this garland. Returns ErrNotSupported when emacs locks were
// not enabled for this garland.
func (g *Garland) BreakSourceLock() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	el := g.emacsLock
	if el == nil || !el.enabled {
		return ErrNotSupported
	}
	if g.sourcePath == "" {
		return ErrNoDataSource
	}
	fs := g.lockFSLocked()
	if fs == nil {
		return ErrNotSupported
	}
	if err := fs.Remove(emacsLockPath(g.sourcePath)); err != nil && !os.IsNotExist(err) {
		return err
	}
	el.foreignOwner = ""
	el.held = false
	dirty := !(el.haveClean && g.contentSeq == el.cleanContentSeq)
	if dirty {
		g.acquireEmacsLockLocked()
	}
	return nil
}
