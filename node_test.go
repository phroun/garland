package garland

import (
	"bytes"
	"testing"
)

func TestCreateLeafSnapshot(t *testing.T) {
	tests := []struct {
		name           string
		data           []byte
		wantBytes      int64
		wantRunes      int64
		wantLines      int64
		wantLineStarts int
	}{
		{
			name:           "empty",
			data:           []byte{},
			wantBytes:      0,
			wantRunes:      0,
			wantLines:      0,
			wantLineStarts: 1, // always has at least line 0
		},
		{
			name:           "simple ascii",
			data:           []byte("Hello"),
			wantBytes:      5,
			wantRunes:      5,
			wantLines:      0,
			wantLineStarts: 1,
		},
		{
			name:           "with newline",
			data:           []byte("Hello\nWorld"),
			wantBytes:      11,
			wantRunes:      11,
			wantLines:      1,
			wantLineStarts: 2,
		},
		{
			name:           "multiple newlines",
			data:           []byte("a\nb\nc\n"),
			wantBytes:      6,
			wantRunes:      6,
			wantLines:      3,
			wantLineStarts: 3, // lines start at 0, 2, 4; no line start after final \n since it's at EOF
		},
		{
			name:           "unicode",
			data:           []byte("Hello, 世界!"),
			wantBytes:      14, // 7 ASCII + 6 bytes for 2 CJK chars + 1 for !
			wantRunes:      10,
			wantLines:      0,
			wantLineStarts: 1,
		},
		{
			name:           "unicode with newlines",
			data:           []byte("你好\n世界"),
			wantBytes:      13, // 6 + 1 + 6
			wantRunes:      5,  // 2 + 1 + 2
			wantLines:      1,
			wantLineStarts: 2,
		},
		{
			name:           "emoji",
			data:           []byte("Hello 👋"),
			wantBytes:      10, // 6 ASCII ("Hello ") + 4 bytes for emoji
			wantRunes:      7,  // 6 ASCII runes + 1 emoji rune
			wantLines:      0,
			wantLineStarts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := createLeafSnapshot(tt.data, nil, 0)

			if snap.ByteCount() != tt.wantBytes {
				t.Errorf("ByteCount() = %d, want %d", snap.ByteCount(), tt.wantBytes)
			}
			if snap.RuneCount() != tt.wantRunes {
				t.Errorf("RuneCount() = %d, want %d", snap.RuneCount(), tt.wantRunes)
			}
			if snap.LineCount() != tt.wantLines {
				t.Errorf("LineCount() = %d, want %d", snap.LineCount(), tt.wantLines)
			}
			if len(snap.lineStarts) != tt.wantLineStarts {
				t.Errorf("len(lineStarts) = %d, want %d", len(snap.lineStarts), tt.wantLineStarts)
			}
			if !snap.IsLeaf() {
				t.Error("IsLeaf() should be true")
			}
			if !bytes.Equal(snap.Data(), tt.data) {
				t.Error("Data() mismatch")
			}
		})
	}
}

func TestCreateLeafSnapshotWithDecorations(t *testing.T) {
	data := []byte("Hello")
	decorations := []Decoration{
		{Key: "a", Position: 0},
		{Key: "b", Position: 3},
	}

	snap := createLeafSnapshot(data, decorations, 100)

	if len(snap.Decorations()) != 2 {
		t.Errorf("Expected 2 decorations, got %d", len(snap.Decorations()))
	}
	if snap.originalFileOffset != 100 {
		t.Errorf("Expected originalFileOffset 100, got %d", snap.originalFileOffset)
	}
	if snap.dataHash == nil {
		t.Error("dataHash should not be nil")
	}
	if snap.decorationHash == nil {
		t.Error("decorationHash should not be nil")
	}
}

func TestCreateInternalSnapshot(t *testing.T) {
	leftSnap := createLeafSnapshot([]byte("Hello\n"), nil, 0)
	rightSnap := createLeafSnapshot([]byte("World"), nil, 6)

	snap := createInternalSnapshot(1, 2, leftSnap, rightSnap)

	if snap.IsLeaf() {
		t.Error("IsLeaf() should be false for internal node")
	}
	if snap.LeftID() != 1 {
		t.Errorf("LeftID() = %d, want 1", snap.LeftID())
	}
	if snap.RightID() != 2 {
		t.Errorf("RightID() = %d, want 2", snap.RightID())
	}
	if snap.ByteCount() != 11 {
		t.Errorf("ByteCount() = %d, want 11", snap.ByteCount())
	}
	if snap.RuneCount() != 11 {
		t.Errorf("RuneCount() = %d, want 11", snap.RuneCount())
	}
	if snap.LineCount() != 1 {
		t.Errorf("LineCount() = %d, want 1", snap.LineCount())
	}
}

func TestNodeSnapshotAt(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "test"})
	defer g.Close()

	node := newNode(1, g)

	// Set snapshot at fork 0, revision 0
	snap0 := createLeafSnapshot([]byte("v0"), nil, -1)
	node.setSnapshot(0, 0, snap0)

	// Set snapshot at fork 0, revision 2
	snap2 := createLeafSnapshot([]byte("v2"), nil, -1)
	node.setSnapshot(0, 2, snap2)

	// Set snapshot at fork 1, revision 3
	snap3 := createLeafSnapshot([]byte("v3"), nil, -1)
	node.setSnapshot(1, 3, snap3)

	tests := []struct {
		name     string
		fork     ForkID
		rev      RevisionID
		wantData string
	}{
		{"exact match r0", 0, 0, "v0"},
		{"exact match r2", 0, 2, "v2"},
		{"fallback to r0 from r1", 0, 1, "v0"},
		{"use r2 for r3 in fork 0", 0, 3, "v2"},
		{"fork 1 r3", 1, 3, "v3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := node.snapshotAt(tt.fork, tt.rev)
			if snap == nil {
				t.Fatal("snapshotAt returned nil")
			}
			if string(snap.Data()) != tt.wantData {
				t.Errorf("got data %q, want %q", string(snap.Data()), tt.wantData)
			}
		})
	}
}

func TestPartitionDecorationsEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		decorations []Decoration
		pos         int64
		wantLeft    int
		wantRight   int
	}{
		{
			name:        "empty",
			decorations: nil,
			pos:         10,
			wantLeft:    0,
			wantRight:   0,
		},
		{
			name: "all left",
			decorations: []Decoration{
				{Key: "a", Position: 0},
				{Key: "b", Position: 5},
			},
			pos:       10,
			wantLeft:  2,
			wantRight: 0,
		},
		{
			name: "all right",
			decorations: []Decoration{
				{Key: "a", Position: 10},
				{Key: "b", Position: 15},
			},
			pos:       5,
			wantLeft:  0,
			wantRight: 2,
		},
		{
			name: "at boundary goes right",
			decorations: []Decoration{
				{Key: "a", Position: 10},
			},
			pos:       10,
			wantLeft:  0,
			wantRight: 1,
		},
		{
			name: "just before boundary goes left",
			decorations: []Decoration{
				{Key: "a", Position: 9},
			},
			pos:       10,
			wantLeft:  1,
			wantRight: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test with insertBefore=true (decorations at pos go right)
			left, right := partitionDecorations(tt.decorations, tt.pos, true)
			if len(left) != tt.wantLeft {
				t.Errorf("left count = %d, want %d", len(left), tt.wantLeft)
			}
			if len(right) != tt.wantRight {
				t.Errorf("right count = %d, want %d", len(right), tt.wantRight)
			}
		})
	}
}

func TestPartitionDecorationsPositionAdjustment(t *testing.T) {
	decorations := []Decoration{
		{Key: "a", Position: 5},
		{Key: "b", Position: 15},
		{Key: "c", Position: 25},
	}

	left, right := partitionDecorations(decorations, 10, true)

	if len(left) != 1 || left[0].Position != 5 {
		t.Error("Left decoration position should remain unchanged")
	}

	if len(right) != 2 {
		t.Fatal("Expected 2 right decorations")
	}
	if right[0].Position != 5 { // 15 - 10
		t.Errorf("Right[0] position = %d, want 5", right[0].Position)
	}
	if right[1].Position != 15 { // 25 - 10
		t.Errorf("Right[1] position = %d, want 15", right[1].Position)
	}
}

func TestComputeHash(t *testing.T) {
	data1 := []byte("Hello")
	data2 := []byte("Hello")
	data3 := []byte("World")

	hash1 := computeHash(data1)
	hash2 := computeHash(data2)
	hash3 := computeHash(data3)

	if !bytes.Equal(hash1, hash2) {
		t.Error("Same data should produce same hash")
	}
	if bytes.Equal(hash1, hash3) {
		t.Error("Different data should produce different hash")
	}
	if len(hash1) != 32 { // SHA-256 produces 32 bytes
		t.Errorf("Hash length = %d, want 32", len(hash1))
	}
}

func TestComputeDecorationHash(t *testing.T) {
	dec1 := []Decoration{{Key: "a", Position: 10}}
	dec2 := []Decoration{{Key: "a", Position: 10}}
	dec3 := []Decoration{{Key: "b", Position: 10}}
	dec4 := []Decoration{{Key: "a", Position: 20}}

	hash1 := computeDecorationHash(dec1)
	hash2 := computeDecorationHash(dec2)
	hash3 := computeDecorationHash(dec3)
	hash4 := computeDecorationHash(dec4)

	if !bytes.Equal(hash1, hash2) {
		t.Error("Same decorations should produce same hash")
	}
	if bytes.Equal(hash1, hash3) {
		t.Error("Different keys should produce different hash")
	}
	if bytes.Equal(hash1, hash4) {
		t.Error("Different positions should produce different hash")
	}
}

func TestLineStartTracking(t *testing.T) {
	// Test that line starts are correctly tracked
	data := []byte("line0\nline1\nline2")
	snap := createLeafSnapshot(data, nil, 0)

	if len(snap.lineStarts) != 3 {
		t.Fatalf("Expected 3 line starts, got %d", len(snap.lineStarts))
	}

	expected := []LineStart{
		{ByteOffset: 0, RuneOffset: 0},   // line 0
		{ByteOffset: 6, RuneOffset: 6},   // line 1 (after "line0\n")
		{ByteOffset: 12, RuneOffset: 12}, // line 2 (after "line1\n")
	}

	for i, want := range expected {
		got := snap.lineStarts[i]
		if got.ByteOffset != want.ByteOffset {
			t.Errorf("lineStarts[%d].ByteOffset = %d, want %d", i, got.ByteOffset, want.ByteOffset)
		}
		if got.RuneOffset != want.RuneOffset {
			t.Errorf("lineStarts[%d].RuneOffset = %d, want %d", i, got.RuneOffset, want.RuneOffset)
		}
	}
}

func TestLineStartTrackingUnicode(t *testing.T) {
	// Unicode: each Chinese character is 3 bytes
	data := []byte("你好\n世界")
	snap := createLeafSnapshot(data, nil, 0)

	if len(snap.lineStarts) != 2 {
		t.Fatalf("Expected 2 line starts, got %d", len(snap.lineStarts))
	}

	// Line 0 starts at byte 0, rune 0
	if snap.lineStarts[0].ByteOffset != 0 || snap.lineStarts[0].RuneOffset != 0 {
		t.Errorf("Line 0 start incorrect: byte=%d, rune=%d",
			snap.lineStarts[0].ByteOffset, snap.lineStarts[0].RuneOffset)
	}

	// Line 1 starts at byte 7 (6 bytes for 你好 + 1 for \n), rune 3 (2 chars + 1 newline)
	if snap.lineStarts[1].ByteOffset != 7 || snap.lineStarts[1].RuneOffset != 3 {
		t.Errorf("Line 1 start incorrect: byte=%d (want 7), rune=%d (want 3)",
			snap.lineStarts[1].ByteOffset, snap.lineStarts[1].RuneOffset)
	}
}

func TestForkInfo(t *testing.T) {
	info := ForkInfo{
		ID:              1,
		ParentFork:      0,
		ParentRevision:  5,
		HighestRevision: 10,
	}

	if info.ID != 1 {
		t.Errorf("ID = %d, want 1", info.ID)
	}
	if info.ParentFork != 0 {
		t.Errorf("ParentFork = %d, want 0", info.ParentFork)
	}
	if info.ParentRevision != 5 {
		t.Errorf("ParentRevision = %d, want 5", info.ParentRevision)
	}
	if info.HighestRevision != 10 {
		t.Errorf("HighestRevision = %d, want 10", info.HighestRevision)
	}
}

func TestRevisionInfo(t *testing.T) {
	info := RevisionInfo{
		Revision:   5,
		Name:       "Test revision",
		HasChanges: true,
	}

	if info.Revision != 5 {
		t.Errorf("Revision = %d, want 5", info.Revision)
	}
	if info.Name != "Test revision" {
		t.Errorf("Name = %q, want %q", info.Name, "Test revision")
	}
	if !info.HasChanges {
		t.Error("HasChanges should be true")
	}
}
