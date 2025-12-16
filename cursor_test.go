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

	// Insert at position 5 (before cursor)
	c.adjustForMutation(5, 3)
	if c.bytePos != 13 {
		t.Errorf("After insert before cursor: bytePos = %d, want 13", c.bytePos)
	}

	// Delete at position 5 (before cursor)
	c.adjustForMutation(5, -2)
	if c.bytePos != 11 {
		t.Errorf("After delete before cursor: bytePos = %d, want 11", c.bytePos)
	}

	// Mutation after cursor should not affect position
	c.bytePos = 10
	c.adjustForMutation(15, 5)
	if c.bytePos != 10 {
		t.Errorf("Mutation after cursor should not change position: got %d, want 10", c.bytePos)
	}

	// Mutation at cursor position should not affect position
	c.bytePos = 10
	c.adjustForMutation(10, 5)
	if c.bytePos != 10 {
		t.Errorf("Mutation at cursor position should not change position: got %d, want 10", c.bytePos)
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
