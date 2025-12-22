package garland

import (
	"testing"
)

// TestMarkOnLineCrossingNodes tests marking positions on lines that span multiple nodes.
func TestMarkOnLineCrossingNodes(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}
	
	// Create content where line 1 spans multiple nodes by inserting in the middle
	g, err := lib.Open(FileOptions{DataString: "Hello\nWorldLine\n"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()
	
	c := g.NewCursor()
	
	// Force tree split by inserting in the middle of line 1
	err = c.SeekLine(1, 5) // Position at 'L' in "WorldLine"
	if err != nil {
		t.Fatalf("SeekLine(1,5) error: %v", err)
	}
	byteAtSeek := c.BytePos()
	t.Logf("Cursor at line 1, rune 5: byte=%d", byteAtSeek)
	
	_, err = c.InsertString(" spanning ", nil, false)
	if err != nil {
		t.Fatalf("Insert error: %v", err)
	}
	t.Logf("After insert, content has %d bytes, %d lines", g.ByteCount().Value, g.LineCount().Value)
	
	// Now line 1 should be "World spanning Line" across potentially multiple nodes
	// Let's set marks at various positions on line 1
	
	testCases := []struct {
		name     string
		line     int64
		rune_    int64
		wantByte int64
	}{
		{"start of line 1", 1, 0, 6},               // 'W' 
		{"middle of 'World'", 1, 2, 8},             // 'r'
		{"end of 'World'", 1, 4, 10},               // 'd'
		{"start of ' spanning '", 1, 5, 11},        // ' '
		{"middle of 'spanning'", 1, 10, 16},        // 'n' in spanning
		{"start of 'Line'", 1, 15, 21},             // 'L'
		{"end of line 1", 1, 19, 25},               // 'e' at end
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set a mark using line:rune address
			markKey := "test_" + tc.name
			_, err := g.Decorate([]DecorationEntry{
				{Key: markKey, Address: &AbsoluteAddress{Mode: LineRuneMode, Line: tc.line, LineRune: tc.rune_}},
			})
			if err != nil {
				t.Fatalf("Decorate error: %v", err)
			}
			
			// Retrieve the mark
			pos, err := g.GetDecorationPosition(markKey)
			if err != nil {
				t.Fatalf("GetDecorationPosition error: %v", err)
			}
			
			t.Logf("Mark at (%d, %d): byte=%d (expected %d)", tc.line, tc.rune_, pos.Byte, tc.wantByte)
			
			if pos.Byte != tc.wantByte {
				t.Errorf("Mark at (%d, %d): got byte %d, want %d", tc.line, tc.rune_, pos.Byte, tc.wantByte)
			}
			
			// Verify the mark is in the correct node by checking consistency
			// Re-lookup using byte position should give same result
			c.SeekByte(pos.Byte)
			line, lineRune := c.LinePos()
			if line != tc.line || lineRune != tc.rune_ {
				t.Errorf("After SeekByte(%d), LinePos()=(%d,%d), want (%d,%d)", 
					pos.Byte, line, lineRune, tc.line, tc.rune_)
			}
		})
	}
}

// TestMarkAtNodeBoundaryOnLine tests marks placed exactly at node boundaries on spanning lines.
func TestMarkAtNodeBoundaryOnLine(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}
	
	// Create content then split it to create a known boundary
	g, err := lib.Open(FileOptions{DataString: "Line0\nABCDEFGH\nLine2"})
	if err != nil {
		t.Fatalf("Failed to create garland: %v", err)
	}
	defer g.Close()
	
	c := g.NewCursor()
	
	// Insert at position to split the ABCDEFGH into two nodes
	err = c.SeekLine(1, 4) // Position at 'E'
	if err != nil {
		t.Fatalf("SeekLine(1,4) error: %v", err)
	}
	beforeInsert := c.BytePos()
	t.Logf("Before insert, cursor at byte %d", beforeInsert)
	
	_, err = c.InsertString("xyz", nil, false)
	if err != nil {
		t.Fatalf("Insert error: %v", err)
	}
	
	// Now line 1 is "ABCDxyzEFGH" - the boundary between original nodes is at position 4
	t.Logf("After insert, line 1 should be ABCDxyzEFGH")
	
	// Set marks at and around the boundary
	boundaryTests := []struct {
		name       string
		runeInLine int64
		wantByte   int64
	}{
		{"before boundary", 3, 9},     // 'D' at byte 9
		{"at boundary start", 4, 10},  // 'x' at byte 10 (inserted content)
		{"at boundary end", 7, 13},    // 'E' at byte 13 (original content after insert)
		{"after boundary", 8, 14},     // 'F' at byte 14
	}
	
	for _, tc := range boundaryTests {
		t.Run(tc.name, func(t *testing.T) {
			markKey := "boundary_" + tc.name
			_, err := g.Decorate([]DecorationEntry{
				{Key: markKey, Address: &AbsoluteAddress{Mode: LineRuneMode, Line: 1, LineRune: tc.runeInLine}},
			})
			if err != nil {
				t.Fatalf("Decorate error: %v", err)
			}
			
			pos, err := g.GetDecorationPosition(markKey)
			if err != nil {
				t.Fatalf("GetDecorationPosition error: %v", err)
			}
			
			t.Logf("Boundary mark at rune %d: byte=%d (expected %d)", tc.runeInLine, pos.Byte, tc.wantByte)
			
			if pos.Byte != tc.wantByte {
				t.Errorf("Mark at rune %d: got byte %d, want %d", tc.runeInLine, pos.Byte, tc.wantByte)
			}
			
			// Verify round-trip consistency
			c.SeekByte(pos.Byte)
			line, lineRune := c.LinePos()
			if line != 1 || lineRune != tc.runeInLine {
				t.Errorf("Round-trip: SeekByte(%d) gives LinePos()=(%d,%d), want (1,%d)",
					pos.Byte, line, lineRune, tc.runeInLine)
			}
		})
	}
}
