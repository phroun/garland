package garland

import (
	"testing"
)

// TestDecorationPositionAfterInsert verifies decorations move correctly after insertions.
// With insertBefore=false (default), decorations AT the insert point STAY at their position.
func TestDecorationPositionAfterInsert(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	// Set decorations at various positions
	// Position: H e l l o   W o r l  d
	//           0 1 2 3 4 5 6 7 8 9 10 (11 = EOF)
	decorations := []struct {
		key      string
		position int64
	}{
		{"start", 0},   // Before 'H'
		{"mid", 5},     // After 'o', before space
		{"space", 6},   // At 'W'
		{"end", 11},    // EOF position
	}

	for _, d := range decorations {
		addr := ByteAddress(d.position)
		_, err := g.Decorate([]DecorationEntry{
			{Key: d.key, Address: &addr},
		})
		if err != nil {
			t.Fatalf("Failed to set decoration %s at %d: %v", d.key, d.position, err)
		}
	}

	// Verify initial positions
	for _, d := range decorations {
		pos, err := g.GetDecorationPosition(d.key)
		if err != nil {
			t.Fatalf("Failed to get decoration %s: %v", d.key, err)
		}
		if pos.Byte != d.position {
			t.Errorf("Decoration %s: expected position %d, got %d", d.key, d.position, pos.Byte)
		}
	}

	// Insert "XYZ" at position 5 (between 'o' and space)
	// Before: "Hello World"
	// After:  "HelloXYZ World"
	cursor := g.NewCursor()
	cursor.SeekByte(5)
	cursor.InsertString("XYZ", nil, false)

	// Expected new positions (with insertBefore=false):
	// "start" at 0 -> stays at 0 (before insertion)
	// "mid" at 5 -> stays at 5 (at insert point, doesn't slide with insertBefore=false)
	// "space" at 6 -> moves to 9 (shifted by 3, was after insert point)
	// "end" at 11 -> moves to 14 (shifted by 3)
	expected := map[string]int64{
		"start": 0,
		"mid":   5,  // Stays at insert point with insertBefore=false
		"space": 9,  // 6 + 3
		"end":   14, // 11 + 3
	}

	cursor.SeekByte(0)
	content, _ := cursor.ReadString(100)
	t.Logf("After insert at position 5: %q", content)

	for key, expectedPos := range expected {
		pos, err := g.GetDecorationPosition(key)
		if err != nil {
			t.Errorf("Failed to get decoration %s: %v", key, err)
			continue
		}
		t.Logf("Decoration %s: position %d (expected %d)", key, pos.Byte, expectedPos)
		if pos.Byte != expectedPos {
			t.Errorf("Decoration %s: expected position %d, got %d", key, expectedPos, pos.Byte)
		}
	}
}

// TestDecorationPositionAfterDelete verifies decorations are consolidated after deletions.
// Decorations within a deleted range are NOT removed - they are consolidated to the deletion point.
func TestDecorationPositionAfterDelete(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	// Set decorations
	decorations := map[string]int64{
		"before_delete": 2,  // 'l' - before deleted range
		"in_delete":     4,  // 'o' - inside deleted range (consolidated to deletion point)
		"in_delete2":    5,  // ' ' - inside deleted range (consolidated to deletion point)
		"at_end":        7,  // 'o' in World - at boundary of delete range
		"after_delete":  9,  // 'l' in World - after deleted range
	}

	for key, position := range decorations {
		addr := ByteAddress(position)
		_, err := g.Decorate([]DecorationEntry{
			{Key: key, Address: &addr},
		})
		if err != nil {
			t.Fatalf("Failed to set decoration %s at %d: %v", key, position, err)
		}
	}

	// Delete "o W" (positions 4-7, 3 bytes)
	// Before: "Hello World"
	//          01234567890
	// After:  "Hellorld"
	cursor := g.NewCursor()
	cursor.SeekByte(4)
	cursor.DeleteBytes(3, false)

	cursor.SeekByte(0)
	content, _ := cursor.ReadString(100)
	t.Logf("After delete: %q", content)

	// Expected - decorations in deleted range are CONSOLIDATED to deletion point:
	// "before_delete" at 2 -> stays at 2
	// "in_delete" at 4 -> consolidated to 4 (deletion point)
	// "in_delete2" at 5 -> consolidated to 4 (deletion point)
	// "at_end" at 7 -> moves to 4 (shifted left by 3)
	// "after_delete" at 9 -> moves to 6 (shifted left by 3)
	expected := map[string]int64{
		"before_delete": 2,
		"in_delete":     4, // consolidated to deletion point
		"in_delete2":    4, // consolidated to deletion point
		"at_end":        4, // was at 7, shifted left by 3
		"after_delete":  6, // was at 9, shifted left by 3
	}

	for key, expectedPos := range expected {
		pos, err := g.GetDecorationPosition(key)
		if err != nil {
			t.Errorf("Decoration %s should exist but got error: %v", key, err)
			continue
		}
		t.Logf("Decoration %s: position %d (expected %d)", key, pos.Byte, expectedPos)
		if pos.Byte != expectedPos {
			t.Errorf("Decoration %s: expected position %d, got %d", key, expectedPos, pos.Byte)
		}
	}
}

// TestDecorationAtExactInsertPositionFalse tests insertBefore=false behavior.
// With insertBefore=false, decoration at insert point stays at same position.
func TestDecorationAtExactInsertPositionFalse(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "AB"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	// Set decoration at position 1 (between A and B)
	boundaryAddr := ByteAddress(1)
	_, err = g.Decorate([]DecorationEntry{
		{Key: "boundary", Address: &boundaryAddr},
	})
	if err != nil {
		t.Fatalf("Failed to set decoration: %v", err)
	}

	// Insert "X" at position 1 with insertBefore=false
	// The decoration at position 1 should STAY at position 1
	cursor := g.NewCursor()
	cursor.SeekByte(1)
	cursor.InsertString("X", nil, false)

	pos, err := g.GetDecorationPosition("boundary")
	if err != nil {
		t.Fatalf("Failed to get decoration: %v", err)
	}

	cursor.SeekByte(0)
	content, _ := cursor.ReadString(100)
	t.Logf("Content after insert: %q", content)
	t.Logf("Decoration 'boundary' at position %d", pos.Byte)

	// With insertBefore=false: decoration at insert point stays
	// Expected: AXB with decoration at position 1 (points to 'X')
	if pos.Byte != 1 {
		t.Errorf("Decoration should be at position 1, got %d", pos.Byte)
	}
}

// TestDecorationAtExactInsertPositionTrue tests insertBefore=true behavior.
// With insertBefore=true, decoration at insert point slides right.
func TestDecorationAtExactInsertPositionTrue(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "AB"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	// Set decoration at position 1 (between A and B)
	boundaryAddr := ByteAddress(1)
	_, err = g.Decorate([]DecorationEntry{
		{Key: "boundary", Address: &boundaryAddr},
	})
	if err != nil {
		t.Fatalf("Failed to set decoration: %v", err)
	}

	// Insert "X" at position 1 with insertBefore=true
	// The decoration at position 1 should SLIDE to position 2
	cursor := g.NewCursor()
	cursor.SeekByte(1)
	cursor.InsertString("X", nil, true)

	pos, err := g.GetDecorationPosition("boundary")
	if err != nil {
		t.Fatalf("Failed to get decoration: %v", err)
	}

	cursor.SeekByte(0)
	content, _ := cursor.ReadString(100)
	t.Logf("Content after insert: %q", content)
	t.Logf("Decoration 'boundary' at position %d", pos.Byte)

	// With insertBefore=true: decoration at insert point slides
	// Expected: AXB with decoration at position 2 (points to 'B')
	if pos.Byte != 2 {
		t.Errorf("Decoration should be at position 2, got %d", pos.Byte)
	}
}

// TestMultipleInsertsWithDecorations tests decorations through multiple insertions.
func TestMultipleInsertsWithDecorations(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "AC"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Decoration at position 1 (between A and C, points to 'C')
	mAddr := ByteAddress(1)
	_, err = g.Decorate([]DecorationEntry{
		{Key: "m", Address: &mAddr},
	})
	if err != nil {
		t.Fatalf("Failed to set decoration: %v", err)
	}

	// Insert "B" at position 1 with insertBefore=false
	// Decoration stays at 1 (now points to 'B')
	cursor.SeekByte(1)
	cursor.InsertString("B", nil, false)

	// Now content is "ABC", decoration should stay at 1
	pos, _ := g.GetDecorationPosition("m")
	cursor.SeekByte(0)
	content, _ := cursor.ReadString(100)
	t.Logf("After first insert: content=%q, decoration at %d", content, pos.Byte)
	if pos.Byte != 1 {
		t.Errorf("After first insert: expected decoration at 1, got %d", pos.Byte)
	}

	// Insert "X" at position 0 with insertBefore=false
	// Decoration at 1 slides to 2 (insert before it)
	cursor.SeekByte(0)
	cursor.InsertString("X", nil, false)

	// Now content is "XABC", decoration should be at 2
	pos, _ = g.GetDecorationPosition("m")
	cursor.SeekByte(0)
	content, _ = cursor.ReadString(100)
	t.Logf("After second insert: content=%q, decoration at %d", content, pos.Byte)
	if pos.Byte != 2 {
		t.Errorf("After second insert: expected decoration at 2, got %d", pos.Byte)
	}

	// Insert "Y" at position 4 (end) with insertBefore=false
	// Decoration at 2 should not move
	cursor.SeekByte(4)
	cursor.InsertString("Y", nil, false)

	// Now content is "XABCY", decoration should still be at 2
	pos, _ = g.GetDecorationPosition("m")
	cursor.SeekByte(0)
	content, _ = cursor.ReadString(100)
	t.Logf("After third insert: content=%q, decoration at %d", content, pos.Byte)
	if pos.Byte != 2 {
		t.Errorf("After third insert: expected decoration at 2, got %d", pos.Byte)
	}
}

// TestDecorationAfterMultiNodeInsert tests decoration behavior when insert causes tree splits.
func TestDecorationAfterMultiNodeInsert(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "ABCDEFGH"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()

	// Place decorations at each position
	for i := int64(0); i <= 8; i++ {
		key := string('0' + byte(i))
		addr := ByteAddress(i)
		_, err = g.Decorate([]DecorationEntry{
			{Key: key, Address: &addr},
		})
		if err != nil {
			t.Fatalf("Failed to set decoration %s at %d: %v", key, i, err)
		}
	}

	cursor := g.NewCursor()

	// Insert in the middle to cause a split (with insertBefore=false)
	cursor.SeekByte(4)
	cursor.InsertString("XYZ", nil, false)

	// Expected: "ABCDXYZEFGH"
	// With insertBefore=false:
	// Decorations 0-3 stay at 0-3 (before insert point)
	// Decoration 4 stays at 4 (at insert point with insertBefore=false)
	// Decorations 5-8 shift by 3 (after insert point)

	cursor.SeekByte(0)
	content, _ := cursor.ReadString(100)
	t.Logf("Content: %q", content)

	expectedPositions := map[string]int64{
		"0": 0, "1": 1, "2": 2, "3": 3,
		"4": 4, // stays at insert point
		"5": 8, "6": 9, "7": 10, "8": 11,
	}

	for key, expected := range expectedPositions {
		pos, err := g.GetDecorationPosition(key)
		if err != nil {
			t.Errorf("Decoration %s: error %v", key, err)
			continue
		}
		if pos.Byte != expected {
			t.Errorf("Decoration %s: expected %d, got %d", key, expected, pos.Byte)
		}
	}
}
