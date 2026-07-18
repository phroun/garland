package garland

import "testing"

// TestSeekLineOverRange: RULING - seeking past a line's rune count
// migrates forward into later lines (bounded at EOF; the final line
// past a trailing newline is reachable). LinePos() and BytePos() must
// always agree afterward - never the pre-fix state where LinePos kept
// the requested out-of-range column while BytePos advanced.
func TestSeekLineOverRange(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "hello\nworld\n"}) // line0=hello(5), line1=world(5), line2=""
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c := g.NewCursor()

	// coherence: LinePos, BytePos, and the conversions all agree.
	check := func(tag string, wantLine, wantRune, wantByte int64) {
		t.Helper()
		gotLine, gotRune := c.LinePos()
		gotByte := c.BytePos()
		if gotLine != wantLine || gotRune != wantRune || gotByte != wantByte {
			t.Fatalf("%s: LinePos=(%d,%d) BytePos=%d, want (%d,%d)/%d",
				tag, gotLine, gotRune, gotByte, wantLine, wantRune, wantByte)
		}
		// Independent coherence: converting BytePos back must match LinePos.
		bl, br, _ := g.ByteToLineRune(gotByte)
		if bl != gotLine || br != gotRune {
			t.Fatalf("%s: BytePos=%d converts to (%d,%d) but LinePos=(%d,%d)",
				tag, gotByte, bl, br, gotLine, gotRune)
		}
	}

	if err := c.SeekLine(0, 5); err != nil {
		t.Fatal(err)
	}
	check("in-range end of line 0", 0, 5, 5) // at the newline

	if err := c.SeekLine(0, 6); err != nil {
		t.Fatal(err)
	}
	check("one past line 0 migrates to line 1", 1, 0, 6)

	if err := c.SeekLine(0, 20); err != nil {
		t.Fatal(err)
	}
	check("far past migrates, bounded at EOF (empty final line)", 2, 0, 12)

	// The final line past the trailing newline is directly reachable too.
	if err := c.SeekLine(2, 0); err != nil {
		t.Fatal(err)
	}
	check("last line directly", 2, 0, 12)
}

// TestSeekLineOverRangeNoTrailingNewline: the last line has content and
// no trailing newline; overshoot bounds at end-of-buffer on that line.
func TestSeekLineOverRangeNoTrailingNewline(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "hello\nworld"}) // 11 bytes, line1="world" no \n
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c := g.NewCursor()
	if err := c.SeekLine(0, 20); err != nil {
		t.Fatal(err)
	}
	gl, gr := c.LinePos()
	gb := c.BytePos()
	if gl != 1 || gr != 5 || gb != 11 {
		t.Fatalf("LinePos=(%d,%d) BytePos=%d, want (1,5)/11 (end of world)", gl, gr, gb)
	}
}

// TestSeekLineOverRangeMultiLeaf: the multi-leaf case from the report -
// small leaves, long lines, seek a huge column; the derived position
// must still be self-consistent.
func TestSeekLineOverRangeMultiLeaf(t *testing.T) {
	var b []byte
	for i := 0; i < 30; i++ { // 30 lines of 60 chars + newline
		for j := 0; j < 60; j++ {
			b = append(b, byte('a'+(j%26)))
		}
		b = append(b, '\n')
	}
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataBytes: b, MaxLeafSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c := g.NewCursor()
	if err := c.SeekLine(10, 200); err != nil {
		t.Fatal(err)
	}
	gl, gr := c.LinePos()
	gb := c.BytePos()
	bl, br, _ := g.ByteToLineRune(gb)
	if bl != gl || br != gr {
		t.Fatalf("multi-leaf incoherent: LinePos=(%d,%d) but BytePos=%d converts to (%d,%d)", gl, gr, gb, bl, br)
	}
	// 200 past 60-rune lines migrates ~3 lines forward, well short of EOF.
	if gl <= 10 {
		t.Fatalf("expected migration past line 10, got line %d", gl)
	}
}
