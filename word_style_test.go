package garland

import "testing"

// TestSeekByWordStyles pins the two word-motion semantics on the same
// content: WordStyleSimple treats punctuation as a separator, while
// WordStyleVi stops on punctuation runs like vi's w/b.
func TestSeekByWordStyles(t *testing.T) {
	//          0123456789012345678
	content := "foo, bar.baz (qux)"
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: content})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c := g.NewCursor()

	seek := func(from int64, n int, style WordStyle) (int64, int) {
		t.Helper()
		if err := c.SeekByte(from); err != nil {
			t.Fatalf("SeekByte(%d): %v", from, err)
		}
		moved, err := c.SeekByWordStyle(n, style)
		if err != nil {
			t.Fatalf("SeekByWordStyle(%d, %v): %v", n, style, err)
		}
		return c.BytePos(), moved
	}

	// Forward from 0 ("foo"): simple skips ", " entirely -> "bar" (5);
	// vi stops on the "," run first (3).
	if pos, _ := seek(0, 1, WordStyleSimple); pos != 5 {
		t.Errorf("simple +1 from 0 = %d, want 5 (bar)", pos)
	}
	if pos, _ := seek(0, 1, WordStyleVi); pos != 3 {
		t.Errorf("vi +1 from 0 = %d, want 3 (,)", pos)
	}

	// Two vi steps from 0: "," then "bar".
	if pos, moved := seek(0, 2, WordStyleVi); pos != 5 || moved != 2 {
		t.Errorf("vi +2 from 0 = (%d, %d), want (5, 2)", pos, moved)
	}

	// From inside "bar.baz": simple sees "bar", "baz" as words with "."
	// as separator; vi sees "bar", ".", "baz".
	if pos, _ := seek(5, 1, WordStyleSimple); pos != 9 {
		t.Errorf("simple +1 from bar = %d, want 9 (baz)", pos)
	}
	if pos, _ := seek(5, 1, WordStyleVi); pos != 8 {
		t.Errorf("vi +1 from bar = %d, want 8 (.)", pos)
	}

	// Backward from EOF (18): simple lands on "qux" (14); vi lands on
	// the ")" run (17).
	if pos, _ := seek(18, -1, WordStyleSimple); pos != 14 {
		t.Errorf("simple -1 from EOF = %d, want 14 (qux)", pos)
	}
	if pos, _ := seek(18, -1, WordStyleVi); pos != 17 {
		t.Errorf("vi -1 from EOF = %d, want 17 ())", pos)
	}

	// Backward vi from "qux" start: previous stop is the "(" run.
	if pos, _ := seek(14, -1, WordStyleVi); pos != 13 {
		t.Errorf("vi -1 from qux = %d, want 13 (()", pos)
	}
	// Backward simple from "qux" start: previous word is "baz".
	if pos, _ := seek(14, -1, WordStyleSimple); pos != 9 {
		t.Errorf("simple -1 from qux = %d, want 9 (baz)", pos)
	}

	// SeekByWord (no style) must behave exactly like WordStyleSimple.
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SeekByWord(1); err != nil {
		t.Fatal(err)
	}
	if pos := c.BytePos(); pos != 5 {
		t.Errorf("SeekByWord default = %d, want 5 (simple semantics)", pos)
	}
}
