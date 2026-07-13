package garland

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// TestConcurrencyHammer: the full-concurrency contract - any goroutine
// may call any public API. Eight goroutines hammer one buffer with
// edits, reads, searches, decorations, storage churn, saves, and undo
// seeks, with background maintenance enabled via a soft memory limit.
// Position errors are expected (the document shrinks under racing
// readers); data races, deadlocks (test timeout), and structural
// corruption are not. Run with -race for the full value.
func TestConcurrencyHammer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	content := integrityDoc(16 << 10)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	lib, _ := Init(LibraryOptions{
		ColdStoragePath: filepath.Join(dir, "cold"),
		MemorySoftLimit: 64 << 10, // keep background maintenance busy
	})
	g, err := lib.Open(FileOptions{FilePath: path, MaxLeafSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Position/version errors are legitimate under concurrency (the
	// buffer shrinks and time-travels beneath every goroutine).
	tolerable := func(err error) bool {
		if err == nil {
			return true
		}
		switch err {
		case ErrInvalidPosition, ErrNotReady, ErrTimeout,
			ErrRevisionNotFound, ErrForkNotFound, ErrTransactionPending,
			ErrDecorationNotFound, ErrInvalidUTF8, ErrOverlappingRanges:
			return true
		}
		return false
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	report := func(who string, err error) {
		if !tolerable(err) {
			select {
			case errs <- fmt.Errorf("%s: %w", who, err):
			default:
			}
		}
	}
	clampPos := func(rnd *rand.Rand) int64 {
		n := g.ByteCount().Value
		if n <= 0 {
			return 0
		}
		return rnd.Int63n(n)
	}

	// Two editors.
	for e := 0; e < 2; e++ {
		e := e
		wg.Add(1)
		go func() {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(int64(100 + e)))
			c := g.NewCursor()
			for i := 0; i < 250; i++ {
				if err := c.SeekByte(clampPos(rnd)); err != nil {
					report("editor-seek", err)
					continue
				}
				switch rnd.Intn(3) {
				case 0:
					_, err := c.InsertString(fmt.Sprintf("<e%d-%d>", e, i), nil, rnd.Intn(2) == 0)
					report("editor-insert", err)
				case 1:
					_, _, err := c.DeleteBytes(int64(rnd.Intn(8)+1), false)
					report("editor-delete", err)
				case 2:
					_, _, err := c.OverwriteBytes(int64(rnd.Intn(5)+1), []byte("#OVR#"))
					report("editor-overwrite", err)
				}
			}
		}()
	}

	// Reader: seeks, reads, conversions, accessors.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rnd := rand.New(rand.NewSource(200))
		c := g.NewCursor()
		for i := 0; i < 500; i++ {
			pos := clampPos(rnd)
			switch rnd.Intn(5) {
			case 0:
				if err := c.SeekByte(pos); err == nil {
					_, err := c.ReadBytes(int64(rnd.Intn(64) + 1))
					report("reader-read", err)
				}
			case 1:
				report("reader-seekrune", c.SeekRune(int64(rnd.Intn(64))))
			case 2:
				_, err := g.ByteToRune(pos)
				report("reader-conv", err)
			case 3:
				_, _, err := g.ByteToLineRune(pos)
				report("reader-conv2", err)
			case 4:
				_ = c.BytePos()
				_, _ = c.LinePos()
				_ = c.Position()
				_ = g.ByteCount()
				_ = g.LineCount()
				_ = g.MemoryPressure()
			}
		}
	}()

	// Searcher: finds and word motions.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rnd := rand.New(rand.NewSource(300))
		c := g.NewCursor()
		for i := 0; i < 150; i++ {
			if err := c.SeekByte(clampPos(rnd)); err != nil {
				continue
			}
			switch rnd.Intn(3) {
			case 0:
				_, err := c.FindString("line", SearchOptions{})
				report("search-find", err)
			case 1:
				_, err := c.CountString("e", SearchOptions{})
				report("search-count", err)
			case 2:
				_, err := c.SeekByWordStyle(1+rnd.Intn(3), WordStyle(rnd.Intn(2)))
				report("search-word", err)
			}
		}
	}()

	// Decorator.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rnd := rand.New(rand.NewSource(400))
		for i := 0; i < 150; i++ {
			key := fmt.Sprintf("hammer-%d", rnd.Intn(10))
			switch rnd.Intn(3) {
			case 0:
				_, err := g.Decorate([]DecorationEntry{{Key: key,
					Address: &AbsoluteAddress{Mode: ByteMode, Byte: clampPos(rnd)}}})
				report("decorate-set", err)
			case 1:
				_, err := g.GetDecorationPosition(key)
				report("decorate-get", err)
			case 2:
				_, err := g.GetDecorationsInByteRange(0, clampPos(rnd)+1)
				report("decorate-range", err)
			}
		}
	}()

	// Storage churner.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rnd := rand.New(rand.NewSource(500))
		levels := []ChillLevel{ChillInactiveForks, ChillOldHistory, ChillUnusedData, ChillEverything}
		for i := 0; i < 40; i++ {
			switch rnd.Intn(3) {
			case 0:
				report("chill", g.Chill(levels[rnd.Intn(len(levels))]))
			case 1:
				report("thaw", g.Thaw())
			case 2:
				a := clampPos(rnd)
				report("thawrange", g.ThawRange(a, a+256))
			}
		}
	}()

	// Saver: both modes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 6; i++ {
			rep, err := g.SaveWith(SaveOptions{PreserveHistory: true, Concurrent: i%2 == 0})
			report("save", err)
			if err == nil && len(rep.Scars) != 0 {
				report("save-scars", fmt.Errorf("scars with no data loss: %+v", rep.Scars))
			}
		}
	}()

	// Historian: undo hops (kept rare; every hop forks under edits).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			rev := g.CurrentRevision()
			if rev > 0 {
				report("undo", g.UndoSeek(rev-1))
			}
			_, err := g.FindForksBetween(0, g.CurrentRevision())
			report("forks", err)
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Structural invariants after the dust settles.
	got := readBack(t, g)
	if int64(len(got)) != g.ByteCount().Value {
		t.Fatalf("ByteCount %d != content length %d", g.ByteCount().Value, len(got))
	}
	if int64(utf8.RuneCountInString(got)) != g.RuneCount().Value {
		t.Fatalf("RuneCount %d != recount %d", g.RuneCount().Value, utf8.RuneCountInString(got))
	}
	if int64(strings.Count(got, "\n")) != g.LineCount().Value {
		t.Fatalf("LineCount %d != recount %d", g.LineCount().Value, strings.Count(got, "\n"))
	}

	// And a final save lands the survivor state byte-for-byte.
	if _, err := g.SaveWith(SaveOptions{PreserveHistory: true, Concurrent: true}); err != nil {
		t.Fatalf("final save: %v", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != got {
		// The buffer may have been edited between readBack and save by
		// nothing (all goroutines joined) - so this must hold exactly.
		t.Fatalf("file != buffer after final save: %d vs %d bytes", len(onDisk), len(got))
	}
}
