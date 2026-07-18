package garland

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// emacs_lock_test.go - opt-in emacs-compatible file locking: the
// ".#<name>" lock file exists exactly while the buffer holds unsaved
// modifications, foreign locks are respected (never clobbered), and
// BreakSourceLock steals deliberately.

func lockFixture(t *testing.T, content string) (*Garland, string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{FilePath: path, UseEmacsLocks: true})
	if err != nil {
		t.Fatal(err)
	}
	return g, path, emacsLockPath(path)
}

func lockExists(t *testing.T, lockPath string) bool {
	t.Helper()
	_, err := os.Stat(lockPath)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatal(err)
	return false
}

// TestEmacsLockLifecycle: appears on first mutation, releases on save,
// follows undo/redo across the clean point, dies with Close.
func TestEmacsLockLifecycle(t *testing.T) {
	g, _, lockPath := lockFixture(t, "locked content\n")
	c := g.NewCursor()

	if lockExists(t, lockPath) {
		t.Fatal("lock file exists before any modification")
	}
	if g.HoldsSourceLock() {
		t.Fatal("HoldsSourceLock true before any modification")
	}

	if _, err := c.InsertString("dirty ", nil, false); err != nil {
		t.Fatal(err)
	}
	if !lockExists(t, lockPath) || !g.HoldsSourceLock() {
		t.Fatal("lock not acquired on first mutation")
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "@") {
		t.Fatalf("lock content = %q, want user@host.pid form", data)
	}

	if _, err := g.Save(); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) || g.HoldsSourceLock() {
		t.Fatal("lock not released by save")
	}
	savedRev := g.CurrentRevision()

	if _, err := c.InsertString("more ", nil, false); err != nil {
		t.Fatal(err)
	}
	dirtyRev := g.CurrentRevision()
	if !lockExists(t, lockPath) {
		t.Fatal("lock not re-acquired after post-save mutation")
	}

	// Undo back to the saved state: buffer matches the file again.
	if err := g.UndoSeek(savedRev); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) {
		t.Fatal("lock survives undo to the saved revision")
	}

	// Redo past it: modified again.
	if err := g.UndoSeek(dirtyRev); err != nil {
		t.Fatal(err)
	}
	if !lockExists(t, lockPath) {
		t.Fatal("lock not re-acquired on redo past the saved revision")
	}

	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) {
		t.Fatal("lock survives Close")
	}
}

// TestEmacsLockRevertToLastSave: reverting to the last saved version
// releases the lock (the buffer matches the file again).
func TestEmacsLockRevertToLastSave(t *testing.T) {
	g, _, lockPath := lockFixture(t, "revert base\n")
	defer g.Close()
	c := g.NewCursor()

	if _, err := c.InsertString("a", nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString("b", nil, false); err != nil {
		t.Fatal(err)
	}
	if !lockExists(t, lockPath) {
		t.Fatal("lock missing while dirty")
	}
	if err := g.RevertToLastSave(); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) {
		t.Fatal("lock survives RevertToLastSave")
	}
}

// TestEmacsLockForeign: a foreign lock is reported, never clobbered,
// and BreakSourceLock steals it deliberately.
func TestEmacsLockForeign(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte("contested\n"), 0644); err != nil {
		t.Fatal(err)
	}
	lockPath := emacsLockPath(path)
	const alice = "alice@elsewhere.4242"
	if err := os.WriteFile(lockPath, []byte(alice), 0644); err != nil {
		t.Fatal(err)
	}

	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{FilePath: path, UseEmacsLocks: true})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	owner, foreign := g.SourceLockOwner()
	if !foreign || owner != alice {
		t.Fatalf("owner = %q foreign=%v, want %q", owner, foreign, alice)
	}
	if got := g.SourceConsistencyCached().LockedBy; got != alice {
		t.Fatalf("LockedBy = %q, want %q", got, alice)
	}

	// Mutating must NOT clobber alice's lock.
	c := g.NewCursor()
	if _, err := c.InsertString("mine ", nil, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != alice {
		t.Fatalf("foreign lock clobbered: %q", data)
	}
	if g.HoldsSourceLock() {
		t.Fatal("claims to hold a foreign lock")
	}

	// Steal it: the buffer is dirty, so breaking also acquires.
	if err := g.BreakSourceLock(); err != nil {
		t.Fatal(err)
	}
	if !g.HoldsSourceLock() {
		t.Fatal("lock not acquired after break while dirty")
	}
	data, err = os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == alice {
		t.Fatal("lock file still alice's after break")
	}

	// Foreign lock vanishing externally is noticed by a consistency
	// probe after our own release.
	if _, err := g.Save(); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) {
		t.Fatal("lock survives save after steal")
	}
	if owner, foreign := g.SourceLockOwner(); foreign {
		t.Fatalf("stale foreign owner %q after save", owner)
	}
}

// TestEmacsLockCustomOwner: FileOptions.LockOwner controls the
// identity stamped into the lock file - the string a foreign editor
// (or another garland) sees in its "being edited by" prompt.
func TestEmacsLockCustomOwner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte("branded\n"), 0644); err != nil {
		t.Fatal(err)
	}
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const mew = "mew@workstation.7"
	g, err := lib.Open(FileOptions{
		FilePath:      path,
		UseEmacsLocks: true,
		LockOwner:     "  " + mew + "\n", // surrounding whitespace is trimmed
	})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	c := g.NewCursor()
	if _, err := c.InsertString("x", nil, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(emacsLockPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != mew {
		t.Fatalf("lock content = %q, want %q", data, mew)
	}

	// A second garland on the same file must report the custom
	// identity as the foreign owner, and recognize it is NOT its own.
	g2, err := lib.Open(FileOptions{FilePath: path, UseEmacsLocks: true})
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()
	owner, foreign := g2.SourceLockOwner()
	if !foreign || owner != mew {
		t.Fatalf("second garland sees owner %q foreign=%v, want %q", owner, foreign, mew)
	}

	// Our own lock (custom identity) is still recognized as ours on a
	// re-probe: consistency checks must not demote it to foreign.
	if _, err := g.SourceConsistency(); err != nil {
		t.Fatal(err)
	}
	if !g.HoldsSourceLock() {
		t.Fatal("custom-owner lock no longer recognized as our own")
	}
	if _, foreign := g.SourceLockOwner(); foreign {
		t.Fatal("our own custom-owner lock reported as foreign")
	}
}

// TestEmacsLockDisabledByDefault: without opting in, no lock file
// appears and the lock APIs answer inert values.
func TestEmacsLockDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte("plain\n"), 0644); err != nil {
		t.Fatal(err)
	}
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	c := g.NewCursor()
	if _, err := c.InsertString("x", nil, false); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, emacsLockPath(path)) {
		t.Fatal("lock file created without opt-in")
	}
	if g.HoldsSourceLock() {
		t.Fatal("HoldsSourceLock true without opt-in")
	}
	if err := g.BreakSourceLock(); err != ErrNotSupported {
		t.Fatalf("BreakSourceLock without opt-in = %v, want ErrNotSupported", err)
	}
}
