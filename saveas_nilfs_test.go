package garland

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSaveAsNilFilesystem: SaveAs(nil, path) resolves the filesystem
// like SaveWith (source fs, else library default) and streams to the
// new path - no host needs to hand-roll a FileSystemInterface, and no
// full-buffer materialization is required.
func TestSaveAsNilFilesystem(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.txt")
	content := integrityDoc(4096)
	if err := os.WriteFile(srcPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{FilePath: srcPath, MaxLeafSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Edit, then SaveAs to a brand-new path with a NIL filesystem.
	c := g.NewCursor()
	if err := c.SeekByte(100); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString("<ADDED>", nil, false); err != nil {
		t.Fatal(err)
	}
	want := readBack(t, g)

	dstPath := filepath.Join(dir, "dst.txt")
	if _, err := g.SaveAs(nil, dstPath); err != nil {
		t.Fatalf("SaveAs(nil, %q): %v", dstPath, err)
	}

	// New file holds the edited buffer...
	onDisk, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != want {
		t.Fatalf("dst content mismatch: %d vs %d bytes", len(onDisk), len(want))
	}
	// ...and the ORIGINAL source is untouched (SaveAs to a new path
	// does not disturb warm backing).
	orig, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(orig) != content {
		t.Fatal("SaveAs to a new path modified the original source file")
	}

	// Empty name is rejected cleanly, not with a filesystem panic.
	if _, err := g.SaveAs(nil, ""); err != ErrNoDataSource {
		t.Errorf("SaveAs(nil, \"\") = %v, want ErrNoDataSource", err)
	}
}

// TestExportedFilesystemAccessors: the two ways to obtain a local
// filesystem without reimplementing FileSystemInterface.
func TestExportedFilesystemAccessors(t *testing.T) {
	if NewLocalFileSystem() == nil {
		t.Fatal("NewLocalFileSystem() returned nil")
	}
	lib, _ := Init(LibraryOptions{})
	if lib.DefaultFS() == nil {
		t.Fatal("Library.DefaultFS() returned nil")
	}
	// An explicitly-obtained local FS drives SaveAs end to end.
	dir := t.TempDir()
	lib2, _ := Init(LibraryOptions{})
	g, err := lib2.Open(FileOptions{DataString: "explicit fs save"})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	dst := filepath.Join(dir, "out.txt")
	if _, err := g.SaveAs(NewLocalFileSystem(), dst); err != nil {
		t.Fatalf("SaveAs(NewLocalFileSystem(), ...): %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "explicit fs save" {
		t.Fatalf("explicit-fs SaveAs wrote %q", b)
	}
}
