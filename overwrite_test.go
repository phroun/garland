package garland

import (
	"testing"
)

func TestOverwriteBytesSameLength(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World!"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.SeekByte(0)

	// Overwrite "Hello" with "Jello"
	_, result, err := cursor.OverwriteBytes(5, []byte("Jello"))
	if err != nil {
		t.Fatalf("OverwriteBytes failed: %v", err)
	}
	if result.Revision != 1 {
		t.Errorf("Revision = %d, want 1", result.Revision)
	}

	// Verify content
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	if string(data) != "Jello World!" {
		t.Errorf("After overwrite: %q, want %q", string(data), "Jello World!")
	}

	// Verify byte count unchanged
	if g.ByteCount().Value != 12 {
		t.Errorf("ByteCount = %d, want 12", g.ByteCount().Value)
	}
}

func TestOverwriteBytesLongerData(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World!"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.SeekByte(6) // At "World!"

	// Overwrite "World" with "Universe" (5 -> 8 bytes)
	_, _, err := cursor.OverwriteBytes(5, []byte("Universe"))
	if err != nil {
		t.Fatalf("OverwriteBytes failed: %v", err)
	}

	// Verify content
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	if string(data) != "Hello Universe!" {
		t.Errorf("After overwrite: %q, want %q", string(data), "Hello Universe!")
	}

	// Verify byte count increased
	if g.ByteCount().Value != 15 {
		t.Errorf("ByteCount = %d, want 15", g.ByteCount().Value)
	}
}

func TestOverwriteBytesShorterData(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World!"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.SeekByte(6) // At "World!"

	// Overwrite "World" with "All" (5 -> 3 bytes)
	_, _, err := cursor.OverwriteBytes(5, []byte("All"))
	if err != nil {
		t.Fatalf("OverwriteBytes failed: %v", err)
	}

	// Verify content
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	if string(data) != "Hello All!" {
		t.Errorf("After overwrite: %q, want %q", string(data), "Hello All!")
	}

	// Verify byte count decreased
	if g.ByteCount().Value != 10 {
		t.Errorf("ByteCount = %d, want 10", g.ByteCount().Value)
	}
}

func TestOverwriteBytesSpanningMultipleLeaves(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	// Create a file with multiple insertions to create multiple leaf nodes
	g, _ := lib.Open(FileOptions{DataString: "AAAA"})
	defer g.Close()

	cursor := g.NewCursor()

	// Insert at various positions to create a tree with multiple leaves
	cursor.SeekByte(2)
	cursor.InsertString("BBBB", nil, true)

	cursor.SeekByte(4)
	cursor.InsertString("CCCC", nil, true)

	cursor.SeekByte(8)
	cursor.InsertString("DDDD", nil, true)

	// Content should be "AABBCCCCDDDDBBAA" - wait, let me trace this:
	// Start: "AAAA"
	// Insert "BBBB" at 2: "AABBBBAA"
	// Insert "CCCC" at 4: "AABBCCCCBBAA"
	// Insert "DDDD" at 8: "AABBCCCCDDDDBBAA"
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	t.Logf("Before overwrite spanning: %q", string(data))

	// Now overwrite bytes 3-10 (spanning multiple leaves)
	cursor.SeekByte(3)
	_, _, err := cursor.OverwriteBytes(8, []byte("XXXXXXXX"))
	if err != nil {
		t.Fatalf("OverwriteBytes spanning leaves failed: %v", err)
	}

	cursor.SeekByte(0)
	data, _ = cursor.ReadBytes(g.ByteCount().Value)
	t.Logf("After overwrite spanning: %q", string(data))

	// The overwrite should have worked even across leaf boundaries
	if len(data) != 16 {
		t.Errorf("After spanning overwrite: len=%d, want 16", len(data))
	}
}

func TestOverwriteBytesWithNewlines(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Line1\nLine2\nLine3"})
	defer g.Close()

	// Initial line count should be 2
	initialLines := g.LineCount().Value
	if initialLines != 2 {
		t.Errorf("Initial LineCount = %d, want 2", initialLines)
	}

	cursor := g.NewCursor()
	cursor.SeekByte(6) // At "Line2\n"

	// Overwrite "Line2\n" (6 bytes) with "A\nB\nC\n" (6 bytes, but 3 newlines instead of 1)
	_, _, err := cursor.OverwriteBytes(6, []byte("A\nB\nC\n"))
	if err != nil {
		t.Fatalf("OverwriteBytes with newlines failed: %v", err)
	}

	// Line count should have increased by 2
	newLines := g.LineCount().Value
	if newLines != 4 {
		t.Errorf("After overwrite LineCount = %d, want 4", newLines)
	}

	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	if string(data) != "Line1\nA\nB\nC\nLine3" {
		t.Errorf("After overwrite: %q, want %q", string(data), "Line1\nA\nB\nC\nLine3")
	}
}

func TestOverwriteBytesWithUTF8(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello 世界!"}) // "世界" = 6 bytes, 2 runes
	defer g.Close()

	initialRunes := g.RuneCount().Value
	if initialRunes != 9 { // "Hello " (6) + "世界" (2) + "!" (1) = 9
		t.Errorf("Initial RuneCount = %d, want 9", initialRunes)
	}

	cursor := g.NewCursor()
	cursor.SeekByte(6) // At "世界!" (byte position after "Hello ")

	// Overwrite "世界" (6 bytes, 2 runes) with "World" (5 bytes, 5 runes)
	_, _, err := cursor.OverwriteBytes(6, []byte("World"))
	if err != nil {
		t.Fatalf("OverwriteBytes with UTF-8 failed: %v", err)
	}

	// Rune count should have changed
	newRunes := g.RuneCount().Value
	if newRunes != 12 { // "Hello " (6) + "World" (5) + "!" (1) = 12
		t.Errorf("After overwrite RuneCount = %d, want 12", newRunes)
	}

	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	if string(data) != "Hello World!" {
		t.Errorf("After overwrite: %q, want %q", string(data), "Hello World!")
	}
}

func TestOverwriteBytesZeroLength(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.SeekByte(2)

	// Overwrite 0 bytes with "XYZ" - should just be an insert
	_, _, err := cursor.OverwriteBytes(0, []byte("XYZ"))
	if err != nil {
		t.Fatalf("OverwriteBytes with zero length failed: %v", err)
	}

	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	if string(data) != "HeXYZllo" {
		t.Errorf("After zero-length overwrite: %q, want %q", string(data), "HeXYZllo")
	}
}

func TestOverwriteBytesEmptyData(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World!"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.SeekByte(5)

	// Overwrite " World" with empty - should just be a delete
	_, _, err := cursor.OverwriteBytes(6, []byte{})
	if err != nil {
		t.Fatalf("OverwriteBytes with empty data failed: %v", err)
	}

	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	if string(data) != "Hello!" {
		t.Errorf("After empty data overwrite: %q, want %q", string(data), "Hello!")
	}
}

func TestOverwriteBytesUndoSeek(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World!"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.SeekByte(0)

	// Overwrite "Hello" with "Jello"
	_, result, err := cursor.OverwriteBytes(5, []byte("Jello"))
	if err != nil {
		t.Fatalf("OverwriteBytes failed: %v", err)
	}

	// Verify change was made
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	if string(data) != "Jello World!" {
		t.Errorf("After overwrite: %q", string(data))
	}

	// UndoSeek back to revision 0
	err = g.UndoSeek(result.Revision - 1)
	if err != nil {
		t.Fatalf("UndoSeek failed: %v", err)
	}

	// Verify original content is restored
	cursor.SeekByte(0)
	data, _ = cursor.ReadBytes(g.ByteCount().Value)
	if string(data) != "Hello World!" {
		t.Errorf("After UndoSeek: %q, want %q", string(data), "Hello World!")
	}
}
