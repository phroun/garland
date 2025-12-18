package garland

import (
	"sync"
	"testing"
	"time"
)

func TestCursorCreation(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello, World!"})
	defer g.Close()

	c := g.NewCursor()
	if c == nil {
		t.Fatal("NewCursor returned nil")
	}

	// Initial position should be 0
	if c.BytePos() != 0 {
		t.Errorf("BytePos() = %d, want 0", c.BytePos())
	}
	if c.RunePos() != 0 {
		t.Errorf("RunePos() = %d, want 0", c.RunePos())
	}

	line, runeInLine := c.LinePos()
	if line != 0 || runeInLine != 0 {
		t.Errorf("LinePos() = (%d, %d), want (0, 0)", line, runeInLine)
	}
}

func TestCursorPosition(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	c := g.NewCursor()
	pos := c.Position()

	if pos.BytePos != 0 || pos.RunePos != 0 || pos.Line != 0 || pos.LineRune != 0 {
		t.Errorf("Position() = %+v, want all zeros", pos)
	}
}

func TestMultipleCursors(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	c1 := g.NewCursor()
	c2 := g.NewCursor()
	c3 := g.NewCursor()

	if c1 == c2 || c2 == c3 || c1 == c3 {
		t.Error("Each cursor should be unique")
	}

	// All should start at position 0
	for i, c := range []*Cursor{c1, c2, c3} {
		if c.BytePos() != 0 {
			t.Errorf("Cursor %d BytePos() = %d, want 0", i, c.BytePos())
		}
	}

	// Remove one cursor
	err := g.RemoveCursor(c2)
	if err != nil {
		t.Errorf("RemoveCursor failed: %v", err)
	}

	// c1 and c3 should still work
	if c1.BytePos() != 0 || c3.BytePos() != 0 {
		t.Error("Remaining cursors should still work")
	}

	// Removing c2 again should fail
	err = g.RemoveCursor(c2)
	if err != ErrCursorNotFound {
		t.Errorf("Expected ErrCursorNotFound, got %v", err)
	}
}

func TestCursorRemoveInvalidatesOperations(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	c := g.NewCursor()
	g.RemoveCursor(c)

	// Operations on removed cursor should fail
	_, err := c.ReadBytes(5)
	if err != ErrCursorNotFound {
		t.Errorf("ReadBytes on removed cursor: expected ErrCursorNotFound, got %v", err)
	}

	_, err = c.ReadString(5)
	if err != ErrCursorNotFound {
		t.Errorf("ReadString on removed cursor: expected ErrCursorNotFound, got %v", err)
	}

	_, err = c.ReadLine()
	if err != ErrCursorNotFound {
		t.Errorf("ReadLine on removed cursor: expected ErrCursorNotFound, got %v", err)
	}

	err = c.SeekByte(0)
	if err != ErrCursorNotFound {
		t.Errorf("SeekByte on removed cursor: expected ErrCursorNotFound, got %v", err)
	}

	_, err = c.InsertBytes([]byte("test"), nil, true)
	if err != ErrCursorNotFound {
		t.Errorf("InsertBytes on removed cursor: expected ErrCursorNotFound, got %v", err)
	}
}

func TestCursorReadyState(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	c := g.NewCursor()

	// For synchronously loaded data, cursor should be ready immediately
	if !c.IsReady() {
		t.Error("Cursor should be ready for synchronously loaded data")
	}
}

func TestCursorWaitReady(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	c := g.NewCursor()

	// Should return immediately for already-ready cursor
	done := make(chan struct{})
	go func() {
		c.WaitReady()
		close(done)
	}()

	select {
	case <-done:
		// Good, returned quickly
	case <-time.After(100 * time.Millisecond):
		t.Error("WaitReady should return immediately for ready cursor")
	}
}

func TestCursorSetReady(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	c := g.NewCursor()

	// Manually set not ready
	c.setReady(false)
	if c.IsReady() {
		t.Error("Cursor should not be ready after setReady(false)")
	}

	// Set ready and verify waiting goroutines are notified
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.WaitReady()
	}()

	// Give goroutine time to start waiting
	time.Sleep(10 * time.Millisecond)

	c.setReady(true)

	// Wait for goroutine with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("WaitReady should wake up when setReady(true) is called")
	}

	if !c.IsReady() {
		t.Error("Cursor should be ready after setReady(true)")
	}
}

func TestCursorSnapshotPosition(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	c := g.NewCursor()
	c.bytePos = 10
	c.runePos = 5
	c.line = 2
	c.lineRune = 3

	snap := c.snapshotPosition()

	if snap.BytePos != 10 {
		t.Errorf("BytePos = %d, want 10", snap.BytePos)
	}
	if snap.RunePos != 5 {
		t.Errorf("RunePos = %d, want 5", snap.RunePos)
	}
	if snap.Line != 2 {
		t.Errorf("Line = %d, want 2", snap.Line)
	}
	if snap.LineRune != 3 {
		t.Errorf("LineRune = %d, want 3", snap.LineRune)
	}
}

func TestCursorRestorePosition(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	c := g.NewCursor()

	pos := &CursorPosition{
		BytePos:  100,
		RunePos:  50,
		Line:     10,
		LineRune: 5,
	}

	c.restorePosition(pos)

	if c.bytePos != 100 {
		t.Errorf("bytePos = %d, want 100", c.bytePos)
	}
	if c.runePos != 50 {
		t.Errorf("runePos = %d, want 50", c.runePos)
	}
	if c.line != 10 {
		t.Errorf("line = %d, want 10", c.line)
	}
	if c.lineRune != 5 {
		t.Errorf("lineRune = %d, want 5", c.lineRune)
	}
}

func TestCursorRestoreNilPosition(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	c := g.NewCursor()
	c.bytePos = 100

	// Restoring nil should not crash and should not change position
	c.restorePosition(nil)

	if c.bytePos != 100 {
		t.Errorf("Position should not change when restoring nil")
	}
}

func TestCursorAdjustForMutation(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World"})
	defer g.Close()

	c := g.NewCursor()
	c.bytePos = 10
	c.runePos = 10

	// Insert at position 5 (before cursor) - 3 bytes, 3 runes, 0 lines
	c.adjustForMutation(5, 3, 3, 0)
	if c.bytePos != 13 {
		t.Errorf("After insert before cursor: bytePos = %d, want 13", c.bytePos)
	}
	if c.runePos != 13 {
		t.Errorf("After insert before cursor: runePos = %d, want 13", c.runePos)
	}

	// Delete at position 5 (before cursor) - 2 bytes, 2 runes, 0 lines
	c.adjustForMutation(5, -2, -2, 0)
	if c.bytePos != 11 {
		t.Errorf("After delete before cursor: bytePos = %d, want 11", c.bytePos)
	}
	if c.runePos != 11 {
		t.Errorf("After delete before cursor: runePos = %d, want 11", c.runePos)
	}

	// Mutation after cursor should not affect position
	c.bytePos = 10
	c.runePos = 10
	c.adjustForMutation(15, 5, 5, 0)
	if c.bytePos != 10 {
		t.Errorf("Mutation after cursor should not change position: got %d, want 10", c.bytePos)
	}

	// Insert at cursor position DOES move cursor (new behavior)
	c.bytePos = 10
	c.runePos = 10
	c.adjustForMutation(10, 5, 5, 0)
	if c.bytePos != 15 {
		t.Errorf("Insert at cursor position should move cursor: got %d, want 15", c.bytePos)
	}
}

func TestCursorPositionHistory(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	c := g.NewCursor()

	// Initial position should be recorded
	if len(c.positionHistory) != 1 {
		t.Errorf("Initial position history length = %d, want 1", len(c.positionHistory))
	}

	initialPos, ok := c.positionHistory[ForkRevision{0, 0}]
	if !ok {
		t.Fatal("Initial position not recorded at fork 0, revision 0")
	}
	if initialPos.BytePos != 0 {
		t.Errorf("Initial recorded BytePos = %d, want 0", initialPos.BytePos)
	}
}

func TestCursorUpdatePositionRecordsHistory(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	c := g.NewCursor()

	// Simulate version change
	g.currentRevision = 1

	// Update position
	c.updatePosition(5, 5, 0, 5)

	// Should have recorded new position
	if len(c.positionHistory) != 2 {
		t.Errorf("Position history length = %d, want 2", len(c.positionHistory))
	}

	newPos, ok := c.positionHistory[ForkRevision{0, 1}]
	if !ok {
		t.Fatal("New position not recorded at fork 0, revision 1")
	}
	if newPos.BytePos != 5 {
		t.Errorf("Recorded BytePos = %d, want 5", newPos.BytePos)
	}
}

func TestCursorUpdatePositionUpdatesHighestSeek(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World"})
	defer g.Close()

	c := g.NewCursor()

	// Initial highest seek should be 0
	if g.highestSeekPos != 0 {
		t.Errorf("Initial highestSeekPos = %d, want 0", g.highestSeekPos)
	}

	// Update to higher position
	c.updatePosition(10, 10, 0, 10)
	if g.highestSeekPos != 10 {
		t.Errorf("After seek to 10: highestSeekPos = %d, want 10", g.highestSeekPos)
	}

	// Seeking to lower position should not decrease highestSeekPos
	c.updatePosition(5, 5, 0, 5)
	if g.highestSeekPos != 10 {
		t.Errorf("After seek to 5: highestSeekPos = %d, want 10 (should not decrease)", g.highestSeekPos)
	}
}

func TestCursorPositionTypes(t *testing.T) {
	pos := CursorPosition{
		BytePos:  100,
		RunePos:  80,
		Line:     5,
		LineRune: 10,
	}

	if pos.BytePos != 100 {
		t.Errorf("BytePos = %d, want 100", pos.BytePos)
	}
	if pos.RunePos != 80 {
		t.Errorf("RunePos = %d, want 80", pos.RunePos)
	}
	if pos.Line != 5 {
		t.Errorf("Line = %d, want 5", pos.Line)
	}
	if pos.LineRune != 10 {
		t.Errorf("LineRune = %d, want 10", pos.LineRune)
	}
}

func TestSeekByWordForward(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello world foo bar"})
	defer g.Close()

	c := g.NewCursor()

	// Move forward 1 word - goes to START of next word
	moved, err := c.SeekByWord(1)
	if err != nil {
		t.Fatalf("SeekByWord(1) error: %v", err)
	}
	if moved != 1 {
		t.Errorf("SeekByWord(1) moved = %d, want 1", moved)
	}
	// Should be at position 6 (start of "world")
	if c.BytePos() != 6 {
		t.Errorf("After SeekByWord(1): BytePos = %d, want 6", c.BytePos())
	}

	// Move forward 2 more words
	moved, err = c.SeekByWord(2)
	if err != nil {
		t.Fatalf("SeekByWord(2) error: %v", err)
	}
	if moved != 2 {
		t.Errorf("SeekByWord(2) moved = %d, want 2", moved)
	}
	// Should be at position 16 (start of "bar")
	if c.BytePos() != 16 {
		t.Errorf("After SeekByWord(2): BytePos = %d, want 16", c.BytePos())
	}
}

func TestSeekByWordBackward(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello world foo bar"})
	defer g.Close()

	c := g.NewCursor()

	// Move to end of content
	c.SeekByte(19)

	// Move backward 1 word - returns absolute count of words moved
	moved, err := c.SeekByWord(-1)
	if err != nil {
		t.Fatalf("SeekByWord(-1) error: %v", err)
	}
	if moved != 1 {
		t.Errorf("SeekByWord(-1) moved = %d, want 1", moved)
	}
	// Should be at start of "bar" (position 16)
	if c.BytePos() != 16 {
		t.Errorf("After SeekByWord(-1) from end: BytePos = %d, want 16", c.BytePos())
	}

	// Move backward 2 more words
	moved, err = c.SeekByWord(-2)
	if err != nil {
		t.Fatalf("SeekByWord(-2) error: %v", err)
	}
	if moved != 2 {
		t.Errorf("SeekByWord(-2) moved = %d, want 2", moved)
	}
	// Should be at start of "world" (position 6)
	if c.BytePos() != 6 {
		t.Errorf("After SeekByWord(-2): BytePos = %d, want 6", c.BytePos())
	}
}

func TestSeekByWordAtBoundaries(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello world"})
	defer g.Close()

	c := g.NewCursor()

	// Try to move backward from start - should return 0
	moved, err := c.SeekByWord(-1)
	if err != nil {
		t.Fatalf("SeekByWord(-1) from start error: %v", err)
	}
	if moved != 0 {
		t.Errorf("SeekByWord(-1) from start: moved = %d, want 0", moved)
	}
	if c.BytePos() != 0 {
		t.Errorf("Should still be at position 0")
	}

	// Move to end
	c.SeekByte(11)

	// Try to move forward from end - should return 0
	moved, err = c.SeekByWord(1)
	if err != nil {
		t.Fatalf("SeekByWord(1) from end error: %v", err)
	}
	if moved != 0 {
		t.Errorf("SeekByWord(1) from end: moved = %d, want 0", moved)
	}
}

func TestSeekByWordZero(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello world"})
	defer g.Close()

	c := g.NewCursor()
	c.SeekByte(5)

	// SeekByWord(0) should not move
	moved, err := c.SeekByWord(0)
	if err != nil {
		t.Fatalf("SeekByWord(0) error: %v", err)
	}
	if moved != 0 {
		t.Errorf("SeekByWord(0) moved = %d, want 0", moved)
	}
	if c.BytePos() != 5 {
		t.Errorf("Position should not change: BytePos = %d, want 5", c.BytePos())
	}
}

func TestSeekByWordMultipleSpaces(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello   world"})
	defer g.Close()

	c := g.NewCursor()

	// Move forward - goes to start of next word (skipping multiple spaces)
	moved, err := c.SeekByWord(1)
	if err != nil {
		t.Fatalf("SeekByWord(1) error: %v", err)
	}
	if moved != 1 {
		t.Errorf("SeekByWord(1) moved = %d, want 1", moved)
	}
	// Should be at position 8 (start of "world" after 3 spaces)
	if c.BytePos() != 8 {
		t.Errorf("After SeekByWord(1): BytePos = %d, want 8", c.BytePos())
	}

	// From "world", moving forward goes to end of word then finds no next word
	moved, err = c.SeekByWord(1)
	if err != nil {
		t.Fatalf("Second SeekByWord(1) error: %v", err)
	}
	// Should move to end of "world" (position 13) but count as 1 word
	t.Logf("Second move: moved=%d, pos=%d", moved, c.BytePos())
	// Verifying it ends at position 13 (end of content)
	if c.BytePos() != 13 {
		t.Errorf("After second SeekByWord(1): BytePos = %d, want 13", c.BytePos())
	}
}

func TestSeekLineStart(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello\nworld\nfoo"})
	defer g.Close()

	c := g.NewCursor()

	// Move to middle of first line
	c.SeekByte(3) // "hel|lo"

	err := c.SeekLineStart()
	if err != nil {
		t.Fatalf("SeekLineStart error: %v", err)
	}
	if c.BytePos() != 0 {
		t.Errorf("SeekLineStart on line 0: BytePos = %d, want 0", c.BytePos())
	}
	line, lineRune := c.LinePos()
	if lineRune != 0 {
		t.Errorf("SeekLineStart: lineRune = %d, want 0", lineRune)
	}
	if line != 0 {
		t.Errorf("SeekLineStart: line = %d, want 0", line)
	}

	// Move to middle of second line
	c.SeekLine(1, 3) // "wor|ld"

	err = c.SeekLineStart()
	if err != nil {
		t.Fatalf("SeekLineStart on line 1 error: %v", err)
	}
	if c.BytePos() != 6 {
		t.Errorf("SeekLineStart on line 1: BytePos = %d, want 6", c.BytePos())
	}
	line, lineRune = c.LinePos()
	if lineRune != 0 {
		t.Errorf("SeekLineStart on line 1: lineRune = %d, want 0", lineRune)
	}
	if line != 1 {
		t.Errorf("SeekLineStart on line 1: line = %d, want 1", line)
	}
}

func TestSeekLineEnd(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello\nworld\nfoo"})
	defer g.Close()

	c := g.NewCursor()

	// Start at beginning of first line
	err := c.SeekLineEnd()
	if err != nil {
		t.Fatalf("SeekLineEnd error: %v", err)
	}
	// Should be at position 5 (just before newline)
	if c.BytePos() != 5 {
		t.Errorf("SeekLineEnd on line 0: BytePos = %d, want 5", c.BytePos())
	}

	// Move to second line
	c.SeekLine(1, 0)

	err = c.SeekLineEnd()
	if err != nil {
		t.Fatalf("SeekLineEnd on line 1 error: %v", err)
	}
	// Should be at position 11 (just before second newline)
	if c.BytePos() != 11 {
		t.Errorf("SeekLineEnd on line 1: BytePos = %d, want 11", c.BytePos())
	}

	// Move to last line (no newline at end)
	c.SeekLine(2, 0)

	err = c.SeekLineEnd()
	if err != nil {
		t.Fatalf("SeekLineEnd on last line error: %v", err)
	}
	// Should be at position 15 (end of file)
	if c.BytePos() != 15 {
		t.Errorf("SeekLineEnd on last line: BytePos = %d, want 15", c.BytePos())
	}
}

func TestSeekLineStartAlreadyAtStart(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello\nworld"})
	defer g.Close()

	c := g.NewCursor()

	// Already at start of line 0
	err := c.SeekLineStart()
	if err != nil {
		t.Fatalf("SeekLineStart error: %v", err)
	}
	if c.BytePos() != 0 {
		t.Errorf("Should stay at position 0")
	}
}

func TestSeekLineEndAlreadyAtEnd(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello\nworld"})
	defer g.Close()

	c := g.NewCursor()

	// Go to end of first line
	c.SeekByte(5)

	err := c.SeekLineEnd()
	if err != nil {
		t.Fatalf("SeekLineEnd error: %v", err)
	}
	if c.BytePos() != 5 {
		t.Errorf("Should stay at position 5")
	}
}

func TestSeekByWordUTF8(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	// Chinese: "你好 世界" (hello world)
	g, _ := lib.Open(FileOptions{DataString: "你好 世界"})
	defer g.Close()

	c := g.NewCursor()

	// Move forward 1 word - goes to start of next word
	moved, err := c.SeekByWord(1)
	if err != nil {
		t.Fatalf("SeekByWord(1) UTF8 error: %v", err)
	}
	if moved != 1 {
		t.Errorf("SeekByWord(1) UTF8 moved = %d, want 1", moved)
	}
	// "你好" is 6 bytes (3 bytes per character), space is 1 byte
	// So start of "世界" is at position 7
	if c.BytePos() != 7 {
		t.Errorf("After SeekByWord(1) UTF8: BytePos = %d, want 7", c.BytePos())
	}
}

func TestSeekByWordRemovedCursor(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello world"})
	defer g.Close()

	c := g.NewCursor()
	g.RemoveCursor(c)

	_, err := c.SeekByWord(1)
	if err != ErrCursorNotFound {
		t.Errorf("SeekByWord on removed cursor: expected ErrCursorNotFound, got %v", err)
	}
}

func TestSeekLineStartRemovedCursor(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello world"})
	defer g.Close()

	c := g.NewCursor()
	g.RemoveCursor(c)

	err := c.SeekLineStart()
	if err != ErrCursorNotFound {
		t.Errorf("SeekLineStart on removed cursor: expected ErrCursorNotFound, got %v", err)
	}
}

func TestSeekLineEndRemovedCursor(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "hello world"})
	defer g.Close()

	c := g.NewCursor()
	g.RemoveCursor(c)

	err := c.SeekLineEnd()
	if err != ErrCursorNotFound {
		t.Errorf("SeekLineEnd on removed cursor: expected ErrCursorNotFound, got %v", err)
	}
}
