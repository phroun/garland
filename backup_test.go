package garland

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// backup_test.go - pre-session backups: streamed in the background on
// the first edit (in place before Save), committed only when a save
// overwrites the protected file, and self-cleaning so viewing never
// accumulates backup storage.

func backupFixture(t *testing.T, content string) (*Garland, string, string) {
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
	g, err := lib.Open(FileOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "backups")
	if err := g.SetBackupLocation(nil, backupDir, BackupOptions{}); err != nil {
		t.Fatal(err)
	}
	return g, path, filepath.Join(backupDir, "doc.txt~")
}

func waitBackupState(t *testing.T, g *Garland, want BackupState) BackupInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		info := g.BackupInfo()
		if info.State == want {
			return info
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup state = %v, want %v (info %+v)", info.State, want, info)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestBackupNotCreatedByViewing: opening and reading a file must never
// leave backup files behind.
func TestBackupNotCreatedByViewing(t *testing.T) {
	g, _, backupPath := backupFixture(t, "view only\n")
	c := g.NewCursor()
	_ = contentOf(t, g, c)

	if got := g.BackupInfo().State; got != BackupArmed {
		t.Fatalf("state = %v, want armed", got)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(backupPath))
	if err == nil && len(entries) > 0 {
		t.Fatalf("backup dir not empty after view-only session: %v", entries)
	}
}

// TestBackupStreamsOnFirstEdit: the first mutation arms a background
// copy of the PRE-session content; a later save commits it; the
// committed backup survives Close.
func TestBackupStreamsOnFirstEdit(t *testing.T) {
	original := "original content to protect\n"
	g, path, backupPath := backupFixture(t, original)
	c := g.NewCursor()

	if _, err := c.InsertString("edit: ", nil, false); err != nil {
		t.Fatal(err)
	}
	info := waitBackupState(t, g, BackupReady)
	if info.Path != backupPath || info.Subject != path {
		t.Fatalf("backup info = %+v", info)
	}
	if info.Bytes != int64(len(original)) {
		t.Fatalf("backup bytes = %d, want %d", info.Bytes, len(original))
	}
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("backup content = %q, want %q", data, original)
	}

	if _, err := g.Save(); err != nil {
		t.Fatal(err)
	}
	if got := g.BackupInfo().State; got != BackupCommitted {
		t.Fatalf("state after save = %v, want committed", got)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	// Committed: survives Close, still the pre-session content.
	data, err = os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("backup after close = %q, want %q", data, original)
	}
}

// TestBackupRemovedWithoutSave: edits that are never saved leave the
// source intact, so the backup is not needed and Close removes it.
func TestBackupRemovedWithoutSave(t *testing.T) {
	g, _, backupPath := backupFixture(t, "never saved\n")
	c := g.NewCursor()
	if _, err := c.InsertString("edit ", nil, false); err != nil {
		t.Fatal(err)
	}
	waitBackupState(t, g, BackupReady)
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("backup survives close without a save (stat err %v)", err)
	}
}

// TestBackupBeatsImmediateSave: a Save issued before the background
// copy ever ran must still find the pre-session backup in place - the
// save performs the copy inline first.
func TestBackupBeatsImmediateSave(t *testing.T) {
	original := "must be captured before the save\n"
	g, _, backupPath := backupFixture(t, original)
	defer g.Close()
	c := g.NewCursor()

	if _, err := c.InsertString("overwrite! ", nil, false); err != nil {
		t.Fatal(err)
	}
	// No waiting: race the background worker on purpose.
	if _, err := g.Save(); err != nil {
		t.Fatal(err)
	}
	info := g.BackupInfo()
	if info.State != BackupCommitted {
		t.Fatalf("state = %v, want committed (info %+v)", info.State, info)
	}
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("backup content = %q, want pre-session %q", data, original)
	}
}

// TestBackupNotCommittedBySaveAs: exporting to another path leaves the
// protected file intact - the backup stays uncommitted (and would be
// cleaned at Close).
func TestBackupNotCommittedBySaveAs(t *testing.T) {
	g, path, _ := backupFixture(t, "export case\n")
	defer g.Close()
	c := g.NewCursor()
	if _, err := c.InsertString("edit ", nil, false); err != nil {
		t.Fatal(err)
	}
	waitBackupState(t, g, BackupReady)

	export := filepath.Join(filepath.Dir(path), "export.txt")
	if _, err := g.SaveAs(nil, export); err != nil {
		t.Fatal(err)
	}
	if got := g.BackupInfo().State; got != BackupReady {
		t.Fatalf("state after SaveAs = %v, want still ready (uncommitted)", got)
	}
}

// TestBackupDisableRemoves: turning the backup location off removes an
// uncommitted backup immediately.
func TestBackupDisableRemoves(t *testing.T) {
	g, _, backupPath := backupFixture(t, "disable me\n")
	defer g.Close()
	c := g.NewCursor()
	if _, err := c.InsertString("edit ", nil, false); err != nil {
		t.Fatal(err)
	}
	waitBackupState(t, g, BackupReady)

	if err := g.SetBackupLocation(nil, "", BackupOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := g.BackupInfo().State; got != BackupDisabled {
		t.Fatalf("state = %v, want disabled", got)
	}
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("backup survives disable (stat err %v)", err)
	}
}

// TestBackupConfiguredAfterEdits: setting the location on an
// already-dirty buffer captures immediately - the source file still
// holds the pre-session content.
func TestBackupConfiguredAfterEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	original := "late configuration\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
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
	if _, err := c.InsertString("dirty ", nil, false); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(dir, "backups")
	if err := g.SetBackupLocation(nil, backupDir, BackupOptions{Name: "custom.bak"}); err != nil {
		t.Fatal(err)
	}
	info := waitBackupState(t, g, BackupReady)
	want := filepath.Join(backupDir, "custom.bak")
	if info.Path != want {
		t.Fatalf("backup path = %q, want %q", info.Path, want)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("backup content = %q, want %q", data, original)
	}
}
