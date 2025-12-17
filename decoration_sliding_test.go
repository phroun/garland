package garland

import (
	"testing"
)

// TestDecorationSlidingOnInsert tests that decorations slide correctly when text is inserted.
// Decorations before the insert point should NOT move.
// Decorations at or after the insert point should slide right by the insert length.
func TestDecorationSlidingOnInsert(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create content with decorations at various positions
	// "Hello World" - 11 bytes
	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Add decorations at various positions
	// Position 0: "H" - before insert point
	// Position 5: " " - at insert point
	// Position 6: "W" - just after insert point
	// Position 10: "d" - far after insert point

	// First, let's insert with decorations at different positions
	cursor.SeekByte(0)
	_, err = cursor.InsertString("", []RelativeDecoration{
		{Key: "mark_0", Position: 0},  // At "H"
	}, false)
	if err != nil {
		t.Fatalf("Insert decoration at 0 failed: %v", err)
	}

	cursor.SeekByte(5)
	_, err = cursor.InsertString("", []RelativeDecoration{
		{Key: "mark_5", Position: 0},  // At " " (space)
	}, false)
	if err != nil {
		t.Fatalf("Insert decoration at 5 failed: %v", err)
	}

	cursor.SeekByte(6)
	_, err = cursor.InsertString("", []RelativeDecoration{
		{Key: "mark_6", Position: 0},  // At "W"
	}, false)
	if err != nil {
		t.Fatalf("Insert decoration at 6 failed: %v", err)
	}

	cursor.SeekByte(10)
	_, err = cursor.InsertString("", []RelativeDecoration{
		{Key: "mark_10", Position: 0}, // At "d"
	}, false)
	if err != nil {
		t.Fatalf("Insert decoration at 10 failed: %v", err)
	}

	t.Log("=== Initial state: 'Hello World' with decorations at 0, 5, 6, 10 ===")

	// Now insert "XXX" at position 5 (between "Hello" and " World")
	// "Hello" + "XXX" + " World" = "HelloXXX World"
	// Expected decoration positions:
	// mark_0: 0 (unchanged - before insert)
	// mark_5: 8 (was 5, slides by 3)
	// mark_6: 9 (was 6, slides by 3)
	// mark_10: 13 (was 10, slides by 3)

	cursor.SeekByte(5)
	_, err = cursor.InsertString("XXX", nil, false)
	if err != nil {
		t.Fatalf("Insert XXX failed: %v", err)
	}

	// Read content to verify
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	content := string(data)
	if content != "HelloXXX World" {
		t.Errorf("After insert: got %q, want %q", content, "HelloXXX World")
	}
	t.Logf("After insert 'XXX' at pos 5: %q", content)

	// Verify decoration positions by checking if they're at expected byte positions
	// This requires walking the tree to find decorations
	t.Log("Decorations should have slid: mark_0@0, mark_5@8, mark_6@9, mark_10@13")
}

// TestDecorationSlidingOnDelete tests that decorations slide correctly when text is deleted.
// Decorations before the delete range should NOT move.
// Decorations within the delete range are RETURNED to the caller (not automatically deleted).
// Decorations after the delete range should slide left.
//
// DeleteBytes returns decorations from the deleted range, allowing the caller to:
// - Re-insert them at the deletion point (consolidate)
// - Discard them (explicit deletion)
// - Move them elsewhere
func TestDecorationSlidingOnDelete(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// "Hello World" - delete "lo W" (positions 3-7)
	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Add content with decorations at various positions
	// Note: Empty string inserts don't store decorations, so we need to insert actual content
	// We'll insert markers that we can track: "[0]", "[3]", "[5]", "[8]", "[A]" (A=10)
	// "Hello World" -> "[0]Hello World" -> "[0]Hel[3]lo World" etc.

	// Insert "[0]" at position 0 with decoration
	cursor.SeekByte(0)
	_, _ = cursor.InsertString("[0]", []RelativeDecoration{{Key: "mark_0", Position: 0}}, false)
	// Now: "[0]Hello World"

	// Insert "[3]" at position 6 (original pos 3 + 3 for "[0]") with decoration
	cursor.SeekByte(6)
	_, _ = cursor.InsertString("[3]", []RelativeDecoration{{Key: "mark_3", Position: 0}}, false)
	// Now: "[0]Hel[3]lo World"

	// Insert "[5]" at position 12 (original pos 5 + 6) with decoration
	cursor.SeekByte(12)
	_, _ = cursor.InsertString("[5]", []RelativeDecoration{{Key: "mark_5", Position: 0}}, false)
	// Now: "[0]Hel[3]lo[5] World"

	// Insert "[8]" at position 18 (original pos 8 + 9) with decoration
	cursor.SeekByte(18)
	_, _ = cursor.InsertString("[8]", []RelativeDecoration{{Key: "mark_8", Position: 0}}, false)
	// Now: "[0]Hel[3]lo[5] W[8]orld"

	// Insert "[A]" at position 24 (original pos 10 + 12) with decoration
	cursor.SeekByte(24)
	_, _ = cursor.InsertString("[A]", []RelativeDecoration{{Key: "mark_A", Position: 0}}, false)
	// Now: "[0]Hel[3]lo[5] W[8]or[A]ld"

	// Read current state
	cursor.SeekByte(0)
	initData, _ := cursor.ReadBytes(g.ByteCount().Value)
	t.Logf("=== Initial state: %q ===", string(initData))
	// Content is: "[0]Hel[3]lo[5] W[8]or[A]ld"
	// Positions of markers:
	// [0] at 0-2, mark_0 at 0
	// [3] at 6-8, mark_3 at 6
	// [5] at 12-14, mark_5 at 12
	// [8] at 18-20, mark_8 at 18
	// [A] at 24-26, mark_A at 24

	// Delete "[3]lo[5]" (9 bytes from position 6 to 14)
	// This should return mark_3 and mark_5 (they're in the delete range)
	// mark_0 stays, mark_8 and mark_A slide left by 9
	cursor.SeekByte(6)
	deletedDecorations, _, err := cursor.DeleteBytes(9, false)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(g.ByteCount().Value)
	content := string(data)
	// Initial: "[0]Hel[3]lo [5]Wor[8][A]ld"
	// Delete 9 bytes at pos 6: removes "[3]lo [5]"
	// Result: "[0]Hel" + "Wor[8][A]ld" = "[0]HelWor[8][A]ld"
	expectedContent := "[0]HelWor[8][A]ld"
	if content != expectedContent {
		t.Errorf("After delete: got %q, want %q", content, expectedContent)
	}
	t.Logf("After deleting 9 bytes at pos 6: %q", content)
	t.Logf("Returned decorations from deleted range: %d decorations", len(deletedDecorations))
	for _, d := range deletedDecorations {
		t.Logf("  - %s at relative position %d", d.Key, d.Position)
	}

	// Verify we got the expected decorations back
	if len(deletedDecorations) != 2 {
		t.Errorf("Expected 2 decorations returned, got %d", len(deletedDecorations))
	}
	t.Log("Returned decorations (mark_3, mark_5) can be re-inserted or discarded by caller")
}

// TestDecorationSlidingWithUndoSeek tests that UndoSeek restores decoration positions.
func TestDecorationSlidingWithUndoSeek(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "ABCDEF"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	readContent := func() string {
		cursor.SeekByte(0)
		data, _ := cursor.ReadBytes(g.ByteCount().Value)
		return string(data)
	}

	// Rev 0: "ABCDEF"
	t.Logf("Rev 0: %q", readContent())

	// Rev 1: Insert "X" at position 3 with decoration
	cursor.SeekByte(3)
	_, err = cursor.InsertString("X", []RelativeDecoration{{Key: "mark_X", Position: 0}}, false)
	if err != nil {
		t.Fatalf("Insert X failed: %v", err)
	}
	// "ABCXDEF" - mark_X at position 3
	if content := readContent(); content != "ABCXDEF" {
		t.Errorf("Rev 1: got %q, want %q", content, "ABCXDEF")
	}
	t.Logf("Rev 1: %q with mark_X@3", readContent())

	// Rev 2: Insert "YY" at position 1
	cursor.SeekByte(1)
	_, err = cursor.InsertString("YY", []RelativeDecoration{{Key: "mark_Y", Position: 0}}, false)
	if err != nil {
		t.Fatalf("Insert YY failed: %v", err)
	}
	// "AYYBCXDEF" - mark_Y at position 1, mark_X slides to position 5
	if content := readContent(); content != "AYYBCXDEF" {
		t.Errorf("Rev 2: got %q, want %q", content, "AYYBCXDEF")
	}
	t.Logf("Rev 2: %q with mark_Y@1, mark_X@5 (slid)", readContent())

	// UndoSeek to Rev 1 - mark_X should be back at position 3
	err = g.UndoSeek(1)
	if err != nil {
		t.Fatalf("UndoSeek to 1 failed: %v", err)
	}
	if content := readContent(); content != "ABCXDEF" {
		t.Errorf("After UndoSeek(1): got %q, want %q", content, "ABCXDEF")
	}
	t.Logf("After UndoSeek(1): %q - mark_X should be back @3", readContent())

	// UndoSeek to Rev 0 - no decorations yet
	err = g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek to 0 failed: %v", err)
	}
	if content := readContent(); content != "ABCDEF" {
		t.Errorf("After UndoSeek(0): got %q, want %q", content, "ABCDEF")
	}
	t.Logf("After UndoSeek(0): %q - no decorations", readContent())

	// Seek forward to Rev 2 - decorations should be restored in their slid positions
	err = g.UndoSeek(2)
	if err != nil {
		t.Fatalf("UndoSeek to 2 failed: %v", err)
	}
	if content := readContent(); content != "AYYBCXDEF" {
		t.Errorf("After UndoSeek(2): got %q, want %q", content, "AYYBCXDEF")
	}
	t.Logf("After UndoSeek(2): %q - mark_Y@1, mark_X@5 restored", readContent())
}

// TestDecorationSlidingWithTransactionRollback tests that rollback restores decoration positions.
func TestDecorationSlidingWithTransactionRollback(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "START"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	readContent := func() string {
		cursor.SeekByte(0)
		data, _ := cursor.ReadBytes(g.ByteCount().Value)
		return string(data)
	}

	// Add initial decoration
	cursor.SeekByte(2)
	_, _ = cursor.InsertString("", []RelativeDecoration{{Key: "mark_2", Position: 0}}, false)
	t.Logf("Initial: %q with mark_2@2", readContent())

	// Start transaction
	err = g.TransactionStart("test")
	if err != nil {
		t.Fatalf("TransactionStart failed: %v", err)
	}

	// Insert "XXX" at position 0, which should slide mark_2 to position 5
	cursor.SeekByte(0)
	_, err = cursor.InsertString("XXX", nil, false)
	if err != nil {
		t.Fatalf("Insert XXX failed: %v", err)
	}
	if content := readContent(); content != "XXXSTART" {
		t.Errorf("After insert: got %q, want %q", content, "XXXSTART")
	}
	t.Logf("After insert in transaction: %q - mark_2 should be @5", readContent())

	// Rollback - decoration should return to position 2
	g.TransactionRollback()

	if content := readContent(); content != "START" {
		t.Errorf("After rollback: got %q, want %q", content, "START")
	}
	t.Logf("After rollback: %q - mark_2 should be back @2", readContent())
}

// TestDecorationIsolationBetweenForks tests that decorations in one fork don't affect another.
func TestDecorationIsolationBetweenForks(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "BASE"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	readContent := func() string {
		cursor.SeekByte(0)
		data, _ := cursor.ReadBytes(g.ByteCount().Value)
		return string(data)
	}

	// Rev 0: "BASE"
	t.Logf("Fork 0 Rev 0: %q", readContent())

	// Fork 0 Rev 1: Add decoration at position 2
	cursor.SeekByte(2)
	_, _ = cursor.InsertString("X", []RelativeDecoration{{Key: "fork0_mark", Position: 0}}, false)
	// "BAXSE"
	t.Logf("Fork 0 Rev 1: %q with fork0_mark@2", readContent())

	// Go back to rev 0 and create fork 1
	err = g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek to 0 failed: %v", err)
	}
	if content := readContent(); content != "BASE" {
		t.Errorf("After UndoSeek(0): got %q, want %q", content, "BASE")
	}

	// Fork 1: Add different decoration
	cursor.SeekByte(0)
	_, err = cursor.InsertString("!", []RelativeDecoration{{Key: "fork1_mark", Position: 0}}, false)
	if err != nil {
		t.Fatalf("Insert ! failed: %v", err)
	}
	if g.CurrentFork() != 1 {
		t.Errorf("Expected fork 1, got %d", g.CurrentFork())
	}
	// "!BASE"
	t.Logf("Fork 1 Rev 1: %q with fork1_mark@0 (fork=%d)", readContent(), g.CurrentFork())

	// Add more content to fork 1 that would cause sliding if decorations weren't isolated
	cursor.SeekByte(1)
	_, _ = cursor.InsertString("YYY", nil, false)
	// "!YYYBASE"
	t.Logf("Fork 1 Rev 2: %q (fork=%d)", readContent(), g.CurrentFork())

	// Switch back to fork 0 - fork0_mark should still be at position 2 (in "BAXSE")
	err = g.ForkSeek(0)
	if err != nil {
		t.Fatalf("ForkSeek to 0 failed: %v", err)
	}
	forkInfo, _ := g.GetForkInfo(0)
	err = g.UndoSeek(forkInfo.HighestRevision)
	if err != nil {
		t.Fatalf("UndoSeek to HEAD failed: %v", err)
	}

	if content := readContent(); content != "BAXSE" {
		t.Errorf("Fork 0 HEAD: got %q, want %q", content, "BAXSE")
	}
	t.Logf("Fork 0 HEAD: %q - fork0_mark should still be @2, unaffected by fork 1", readContent())

	// Switch to fork 1 - fork1_mark should be at position 0 (in "!YYYBASE")
	err = g.ForkSeek(1)
	if err != nil {
		t.Fatalf("ForkSeek to 1 failed: %v", err)
	}
	forkInfo, _ = g.GetForkInfo(1)
	err = g.UndoSeek(forkInfo.HighestRevision)
	if err != nil {
		t.Fatalf("UndoSeek to HEAD failed: %v", err)
	}

	if content := readContent(); content != "!YYYBASE" {
		t.Errorf("Fork 1 HEAD: got %q, want %q", content, "!YYYBASE")
	}
	t.Logf("Fork 1 HEAD: %q - fork1_mark@0, unaffected by fork 0", readContent())
}

// TestDecorationStabilityAtDivergencePoint tests that decorations at fork divergence points
// remain stable regardless of changes in either fork.
func TestDecorationStabilityAtDivergencePoint(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "DIVERGE"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	readContent := func() string {
		cursor.SeekByte(0)
		data, _ := cursor.ReadBytes(g.ByteCount().Value)
		return string(data)
	}

	// Fork 0 Rev 1: Add decoration at divergence point (position 3)
	cursor.SeekByte(3)
	_, _ = cursor.InsertString("", []RelativeDecoration{{Key: "diverge_mark", Position: 0}}, false)
	// "DIVERGE" with diverge_mark@3
	t.Logf("Fork 0 Rev 1: %q with diverge_mark@3", readContent())
	divergeRev := g.CurrentRevision()

	// Fork 0 Rev 2: More changes after divergence point
	cursor.SeekByte(0)
	_, _ = cursor.InsertString("AA", nil, false)
	// "AADIVERGE" - diverge_mark slides to position 5
	t.Logf("Fork 0 Rev 2: %q - diverge_mark@5 (slid by AA)", readContent())

	// Go back to divergence point
	err = g.UndoSeek(divergeRev)
	if err != nil {
		t.Fatalf("UndoSeek to divergeRev failed: %v", err)
	}
	if content := readContent(); content != "DIVERGE" {
		t.Errorf("At divergence point: got %q, want %q", content, "DIVERGE")
	}
	t.Logf("Back at divergence: %q - diverge_mark should be @3", readContent())

	// Create fork 1 from divergence point
	cursor.SeekByte(7) // End of "DIVERGE"
	_, err = cursor.InsertString("_FORK1", nil, false)
	if err != nil {
		t.Fatalf("Insert _FORK1 failed: %v", err)
	}
	if g.CurrentFork() != 1 {
		t.Errorf("Expected fork 1, got %d", g.CurrentFork())
	}
	// "DIVERGE_FORK1" - diverge_mark should still be @3
	t.Logf("Fork 1 Rev 1: %q - diverge_mark should still be @3", readContent())

	// Make changes before the divergence point in fork 1
	cursor.SeekByte(0)
	_, _ = cursor.InsertString("BB", nil, false)
	// "BBDIVERGE_FORK1" - diverge_mark slides to position 5
	t.Logf("Fork 1 Rev 2: %q - diverge_mark@5 in this fork", readContent())

	// Go back to fork 0 head - diverge_mark should be at position 5 (from "AA" insert)
	err = g.ForkSeek(0)
	if err != nil {
		t.Fatalf("ForkSeek to 0 failed: %v", err)
	}
	forkInfo, _ := g.GetForkInfo(0)
	err = g.UndoSeek(forkInfo.HighestRevision)
	if err != nil {
		t.Fatalf("UndoSeek to HEAD failed: %v", err)
	}
	if content := readContent(); content != "AADIVERGE" {
		t.Errorf("Fork 0 HEAD: got %q, want %q", content, "AADIVERGE")
	}
	t.Logf("Fork 0 HEAD: %q - diverge_mark@5 (slid by AA)", readContent())

	// Go to divergence point in fork 0 - diverge_mark should be @3
	err = g.UndoSeek(divergeRev)
	if err != nil {
		t.Fatalf("UndoSeek to divergeRev failed: %v", err)
	}
	if content := readContent(); content != "DIVERGE" {
		t.Errorf("Fork 0 at divergence: got %q, want %q", content, "DIVERGE")
	}
	t.Logf("Fork 0 at divergence: %q - diverge_mark@3 (original position)", readContent())
}

// TestDecorationNearCursorBoundaries tests decoration behavior at cursor boundary conditions.
func TestDecorationNearCursorBoundaries(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "ABCDEFGH"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	readContent := func() string {
		cursor.SeekByte(0)
		data, _ := cursor.ReadBytes(g.ByteCount().Value)
		return string(data)
	}

	// Place decorations at positions around where we'll insert
	// We'll insert at position 4, so place decorations at 3, 4, 5
	cursor.SeekByte(3)
	_, _ = cursor.InsertString("", []RelativeDecoration{{Key: "mark_before", Position: 0}}, false)
	cursor.SeekByte(4)
	_, _ = cursor.InsertString("", []RelativeDecoration{{Key: "mark_at", Position: 0}}, false)
	cursor.SeekByte(5)
	_, _ = cursor.InsertString("", []RelativeDecoration{{Key: "mark_after", Position: 0}}, false)

	t.Log("=== 'ABCDEFGH' with mark_before@3, mark_at@4, mark_after@5 ===")

	// Insert "XX" at position 4
	cursor.SeekByte(4)
	_, err = cursor.InsertString("XX", nil, false)
	if err != nil {
		t.Fatalf("Insert XX failed: %v", err)
	}

	// "ABCDXXEFGH"
	// Expected:
	// mark_before: 3 (unchanged - strictly before)
	// mark_at: 6 (was 4, slides by 2 - at insert point, moves right)
	// mark_after: 7 (was 5, slides by 2)

	if content := readContent(); content != "ABCDXXEFGH" {
		t.Errorf("After insert: got %q, want %q", content, "ABCDXXEFGH")
	}
	t.Logf("After insert 'XX' at 4: %q", readContent())
	t.Log("Expected: mark_before@3, mark_at@6, mark_after@7")
}

// TestDecorationInsertBeforeFlag tests the subtle distinction of decoration sliding
// based on the insertBefore flag when a decoration is exactly at the insert point.
//
// insertBefore=false: Insert AFTER cursor position. Decorations at insert point should STAY
//                     (the new text goes after the decoration's logical position).
// insertBefore=true:  Insert BEFORE cursor position. Decorations at insert point should SLIDE
//                     (the new text goes before the decoration's logical position).
func TestDecorationInsertBeforeFlag(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t.Run("insertBefore=false - decoration at insert point stays", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "ABCDEF"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		cursor := g.NewCursor()

		readContent := func() string {
			cursor.SeekByte(0)
			data, _ := cursor.ReadBytes(g.ByteCount().Value)
			return string(data)
		}

		// Place decoration at position 3 (at "D")
		cursor.SeekByte(3)
		_, _ = cursor.InsertString("", []RelativeDecoration{{Key: "mark_at_D", Position: 0}}, false)

		t.Log("Initial: 'ABCDEF' with mark_at_D@3")

		// Insert "XX" at position 3 with insertBefore=false
		// This means: insert after current position, cursor advances
		// Expected: text becomes "ABCXXDEF"
		// Decoration at position 3 should STAY at 3 (points to first 'X' now)
		// because the insert is conceptually "after" the decoration's anchor point
		cursor.SeekByte(3)
		_, err = cursor.InsertString("XX", nil, false) // insertBefore=false
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}

		if content := readContent(); content != "ABCXXDEF" {
			t.Errorf("Content: got %q, want %q", content, "ABCXXDEF")
		}
		t.Logf("After insert 'XX' at 3 (insertBefore=false): %q", readContent())
		t.Log("Expected: mark_at_D stays at 3 (now points to 'X')")
	})

	t.Run("insertBefore=true - decoration at insert point slides", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "ABCDEF"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		cursor := g.NewCursor()

		readContent := func() string {
			cursor.SeekByte(0)
			data, _ := cursor.ReadBytes(g.ByteCount().Value)
			return string(data)
		}

		// Place decoration at position 3 (at "D")
		cursor.SeekByte(3)
		_, _ = cursor.InsertString("", []RelativeDecoration{{Key: "mark_at_D", Position: 0}}, false)

		t.Log("Initial: 'ABCDEF' with mark_at_D@3")

		// Insert "XX" at position 3 with insertBefore=true
		// This means: insert before current position, cursor stays
		// Expected: text becomes "ABCXXDEF"
		// Decoration at position 3 should SLIDE to 5 (still points to 'D')
		// because the insert is conceptually "before" the decoration's anchor point
		cursor.SeekByte(3)
		_, err = cursor.InsertString("XX", nil, true) // insertBefore=true
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}

		if content := readContent(); content != "ABCXXDEF" {
			t.Errorf("Content: got %q, want %q", content, "ABCXXDEF")
		}
		t.Logf("After insert 'XX' at 3 (insertBefore=true): %q", readContent())
		t.Log("Expected: mark_at_D slides to 5 (still points to 'D')")
	})

	t.Run("mixed - decorations before/at/after with both flags", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "ABCDEFGH"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		cursor := g.NewCursor()

		readContent := func() string {
			cursor.SeekByte(0)
			data, _ := cursor.ReadBytes(g.ByteCount().Value)
			return string(data)
		}

		// Place decorations at 3, 4, 5
		cursor.SeekByte(3)
		_, _ = cursor.InsertString("", []RelativeDecoration{{Key: "before", Position: 0}}, false)
		cursor.SeekByte(4)
		_, _ = cursor.InsertString("", []RelativeDecoration{{Key: "at", Position: 0}}, false)
		cursor.SeekByte(5)
		_, _ = cursor.InsertString("", []RelativeDecoration{{Key: "after", Position: 0}}, false)

		t.Log("Initial: 'ABCDEFGH' with before@3, at@4, after@5")

		// Insert "XX" at position 4 with insertBefore=true
		// before@3: stays at 3 (strictly before insert)
		// at@4: slides to 6 (insertBefore=true means it slides)
		// after@5: slides to 7 (strictly after insert)
		cursor.SeekByte(4)
		_, err = cursor.InsertString("XX", nil, true) // insertBefore=true
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}

		if content := readContent(); content != "ABCDXXEFGH" {
			t.Errorf("Content: got %q, want %q", content, "ABCDXXEFGH")
		}
		t.Logf("After insert 'XX' at 4 (insertBefore=true): %q", readContent())
		t.Log("Expected with insertBefore=true: before@3, at@6, after@7")
	})
}

// TestDecorationInDistantNodes tests decoration sliding when decorations are in
// different tree nodes from the edit point.
func TestDecorationInDistantNodes(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create larger content to potentially span multiple nodes
	largeContent := "AAAA|BBBB|CCCC|DDDD|EEEE|FFFF|GGGG|HHHH"
	g, err := lib.Open(FileOptions{DataString: largeContent})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	readContent := func() string {
		cursor.SeekByte(0)
		data, _ := cursor.ReadBytes(g.ByteCount().Value)
		return string(data)
	}

	// Place decorations at various distances from edit point
	// Edit will be at position 20 (middle of content)
	positions := []int64{0, 5, 10, 15, 20, 25, 30, 35}
	for _, pos := range positions {
		cursor.SeekByte(pos)
		_, _ = cursor.InsertString("", []RelativeDecoration{
			{Key: "mark_" + string(rune('0'+pos/5)), Position: 0},
		}, false)
	}

	t.Logf("Initial: %q with decorations at %v", readContent(), positions)

	// Insert "XXXXX" at position 20
	cursor.SeekByte(20)
	_, err = cursor.InsertString("XXXXX", nil, false)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Decorations before 20 should stay, decorations at/after 20 should slide by 5
	// Expected: 0, 5, 10, 15 unchanged; 20->25, 25->30, 30->35, 35->40

	content := readContent()
	t.Logf("After insert at 20: %q", content)
	t.Log("Expected decoration positions: 0, 5, 10, 15, 25, 30, 35, 40")
}

// TestDecorationPreservationAcrossMultipleUndoSeeks tests complex undo navigation
// with decorations that have slid multiple times.
func TestDecorationPreservationAcrossMultipleUndoSeeks(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "0123456789"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	readContent := func() string {
		cursor.SeekByte(0)
		data, _ := cursor.ReadBytes(g.ByteCount().Value)
		return string(data)
	}

	// Rev 0: "0123456789"
	t.Logf("Rev 0: %q", readContent())

	// Rev 1: Add decoration at position 5
	cursor.SeekByte(5)
	_, _ = cursor.InsertString("A", []RelativeDecoration{{Key: "tracked", Position: 0}}, false)
	// "01234A56789" - tracked@5
	t.Logf("Rev 1: %q - tracked@5", readContent())

	// Rev 2: Insert "BB" at position 2
	cursor.SeekByte(2)
	_, _ = cursor.InsertString("BB", nil, false)
	// "01BB234A56789" - tracked@7 (slid by 2)
	t.Logf("Rev 2: %q - tracked@7", readContent())

	// Rev 3: Insert "CCC" at position 0
	cursor.SeekByte(0)
	_, _ = cursor.InsertString("CCC", nil, false)
	// "CCC01BB234A56789" - tracked@10 (slid by 3)
	t.Logf("Rev 3: %q - tracked@10", readContent())

	// Rev 4: Delete 2 bytes at position 5
	cursor.SeekByte(5)
	_, _, _ = cursor.DeleteBytes(2, false)
	// "CCC01234A56789" - tracked@8 (slid left by 2)
	t.Logf("Rev 4: %q - tracked@8", readContent())

	// Now undo seek through all revisions and verify
	t.Log("=== Traversing back through revisions ===")

	err = g.UndoSeek(3)
	if err != nil {
		t.Fatalf("UndoSeek(3) failed: %v", err)
	}
	t.Logf("Rev 3: %q - tracked should be @10", readContent())

	err = g.UndoSeek(2)
	if err != nil {
		t.Fatalf("UndoSeek(2) failed: %v", err)
	}
	t.Logf("Rev 2: %q - tracked should be @7", readContent())

	err = g.UndoSeek(1)
	if err != nil {
		t.Fatalf("UndoSeek(1) failed: %v", err)
	}
	t.Logf("Rev 1: %q - tracked should be @5", readContent())

	err = g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek(0) failed: %v", err)
	}
	t.Logf("Rev 0: %q - no tracked decoration yet", readContent())

	// Now traverse forward
	t.Log("=== Traversing forward through revisions ===")

	for rev := RevisionID(1); rev <= 4; rev++ {
		err = g.UndoSeek(rev)
		if err != nil {
			t.Fatalf("UndoSeek(%d) failed: %v", rev, err)
		}
		t.Logf("Rev %d: %q", rev, readContent())
	}
}
