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

	// Verify decoration positions
	// With insertBefore=false, decorations AT the insert point stay (don't slide)
	expectedPositions := map[string]int64{
		"mark_0":  0,  // unchanged - before insert
		"mark_5":  5,  // stays - at insert point with insertBefore=false
		"mark_6":  9,  // was 6, slides by 3
		"mark_10": 13, // was 10, slides by 3
	}

	for key, expected := range expectedPositions {
		pos, err := g.GetDecorationPosition(key)
		if err != nil {
			t.Fatalf("GetDecorationPosition(%s) failed: %v", key, err)
		}
		if pos.Byte != expected {
			t.Errorf("Decoration %s: got position %d, want %d", key, pos.Byte, expected)
		}
	}
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
	pos, err := g.GetDecorationPosition("mark_X")
	if err != nil {
		t.Fatalf("GetDecorationPosition(mark_X) failed: %v", err)
	}
	if pos.Byte != 3 {
		t.Errorf("Rev 1 mark_X: got position %d, want 3", pos.Byte)
	}

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
	pos, err = g.GetDecorationPosition("mark_Y")
	if err != nil {
		t.Fatalf("GetDecorationPosition(mark_Y) failed: %v", err)
	}
	if pos.Byte != 1 {
		t.Errorf("Rev 2 mark_Y: got position %d, want 1", pos.Byte)
	}
	pos, err = g.GetDecorationPosition("mark_X")
	if err != nil {
		t.Fatalf("GetDecorationPosition(mark_X) failed: %v", err)
	}
	if pos.Byte != 5 {
		t.Errorf("Rev 2 mark_X: got position %d, want 5 (should have slid)", pos.Byte)
	}

	// UndoSeek to Rev 1 - mark_X should be back at position 3
	err = g.UndoSeek(1)
	if err != nil {
		t.Fatalf("UndoSeek to 1 failed: %v", err)
	}
	if content := readContent(); content != "ABCXDEF" {
		t.Errorf("After UndoSeek(1): got %q, want %q", content, "ABCXDEF")
	}
	pos, err = g.GetDecorationPosition("mark_X")
	if err != nil {
		t.Fatalf("After UndoSeek(1) GetDecorationPosition(mark_X) failed: %v", err)
	}
	if pos.Byte != 3 {
		t.Errorf("After UndoSeek(1) mark_X: got position %d, want 3", pos.Byte)
	}

	// UndoSeek to Rev 0 - no decorations yet
	err = g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek to 0 failed: %v", err)
	}
	if content := readContent(); content != "ABCDEF" {
		t.Errorf("After UndoSeek(0): got %q, want %q", content, "ABCDEF")
	}
	// At Rev 0, decorations shouldn't exist yet
	_, err = g.GetDecorationPosition("mark_X")
	if err == nil {
		t.Errorf("After UndoSeek(0): mark_X should not exist")
	}

	// Seek forward to Rev 2 - decorations should be restored in their slid positions
	err = g.UndoSeek(2)
	if err != nil {
		t.Fatalf("UndoSeek to 2 failed: %v", err)
	}
	if content := readContent(); content != "AYYBCXDEF" {
		t.Errorf("After UndoSeek(2): got %q, want %q", content, "AYYBCXDEF")
	}
	pos, err = g.GetDecorationPosition("mark_Y")
	if err != nil {
		t.Fatalf("After UndoSeek(2) GetDecorationPosition(mark_Y) failed: %v", err)
	}
	if pos.Byte != 1 {
		t.Errorf("After UndoSeek(2) mark_Y: got position %d, want 1", pos.Byte)
	}
	pos, err = g.GetDecorationPosition("mark_X")
	if err != nil {
		t.Fatalf("After UndoSeek(2) GetDecorationPosition(mark_X) failed: %v", err)
	}
	if pos.Byte != 5 {
		t.Errorf("After UndoSeek(2) mark_X: got position %d, want 5", pos.Byte)
	}
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
	_, err = cursor.InsertString("", []RelativeDecoration{{Key: "mark_2", Position: 0}}, false)
	if err != nil {
		t.Fatalf("Insert decoration failed: %v", err)
	}
	pos, err := g.GetDecorationPosition("mark_2")
	if err != nil {
		t.Fatalf("GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 2 {
		t.Errorf("Initial mark_2: got position %d, want 2", pos.Byte)
	}

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
	pos, err = g.GetDecorationPosition("mark_2")
	if err != nil {
		t.Fatalf("After insert GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 5 {
		t.Errorf("After insert mark_2: got position %d, want 5", pos.Byte)
	}

	// Rollback - decoration should return to position 2
	g.TransactionRollback()

	if content := readContent(); content != "START" {
		t.Errorf("After rollback: got %q, want %q", content, "START")
	}
	pos, err = g.GetDecorationPosition("mark_2")
	if err != nil {
		t.Fatalf("After rollback GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 2 {
		t.Errorf("After rollback mark_2: got position %d, want 2", pos.Byte)
	}
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
	_, err = cursor.InsertString("X", []RelativeDecoration{{Key: "fork0_mark", Position: 0}}, false)
	if err != nil {
		t.Fatalf("Insert X failed: %v", err)
	}
	// "BAXSE"
	pos, err := g.GetDecorationPosition("fork0_mark")
	if err != nil {
		t.Fatalf("GetDecorationPosition(fork0_mark) failed: %v", err)
	}
	if pos.Byte != 2 {
		t.Errorf("Fork 0 Rev 1 fork0_mark: got position %d, want 2", pos.Byte)
	}

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
	pos, err = g.GetDecorationPosition("fork0_mark")
	if err != nil {
		t.Fatalf("Fork 0 HEAD GetDecorationPosition(fork0_mark) failed: %v", err)
	}
	if pos.Byte != 2 {
		t.Errorf("Fork 0 HEAD fork0_mark: got position %d, want 2", pos.Byte)
	}

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
	pos, err = g.GetDecorationPosition("fork1_mark")
	if err != nil {
		t.Fatalf("Fork 1 HEAD GetDecorationPosition(fork1_mark) failed: %v", err)
	}
	if pos.Byte != 0 {
		t.Errorf("Fork 1 HEAD fork1_mark: got position %d, want 0", pos.Byte)
	}
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
	_, err = cursor.InsertString("", []RelativeDecoration{{Key: "diverge_mark", Position: 0}}, false)
	if err != nil {
		t.Fatalf("Insert decoration failed: %v", err)
	}
	// "DIVERGE" with diverge_mark@3
	pos, err := g.GetDecorationPosition("diverge_mark")
	if err != nil {
		t.Fatalf("GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 3 {
		t.Errorf("Fork 0 Rev 1 diverge_mark: got position %d, want 3", pos.Byte)
	}
	divergeRev := g.CurrentRevision()

	// Fork 0 Rev 2: More changes after divergence point
	cursor.SeekByte(0)
	_, err = cursor.InsertString("AA", nil, false)
	if err != nil {
		t.Fatalf("Insert AA failed: %v", err)
	}
	// "AADIVERGE" - diverge_mark slides to position 5
	pos, err = g.GetDecorationPosition("diverge_mark")
	if err != nil {
		t.Fatalf("GetDecorationPosition after AA failed: %v", err)
	}
	if pos.Byte != 5 {
		t.Errorf("Fork 0 Rev 2 diverge_mark: got position %d, want 5", pos.Byte)
	}

	// Go back to divergence point
	err = g.UndoSeek(divergeRev)
	if err != nil {
		t.Fatalf("UndoSeek to divergeRev failed: %v", err)
	}
	if content := readContent(); content != "DIVERGE" {
		t.Errorf("At divergence point: got %q, want %q", content, "DIVERGE")
	}
	pos, err = g.GetDecorationPosition("diverge_mark")
	if err != nil {
		t.Fatalf("GetDecorationPosition at divergence failed: %v", err)
	}
	if pos.Byte != 3 {
		t.Errorf("Back at divergence diverge_mark: got position %d, want 3", pos.Byte)
	}

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
	pos, err = g.GetDecorationPosition("diverge_mark")
	if err != nil {
		t.Fatalf("Fork 1 Rev 1 GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 3 {
		t.Errorf("Fork 1 Rev 1 diverge_mark: got position %d, want 3", pos.Byte)
	}

	// Make changes before the divergence point in fork 1
	cursor.SeekByte(0)
	_, err = cursor.InsertString("BB", nil, false)
	if err != nil {
		t.Fatalf("Insert BB failed: %v", err)
	}
	// "BBDIVERGE_FORK1" - diverge_mark slides to position 5
	pos, err = g.GetDecorationPosition("diverge_mark")
	if err != nil {
		t.Fatalf("Fork 1 Rev 2 GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 5 {
		t.Errorf("Fork 1 Rev 2 diverge_mark: got position %d, want 5", pos.Byte)
	}

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
	pos, err = g.GetDecorationPosition("diverge_mark")
	if err != nil {
		t.Fatalf("Fork 0 HEAD GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 5 {
		t.Errorf("Fork 0 HEAD diverge_mark: got position %d, want 5", pos.Byte)
	}

	// Go to divergence point in fork 0 - diverge_mark should be @3
	err = g.UndoSeek(divergeRev)
	if err != nil {
		t.Fatalf("UndoSeek to divergeRev failed: %v", err)
	}
	if content := readContent(); content != "DIVERGE" {
		t.Errorf("Fork 0 at divergence: got %q, want %q", content, "DIVERGE")
	}
	pos, err = g.GetDecorationPosition("diverge_mark")
	if err != nil {
		t.Fatalf("Fork 0 at divergence GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 3 {
		t.Errorf("Fork 0 at divergence diverge_mark: got position %d, want 3", pos.Byte)
	}
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
	// Expected with insertBefore=false:
	// mark_before: 3 (unchanged - strictly before)
	// mark_at: 4 (stays - insertBefore=false means decoration at insert point stays)
	// mark_after: 7 (was 5, slides by 2)

	if content := readContent(); content != "ABCDXXEFGH" {
		t.Errorf("After insert: got %q, want %q", content, "ABCDXXEFGH")
	}

	// Verify decoration positions
	expectedPositions := map[string]int64{
		"mark_before": 3, // unchanged - strictly before
		"mark_at":     4, // stays - insertBefore=false
		"mark_after":  7, // was 5, slides by 2
	}

	for key, expected := range expectedPositions {
		pos, err := g.GetDecorationPosition(key)
		if err != nil {
			t.Fatalf("GetDecorationPosition(%s) failed: %v", key, err)
		}
		if pos.Byte != expected {
			t.Errorf("Decoration %s: got position %d, want %d", key, pos.Byte, expected)
		}
	}
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

		// Verify decoration position
		pos, err := g.GetDecorationPosition("mark_at_D")
		if err != nil {
			t.Fatalf("GetDecorationPosition failed: %v", err)
		}
		if pos.Byte != 3 {
			t.Errorf("mark_at_D position: got %d, want 3 (should stay with insertBefore=false)", pos.Byte)
		}
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

		// Verify decoration position
		pos, err := g.GetDecorationPosition("mark_at_D")
		if err != nil {
			t.Fatalf("GetDecorationPosition failed: %v", err)
		}
		if pos.Byte != 5 {
			t.Errorf("mark_at_D position: got %d, want 5 (should slide with insertBefore=true)", pos.Byte)
		}
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

		// Verify decoration positions
		beforePos, err := g.GetDecorationPosition("before")
		if err != nil {
			t.Fatalf("GetDecorationPosition(before) failed: %v", err)
		}
		if beforePos.Byte != 3 {
			t.Errorf("Decoration 'before': got position %d, want 3 (strictly before insert)", beforePos.Byte)
		}

		atPos, err := g.GetDecorationPosition("at")
		if err != nil {
			t.Fatalf("GetDecorationPosition(at) failed: %v", err)
		}
		if atPos.Byte != 6 {
			t.Errorf("Decoration 'at': got position %d, want 6 (should slide with insertBefore=true)", atPos.Byte)
		}

		afterPos, err := g.GetDecorationPosition("after")
		if err != nil {
			t.Fatalf("GetDecorationPosition(after) failed: %v", err)
		}
		if afterPos.Byte != 7 {
			t.Errorf("Decoration 'after': got position %d, want 7 (strictly after insert)", afterPos.Byte)
		}
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

	// With insertBefore=false, decorations AT the insert point stay
	// Decorations before 20 stay, decoration at 20 stays, decorations after 20 slide by 5
	// Expected: 0, 5, 10, 15, 20 unchanged; 25->30, 30->35, 35->40

	content := readContent()
	if content != "AAAA|BBBB|CCCC|DDDD|XXXXXEEEE|FFFF|GGGG|HHHH" {
		t.Errorf("Content: got %q, want %q", content, "AAAA|BBBB|CCCC|DDDD|XXXXXEEEE|FFFF|GGGG|HHHH")
	}

	// Verify decoration positions
	expectedPositions := map[string]int64{
		"mark_0": 0,  // unchanged
		"mark_1": 5,  // unchanged
		"mark_2": 10, // unchanged
		"mark_3": 15, // unchanged
		"mark_4": 20, // stays - at insert point with insertBefore=false
		"mark_5": 30, // was 25, slides by 5
		"mark_6": 35, // was 30, slides by 5
		"mark_7": 40, // was 35, slides by 5
	}

	for key, expected := range expectedPositions {
		pos, err := g.GetDecorationPosition(key)
		if err != nil {
			t.Fatalf("GetDecorationPosition(%s) failed: %v", key, err)
		}
		if pos.Byte != expected {
			t.Errorf("Decoration %s: got position %d, want %d", key, pos.Byte, expected)
		}
	}
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
	_, err = cursor.InsertString("A", []RelativeDecoration{{Key: "tracked", Position: 0}}, false)
	if err != nil {
		t.Fatalf("Insert A failed: %v", err)
	}
	// "01234A56789" - tracked@5
	pos, err := g.GetDecorationPosition("tracked")
	if err != nil {
		t.Fatalf("Rev 1 GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 5 {
		t.Errorf("Rev 1 tracked: got position %d, want 5", pos.Byte)
	}

	// Rev 2: Insert "BB" at position 2
	cursor.SeekByte(2)
	_, err = cursor.InsertString("BB", nil, false)
	if err != nil {
		t.Fatalf("Insert BB failed: %v", err)
	}
	// "01BB234A56789" - tracked@7 (slid by 2)
	pos, err = g.GetDecorationPosition("tracked")
	if err != nil {
		t.Fatalf("Rev 2 GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 7 {
		t.Errorf("Rev 2 tracked: got position %d, want 7", pos.Byte)
	}

	// Rev 3: Insert "CCC" at position 0
	cursor.SeekByte(0)
	_, err = cursor.InsertString("CCC", nil, false)
	if err != nil {
		t.Fatalf("Insert CCC failed: %v", err)
	}
	// "CCC01BB234A56789" - tracked@10 (slid by 3)
	pos, err = g.GetDecorationPosition("tracked")
	if err != nil {
		t.Fatalf("Rev 3 GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 10 {
		t.Errorf("Rev 3 tracked: got position %d, want 10", pos.Byte)
	}

	// Rev 4: Delete 2 bytes at position 5
	cursor.SeekByte(5)
	_, _, err = cursor.DeleteBytes(2, false)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	// "CCC01234A56789" - tracked@8 (slid left by 2)
	pos, err = g.GetDecorationPosition("tracked")
	if err != nil {
		t.Fatalf("Rev 4 GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 8 {
		t.Errorf("Rev 4 tracked: got position %d, want 8", pos.Byte)
	}

	// Now undo seek through all revisions and verify
	t.Log("=== Traversing back through revisions ===")

	err = g.UndoSeek(3)
	if err != nil {
		t.Fatalf("UndoSeek(3) failed: %v", err)
	}
	pos, err = g.GetDecorationPosition("tracked")
	if err != nil {
		t.Fatalf("UndoSeek(3) GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 10 {
		t.Errorf("After UndoSeek(3) tracked: got position %d, want 10", pos.Byte)
	}

	err = g.UndoSeek(2)
	if err != nil {
		t.Fatalf("UndoSeek(2) failed: %v", err)
	}
	pos, err = g.GetDecorationPosition("tracked")
	if err != nil {
		t.Fatalf("UndoSeek(2) GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 7 {
		t.Errorf("After UndoSeek(2) tracked: got position %d, want 7", pos.Byte)
	}

	err = g.UndoSeek(1)
	if err != nil {
		t.Fatalf("UndoSeek(1) failed: %v", err)
	}
	pos, err = g.GetDecorationPosition("tracked")
	if err != nil {
		t.Fatalf("UndoSeek(1) GetDecorationPosition failed: %v", err)
	}
	if pos.Byte != 5 {
		t.Errorf("After UndoSeek(1) tracked: got position %d, want 5", pos.Byte)
	}

	err = g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek(0) failed: %v", err)
	}
	// At Rev 0, decoration shouldn't exist
	_, err = g.GetDecorationPosition("tracked")
	if err == nil {
		t.Error("After UndoSeek(0): tracked should not exist")
	}

	// Now traverse forward and verify positions
	t.Log("=== Traversing forward through revisions ===")

	expectedForward := map[RevisionID]int64{
		1: 5,
		2: 7,
		3: 10,
		4: 8,
	}

	for rev := RevisionID(1); rev <= 4; rev++ {
		err = g.UndoSeek(rev)
		if err != nil {
			t.Fatalf("UndoSeek(%d) failed: %v", rev, err)
		}
		pos, err = g.GetDecorationPosition("tracked")
		if err != nil {
			t.Fatalf("Forward UndoSeek(%d) GetDecorationPosition failed: %v", rev, err)
		}
		if pos.Byte != expectedForward[rev] {
			t.Errorf("Forward Rev %d tracked: got position %d, want %d", rev, pos.Byte, expectedForward[rev])
		}
	}
}

// TestDecorateFunction tests the Decorate(entries []DecorationEntry) API
// which applies decorations using absolute positions.
func TestDecorateFunction(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t.Run("single decoration with ByteMode", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Hello World"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		initialRev := g.CurrentRevision()

		// Add a decoration at byte position 5
		addr := ByteAddress(5)
		result, err := g.Decorate([]DecorationEntry{
			{Key: "marker", Address: &addr},
		})
		if err != nil {
			t.Fatalf("Decorate failed: %v", err)
		}

		// Should have created a new revision
		if result.Revision <= initialRev {
			t.Errorf("Expected new revision, got %d (initial was %d)", result.Revision, initialRev)
		}
		t.Logf("Added decoration, revision: %d -> %d", initialRev, result.Revision)
	})

	t.Run("multiple decorations creates single revision", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "ABCDEFGHIJ"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		initialRev := g.CurrentRevision()

		// Add multiple decorations in a single call
		addr1 := ByteAddress(2)
		addr2 := ByteAddress(5)
		addr3 := ByteAddress(8)
		result, err := g.Decorate([]DecorationEntry{
			{Key: "mark_C", Address: &addr1},
			{Key: "mark_F", Address: &addr2},
			{Key: "mark_I", Address: &addr3},
		})
		if err != nil {
			t.Fatalf("Decorate failed: %v", err)
		}

		// Should have created exactly ONE new revision
		expectedRev := initialRev + 1
		if result.Revision != expectedRev {
			t.Errorf("Expected revision %d, got %d (should be single increment)", expectedRev, result.Revision)
		}
		t.Logf("Added 3 decorations, revision: %d -> %d (single revision)", initialRev, result.Revision)
	})

	t.Run("delete decoration with nil Address", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Hello World"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// First add some decorations
		addr1 := ByteAddress(0)
		addr2 := ByteAddress(6)
		_, err = g.Decorate([]DecorationEntry{
			{Key: "start", Address: &addr1},
			{Key: "world", Address: &addr2},
		})
		if err != nil {
			t.Fatalf("Add decorations failed: %v", err)
		}
		revAfterAdd := g.CurrentRevision()
		t.Logf("Added 2 decorations at rev %d", revAfterAdd)

		// Now delete one of them by passing nil Address
		result, err := g.Decorate([]DecorationEntry{
			{Key: "start", Address: nil}, // delete "start"
		})
		if err != nil {
			t.Fatalf("Delete decoration failed: %v", err)
		}

		if result.Revision <= revAfterAdd {
			t.Errorf("Expected new revision after delete")
		}
		t.Logf("Deleted 'start' decoration, revision: %d -> %d", revAfterAdd, result.Revision)
	})

	t.Run("mixed add and delete in single call", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "ABCDEFGHIJ"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// First add a decoration to delete later
		addr := ByteAddress(0)
		_, err = g.Decorate([]DecorationEntry{
			{Key: "to_delete", Address: &addr},
		})
		if err != nil {
			t.Fatalf("Initial add failed: %v", err)
		}
		initialRev := g.CurrentRevision()

		// Now add new decorations AND delete the old one in a single call
		addr1 := ByteAddress(3)
		addr2 := ByteAddress(7)
		result, err := g.Decorate([]DecorationEntry{
			{Key: "to_delete", Address: nil},  // delete
			{Key: "new_D", Address: &addr1},   // add at position 3
			{Key: "new_H", Address: &addr2},   // add at position 7
		})
		if err != nil {
			t.Fatalf("Mixed operations failed: %v", err)
		}

		// Should be single revision increment
		expectedRev := initialRev + 1
		if result.Revision != expectedRev {
			t.Errorf("Expected revision %d, got %d", expectedRev, result.Revision)
		}
		t.Logf("Mixed add/delete, revision: %d -> %d (single revision)", initialRev, result.Revision)
	})

	t.Run("decorations on same node create single revision", func(t *testing.T) {
		// Small content that likely stays in a single node
		g, err := lib.Open(FileOptions{DataString: "ABC"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		initialRev := g.CurrentRevision()

		// Add multiple decorations to positions in the same leaf node
		addr0 := ByteAddress(0)
		addr1 := ByteAddress(1)
		addr2 := ByteAddress(2)
		result, err := g.Decorate([]DecorationEntry{
			{Key: "A", Address: &addr0},
			{Key: "B", Address: &addr1},
			{Key: "C", Address: &addr2},
		})
		if err != nil {
			t.Fatalf("Decorate failed: %v", err)
		}

		// All decorations should result in exactly ONE revision
		expectedRev := initialRev + 1
		if result.Revision != expectedRev {
			t.Errorf("Expected single revision %d, got %d", expectedRev, result.Revision)
		}
		t.Logf("3 decorations on same node: revision %d -> %d (single revision)", initialRev, result.Revision)
	})

	t.Run("RuneMode addressing", func(t *testing.T) {
		// Content with multi-byte characters: "Hello 世界"
		g, err := lib.Open(FileOptions{DataString: "Hello 世界"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// Add decoration at rune position 6 (first Chinese character 世)
		addr := RuneAddress(6)
		result, err := g.Decorate([]DecorationEntry{
			{Key: "chinese_start", Address: &addr},
		})
		if err != nil {
			t.Fatalf("Decorate with RuneMode failed: %v", err)
		}

		t.Logf("Added decoration at rune 6 (世), revision: %d", result.Revision)
	})

	t.Run("LineRuneMode addressing", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Line1\nLine2\nLine3"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// Add decoration at line 1 (0-indexed), rune 2 (the 'n' in Line2)
		addr := LineAddress(1, 2)
		result, err := g.Decorate([]DecorationEntry{
			{Key: "line2_n", Address: &addr},
		})
		if err != nil {
			t.Fatalf("Decorate with LineRuneMode failed: %v", err)
		}

		t.Logf("Added decoration at line 1, rune 2, revision: %d", result.Revision)
	})

	t.Run("update existing decoration", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "ABCDEFGHIJ"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// Add decoration at position 2
		addr1 := ByteAddress(2)
		_, err = g.Decorate([]DecorationEntry{
			{Key: "movable", Address: &addr1},
		})
		if err != nil {
			t.Fatalf("Initial add failed: %v", err)
		}
		revAfterAdd := g.CurrentRevision()

		// Update the same decoration to a new position
		addr2 := ByteAddress(7)
		result, err := g.Decorate([]DecorationEntry{
			{Key: "movable", Address: &addr2},
		})
		if err != nil {
			t.Fatalf("Update failed: %v", err)
		}

		if result.Revision <= revAfterAdd {
			t.Errorf("Expected new revision after update")
		}
		t.Logf("Updated decoration position 2 -> 7, revision: %d -> %d", revAfterAdd, result.Revision)
	})

	t.Run("empty entries is no-op", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Test"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		initialRev := g.CurrentRevision()

		result, err := g.Decorate([]DecorationEntry{})
		if err != nil {
			t.Fatalf("Empty Decorate failed: %v", err)
		}

		// Should NOT create a new revision
		if result.Revision != initialRev {
			t.Errorf("Expected no revision change for empty entries, got %d (was %d)", result.Revision, initialRev)
		}
		t.Logf("Empty entries: revision unchanged at %d", result.Revision)
	})

	t.Run("invalid position returns error", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Short"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// Try to add decoration past end of content
		addr := ByteAddress(100)
		_, err = g.Decorate([]DecorationEntry{
			{Key: "invalid", Address: &addr},
		})
		if err == nil {
			t.Error("Expected error for invalid position")
		}
		t.Logf("Invalid position correctly returned error: %v", err)
	})
}

// TestDecorationQueryFunctions tests GetDecorationPosition, GetDecorationsInByteRange, and GetDecorationsOnLine
func TestDecorationQueryFunctions(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t.Run("GetDecorationPosition - found", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Hello World"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// Add decoration at position 6
		addr := ByteAddress(6)
		_, err = g.Decorate([]DecorationEntry{
			{Key: "world_start", Address: &addr},
		})
		if err != nil {
			t.Fatalf("Decorate failed: %v", err)
		}

		// Query the decoration position
		pos, err := g.GetDecorationPosition("world_start")
		if err != nil {
			t.Fatalf("GetDecorationPosition failed: %v", err)
		}

		if pos.Mode != ByteMode || pos.Byte != 6 {
			t.Errorf("Expected ByteMode position 6, got mode=%d byte=%d", pos.Mode, pos.Byte)
		}
		t.Logf("Found decoration 'world_start' at byte position %d", pos.Byte)
	})

	t.Run("GetDecorationPosition - not found", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Hello World"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		_, err = g.GetDecorationPosition("nonexistent")
		if err == nil {
			t.Error("Expected error for nonexistent decoration")
		}
		t.Logf("Correctly returned error for nonexistent: %v", err)
	})

	t.Run("GetDecorationsInByteRange", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "ABCDEFGHIJ"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// Add decorations at various positions
		addr0 := ByteAddress(0)
		addr3 := ByteAddress(3)
		addr5 := ByteAddress(5)
		addr8 := ByteAddress(8)
		_, err = g.Decorate([]DecorationEntry{
			{Key: "A", Address: &addr0},
			{Key: "D", Address: &addr3},
			{Key: "F", Address: &addr5},
			{Key: "I", Address: &addr8},
		})
		if err != nil {
			t.Fatalf("Decorate failed: %v", err)
		}

		// Query range [2, 7) - should include D@3 and F@5
		decorations, err := g.GetDecorationsInByteRange(2, 7)
		if err != nil {
			t.Fatalf("GetDecorationsInByteRange failed: %v", err)
		}

		if len(decorations) != 2 {
			t.Errorf("Expected 2 decorations in range [2,7), got %d", len(decorations))
		}

		// Check we got the right ones
		keys := make(map[string]bool)
		for _, d := range decorations {
			keys[d.Key] = true
			t.Logf("Found decoration '%s' at %d", d.Key, d.Address.Byte)
		}

		if !keys["D"] || !keys["F"] {
			t.Errorf("Expected decorations D and F, got keys: %v", keys)
		}
	})

	t.Run("GetDecorationsInByteRange - empty", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Hello World"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// No decorations added, query should return empty
		decorations, err := g.GetDecorationsInByteRange(0, 5)
		if err != nil {
			t.Fatalf("GetDecorationsInByteRange failed: %v", err)
		}

		if len(decorations) != 0 {
			t.Errorf("Expected 0 decorations, got %d", len(decorations))
		}
		t.Log("Correctly returned empty slice for no decorations")
	})

	t.Run("GetDecorationsOnLine", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Line0\nLine1\nLine2"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// Add decorations on different lines
		addr0 := ByteAddress(2)  // On line 0
		addr1 := ByteAddress(8)  // On line 1
		addr2 := ByteAddress(14) // On line 2
		_, err = g.Decorate([]DecorationEntry{
			{Key: "line0_mark", Address: &addr0},
			{Key: "line1_mark", Address: &addr1},
			{Key: "line2_mark", Address: &addr2},
		})
		if err != nil {
			t.Fatalf("Decorate failed: %v", err)
		}

		// Query line 1
		decorations, err := g.GetDecorationsOnLine(1)
		if err != nil {
			t.Fatalf("GetDecorationsOnLine failed: %v", err)
		}

		if len(decorations) != 1 {
			t.Errorf("Expected 1 decoration on line 1, got %d", len(decorations))
		}

		if len(decorations) > 0 && decorations[0].Key != "line1_mark" {
			t.Errorf("Expected 'line1_mark', got '%s'", decorations[0].Key)
		}
		t.Logf("Found %d decoration(s) on line 1", len(decorations))
	})

	t.Run("GetDecorationsOnLine - multiple on same line", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "ABCDEF\nGHIJKL"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// Add multiple decorations on line 0
		addr0 := ByteAddress(0)
		addr2 := ByteAddress(2)
		addr5 := ByteAddress(5)
		_, err = g.Decorate([]DecorationEntry{
			{Key: "A", Address: &addr0},
			{Key: "C", Address: &addr2},
			{Key: "F", Address: &addr5},
		})
		if err != nil {
			t.Fatalf("Decorate failed: %v", err)
		}

		// Query line 0
		decorations, err := g.GetDecorationsOnLine(0)
		if err != nil {
			t.Fatalf("GetDecorationsOnLine failed: %v", err)
		}

		if len(decorations) != 3 {
			t.Errorf("Expected 3 decorations on line 0, got %d", len(decorations))
		}
		t.Logf("Found %d decorations on line 0", len(decorations))
	})
}

// TestAddressConversion tests the public address conversion functions
func TestAddressConversion(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t.Run("ByteToRune and RuneToByte", func(t *testing.T) {
		// "Hello 世界" - 6 ASCII + 2 Chinese (3 bytes each)
		g, err := lib.Open(FileOptions{DataString: "Hello 世界"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// Byte 6 is '世' (first Chinese char), rune 6
		runePos, err := g.ByteToRune(6)
		if err != nil {
			t.Fatalf("ByteToRune failed: %v", err)
		}
		if runePos != 6 {
			t.Errorf("Expected rune pos 6, got %d", runePos)
		}

		// Rune 6 is at byte 6 (start of '世')
		bytePos, err := g.RuneToByte(6)
		if err != nil {
			t.Fatalf("RuneToByte failed: %v", err)
		}
		if bytePos != 6 {
			t.Errorf("Expected byte pos 6, got %d", bytePos)
		}

		// Rune 7 (second Chinese '界') is at byte 9
		bytePos7, err := g.RuneToByte(7)
		if err != nil {
			t.Fatalf("RuneToByte(7) failed: %v", err)
		}
		if bytePos7 != 9 {
			t.Errorf("Expected byte pos 9 for rune 7, got %d", bytePos7)
		}

		t.Logf("ByteToRune(6)=%d, RuneToByte(6)=%d, RuneToByte(7)=%d", runePos, bytePos, bytePos7)
	})

	t.Run("ByteToLineRune and LineRuneToByte", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "AB\nCD\nEF"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		// Byte 4 (second 'C') should be line 1, rune 1
		line, runeInLine, err := g.ByteToLineRune(4)
		if err != nil {
			t.Fatalf("ByteToLineRune failed: %v", err)
		}
		if line != 1 || runeInLine != 1 {
			t.Errorf("Expected line=1 rune=1, got line=%d rune=%d", line, runeInLine)
		}

		// Line 2, rune 0 should be byte 6 ('E')
		bytePos, err := g.LineRuneToByte(2, 0)
		if err != nil {
			t.Fatalf("LineRuneToByte failed: %v", err)
		}
		if bytePos != 6 {
			t.Errorf("Expected byte 6, got %d", bytePos)
		}

		t.Logf("ByteToLineRune(4)=(%d,%d), LineRuneToByte(2,0)=%d", line, runeInLine, bytePos)
	})
}
