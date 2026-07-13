package garland

import "testing"

// Composite operations (overwrite, move, copy) are built from
// deleteRange + insertInternal. These regression tests pin the boundary
// semantics at the seams of that composition, where marks that collapse
// onto the insertion point during the delete phase must remain
// distinguishable from marks that were genuinely at the insertion point.

// TestMoveBoundaryMarks: marks above a pure insertion point slide by
// the moved length; marks inside the source travel with the content;
// marks exactly at the insertion point are governed by insertBefore.
func TestMoveBoundaryMarks(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataString: "0123456789abcdefghijklmnopqrstuv"})
	if err != nil {
		t.Fatal(err)
	}
	c := g.NewCursor()
	set := func(k string, p int64) {
		addr := ByteAddress(p)
		if _, err := g.Decorate([]DecorationEntry{{Key: k, Address: &addr}}); err != nil {
			t.Fatalf("decorate %s@%d: %v", k, p, err)
		}
	}
	set("k5", 0)
	set("k0", 3)
	set("k4", 6)
	set("k6", 6)
	set("k2", 20)

	// Move [19,24) to insertion point 3 with insertBefore=true.
	if _, err := c.MoveBytes(19, 24, 3, 3, true); err != nil {
		t.Fatalf("move: %v", err)
	}

	want := map[string]int64{"k5": 0, "k0": 8, "k4": 11, "k6": 11, "k2": 4}
	for k, w := range want {
		addr, err := g.GetDecorationPosition(k)
		if err != nil {
			t.Errorf("%s: %v", k, err)
			continue
		}
		if addr.Byte != w {
			t.Errorf("%s at %d, want %d", k, addr.Byte, w)
		}
	}
}

// TestNoOpMoveKeepsMarks: moving [s,e) to insertion point s leaves the
// content unchanged, so a mark at e (after the block) must stay at e -
// it collapses onto the seam during the source delete and must slide
// back past the re-inserted block regardless of the insertBefore flag.
func TestNoOpMoveKeepsMarks(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataString: "0123456789abcdefghij"})
	if err != nil {
		t.Fatal(err)
	}
	c := g.NewCursor()
	addr := ByteAddress(12)
	if _, err := g.Decorate([]DecorationEntry{{Key: "after", Address: &addr}}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.MoveBytes(4, 12, 4, 4, false); err != nil {
		t.Fatalf("move: %v", err)
	}
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	if got, err := c.ReadBytes(g.ByteCount().Value); err != nil || string(got) != "0123456789abcdefghij" {
		t.Fatalf("content changed by no-op move: %q, %v", got, err)
	}
	pos, err := g.GetDecorationPosition("after")
	if err != nil {
		t.Fatal(err)
	}
	if pos.Byte != 12 {
		t.Errorf("mark at %d after no-op move, want 12", pos.Byte)
	}
}

// TestOverwriteEndBoundaryMark: a mark exactly at the end of an
// overwritten range is after the replaced content and must shift by the
// net length change.
func TestOverwriteEndBoundaryMark(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataString: "0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	c := g.NewCursor()
	addr := ByteAddress(6)
	if _, err := g.Decorate([]DecorationEntry{{Key: "end", Address: &addr}}); err != nil {
		t.Fatal(err)
	}

	// Replace [2,6) with a longer piece: net +3.
	if err := c.SeekByte(2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.OverwriteBytes(4, []byte("ABCDEFG")); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	pos, err := g.GetDecorationPosition("end")
	if err != nil {
		t.Fatal(err)
	}
	if pos.Byte != 9 {
		t.Errorf("end mark at %d after overwrite, want 9", pos.Byte)
	}
}
