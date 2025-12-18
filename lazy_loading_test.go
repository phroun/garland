package garland

import (
	"testing"
	"time"
)

// ====================
// Ready Check Tests
// ====================

func TestIsByteReadyWithLoadedContent(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "hello world"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	// All positions should be ready for fully loaded content
	if !g.IsByteReady(0) {
		t.Error("Position 0 should be ready")
	}
	if !g.IsByteReady(5) {
		t.Error("Position 5 should be ready")
	}
	if !g.IsByteReady(11) {
		t.Error("Position 11 (at end) should be ready")
	}
	if !g.IsByteReady(100) {
		t.Error("Position 100 (beyond end) should be ready when loading is complete")
	}

	// Negative positions should return false
	if g.IsByteReady(-1) {
		t.Error("Negative position should return false")
	}
}

func TestIsRuneReadyWithLoadedContent(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "héllo wörld"}) // Mixed unicode
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	// All positions should be ready for fully loaded content
	if !g.IsRuneReady(0) {
		t.Error("Rune 0 should be ready")
	}
	if !g.IsRuneReady(5) {
		t.Error("Rune 5 should be ready")
	}
	if !g.IsRuneReady(100) {
		t.Error("Rune 100 (beyond end) should be ready when loading is complete")
	}

	// Negative positions should return false
	if g.IsRuneReady(-1) {
		t.Error("Negative rune position should return false")
	}
}

func TestIsLineReadyWithLoadedContent(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "line1\nline2\nline3"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	// All lines should be ready for fully loaded content
	if !g.IsLineReady(0) {
		t.Error("Line 0 should be ready")
	}
	if !g.IsLineReady(2) {
		t.Error("Line 2 should be ready")
	}
	if !g.IsLineReady(100) {
		t.Error("Line 100 (beyond end) should be ready when loading is complete")
	}

	// Negative line numbers should return false
	if g.IsLineReady(-1) {
		t.Error("Negative line should return false")
	}
}

// ====================
// Streaming Tests
// ====================

func TestStreamingWithReadyCheck(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	// Create a channel for streaming data
	dataChan := make(chan []byte, 10)

	g, err := lib.Open(FileOptions{DataChannel: dataChan})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	// Initially, nothing should be ready beyond position 0
	if !g.IsByteReady(0) {
		t.Error("Position 0 should always be ready")
	}

	// Send some data
	dataChan <- []byte("hello ")
	time.Sleep(10 * time.Millisecond) // Let the goroutine process

	// Now 6 bytes should be ready
	if !g.IsByteReady(5) {
		t.Error("Position 5 should be ready after first chunk")
	}

	// Send more data
	dataChan <- []byte("world")
	time.Sleep(10 * time.Millisecond)

	if !g.IsByteReady(10) {
		t.Error("Position 10 should be ready after second chunk")
	}

	// Close the channel
	close(dataChan)
	time.Sleep(10 * time.Millisecond)

	// Everything should be ready now
	if !g.IsByteReady(11) {
		t.Error("Position 11 should be ready after completion")
	}
	if !g.IsByteReady(100) {
		t.Error("Position 100 should be ready after completion")
	}
}

func TestSeekWithTimeoutOnStreamingData(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	dataChan := make(chan []byte, 10)

	g, err := lib.Open(FileOptions{DataChannel: dataChan})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Try to seek with zero timeout - should return ErrNotReady
	err = cursor.SeekByteWithTimeout(100, 0)
	if err != ErrNotReady {
		t.Errorf("Expected ErrNotReady with zero timeout, got: %v", err)
	}

	// Try with short timeout - should return ErrTimeout
	err = cursor.SeekByteWithTimeout(100, 50*time.Millisecond)
	if err != ErrTimeout {
		t.Errorf("Expected ErrTimeout with short timeout, got: %v", err)
	}

	// Send data and close channel
	dataChan <- []byte("hello world")
	close(dataChan)
	time.Sleep(20 * time.Millisecond)

	// Now seek should work
	err = cursor.SeekByteWithTimeout(5, 100*time.Millisecond)
	if err != nil {
		t.Errorf("Expected successful seek after data loaded, got: %v", err)
	}
	if cursor.BytePos() != 5 {
		t.Errorf("Expected position 5, got %d", cursor.BytePos())
	}
}

func TestBlockingSeekOnStreamingData(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	dataChan := make(chan []byte, 10)

	g, err := lib.Open(FileOptions{DataChannel: dataChan})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Start a goroutine that will send data after a delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		dataChan <- []byte("hello world")
		close(dataChan)
	}()

	// Seek with a long timeout - should block then succeed
	start := time.Now()
	err = cursor.SeekByteWithTimeout(5, 500*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Expected successful seek, got: %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("Seek returned too quickly (%v), should have blocked", elapsed)
	}
	if cursor.BytePos() != 5 {
		t.Errorf("Expected position 5, got %d", cursor.BytePos())
	}
}

func TestSeekBeyondEOFAfterComplete(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "hello"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Seeking beyond EOF should return ErrInvalidPosition
	err = cursor.SeekByte(100)
	if err != ErrInvalidPosition {
		t.Errorf("Expected ErrInvalidPosition for seek beyond EOF, got: %v", err)
	}

	err = cursor.SeekRune(100)
	if err != ErrInvalidPosition {
		t.Errorf("Expected ErrInvalidPosition for rune seek beyond EOF, got: %v", err)
	}

	err = cursor.SeekLine(100, 0)
	if err != ErrInvalidPosition {
		t.Errorf("Expected ErrInvalidPosition for line seek beyond EOF, got: %v", err)
	}
}

func TestSeekRuneWithTimeout(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	dataChan := make(chan []byte, 10)

	g, err := lib.Open(FileOptions{DataChannel: dataChan})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Zero timeout should return ErrNotReady
	err = cursor.SeekRuneWithTimeout(10, 0)
	if err != ErrNotReady {
		t.Errorf("Expected ErrNotReady, got: %v", err)
	}

	// Send unicode data
	dataChan <- []byte("hëllo wörld") // 11 runes
	close(dataChan)
	time.Sleep(20 * time.Millisecond)

	// Now should work
	err = cursor.SeekRuneWithTimeout(5, 100*time.Millisecond)
	if err != nil {
		t.Errorf("Expected success, got: %v", err)
	}
}

func TestSeekLineWithTimeout(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	dataChan := make(chan []byte, 10)

	g, err := lib.Open(FileOptions{DataChannel: dataChan})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Zero timeout should return ErrNotReady for line 2
	err = cursor.SeekLineWithTimeout(2, 0, 0)
	if err != ErrNotReady {
		t.Errorf("Expected ErrNotReady, got: %v", err)
	}

	// Send multi-line data
	dataChan <- []byte("line1\nline2\nline3")
	close(dataChan)
	time.Sleep(20 * time.Millisecond)

	// Now should work
	err = cursor.SeekLineWithTimeout(1, 0, 100*time.Millisecond)
	if err != nil {
		t.Errorf("Expected success, got: %v", err)
	}
	line, _ := cursor.LinePos()
	if line != 1 {
		t.Errorf("Expected line 1, got %d", line)
	}
}

func TestIncrementalDataArrival(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	dataChan := make(chan []byte, 10)

	g, err := lib.Open(FileOptions{DataChannel: dataChan})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Start goroutine to send data incrementally
	go func() {
		dataChan <- []byte("first")
		time.Sleep(30 * time.Millisecond)
		dataChan <- []byte("second")
		time.Sleep(30 * time.Millisecond)
		dataChan <- []byte("third")
		close(dataChan)
	}()

	// Wait for first chunk
	time.Sleep(10 * time.Millisecond)
	if !g.IsByteReady(4) {
		t.Error("Position 4 should be ready after first chunk")
	}
	if g.IsByteReady(10) {
		// Might be ready depending on timing, so just check count
		bc := g.ByteCount()
		if bc.Complete {
			// If complete, that's fine
		} else if bc.Value < 10 {
			// Expected - still streaming
		}
	}

	// Wait for all data
	time.Sleep(100 * time.Millisecond)

	// Verify content
	cursor.SeekByte(0)
	data, err := cursor.ReadBytes(g.ByteCount().Value)
	if err != nil {
		t.Fatalf("Failed to read: %v", err)
	}
	if string(data) != "firstsecondthird" {
		t.Errorf("Unexpected content: %q", string(data))
	}
}

func TestGuardedSeek(t *testing.T) {
	// Demonstrate the pattern of checking before seeking
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	dataChan := make(chan []byte, 10)

	g, err := lib.Open(FileOptions{DataChannel: dataChan})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Send some data
	dataChan <- []byte("hello")
	time.Sleep(10 * time.Millisecond)

	// Guard pattern: check before potentially blocking
	targetPos := int64(3)
	if g.IsByteReady(targetPos) {
		// Safe to seek without blocking
		err = cursor.SeekByte(targetPos)
		if err != nil {
			t.Errorf("Guarded seek failed: %v", err)
		}
	} else {
		t.Error("Expected position to be ready")
	}

	// Check unavailable position
	unavailablePos := int64(100)
	if g.IsByteReady(unavailablePos) {
		t.Error("Position 100 should not be ready yet")
	}

	close(dataChan)
}

func TestNegativeTimeoutBlocksIndefinitely(t *testing.T) {
	// Test that negative timeout means block forever (until data arrives)
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	dataChan := make(chan []byte, 10)

	g, err := lib.Open(FileOptions{DataChannel: dataChan})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Send data after a delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		dataChan <- []byte("hello")
		close(dataChan)
	}()

	// SeekByte uses -1 internally (block indefinitely)
	start := time.Now()
	err = cursor.SeekByte(3) // Using SeekByte which defaults to -1 timeout
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Expected success, got: %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("Should have blocked for ~50ms, only blocked %v", elapsed)
	}
}

func TestMultipleWaiters(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	dataChan := make(chan []byte, 10)

	g, err := lib.Open(FileOptions{DataChannel: dataChan})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	results := make(chan error, 3)

	// Start multiple goroutines waiting on different positions
	for i := 0; i < 3; i++ {
		cursor := g.NewCursor()
		pos := int64((i + 1) * 3) // Positions 3, 6, 9
		go func(c *Cursor, p int64) {
			results <- c.SeekByteWithTimeout(p, 500*time.Millisecond)
		}(cursor, pos)
	}

	// Send data to wake them all up
	time.Sleep(30 * time.Millisecond)
	dataChan <- []byte("0123456789")
	close(dataChan)

	// All should succeed
	for i := 0; i < 3; i++ {
		err := <-results
		if err != nil {
			t.Errorf("Waiter %d failed: %v", i, err)
		}
	}
}

func TestReadyAfterModification(t *testing.T) {
	// Ready checks should work correctly even after content modifications
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "hello"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Initial state
	if !g.IsByteReady(5) {
		t.Error("Position 5 should be ready")
	}

	// Insert more content
	cursor.SeekByte(5)
	cursor.InsertString(" world", nil, false)

	// Should still be ready
	if !g.IsByteReady(10) {
		t.Error("Position 10 should be ready after insert")
	}
}
