package garland

import (
	"testing"
)

func TestCursorHistoryWithUndoSeek(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World"})
	defer g.Close()

	cursor := g.NewCursor()

	// Move cursor to position 6 ("World")
	cursor.SeekByte(6)
	if cursor.BytePos() != 6 {
		t.Fatalf("After SeekByte(6): BytePos = %d, want 6", cursor.BytePos())
	}
	t.Logf("Rev 0: cursor at byte %d", cursor.BytePos())

	// Make an edit at position 0 - insert "Hey "
	cursor2 := g.NewCursor()
	cursor2.InsertString("Hey ", nil, true)
	t.Logf("Rev 1: After insert, cursor at byte %d", cursor.BytePos())

	// Cursor should have moved from 6 to 10 (shifted by 4 bytes)
	if cursor.BytePos() != 10 {
		t.Errorf("After insert at 0: BytePos = %d, want 10", cursor.BytePos())
	}

	// UndoSeek back to revision 0
	err := g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek failed: %v", err)
	}

	// Cursor should be restored to position 6 (where it was at revision 0)
	if cursor.BytePos() != 6 {
		t.Errorf("After UndoSeek(0): BytePos = %d, want 6", cursor.BytePos())
	}
	t.Logf("After UndoSeek(0): cursor at byte %d", cursor.BytePos())
}

func TestCursorHistoryMultipleEdits(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "ABCDEF"})
	defer g.Close()

	cursor := g.NewCursor()

	// Rev 0: cursor at 0
	t.Logf("Rev 0: cursor at byte %d, content 'ABCDEF'", cursor.BytePos())

	// Move cursor to position 3 (after "ABC")
	cursor.SeekByte(3)
	t.Logf("Rev 0: cursor moved to byte %d", cursor.BytePos())

	// Edit 1: Insert "X" at position 1 -> "AXBCDEF"
	cursor2 := g.NewCursor()
	cursor2.SeekByte(1)
	cursor2.InsertString("X", nil, true)
	// Cursor should shift from 3 to 4
	if cursor.BytePos() != 4 {
		t.Errorf("After edit 1: BytePos = %d, want 4", cursor.BytePos())
	}
	t.Logf("Rev 1: cursor at byte %d, content 'AXBCDEF'", cursor.BytePos())

	// Edit 2: Insert "Y" at position 2 -> "AXYBCDEF"
	cursor2.SeekByte(2)
	cursor2.InsertString("Y", nil, true)
	// Cursor should shift from 4 to 5
	if cursor.BytePos() != 5 {
		t.Errorf("After edit 2: BytePos = %d, want 5", cursor.BytePos())
	}
	t.Logf("Rev 2: cursor at byte %d, content 'AXYBCDEF'", cursor.BytePos())

	// UndoSeek to revision 1 - cursor should be at 4
	err := g.UndoSeek(1)
	if err != nil {
		t.Fatalf("UndoSeek(1) failed: %v", err)
	}
	if cursor.BytePos() != 4 {
		t.Errorf("After UndoSeek(1): BytePos = %d, want 4", cursor.BytePos())
	}
	t.Logf("After UndoSeek(1): cursor at byte %d", cursor.BytePos())

	// UndoSeek to revision 0 - cursor should be at 3
	err = g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek(0) failed: %v", err)
	}
	if cursor.BytePos() != 3 {
		t.Errorf("After UndoSeek(0): BytePos = %d, want 3", cursor.BytePos())
	}
	t.Logf("After UndoSeek(0): cursor at byte %d", cursor.BytePos())
}

func TestCursorHistoryWithDeletions(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World"})
	defer g.Close()

	cursor := g.NewCursor()

	// Move cursor to position 8 ("rld")
	cursor.SeekByte(8)
	t.Logf("Rev 0: cursor at byte %d", cursor.BytePos())

	// Delete "World" (positions 6-10) - cursor at 8 is within deleted range
	cursor2 := g.NewCursor()
	cursor2.SeekByte(6)
	cursor2.DeleteBytes(5, false)
	// Cursor was within deleted range, should be moved to deletion point (6)
	if cursor.BytePos() != 6 {
		t.Errorf("After delete: BytePos = %d, want 6 (moved to deletion point)", cursor.BytePos())
	}
	t.Logf("Rev 1: cursor at byte %d", cursor.BytePos())

	// UndoSeek back to revision 0 - cursor should be restored to 8
	err := g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek(0) failed: %v", err)
	}
	if cursor.BytePos() != 8 {
		t.Errorf("After UndoSeek(0): BytePos = %d, want 8", cursor.BytePos())
	}
	t.Logf("After UndoSeek(0): cursor at byte %d", cursor.BytePos())
}

func TestCursorHistoryNewCursorAfterEdit(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	// Make an edit first (revision 1)
	cursor1 := g.NewCursor()
	cursor1.InsertString("X", nil, true)

	// Create a new cursor at revision 1
	cursor2 := g.NewCursor()
	cursor2.SeekByte(3)
	t.Logf("Rev 1: cursor2 at byte %d", cursor2.BytePos())

	// UndoSeek to revision 0 - cursor2 didn't exist at rev 0
	err := g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek(0) failed: %v", err)
	}

	// cursor2 should be clamped to valid range (or stay at logical position)
	// Since "Hello" is 5 bytes at rev 0, position 3 is valid
	if cursor2.BytePos() > g.ByteCount().Value {
		t.Errorf("cursor2 BytePos %d exceeds file size %d", cursor2.BytePos(), g.ByteCount().Value)
	}
	t.Logf("After UndoSeek(0): cursor2 at byte %d (file size %d)", cursor2.BytePos(), g.ByteCount().Value)
}

func TestCursorHistoryWithForking(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	cursor := g.NewCursor()

	// Rev 0: Move cursor to position 2
	cursor.SeekByte(2)
	t.Logf("Fork 0 Rev 0: cursor at byte %d", cursor.BytePos())

	// Edit 1: Insert "A" at position 0 -> "ABASE"
	cursor2 := g.NewCursor()
	cursor2.InsertString("A", nil, true)
	if cursor.BytePos() != 3 {
		t.Errorf("After edit 1: BytePos = %d, want 3", cursor.BytePos())
	}
	t.Logf("Fork 0 Rev 1: cursor at byte %d", cursor.BytePos())

	// Edit 2: Insert "B" at position 1 -> "ABBASE"
	cursor2.SeekByte(1)
	cursor2.InsertString("B", nil, true)
	if cursor.BytePos() != 4 {
		t.Errorf("After edit 2: BytePos = %d, want 4", cursor.BytePos())
	}
	t.Logf("Fork 0 Rev 2: cursor at byte %d", cursor.BytePos())

	// UndoSeek to revision 1, then make a different edit (creates fork)
	err := g.UndoSeek(1)
	if err != nil {
		t.Fatalf("UndoSeek(1) failed: %v", err)
	}
	if cursor.BytePos() != 3 {
		t.Errorf("After UndoSeek(1): BytePos = %d, want 3", cursor.BytePos())
	}
	t.Logf("Fork 0 Rev 1 (after undo): cursor at byte %d", cursor.BytePos())

	// Make a different edit - should create fork 1
	cursor2.SeekByte(1)
	cursor2.InsertString("X", nil, true) // -> "AXBASE"
	t.Logf("Fork %d Rev %d: cursor at byte %d", g.CurrentFork(), g.CurrentRevision(), cursor.BytePos())

	// Should now be in fork 1
	if g.CurrentFork() != 1 {
		t.Errorf("Expected fork 1, got %d", g.CurrentFork())
	}
}
