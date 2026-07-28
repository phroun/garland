package garland

import (
	"fmt"
	"strings"
	"testing"
)

// large_insert_test.go - a single large InsertString/InsertBytes must
// build a balanced subtree of bounded leaves (the loader's shape for
// the same bytes), not one oversized leaf that every later seek scans.

// maxLeafBytesIn walks the current tree and returns the largest leaf
// payload plus the leaf count.
func maxLeafBytesIn(t *testing.T, g *Garland) (maxBytes int64, leaves int) {
	t.Helper()
	var walk func(id NodeID)
	walk = func(id NodeID) {
		node := g.nodeRegistry[id]
		if node == nil {
			t.Fatalf("node %d missing from registry", id)
		}
		snap := node.snapshotAt(g.currentFork, g.currentRevision)
		if snap == nil {
			t.Fatalf("node %d has no snapshot at current version", id)
		}
		if snap.isLeaf {
			leaves++
			if snap.byteCount > maxBytes {
				maxBytes = snap.byteCount
			}
			return
		}
		walk(snap.leftID)
		walk(snap.rightID)
	}
	walk(g.root.id)
	return maxBytes, leaves
}

func largeCorpus(lines int) string {
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&sb, "line %d: the quick brown fox jumps over the lazy dog %d\n", i, i)
	}
	return sb.String()
}

// TestLargeInsertBuildsBoundedLeaves: one big insert into an empty
// document must leave no leaf over maxLeafSize, and the content must
// round-trip exactly.
func TestLargeInsertBuildsBoundedLeaves(t *testing.T) {
	const leafMax = 4096
	content := largeCorpus(2000) // ~120KB, ~30x leafMax

	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataBytes: []byte{}, MaxLeafSize: leafMax})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	c := g.NewCursor()
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString(content, nil, false); err != nil {
		t.Fatal(err)
	}

	maxBytes, leaves := maxLeafBytesIn(t, g)
	if maxBytes > leafMax {
		t.Fatalf("largest leaf is %d bytes, want <= %d (one big insert built an oversized leaf; %d leaves total)",
			maxBytes, leafMax, leaves)
	}
	if leaves < len(content)/leafMax {
		t.Fatalf("only %d leaves for %d bytes with max leaf %d", leaves, len(content), leafMax)
	}
	if got := contentOf(t, g, c); got != content {
		t.Fatalf("content mismatch after large insert: got %d bytes, want %d", len(got), len(content))
	}
}

// TestLargeInsertIntoMiddle: a large insert into the middle of existing
// content keeps leaves bounded and stitches left + payload + right.
func TestLargeInsertIntoMiddle(t *testing.T) {
	const leafMax = 4096
	payload := largeCorpus(1000)
	seed := "seed line one\nseed line two\n"

	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataString: seed, MaxLeafSize: leafMax})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	c := g.NewCursor()
	insertAt := int64(len("seed line one\n"))
	if err := c.SeekByte(insertAt); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString(payload, nil, false); err != nil {
		t.Fatal(err)
	}

	maxBytes, _ := maxLeafBytesIn(t, g)
	if maxBytes > leafMax {
		t.Fatalf("largest leaf is %d bytes, want <= %d", maxBytes, leafMax)
	}
	want := seed[:insertAt] + payload + seed[insertAt:]
	if got := contentOf(t, g, c); got != want {
		t.Fatalf("content mismatch after middle insert: got %d bytes, want %d", len(got), len(want))
	}

	// The structure must serve line addressing correctly across the
	// payload boundary.
	if err := c.SeekLine(1001, 0); err != nil { // "seed line two" again
		t.Fatal(err)
	}
	line, err := c.ReadLine()
	if err != nil {
		t.Fatal(err)
	}
	if line != "seed line two\n" && line != "seed line two" {
		t.Fatalf("SeekLine after payload = %q, want the second seed line", line)
	}
}

// TestLargeInsertDecorationsPartitioned: decorations riding a large
// insert land in whichever bounded leaf holds their byte, at the right
// absolute position - and decorations already in the split leaf
// survive on both sides.
func TestLargeInsertDecorationsPartitioned(t *testing.T) {
	const leafMax = 4096
	payload := largeCorpus(1000)

	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataString: "left|right", MaxLeafSize: leafMax})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Pre-existing marks either side of the future insert point.
	for key, pos := range map[string]int64{"pre_left": 2, "pre_right": 7} {
		addr := ByteAddress(pos)
		if _, err := g.Decorate([]DecorationEntry{{Key: key, Address: &addr}}); err != nil {
			t.Fatal(err)
		}
	}

	insertAt := int64(5) // between "left|" and "right"
	riding := []RelativeDecoration{
		{Key: "ride_start", Position: 0},
		{Key: "ride_mid", Position: int64(len(payload)) / 2},
		{Key: "ride_late", Position: int64(len(payload)) - 3},
	}
	c := g.NewCursor()
	if err := c.SeekByte(insertAt); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString(payload, riding, false); err != nil {
		t.Fatal(err)
	}

	maxBytes, _ := maxLeafBytesIn(t, g)
	if maxBytes > leafMax {
		t.Fatalf("largest leaf is %d bytes, want <= %d", maxBytes, leafMax)
	}

	want := map[string]int64{
		"pre_left":   2,
		"ride_start": insertAt + 0,
		"ride_mid":   insertAt + int64(len(payload))/2,
		"ride_late":  insertAt + int64(len(payload)) - 3,
		"pre_right":  7 + int64(len(payload)),
	}
	for key, wantPos := range want {
		addr, err := g.GetDecorationPosition(key)
		if err != nil {
			t.Fatalf("decoration %s lost in large insert: %v", key, err)
		}
		if addr.Byte != wantPos {
			t.Errorf("decoration %s at byte %d, want %d", key, addr.Byte, wantPos)
		}
	}
}

// TestLargeInsertUndoRedo: the balanced middle participates in history
// like any other edit.
func TestLargeInsertUndoRedo(t *testing.T) {
	const leafMax = 4096
	payload := largeCorpus(500)
	seed := "before\nafter\n"

	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataString: seed, MaxLeafSize: leafMax})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	c := g.NewCursor()
	if err := c.SeekByte(int64(len("before\n"))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString(payload, nil, false); err != nil {
		t.Fatal(err)
	}
	insertedRev := g.CurrentRevision()

	if err := g.UndoSeek(0); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != seed {
		t.Fatalf("undo of large insert: got %d bytes, want the %d-byte seed", len(got), len(seed))
	}
	if err := g.UndoSeek(insertedRev); err != nil {
		t.Fatal(err)
	}
	want := "before\n" + payload + "after\n"
	if got := contentOf(t, g, c); got != want {
		t.Fatalf("redo of large insert: got %d bytes, want %d", len(got), len(want))
	}
	maxBytes, _ := maxLeafBytesIn(t, g)
	if maxBytes > leafMax {
		t.Fatalf("largest leaf is %d bytes after redo, want <= %d", maxBytes, leafMax)
	}
}

// TestLargeOverwriteBuildsBoundedLeaves: overwrite funnels its insert
// half through the same path - a large replacement must stay bounded.
func TestLargeOverwriteBuildsBoundedLeaves(t *testing.T) {
	const leafMax = 4096
	payload := largeCorpus(1000)
	seed := "0123456789abcdef\n"

	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataString: seed, MaxLeafSize: leafMax})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	c := g.NewCursor()
	if err := c.SeekByte(4); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.OverwriteBytes(8, []byte(payload)); err != nil {
		t.Fatal(err)
	}

	maxBytes, _ := maxLeafBytesIn(t, g)
	if maxBytes > leafMax {
		t.Fatalf("largest leaf is %d bytes after large overwrite, want <= %d", maxBytes, leafMax)
	}
	want := seed[:4] + payload + seed[12:]
	if got := contentOf(t, g, c); got != want {
		t.Fatalf("content mismatch after large overwrite: got %d bytes, want %d", len(got), len(want))
	}
}

// TestLargeInsertUTF8Boundaries: split points inside a large multi-byte
// payload must land on rune boundaries so per-leaf rune indexes stay
// meaningful.
func TestLargeInsertUTF8Boundaries(t *testing.T) {
	const leafMax = 4096
	payload := strings.Repeat("héllo wörld 世界 ", 4000) // multi-byte throughout

	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataBytes: []byte{}, MaxLeafSize: leafMax})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	c := g.NewCursor()
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString(payload, nil, false); err != nil {
		t.Fatal(err)
	}

	// Every leaf must start on a rune boundary.
	var offset int64
	var walk func(id NodeID)
	walk = func(id NodeID) {
		node := g.nodeRegistry[id]
		snap := node.snapshotAt(g.currentFork, g.currentRevision)
		if snap.isLeaf {
			if len(snap.data) > 0 && (snap.data[0]&0xC0) == 0x80 {
				t.Fatalf("leaf at offset %d starts mid-rune", offset)
			}
			if snap.byteCount > leafMax {
				t.Fatalf("leaf at offset %d is %d bytes, want <= %d", offset, snap.byteCount, leafMax)
			}
			offset += snap.byteCount
			return
		}
		walk(snap.leftID)
		walk(snap.rightID)
	}
	walk(g.root.id)

	if got := contentOf(t, g, c); got != payload {
		t.Fatalf("UTF-8 content mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}
