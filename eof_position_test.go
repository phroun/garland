package garland

import (
	"testing"
)

// TestEOFLinePosition tests that LinePos is correct at EOF.
func TestEOFLinePosition(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	tests := []struct {
		name         string
		content      string
		expectedLine int64
		expectedRune int64
	}{
		// Skip empty file - needs special handling
		{"single line no newline", "Hello", 0, 5},
		{"single line with newline", "Hello\n", 1, 0},
		{"two lines no trailing newline", "Hello\nWorld", 1, 5},
		{"two lines with trailing newline", "Hello\nWorld\n", 2, 0},
		{"three lines", "A\nB\nC", 2, 1},
		{"three lines with trailing newline", "A\nB\nC\n", 3, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, err := lib.Open(FileOptions{DataString: tc.content})
			if err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			defer g.Close()

			cursor := g.NewCursor()

			// Seek to EOF
			eofByte := g.ByteCount().Value
			cursor.SeekByte(eofByte)

			line, rune_ := cursor.LinePos()

			t.Logf("Content: %q (len=%d), EOF byte=%d, LinePos=(%d, %d)",
				tc.content, len(tc.content), eofByte, line, rune_)

			if line != tc.expectedLine || rune_ != tc.expectedRune {
				t.Errorf("At EOF: got LinePos(%d, %d), want (%d, %d)",
					line, rune_, tc.expectedLine, tc.expectedRune)
			}
		})
	}
}

// TestDecorationAtEOF tests decoration placement at EOF position.
func TestDecorationAtEOF(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "Hello\nWorld"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	// Place decoration at EOF (byte 11)
	eofAddr := ByteAddress(11)
	_, err = g.Decorate([]DecorationEntry{
		{Key: "eof_mark", Address: &eofAddr},
	})
	if err != nil {
		t.Fatalf("Failed to set decoration at EOF: %v", err)
	}

	// Verify position
	pos, err := g.GetDecorationPosition("eof_mark")
	if err != nil {
		t.Fatalf("Failed to get decoration: %v", err)
	}

	t.Logf("EOF decoration at byte %d", pos.Byte)

	if pos.Byte != 11 {
		t.Errorf("Expected decoration at byte 11, got %d", pos.Byte)
	}

	// Verify line position using cursor
	cursor := g.NewCursor()
	cursor.SeekByte(pos.Byte)
	line, rune_ := cursor.LinePos()
	t.Logf("EOF decoration at line %d rune %d", line, rune_)
	if line != 1 || rune_ != 5 {
		t.Errorf("Expected EOF at line 1 rune 5, got line %d rune %d", line, rune_)
	}
}

// TestSeekToEOFLine tests seeking to the EOF line.
func TestSeekToEOFLine(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	tests := []struct {
		name        string
		content     string
		line        int64
		rune_       int64
		expectByte  int64
		expectError bool
	}{
		// Skip empty file - needs special handling
		{"seek to line 0 rune 0", "Hello", 0, 0, 0, false},
		{"seek to line 0 rune 5 (EOF)", "Hello", 0, 5, 5, false},
		{"seek to line 1 rune 0 after newline", "Hello\n", 1, 0, 6, false},
		{"seek to line 1 rune 5 (EOF)", "Hello\nWorld", 1, 5, 11, false},
		{"seek to line 2 rune 0 after trailing newline", "Hello\nWorld\n", 2, 0, 12, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, err := lib.Open(FileOptions{DataString: tc.content})
			if err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			defer g.Close()

			cursor := g.NewCursor()
			err = cursor.SeekLine(tc.line, tc.rune_)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error, got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("SeekLine(%d, %d) failed: %v", tc.line, tc.rune_, err)
			}

			bytePos := cursor.BytePos()
			t.Logf("SeekLine(%d, %d) -> byte %d (expected %d)", tc.line, tc.rune_, bytePos, tc.expectByte)

			if bytePos != tc.expectByte {
				t.Errorf("Expected byte %d, got %d", tc.expectByte, bytePos)
			}
		})
	}
}
