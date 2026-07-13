package garland

import "testing"

// TestCaseFoldLengthChange: case-insensitive search must use Unicode
// case folding on the original bytes. The Kelvin sign K (U+212A, 3
// bytes) folds with 'k' (1 byte); a byte-lowering implementation
// computes match offsets in lowered coordinates that no longer line up
// with the document.
func TestCaseFoldLengthChange(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// "a K b k c" with the first K being the 3-byte Kelvin sign.
	doc := "a Kb k c"
	g, err := lib.Open(FileOptions{DataString: doc})
	if err != nil {
		t.Fatal(err)
	}
	c := g.NewCursor()

	matches, err := c.FindStringAll("k", SearchOptions{CaseSensitive: false})
	if err != nil {
		t.Fatal(err)
	}
	// Kelvin sign at bytes [2,5), ascii 'b'... layout:
	// a(0) space(1) K(2,3,4) b(5) space(6) k(7) space(8) c(9)
	want := [][2]int64{{2, 5}, {7, 8}}
	if len(matches) != len(want) {
		t.Fatalf("got %d matches (%v), want %d", len(matches), matches, len(want))
	}
	for i, m := range matches {
		if m.ByteStart != want[i][0] || m.ByteEnd != want[i][1] {
			t.Errorf("match %d = [%d,%d), want [%d,%d)", i, m.ByteStart, m.ByteEnd, want[i][0], want[i][1])
		}
	}

	// Replacing case-insensitively must edit the right bytes.
	n, _, err := c.ReplaceStringAll("k", "X", SearchOptions{CaseSensitive: false})
	if err != nil || n != 2 {
		t.Fatalf("ReplaceStringAll: n=%d err=%v", n, err)
	}
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadBytes(g.ByteCount().Value)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a Xb X c" {
		t.Errorf("content after fold-insensitive replace = %q, want %q", got, "a Xb X c")
	}
}
