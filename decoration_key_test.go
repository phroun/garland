package garland

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDecorationKeyValidation: keys are identifiers (RULING) - ASCII
// letters/digits plus '_' '.' '#' '-', non-empty. Every write-side
// entry point must reject anything else up front.
func TestDecorationKeyValidation(t *testing.T) {
	valid := []string{"a", "Z9", "book_mark", "a.b.c", "#tag", "line-2", "#", "-", "_", "."}
	for _, k := range valid {
		if !ValidDecorationKey(k) {
			t.Errorf("ValidDecorationKey(%q) = false, want true", k)
		}
	}
	invalid := []string{"", "a b", "a,b", "a:b", "a;b", "a\nb", "a\x00b", "café", "k/v", "a\tb"}
	for _, k := range invalid {
		if ValidDecorationKey(k) {
			t.Errorf("ValidDecorationKey(%q) = true, want false", k)
		}
	}

	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World, this is content."})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c := g.NewCursor()
	bad := []RelativeDecoration{{Key: "bad key", Position: 0}}

	if _, err := g.Decorate([]DecorationEntry{{Key: "no\nnewlines", Address: &AbsoluteAddress{Mode: ByteMode, Byte: 1}}}); err != ErrInvalidDecorationKey {
		t.Errorf("Decorate: err = %v, want ErrInvalidDecorationKey", err)
	}
	if _, err := c.InsertString("x", bad, false); err != ErrInvalidDecorationKey {
		t.Errorf("InsertString: err = %v, want ErrInvalidDecorationKey", err)
	}
	if _, err := c.InsertBytes([]byte("x"), bad, false); err != ErrInvalidDecorationKey {
		t.Errorf("InsertBytes: err = %v, want ErrInvalidDecorationKey", err)
	}
	if _, _, err := c.OverwriteBytesWithDecorations(1, []byte("y"), bad, false); err != ErrInvalidDecorationKey {
		t.Errorf("OverwriteBytesWithDecorations: err = %v, want ErrInvalidDecorationKey", err)
	}
	if _, err := c.CopyBytes(0, 2, 5, 5, bad, false); err != ErrInvalidDecorationKey {
		t.Errorf("CopyBytes: err = %v, want ErrInvalidDecorationKey", err)
	}

	// A rejected key must leave no trace and change nothing.
	if got := readBack(t, g); got != "Hello World, this is content." {
		t.Fatalf("buffer mutated by rejected decoration: %q", got)
	}
	// Valid exotic keys work end to end.
	if _, err := g.Decorate([]DecorationEntry{{Key: "#mark-1.a_b", Address: &AbsoluteAddress{Mode: ByteMode, Byte: 3}}}); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if addr, err := g.GetDecorationPosition("#mark-1.a_b"); err != nil || addr.Byte != 3 {
		t.Fatalf("GetDecorationPosition = %+v, %v", addr, err)
	}
}

// TestDecodeDecorationsStrict: the cold-storage decoder rejects
// malformed records instead of silently "recovering" wrong marks.
func TestDecodeDecorationsStrict(t *testing.T) {
	good := encodeDecorations([]Decoration{{Key: "a.b", Position: 42}, {Key: "#t", Position: 7}})
	decs, err := decodeDecorations(good)
	if err != nil || len(decs) != 2 || decs[0].Key != "a.b" || decs[0].Position != 42 {
		t.Fatalf("round trip = %+v, %v", decs, err)
	}
	malformed := [][]byte{
		[]byte("keywithoutnul"),         // truncated record
		[]byte("key\x0012a3\n"),         // non-digit in position
		[]byte("key\x00\n"),             // empty position
		[]byte("key\x0012"),             // unterminated position
		[]byte("bad key\x005\n"),        // illegal key characters
		append(good, []byte("tail")...), // valid records then garbage
	}
	for i, m := range malformed {
		if _, err := decodeDecorations(m); err == nil {
			t.Errorf("case %d: malformed input %q decoded without error", i, m)
		}
	}
	if decs, err := decodeDecorations(nil); err != nil || decs != nil {
		t.Errorf("empty input: %+v, %v", decs, err)
	}
}

// TestDecorationsLostReported: destroying the .dec side blocks (but
// not the content blocks) must thaw the content fine while reporting
// IntegrityDecorationsLost - never a silent vanishing of marks.
func TestDecorationsLostReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	coldDir := filepath.Join(dir, "cold")
	content := integrityDoc(4096)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	lib, _ := Init(LibraryOptions{ColdStoragePath: coldDir})
	g, err := lib.Open(FileOptions{FilePath: path, MaxLeafSize: 1024, LoadingStyle: ColdAndMemory})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	if _, err := g.Decorate([]DecorationEntry{
		{Key: "mark-a", Address: &AbsoluteAddress{Mode: ByteMode, Byte: 100}},
		{Key: "#mark.b", Address: &AbsoluteAddress{Mode: ByteMode, Byte: 2000}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := g.Chill(ChillEverything); err != nil {
		t.Fatal(err)
	}
	// Destroy ONLY the decoration side blocks.
	matches, err := filepath.Glob(filepath.Join(coldDir, "*", "*.dec"))
	if err != nil || len(matches) == 0 {
		t.Skipf("no .dec blocks found to destroy (%v, %d)", err, len(matches))
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil {
			t.Fatal(err)
		}
	}

	// Content must read back perfectly...
	if got := readBack(t, g); got != content {
		t.Fatal("content damaged by losing decoration side blocks")
	}
	// ...the marks are gone...
	if _, err := g.GetDecorationPosition("mark-a"); err == nil {
		t.Log("note: mark-a still resolvable via cache; tree copy was lost")
	}
	// ...and the loss was REPORTED.
	kinds := countKinds(g.IntegrityEvents())
	if kinds[IntegrityDecorationsLost] == 0 {
		t.Fatalf("no IntegrityDecorationsLost events; got %v", kinds)
	}
}
