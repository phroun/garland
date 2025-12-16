package garland

import (
	"bytes"
	"testing"
)

func TestFindLeafByByte(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello, World!"})
	defer g.Close()

	tests := []struct {
		name       string
		pos        int64
		wantErr    bool
		wantOffset int64
	}{
		{"start", 0, false, 0},
		{"middle", 7, false, 7},
		{"end", 13, false, 13},
		{"negative", -1, true, 0},
		{"past_end", 20, true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := g.findLeafByByte(tt.pos)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result.ByteOffset != tt.wantOffset {
				t.Errorf("ByteOffset = %d, want %d", result.ByteOffset, tt.wantOffset)
			}
		})
	}
}

func TestFindLeafByByteUnicode(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	// "Hello, 世界!" - 7 ASCII + 6 bytes for 2 Chinese chars + 1 for ! = 14 bytes
	g, _ := lib.Open(FileOptions{DataString: "Hello, 世界!"})
	defer g.Close()

	tests := []struct {
		name        string
		bytePos     int64
		wantRuneOff int64
	}{
		{"before_unicode", 5, 5},
		{"at_first_chinese", 7, 7},
		{"at_second_chinese", 10, 8},
		{"at_exclamation", 13, 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := g.findLeafByByte(tt.bytePos)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result.RuneOffset != tt.wantRuneOff {
				t.Errorf("RuneOffset = %d, want %d", result.RuneOffset, tt.wantRuneOff)
			}
		})
	}
}

func TestFindLeafByRune(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello, World!"})
	defer g.Close()

	tests := []struct {
		name       string
		pos        int64
		wantErr    bool
		wantOffset int64
	}{
		{"start", 0, false, 0},
		{"middle", 7, false, 7},
		{"end", 13, false, 13},
		{"negative", -1, true, 0},
		{"past_end", 20, true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := g.findLeafByRune(tt.pos)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result.RuneOffset != tt.wantOffset {
				t.Errorf("RuneOffset = %d, want %d", result.RuneOffset, tt.wantOffset)
			}
		})
	}
}

func TestFindLeafByRuneUnicode(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	// "Hello, 世界!" - 10 runes total
	g, _ := lib.Open(FileOptions{DataString: "Hello, 世界!"})
	defer g.Close()

	tests := []struct {
		name        string
		runePos     int64
		wantByteOff int64
	}{
		{"before_unicode", 5, 5},
		{"at_first_chinese", 7, 7},
		{"at_second_chinese", 8, 10},
		{"at_exclamation", 9, 13},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := g.findLeafByRune(tt.runePos)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result.ByteOffset != tt.wantByteOff {
				t.Errorf("ByteOffset = %d, want %d", result.ByteOffset, tt.wantByteOff)
			}
		})
	}
}

func TestFindLeafByLine(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "line0\nline1\nline2"})
	defer g.Close()

	tests := []struct {
		name          string
		line          int64
		runeInLine    int64
		wantErr       bool
		wantLineStart int64
	}{
		{"line0_start", 0, 0, false, 0},
		{"line0_middle", 0, 3, false, 0},
		{"line1_start", 1, 0, false, 6},
		{"line1_middle", 1, 2, false, 6},
		{"line2_start", 2, 0, false, 12},
		{"invalid_line", 10, 0, true, 0},
		{"negative_line", -1, 0, true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := g.findLeafByLine(tt.line, tt.runeInLine)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result.LineByteStart != tt.wantLineStart {
				t.Errorf("LineByteStart = %d, want %d", result.LineByteStart, tt.wantLineStart)
			}
		})
	}
}

func TestFindLeafByLineUnicode(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	// "你好\n世界" - line 0 has 2 Chinese chars + newline, line 1 has 2 Chinese chars
	g, _ := lib.Open(FileOptions{DataString: "你好\n世界"})
	defer g.Close()

	result, err := g.findLeafByLine(1, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Line 1 starts at byte 7 (6 bytes for 你好 + 1 for \n)
	if result.LineByteStart != 7 {
		t.Errorf("LineByteStart = %d, want 7", result.LineByteStart)
	}

	// Line 1 starts at rune 3 (2 chars + 1 newline)
	if result.LineRuneStart != 3 {
		t.Errorf("LineRuneStart = %d, want 3", result.LineRuneStart)
	}
}

func TestByteToRuneInternal(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello, 世界!"})
	defer g.Close()

	tests := []struct {
		bytePos  int64
		wantRune int64
	}{
		{0, 0},
		{5, 5},
		{7, 7},   // first Chinese char
		{10, 8},  // second Chinese char
		{13, 9},  // exclamation
		{14, 10}, // end
	}

	for _, tt := range tests {
		result, err := g.byteToRuneInternal(tt.bytePos)
		if err != nil {
			t.Errorf("byteToRuneInternal(%d): unexpected error: %v", tt.bytePos, err)
			continue
		}
		if result != tt.wantRune {
			t.Errorf("byteToRuneInternal(%d) = %d, want %d", tt.bytePos, result, tt.wantRune)
		}
	}
}

func TestRuneToByteInternal(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello, 世界!"})
	defer g.Close()

	tests := []struct {
		runePos  int64
		wantByte int64
	}{
		{0, 0},
		{5, 5},
		{7, 7},   // first Chinese char
		{8, 10},  // second Chinese char
		{9, 13},  // exclamation
		{10, 14}, // end
	}

	for _, tt := range tests {
		result, err := g.runeToByteInternal(tt.runePos)
		if err != nil {
			t.Errorf("runeToByteInternal(%d): unexpected error: %v", tt.runePos, err)
			continue
		}
		if result != tt.wantByte {
			t.Errorf("runeToByteInternal(%d) = %d, want %d", tt.runePos, result, tt.wantByte)
		}
	}
}

func TestByteToLineRuneInternal(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "line0\nline1\nline2"})
	defer g.Close()

	tests := []struct {
		bytePos      int64
		wantLine     int64
		wantLineRune int64
	}{
		{0, 0, 0},
		{3, 0, 3},
		{5, 0, 5},  // newline char
		{6, 1, 0},  // start of line1
		{8, 1, 2},  // middle of line1
		{12, 2, 0}, // start of line2
		{15, 2, 3}, // middle of line2
	}

	for _, tt := range tests {
		line, runeInLine, err := g.byteToLineRuneInternal(tt.bytePos)
		if err != nil {
			t.Errorf("byteToLineRuneInternal(%d): unexpected error: %v", tt.bytePos, err)
			continue
		}
		if line != tt.wantLine || runeInLine != tt.wantLineRune {
			t.Errorf("byteToLineRuneInternal(%d) = (%d, %d), want (%d, %d)",
				tt.bytePos, line, runeInLine, tt.wantLine, tt.wantLineRune)
		}
	}
}

func TestReadBytesAt(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello, World!"})
	defer g.Close()

	tests := []struct {
		name        string
		pos         int64
		length      int64
		want        string
		wantSeekErr bool
		wantReadErr bool
	}{
		{"start", 0, 5, "Hello", false, false},
		{"middle", 7, 5, "World", false, false},
		{"full", 0, 13, "Hello, World!", false, false},
		{"past_end_clamped", 10, 10, "ld!", false, false},
		{"zero_length", 5, 0, "", false, false},
		{"negative_pos", -1, 5, "", true, false},
	}

	cursor := g.NewCursor()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cursor.SeekByte(tt.pos)
			if tt.wantSeekErr {
				if err == nil {
					t.Error("expected seek error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected seek error: %v", err)
				return
			}

			data, err := cursor.ReadBytes(tt.length)
			if tt.wantReadErr {
				if err == nil {
					t.Error("expected read error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected read error: %v", err)
				return
			}
			if string(data) != tt.want {
				t.Errorf("ReadBytes() = %q, want %q", string(data), tt.want)
			}
		})
	}
}

func TestReadStringAt(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello, 世界!"})
	defer g.Close()

	tests := []struct {
		name    string
		pos     int64
		length  int64
		want    string
		wantErr bool
	}{
		{"ascii", 0, 5, "Hello", false},
		{"unicode", 7, 2, "世界", false},
		{"mixed", 5, 5, ", 世界!", false},
	}

	cursor := g.NewCursor()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cursor.SeekRune(tt.pos)
			data, err := cursor.ReadString(tt.length)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if data != tt.want {
				t.Errorf("ReadString() = %q, want %q", data, tt.want)
			}
		})
	}
}

func TestReadLineAt(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "line0\nline1\nline2"})
	defer g.Close()

	tests := []struct {
		name        string
		line        int64
		want        string
		wantSeekErr bool
		wantReadErr bool
	}{
		{"line0", 0, "line0\n", false, false},
		{"line1", 1, "line1\n", false, false},
		{"line2_no_newline", 2, "line2", false, false},
		{"invalid_line", 10, "", true, false},
	}

	cursor := g.NewCursor()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cursor.SeekLine(tt.line, 0)
			if tt.wantSeekErr {
				if err == nil {
					t.Error("expected seek error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected seek error: %v", err)
				return
			}

			data, err := cursor.ReadLine()
			if tt.wantReadErr {
				if err == nil {
					t.Error("expected read error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected read error: %v", err)
				return
			}
			if data != tt.want {
				t.Errorf("ReadLine() = %q, want %q", data, tt.want)
			}
		})
	}
}

func TestSplitLeaf(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "HelloWorld"})
	defer g.Close()

	// Get the content node (left child of root)
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	contentNode := g.nodeRegistry[rootSnap.leftID]
	contentSnap := contentNode.snapshotAt(g.currentFork, g.currentRevision)

	// Split at position 5
	leftID, rightID, err := g.splitLeaf(contentNode, contentSnap, 5)
	if err != nil {
		t.Fatalf("splitLeaf failed: %v", err)
	}

	leftNode := g.nodeRegistry[leftID]
	rightNode := g.nodeRegistry[rightID]

	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)

	if string(leftSnap.data) != "Hello" {
		t.Errorf("left data = %q, want %q", string(leftSnap.data), "Hello")
	}
	if string(rightSnap.data) != "World" {
		t.Errorf("right data = %q, want %q", string(rightSnap.data), "World")
	}
}

func TestSplitLeafUnicode(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "你好世界"})
	defer g.Close()

	// Get the content node
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	contentNode := g.nodeRegistry[rootSnap.leftID]
	contentSnap := contentNode.snapshotAt(g.currentFork, g.currentRevision)

	// Try to split at byte 4 (middle of second Chinese char)
	// Should align to byte 3 (start of second char) or byte 6 (start of third char)
	leftID, rightID, err := g.splitLeaf(contentNode, contentSnap, 4)
	if err != nil {
		t.Fatalf("splitLeaf failed: %v", err)
	}

	leftNode := g.nodeRegistry[leftID]
	rightNode := g.nodeRegistry[rightID]

	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)

	// Should have aligned to byte 3 (after 你)
	if string(leftSnap.data) != "你" {
		t.Errorf("left data = %q, want %q", string(leftSnap.data), "你")
	}
	if string(rightSnap.data) != "好世界" {
		t.Errorf("right data = %q, want %q", string(rightSnap.data), "好世界")
	}
}

func TestConcatenate(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "HelloWorld"})
	defer g.Close()

	// Get the content node and split it
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	contentNode := g.nodeRegistry[rootSnap.leftID]
	contentSnap := contentNode.snapshotAt(g.currentFork, g.currentRevision)

	leftID, rightID, _ := g.splitLeaf(contentNode, contentSnap, 5)

	// Concatenate them back
	newRootID, err := g.concatenate(leftID, rightID)
	if err != nil {
		t.Fatalf("concatenate failed: %v", err)
	}

	newRoot := g.nodeRegistry[newRootID]
	newSnap := newRoot.snapshotAt(g.currentFork, g.currentRevision)

	// New root should be internal
	if newSnap.isLeaf {
		t.Error("concatenate should produce internal node")
	}

	// Total bytes should be preserved
	if newSnap.byteCount != 10 {
		t.Errorf("byteCount = %d, want 10", newSnap.byteCount)
	}

	// Collect leaves should return original data
	data := g.collectLeaves(newRootID)
	if string(data) != "HelloWorld" {
		t.Errorf("collectLeaves = %q, want %q", string(data), "HelloWorld")
	}
}

func TestCollectLeaves(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello, World!"})
	defer g.Close()

	// Collect all data
	data := g.collectLeaves(g.root.id)

	if string(data) != "Hello, World!" {
		t.Errorf("collectLeaves = %q, want %q", string(data), "Hello, World!")
	}
}

func TestByteToRuneOffset(t *testing.T) {
	data := []byte("Hello, 世界!")

	tests := []struct {
		byteOff int64
		wantRune int64
	}{
		{0, 0},
		{5, 5},
		{7, 7},
		{10, 8},
		{13, 9},
		{14, 10},
	}

	for _, tt := range tests {
		result := byteToRuneOffset(data, tt.byteOff)
		if result != tt.wantRune {
			t.Errorf("byteToRuneOffset(%d) = %d, want %d", tt.byteOff, result, tt.wantRune)
		}
	}
}

func TestRuneToByteOffset(t *testing.T) {
	data := []byte("Hello, 世界!")

	tests := []struct {
		runeOff  int64
		wantByte int64
	}{
		{0, 0},
		{5, 5},
		{7, 7},
		{8, 10},
		{9, 13},
		{10, 14},
	}

	for _, tt := range tests {
		result := runeToByteOffset(data, tt.runeOff)
		if result != tt.wantByte {
			t.Errorf("runeToByteOffset(%d) = %d, want %d", tt.runeOff, result, tt.wantByte)
		}
	}
}

func TestAlignToRuneBoundary(t *testing.T) {
	data := []byte("A世B") // A=1, 世=3, B=1 total 5 bytes

	tests := []struct {
		pos  int64
		want int64
	}{
		{0, 0}, // start of A
		{1, 1}, // start of 世
		{2, 1}, // middle of 世 -> align to 1
		{3, 1}, // middle of 世 -> align to 1
		{4, 4}, // start of B
		{5, 5}, // end
	}

	for _, tt := range tests {
		result := alignToRuneBoundary(data, tt.pos)
		if result != tt.want {
			t.Errorf("alignToRuneBoundary(%d) = %d, want %d", tt.pos, result, tt.want)
		}
	}
}

func TestEmptyFileOperations(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	// Use DataBytes with empty slice for truly empty content
	// (DataString: "" is treated as "no data source")
	g, err := lib.Open(FileOptions{DataBytes: []byte{}})
	if err != nil {
		t.Fatalf("Failed to open empty file: %v", err)
	}
	defer g.Close()

	// Finding position 0 should work
	result, err := g.findLeafByByte(0)
	if err != nil {
		t.Errorf("findLeafByByte(0) on empty file: unexpected error: %v", err)
	}
	if result.ByteOffset != 0 {
		t.Errorf("ByteOffset = %d, want 0", result.ByteOffset)
	}

	// Finding line 0 should work
	lineResult, err := g.findLeafByLine(0, 0)
	if err != nil {
		t.Errorf("findLeafByLine(0, 0) on empty file: unexpected error: %v", err)
	}
	if lineResult.LineByteStart != 0 {
		t.Errorf("LineByteStart = %d, want 0", lineResult.LineByteStart)
	}
}

func TestPartitionDecorationsInTree(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{
		DataString: "HelloWorld",
	})
	defer g.Close()

	// Add decorations to the content node
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	contentNode := g.nodeRegistry[rootSnap.leftID]

	decorations := []Decoration{
		{Key: "dec1", Position: 2},
		{Key: "dec2", Position: 5},
		{Key: "dec3", Position: 7},
	}

	contentSnap := createLeafSnapshot([]byte("HelloWorld"), decorations, 0)
	contentNode.setSnapshot(g.currentFork, g.currentRevision, contentSnap)

	// Split at position 5
	leftID, rightID, err := g.splitLeaf(contentNode, contentSnap, 5)
	if err != nil {
		t.Fatalf("splitLeaf failed: %v", err)
	}

	leftNode := g.nodeRegistry[leftID]
	rightNode := g.nodeRegistry[rightID]

	leftSnap := leftNode.snapshotAt(g.currentFork, g.currentRevision)
	rightSnap := rightNode.snapshotAt(g.currentFork, g.currentRevision)

	// Left should have dec1 (position 2)
	if len(leftSnap.decorations) != 1 {
		t.Errorf("left decorations count = %d, want 1", len(leftSnap.decorations))
	}
	if leftSnap.decorations[0].Key != "dec1" || leftSnap.decorations[0].Position != 2 {
		t.Errorf("left decoration wrong: %+v", leftSnap.decorations[0])
	}

	// Right should have dec2 (position 0, adjusted from 5) and dec3 (position 2, adjusted from 7)
	if len(rightSnap.decorations) != 2 {
		t.Errorf("right decorations count = %d, want 2", len(rightSnap.decorations))
	}
	if rightSnap.decorations[0].Key != "dec2" || rightSnap.decorations[0].Position != 0 {
		t.Errorf("right decoration 0 wrong: %+v", rightSnap.decorations[0])
	}
	if rightSnap.decorations[1].Key != "dec3" || rightSnap.decorations[1].Position != 2 {
		t.Errorf("right decoration 1 wrong: %+v", rightSnap.decorations[1])
	}
}

func TestTreeHeight(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	height := g.getHeight(g.root.id)
	if height < 1 {
		t.Errorf("height = %d, want >= 1", height)
	}
}

func TestFindLeafByByteAtEOF(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello"})
	defer g.Close()

	// Position at EOF (byte 5) should be valid
	result, err := g.findLeafByByte(5)
	if err != nil {
		t.Fatalf("findLeafByByte at EOF: unexpected error: %v", err)
	}

	if result.ByteOffset != 5 {
		t.Errorf("ByteOffset = %d, want 5", result.ByteOffset)
	}
}

func TestInsertAtLeaf(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "HelloWorld"})
	defer g.Close()

	// Find position to insert
	result, err := g.findLeafByByte(5)
	if err != nil {
		t.Fatalf("findLeafByByte failed: %v", err)
	}

	// Insert ", " at position 5
	newRootID, err := g.insertAtLeaf(result, []byte(", "), nil, false)
	if err != nil {
		t.Fatalf("insertAtLeaf failed: %v", err)
	}

	// Verify the result
	data := g.collectLeaves(newRootID)
	if string(data) != "Hello, World" {
		t.Errorf("after insert: %q, want %q", string(data), "Hello, World")
	}
}

func TestDeleteRange(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello, World!"})
	defer g.Close()

	// Delete ", " (bytes 5-7)
	deletedDecs, newRootID, err := g.deleteRange(5, 2)
	if err != nil {
		t.Fatalf("deleteRange failed: %v", err)
	}

	// No decorations should be deleted (none were set)
	if len(deletedDecs) != 0 {
		t.Errorf("deletedDecs count = %d, want 0", len(deletedDecs))
	}

	// Verify the result
	data := g.collectLeaves(newRootID)
	if string(data) != "HelloWorld!" {
		t.Errorf("after delete: %q, want %q", string(data), "HelloWorld!")
	}
}

func TestDeleteRangeWithDecorations(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello, World!"})
	defer g.Close()

	// Add decorations
	rootSnap := g.root.snapshotAt(g.currentFork, g.currentRevision)
	contentNode := g.nodeRegistry[rootSnap.leftID]

	decorations := []Decoration{
		{Key: "before", Position: 3},
		{Key: "in_range", Position: 6},
		{Key: "after", Position: 10},
	}

	contentSnap := createLeafSnapshot([]byte("Hello, World!"), decorations, 0)
	contentNode.setSnapshot(g.currentFork, g.currentRevision, contentSnap)

	// Delete ", " (bytes 5-7)
	deletedDecs, newRootID, err := g.deleteRange(5, 2)
	if err != nil {
		t.Fatalf("deleteRange failed: %v", err)
	}

	// "in_range" decoration should be deleted
	if len(deletedDecs) != 1 {
		t.Errorf("deletedDecs count = %d, want 1", len(deletedDecs))
	}
	if len(deletedDecs) > 0 && deletedDecs[0].Key != "in_range" {
		t.Errorf("deleted decoration key = %q, want %q", deletedDecs[0].Key, "in_range")
	}

	// Verify data
	data := g.collectLeaves(newRootID)
	if !bytes.Equal(data, []byte("HelloWorld!")) {
		t.Errorf("after delete: %q, want %q", string(data), "HelloWorld!")
	}
}
