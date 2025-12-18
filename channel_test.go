package garland

import (
	"strings"
	"testing"
	"time"
)

func TestChannelSourceControlledFeeding(t *testing.T) {
	lib, _ := Init(LibraryOptions{})

	// Create a channel to control data flow
	dataChan := make(chan []byte)

	// Open with channel source
	g, err := lib.Open(FileOptions{DataChannel: dataChan})
	if err != nil {
		t.Fatalf("Failed to open with channel: %v", err)
	}
	defer g.Close()

	// Initially, counts should be zero and incomplete
	bc := g.ByteCount()
	if bc.Value != 0 {
		t.Errorf("Initial ByteCount = %d, want 0", bc.Value)
	}
	if bc.Complete {
		t.Error("ByteCount should not be complete initially")
	}

	rc := g.RuneCount()
	if rc.Complete {
		t.Error("RuneCount should not be complete initially")
	}

	lc := g.LineCount()
	if lc.Complete {
		t.Error("LineCount should not be complete initially")
	}

	// Feed first chunk (1KB of data with some lines)
	chunk1 := strings.Repeat("Hello World!\n", 80) // ~1040 bytes, 80 lines
	dataChan <- []byte(chunk1)

	// Give goroutine time to process
	time.Sleep(10 * time.Millisecond)

	// Now counts should reflect the chunk but still be incomplete
	bc = g.ByteCount()
	if bc.Value != int64(len(chunk1)) {
		t.Errorf("After chunk1: ByteCount = %d, want %d", bc.Value, len(chunk1))
	}
	if bc.Complete {
		t.Error("ByteCount should still be incomplete after partial data")
	}

	lc = g.LineCount()
	if lc.Value != 80 {
		t.Errorf("After chunk1: LineCount = %d, want 80", lc.Value)
	}
	if lc.Complete {
		t.Error("LineCount should still be incomplete after partial data")
	}

	// Test operations on the partial data
	cursor := g.NewCursor()

	// Read from beginning
	data, err := cursor.ReadBytes(13) // "Hello World!\n"
	if err != nil {
		t.Fatalf("ReadBytes failed: %v", err)
	}
	if string(data) != "Hello World!\n" {
		t.Errorf("ReadBytes = %q, want %q", string(data), "Hello World!\n")
	}

	// Seek within the loaded data
	err = cursor.SeekByte(26) // Start of third "Hello"
	if err != nil {
		t.Fatalf("SeekByte failed: %v", err)
	}

	// Read a string
	str, err := cursor.ReadString(5)
	if err != nil {
		t.Fatalf("ReadString failed: %v", err)
	}
	if str != "Hello" {
		t.Errorf("ReadString = %q, want %q", str, "Hello")
	}

	// Insert at current position
	_, err = cursor.InsertString("INSERTED", nil, true)
	if err != nil {
		t.Fatalf("InsertString failed: %v", err)
	}

	// Verify byte count increased
	bc = g.ByteCount()
	expectedBytes := int64(len(chunk1)) + 8 // "INSERTED" is 8 bytes
	if bc.Value != expectedBytes {
		t.Errorf("After insert: ByteCount = %d, want %d", bc.Value, expectedBytes)
	}

	// Delete some bytes
	cursor.SeekByte(0)
	_, _, err = cursor.DeleteBytes(5, false) // Delete "Hello"
	if err != nil {
		t.Fatalf("DeleteBytes failed: %v", err)
	}

	bc = g.ByteCount()
	expectedBytes -= 5
	if bc.Value != expectedBytes {
		t.Errorf("After delete: ByteCount = %d, want %d", bc.Value, expectedBytes)
	}

	// Feed second chunk
	chunk2 := strings.Repeat("More data!\n", 50) // ~550 bytes, 50 lines
	dataChan <- []byte(chunk2)
	time.Sleep(10 * time.Millisecond)

	// Counts should increase
	bc = g.ByteCount()
	expectedBytes += int64(len(chunk2))
	if bc.Value != expectedBytes {
		t.Errorf("After chunk2: ByteCount = %d, want %d", bc.Value, expectedBytes)
	}
	if bc.Complete {
		t.Error("ByteCount should still be incomplete")
	}

	// Close the channel to signal EOF
	close(dataChan)
	time.Sleep(10 * time.Millisecond)

	// Now counts should be complete
	bc = g.ByteCount()
	if !bc.Complete {
		t.Error("ByteCount should be complete after channel close")
	}

	lc = g.LineCount()
	if !lc.Complete {
		t.Error("LineCount should be complete after channel close")
	}

	rc = g.RuneCount()
	if !rc.Complete {
		t.Error("RuneCount should be complete after channel close")
	}

	// Verify IsComplete
	if !g.IsComplete() {
		t.Error("IsComplete should return true after channel close")
	}
}

func TestChannelSourceReadLine(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	dataChan := make(chan []byte)

	g, _ := lib.Open(FileOptions{DataChannel: dataChan})
	defer g.Close()

	// Feed data with multiple lines
	dataChan <- []byte("First line\nSecond line\nThird line\n")
	time.Sleep(10 * time.Millisecond)

	cursor := g.NewCursor()

	// Read first line
	line, err := cursor.ReadLine()
	if err != nil {
		t.Fatalf("ReadLine failed: %v", err)
	}
	if line != "First line\n" {
		t.Errorf("ReadLine = %q, want %q", line, "First line\n")
	}

	// Seek to line 1 and read
	cursor.SeekLine(1, 0)
	line, err = cursor.ReadLine()
	if err != nil {
		t.Fatalf("ReadLine failed: %v", err)
	}
	if line != "Second line\n" {
		t.Errorf("ReadLine at line 1 = %q, want %q", line, "Second line\n")
	}

	close(dataChan)
}

func TestChannelSourceAddressConversion(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	dataChan := make(chan []byte)

	g, _ := lib.Open(FileOptions{DataChannel: dataChan})
	defer g.Close()

	// Feed UTF-8 data with multi-byte characters
	dataChan <- []byte("Hello 世界!\nLine 2\n")
	time.Sleep(10 * time.Millisecond)

	// Test byte to rune conversion
	// "Hello " = 6 bytes, 6 runes
	// "世" = 3 bytes, 1 rune (at byte 6, rune 6)
	// "界" = 3 bytes, 1 rune (at byte 9, rune 7)
	runePos, err := g.ByteToRune(6)
	if err != nil {
		t.Fatalf("ByteToRune failed: %v", err)
	}
	if runePos != 6 {
		t.Errorf("ByteToRune(6) = %d, want 6", runePos)
	}

	// Test rune to byte conversion
	bytePos, err := g.RuneToByte(7) // "界" starts at rune 7
	if err != nil {
		t.Fatalf("RuneToByte failed: %v", err)
	}
	if bytePos != 9 {
		t.Errorf("RuneToByte(7) = %d, want 9", bytePos)
	}

	// Test line:rune to byte
	bytePos, err = g.LineRuneToByte(1, 0) // Start of "Line 2"
	if err != nil {
		t.Fatalf("LineRuneToByte failed: %v", err)
	}
	// "Hello 世界!\n" = 6 + 3 + 3 + 1 + 1 = 14 bytes
	if bytePos != 14 {
		t.Errorf("LineRuneToByte(1, 0) = %d, want 14", bytePos)
	}

	close(dataChan)
}

func TestChannelSourceDecorations(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	dataChan := make(chan []byte)

	g, _ := lib.Open(FileOptions{DataChannel: dataChan})
	defer g.Close()

	// Feed initial data
	dataChan <- []byte("Hello World!")
	time.Sleep(10 * time.Millisecond)

	// Add decoration within loaded range
	_, err := g.Decorate([]DecorationEntry{
		{Key: "marker", Address: &AbsoluteAddress{Mode: ByteMode, Byte: 5}},
	})
	if err != nil {
		t.Fatalf("Decorate failed: %v", err)
	}

	// Query decoration
	addr, err := g.GetDecorationPosition("marker")
	if err != nil {
		t.Fatalf("GetDecorationPosition failed: %v", err)
	}
	if addr.Byte != 5 {
		t.Errorf("Decoration position = %d, want 5", addr.Byte)
	}

	// Feed more data
	dataChan <- []byte(" More text")
	time.Sleep(10 * time.Millisecond)

	// Decoration should still be at same position
	addr, err = g.GetDecorationPosition("marker")
	if err != nil {
		t.Fatalf("GetDecorationPosition after more data: %v", err)
	}
	if addr.Byte != 5 {
		t.Errorf("Decoration position after more data = %d, want 5", addr.Byte)
	}

	close(dataChan)
}

func TestChannelSourceEditsCreateRevisions(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	dataChan := make(chan []byte)

	g, _ := lib.Open(FileOptions{DataChannel: dataChan})
	defer g.Close()

	// Feed data - this doesn't create a new revision, it's still rev 0
	dataChan <- []byte("Initial content")
	time.Sleep(10 * time.Millisecond)
	close(dataChan)
	time.Sleep(10 * time.Millisecond)

	cursor := g.NewCursor()

	// Verify we start at revision 0
	if g.CurrentRevision() != 0 {
		t.Errorf("Initial revision = %d, want 0", g.CurrentRevision())
	}

	// Make an edit - this creates revision 1
	cursor.SeekByte(7)
	result, _ := cursor.InsertString("_INSERTED_", nil, true)

	if result.Revision != 1 {
		t.Errorf("After insert, revision = %d, want 1", result.Revision)
	}

	// Verify current state
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(25)
	if string(data) != "Initial_INSERTED_ content" {
		t.Errorf("After insert: %q, want %q", string(data), "Initial_INSERTED_ content")
	}

	// Make another edit - creates revision 2
	cursor.SeekByte(0)
	result, _ = cursor.InsertString("PREFIX:", nil, true)

	if result.Revision != 2 {
		t.Errorf("After second insert, revision = %d, want 2", result.Revision)
	}

	// Undo back to revision 1
	err := g.UndoSeek(1)
	if err != nil {
		t.Fatalf("UndoSeek to 1 failed: %v", err)
	}

	// Verify state at revision 1
	cursor.SeekByte(0)
	data, _ = cursor.ReadBytes(25)
	if string(data) != "Initial_INSERTED_ content" {
		t.Errorf("After undo to rev 1: %q, want %q", string(data), "Initial_INSERTED_ content")
	}
}

func TestChannelSourceUndoToRevision0(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	dataChan := make(chan []byte)

	g, _ := lib.Open(FileOptions{DataChannel: dataChan})
	defer g.Close()

	// Feed data and close channel (completing the stream)
	dataChan <- []byte("Streamed content")
	time.Sleep(10 * time.Millisecond)
	close(dataChan)
	time.Sleep(10 * time.Millisecond)

	cursor := g.NewCursor()

	// Verify initial content
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(16)
	if string(data) != "Streamed content" {
		t.Errorf("Initial content: %q, want %q", string(data), "Streamed content")
	}

	// Make an edit - creates revision 1
	cursor.SeekByte(0)
	cursor.InsertString("PREFIX:", nil, true)

	// Verify edited state
	cursor.SeekByte(0)
	data, _ = cursor.ReadBytes(23)
	if string(data) != "PREFIX:Streamed content" {
		t.Errorf("After edit: %q", string(data))
	}

	// Undo back to revision 0 - should restore streamed content
	err := g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek to 0 failed: %v", err)
	}

	// Verify revision 0 has the complete streamed content
	cursor.SeekByte(0)
	data, _ = cursor.ReadBytes(16)
	if string(data) != "Streamed content" {
		t.Errorf("After undo to rev 0: %q, want %q", string(data), "Streamed content")
	}
}

func TestChannelSourceStreamingContinuesAfterEdits(t *testing.T) {
	// This test verifies that streaming content arriving AFTER an edit
	// is visible when you UndoSeek back to that revision.
	// The user's model: streaming content "was always there" - it should
	// be visible from ALL revisions as the "remainder" that wasn't
	// accessible yet when that revision was created.
	lib, _ := Init(LibraryOptions{})
	dataChan := make(chan []byte)

	g, _ := lib.Open(FileOptions{DataChannel: dataChan})
	defer g.Close()

	// Feed initial data
	dataChan <- []byte("Initial")
	time.Sleep(10 * time.Millisecond)

	cursor := g.NewCursor()

	// Make an edit while stream is still open - creates revision 1
	cursor.SeekByte(0)
	cursor.InsertString("PREFIX:", nil, true)

	// At revision 1, we should see "PREFIX:Initial"
	cursor.SeekByte(0)
	data, _ := cursor.ReadBytes(15)
	if string(data) != "PREFIX:Initial" {
		t.Errorf("At revision 1: %q, want %q", string(data), "PREFIX:Initial")
	}

	// Now stream more data AFTER the edit
	dataChan <- []byte(" More")
	time.Sleep(10 * time.Millisecond)

	// At current revision (1), should we see the new streaming content?
	// With the "remainder node" model, yes - the content at the end
	// "was always there" even if we couldn't see it yet.
	bc := g.ByteCount()
	// Current working tree: "PREFIX:" (7) + "Initial" (7) + " More" (5) = 19
	// But the streaming tree is separate from revision 1's tree...
	t.Logf("ByteCount after more streaming: %d, complete=%v", bc.Value, bc.Complete)

	// Close the stream
	close(dataChan)
	time.Sleep(10 * time.Millisecond)

	// UndoSeek to revision 0 - should see all streamed content
	err := g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek to 0 failed: %v", err)
	}

	cursor.SeekByte(0)
	data, _ = cursor.ReadBytes(12)
	if string(data) != "Initial More" {
		t.Errorf("At revision 0 after full stream: %q, want %q", string(data), "Initial More")
	}

	// UndoSeek to revision 1 - the question is: do we see " More"?
	// With remainder node model, revision 1 should be: "PREFIX:Initial More"
	err = g.UndoSeek(1)
	if err != nil {
		t.Fatalf("UndoSeek to 1 failed: %v", err)
	}

	cursor.SeekByte(0)
	bc = g.ByteCount()
	t.Logf("At revision 1, ByteCount = %d", bc.Value)

	// What does revision 1 show?
	data, _ = cursor.ReadBytes(bc.Value)
	t.Logf("Revision 1 content: %q (len=%d)", string(data), len(data))

	// Revision 1 should show "PREFIX:Initial More" (19 bytes)
	// because the streaming remainder is appended to all revisions
	expected := "PREFIX:Initial More"
	if string(data) != expected {
		t.Errorf("Revision 1 content = %q, want %q", string(data), expected)
	}
}
