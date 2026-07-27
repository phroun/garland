package garland

import (
	"testing"
)

// emacs_lock_timing_test.go - the lazy-lock contract: Open never
// creates a lock file, the lock appears on the first CONTENT mutation
// past a clean point, and decoration-only mutations (marks, bookmarks)
// neither engage the lock / pre-session backup nor disturb the clean
// point - while still minting revisions for undo/redo like any other
// edit.

func decorate(t *testing.T, g *Garland, key string, bytePos int64) {
	t.Helper()
	addr := ByteAddress(bytePos)
	if _, err := g.Decorate([]DecorationEntry{{Key: key, Address: &addr}}); err != nil {
		t.Fatal(err)
	}
}

// TestEmacsLockOpenCloseLeavesNoLockFile: a pure viewer session -
// open, read, close - must never put a lock file on disk.
func TestEmacsLockOpenCloseLeavesNoLockFile(t *testing.T) {
	g, _, lockPath := lockFixture(t, "view only\n")

	if lockExists(t, lockPath) {
		t.Fatal("lock file exists right after Open")
	}
	c := g.NewCursor()
	if _, err := c.ReadString(4); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) || g.HoldsSourceLock() {
		t.Fatal("lock file appeared from reading")
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) {
		t.Fatal("lock file exists after Close")
	}
}

// TestEmacsLockDecorationOnlyNeverLocks: setting, moving, and removing
// marks mints revisions (undo/redo sees them) but never acquires the
// lock - a user who opens a file and marks a block to copy from it is
// not "editing" it.
func TestEmacsLockDecorationOnlyNeverLocks(t *testing.T) {
	g, _, lockPath := lockFixture(t, "marked content\n")
	defer g.Close()

	rev0 := g.CurrentRevision()
	decorate(t, g, "block_begin", 0)
	decorate(t, g, "block_end", 6)
	addr := ByteAddress(8)
	if _, err := g.Decorate([]DecorationEntry{
		{Key: "block_end", Address: &addr}, // move
		{Key: "block_begin"},               // remove
	}); err != nil {
		t.Fatal(err)
	}

	// Undo/redo history is real: each Decorate call minted a revision.
	if g.CurrentRevision() != rev0+3 {
		t.Fatalf("revision = %d, want %d (decorations must mint revisions)",
			g.CurrentRevision(), rev0+3)
	}
	if lockExists(t, lockPath) || g.HoldsSourceLock() {
		t.Fatal("decoration-only mutations acquired the emacs lock")
	}

	// Undoing a decoration revision works and still involves no lock.
	if err := g.UndoSeek(rev0 + 1); err != nil {
		t.Fatal(err)
	}
	if _, err := g.GetDecorationPosition("block_begin"); err != nil {
		t.Fatal("mark not restored by undo across decoration revisions:", err)
	}
	if lockExists(t, lockPath) || g.HoldsSourceLock() {
		t.Fatal("undo across decoration-only revisions acquired the lock")
	}

	// A save with no content changes has nothing to release and leaves
	// no lock behind.
	if _, err := g.Save(); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) || g.HoldsSourceLock() {
		t.Fatal("save after decoration-only changes left a lock")
	}
}

// TestEmacsLockDecorationRevisionsDontPoisonCleanPoint: with
// decoration revisions interleaved around content edits, undoing the
// content back to the clean state must still release the lock - the
// clean point is a CONTENT state, not a (fork, revision) identity.
func TestEmacsLockDecorationRevisionsDontPoisonCleanPoint(t *testing.T) {
	g, _, lockPath := lockFixture(t, "abcdef\n")
	defer g.Close()
	c := g.NewCursor()

	decorate(t, g, "mark", 2) // rev1: decoration only
	markRev := g.CurrentRevision()
	if lockExists(t, lockPath) {
		t.Fatal("lock acquired by decoration")
	}

	if _, err := c.InsertString("X", nil, false); err != nil { // rev2: content
		t.Fatal(err)
	}
	if !lockExists(t, lockPath) || !g.HoldsSourceLock() {
		t.Fatal("lock not acquired by content mutation after decoration")
	}

	decorate(t, g, "mark2", 4) // rev3: decoration only, lock stays
	dirtyRev := g.CurrentRevision()
	if !lockExists(t, lockPath) {
		t.Fatal("lock dropped by a decoration mutation while content dirty")
	}

	// Undo to the decoration-only revision: content matches the file
	// again even though (fork, revision) differs from the open state.
	if err := g.UndoSeek(markRev); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) || g.HoldsSourceLock() {
		t.Fatal("lock survives undo to a content-clean (decoration-only) revision")
	}

	// Redo past the content edit: dirty again.
	if err := g.UndoSeek(dirtyRev); err != nil {
		t.Fatal(err)
	}
	if !lockExists(t, lockPath) {
		t.Fatal("lock not re-acquired on redo past the content edit")
	}

	// All the way back to the open state: clean again.
	if err := g.UndoSeek(0); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) {
		t.Fatal("lock survives undo to revision 0")
	}
}

// TestEmacsLockCleanPointAfterSaveWithDecorations: a save re-anchors
// the clean point at the saved CONTENT; later decoration revisions and
// content edits behave against the new anchor.
func TestEmacsLockCleanPointAfterSaveWithDecorations(t *testing.T) {
	g, _, lockPath := lockFixture(t, "base\n")
	defer g.Close()
	c := g.NewCursor()

	if _, err := c.InsertString("v2 ", nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Save(); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) {
		t.Fatal("lock not released by save")
	}

	decorate(t, g, "bookmark", 1) // post-save decoration, still clean
	postSaveMarkRev := g.CurrentRevision()
	if lockExists(t, lockPath) {
		t.Fatal("post-save decoration acquired the lock")
	}

	if _, err := c.InsertString("dirty ", nil, false); err != nil {
		t.Fatal(err)
	}
	if !lockExists(t, lockPath) {
		t.Fatal("lock not acquired after save")
	}

	// Undo to the post-save decoration revision: content equals the
	// save, lock releases despite the decoration revision in between.
	if err := g.UndoSeek(postSaveMarkRev); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) || g.HoldsSourceLock() {
		t.Fatal("lock survives undo to saved content with decoration revision between")
	}
}

// TestEmacsLockTransactionRollback: a rollback restores the
// pre-transaction state - if that state was content-clean, the lock a
// discarded edit acquired must be released with it.
func TestEmacsLockTransactionRollback(t *testing.T) {
	g, _, lockPath := lockFixture(t, "txn base\n")
	defer g.Close()
	c := g.NewCursor()

	if err := g.TransactionStart("doomed"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString("edit", nil, false); err != nil {
		t.Fatal(err)
	}
	if !lockExists(t, lockPath) {
		t.Fatal("lock not acquired by content mutation inside transaction")
	}
	if err := g.TransactionRollback(); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) || g.HoldsSourceLock() {
		t.Fatal("lock survives rollback to a content-clean state")
	}
}

// TestEmacsLockDecorationOnlyTransaction: a committed transaction that
// only touched decorations never engages the lock.
func TestEmacsLockDecorationOnlyTransaction(t *testing.T) {
	g, _, lockPath := lockFixture(t, "txn marks\n")
	defer g.Close()

	if err := g.TransactionStart("marks"); err != nil {
		t.Fatal(err)
	}
	decorate(t, g, "a", 1)
	decorate(t, g, "b", 3)
	if _, err := g.TransactionCommit(); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, lockPath) || g.HoldsSourceLock() {
		t.Fatal("decoration-only transaction acquired the lock")
	}
}

// TestBackupNotArmedByDecorationOnly: the pre-session backup protects
// the source from being overwritten by a save of DIVERGED content -
// marking a block must not arm it.
func TestBackupNotArmedByDecorationOnly(t *testing.T) {
	g, _, backupPath := backupFixture(t, "backup subject\n")
	defer g.Close()

	decorate(t, g, "mark", 3)
	if got := g.BackupInfo().State; got != BackupArmed {
		t.Fatalf("backup state after decoration = %v, want still armed (not copying)", got)
	}
	if fileExistsForTest(t, backupPath) {
		t.Fatal("decoration-only mutation produced a backup file")
	}

	c := g.NewCursor()
	if _, err := c.InsertString("real edit ", nil, false); err != nil {
		t.Fatal(err)
	}
	waitBackupState(t, g, BackupReady)
	if !fileExistsForTest(t, backupPath) {
		t.Fatal("content mutation did not produce a backup file")
	}
}

// TestBackupConfiguredAfterDecorationsNotDirty: configuring the backup
// location after decoration-only changes must not treat the buffer as
// already dirty (bufferDirtyLocked compares content, not coordinates).
func TestBackupConfiguredAfterDecorationsNotDirty(t *testing.T) {
	g, _, _ := lockFixture(t, "late config\n")
	defer g.Close()

	decorate(t, g, "mark", 2)

	dir := t.TempDir()
	if err := g.SetBackupLocation(nil, dir, BackupOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := g.BackupInfo().State; got != BackupArmed {
		t.Fatalf("backup state = %v, want armed (decoration-only buffer is not dirty)", got)
	}
}

func fileExistsForTest(t *testing.T, path string) bool {
	t.Helper()
	return lockExists(t, path) // same stat helper, any path
}
