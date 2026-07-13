package garland

import (
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"
)

// Benchmarks for the editing hot paths, focused on the "huge file, lots
// of tiny edits" workload that optimized regions were designed for.

func makeDoc(size int) string {
	line := "The quick brown fox jumps over the lazy dog 0123456789 abcdef\n" // 63 bytes
	buf := make([]byte, 0, size+len(line))
	for len(buf) < size {
		buf = append(buf, line...)
	}
	return string(buf[:size])
}

func openBench(b *testing.B, size int) (*Garland, *Cursor) {
	b.Helper()
	lib, err := Init(LibraryOptions{})
	if err != nil {
		b.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataString: makeDoc(size)})
	if err != nil {
		b.Fatal(err)
	}
	return g, g.NewCursor()
}

// benchTyping simulates a typing burst: one-character inserts at an
// advancing cursor, all in one spot of the document.
func benchTyping(b *testing.B, size int) {
	g, c := openBench(b, size)
	if err := c.SeekByte(int64(size / 2)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.InsertString("x", nil, false); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	_ = g
}

func BenchmarkTyping1MB(b *testing.B)   { benchTyping(b, 1<<20) }
func BenchmarkTyping10MB(b *testing.B)  { benchTyping(b, 10<<20) }
func BenchmarkTyping100MB(b *testing.B) { benchTyping(b, 100<<20) }

// benchScatteredEdits: one-character inserts at random positions -
// worst case for locality.
func benchScatteredEdits(b *testing.B, size int) {
	g, c := openBench(b, size)
	rnd := rand.New(rand.NewSource(42))
	positions := make([]int64, b.N)
	for i := range positions {
		positions[i] = rnd.Int63n(int64(size))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := c.SeekByte(positions[i]); err != nil {
			b.Fatal(err)
		}
		if _, err := c.InsertString("x", nil, false); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	_ = g
}

func BenchmarkScattered1MB(b *testing.B)  { benchScatteredEdits(b, 1<<20) }
func BenchmarkScattered10MB(b *testing.B) { benchScatteredEdits(b, 10<<20) }

// benchBackspace: delete one byte at the cursor, repeatedly.
func benchBackspace(b *testing.B, size int) {
	g, c := openBench(b, size)
	if err := c.SeekByte(int64(size / 2)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := c.DeleteBytes(1, false); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	_ = g
}

func BenchmarkBackspace10MB(b *testing.B) { benchBackspace(b, 10<<20) }

// TestEditScaling is not a Go benchmark: it reports how per-edit cost
// evolves over a long session (history accumulation, fragmentation),
// which b.N-style averaging hides. Run with: go test -run TestEditScaling -v
func TestEditScaling(t *testing.T) {
	if os.Getenv("GARLAND_SCALING") == "" {
		t.Skip("set GARLAND_SCALING=1 to run the scaling report (slow; documents the known quadratic-edit pathology)")
	}
	for _, size := range []int{1 << 20, 10 << 20, 100 << 20} {
		lib, _ := Init(LibraryOptions{})
		g, err := lib.Open(FileOptions{DataString: makeDoc(size)})
		if err != nil {
			t.Fatal(err)
		}
		c := g.NewCursor()
		if err := c.SeekByte(int64(size / 2)); err != nil {
			t.Fatal(err)
		}
		const batch = 2000
		fmt.Printf("=== %dMB document, typing at one spot ===\n", size>>20)
		for round := 1; round <= 5; round++ {
			start := nowNano()
			for i := 0; i < batch; i++ {
				if _, err := c.InsertString("x", nil, false); err != nil {
					t.Fatal(err)
				}
			}
			el := nowNano() - start
			fmt.Printf("  edits %5d..%5d: %6.1f us/edit  (nodes=%d)\n",
				(round-1)*batch, round*batch, float64(el)/float64(batch)/1e3, len(g.nodeRegistry))
		}
		// Cost of a read and a line conversion after the session.
		start := nowNano()
		if err := c.SeekByte(0); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ReadBytes(g.ByteCount().Value); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("  full read-back: %.1f ms\n", float64(nowNano()-start)/1e6)
		start = nowNano()
		if _, _, err := g.ByteToLineRune(g.ByteCount().Value / 2); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("  ByteToLineRune(mid): %.3f ms\n", float64(nowNano()-start)/1e6)
		start = nowNano()
		if err := g.UndoSeek(g.CurrentRevision() - 1); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("  UndoSeek(-1): %.3f ms\n", float64(nowNano()-start)/1e6)
	}
}

func nowNano() int64 { return time.Now().UnixNano() }
