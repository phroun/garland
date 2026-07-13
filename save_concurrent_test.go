package garland

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestConcurrentSaveDuringEdits: the point of SaveOptions.Concurrent -
// one goroutine keeps editing (the op goroutine) while another runs
// saves. After both finish, a final save must leave the file exactly
// equal to an independently-tracked model of the edits. Run with
// -race for the full value.
func TestConcurrentSaveDuringEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	content := []byte(integrityDoc(32 << 10)) // 32KB
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	lib, _ := Init(LibraryOptions{ColdStoragePath: filepath.Join(dir, "cold")})
	g, err := lib.Open(FileOptions{FilePath: path, MaxLeafSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if chillCurrentWarmEligible(t, g) == 0 {
		t.Fatal("expected warm leaves")
	}

	var wg sync.WaitGroup
	editErr := make(chan error, 1)
	saveErr := make(chan error, 1)
	model := append([]byte(nil), content...)

	// Editor: the single op goroutine, tracking its own model.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rnd := rand.New(rand.NewSource(7))
		c := g.NewCursor()
		for i := 0; i < 600; i++ {
			pos := int64(rnd.Intn(len(model) + 1))
			if err := c.SeekByte(pos); err != nil {
				editErr <- fmt.Errorf("seek(%d): %w", pos, err)
				return
			}
			if rnd.Intn(3) > 0 || len(model) < 64 {
				ins := []byte(fmt.Sprintf("<edit%d>", i))
				if _, err := c.InsertString(string(ins), nil, false); err != nil {
					editErr <- fmt.Errorf("insert@%d: %w", pos, err)
					return
				}
				model = append(model[:pos], append(append([]byte(nil), ins...), model[pos:]...)...)
			} else {
				n := int64(rnd.Intn(9) + 1)
				if pos+n > int64(len(model)) {
					n = int64(len(model)) - pos
				}
				if n <= 0 {
					continue
				}
				if _, _, err := c.DeleteBytes(n, false); err != nil {
					editErr <- fmt.Errorf("delete@%d: %w", pos, err)
					return
				}
				model = append(model[:pos], model[pos+n:]...)
			}
			// Interleave reads so warm/cold paths run under the saver.
			if i%17 == 0 {
				rp := int64(rnd.Intn(len(model)))
				if err := c.SeekByte(rp); err == nil {
					_, _ = c.ReadBytes(int64(rnd.Intn(64) + 1))
				}
			}
		}
	}()

	// Saver: concurrent saves racing the editor.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 6; i++ {
			rep, err := g.SaveWith(SaveOptions{PreserveHistory: true, Concurrent: true})
			if err != nil {
				saveErr <- fmt.Errorf("concurrent save %d: %w", i, err)
				return
			}
			if len(rep.Scars) != 0 {
				saveErr <- fmt.Errorf("concurrent save %d produced scars: %+v", i, rep.Scars)
				return
			}
		}
	}()

	wg.Wait()
	select {
	case err := <-editErr:
		t.Fatal(err)
	default:
	}
	select {
	case err := <-saveErr:
		t.Fatal(err)
	default:
	}

	// Editing has stopped: buffer must equal the model, and a final
	// save must land it in the file byte-for-byte.
	if got := readBack(t, g); got != string(model) {
		t.Fatalf("buffer diverged from model: %d vs %d bytes", len(got), len(model))
	}
	rep, err := g.SaveWith(SaveOptions{PreserveHistory: true, Concurrent: true})
	if err != nil {
		t.Fatalf("final save: %v", err)
	}
	if !rep.Concurrent {
		t.Error("final save unexpectedly fell back to locked mode")
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, model) {
		t.Fatalf("file != model after final save: %d vs %d bytes", len(onDisk), len(model))
	}
}

// TestConcurrentSaveFallback: no cold backend plus a hard memory limit
// too small for the required evacuation - the concurrent request must
// transparently run the locked zero-copy path and say so.
func TestConcurrentSaveFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	content := integrityDoc(16 << 10)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	lib, _ := Init(LibraryOptions{MemoryHardLimit: 4096}) // no cold storage
	g, err := lib.Open(FileOptions{FilePath: path, MaxLeafSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if chillCurrentWarmEligible(t, g) == 0 {
		t.Fatal("expected warm leaves")
	}

	// Insert at the very front: every following block becomes a mover,
	// so a concurrent save would have to evacuate ~16KB >> 4KB limit.
	c := g.NewCursor()
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString("<front>", nil, false); err != nil {
		t.Fatal(err)
	}
	want := "<front>" + content

	rep, err := g.SaveWith(SaveOptions{PreserveHistory: true, Concurrent: true})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if rep.Concurrent {
		t.Error("save should have fallen back to locked mode under the memory limit")
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != want {
		t.Fatalf("fallback save wrote wrong content: %d vs %d bytes", len(onDisk), len(want))
	}
}

// TestMemoryPressureSignal: the app-side hot-write signal. With no
// cold backend, unsaved edits are SaveableBytes (only a save frees
// them); after the save they become evictable warm-backed data.
func TestMemoryPressureSignal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	content := integrityDoc(8 << 10)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	lib, _ := Init(LibraryOptions{MemoryHardLimit: 1 << 20}) // no cold storage
	g, err := lib.Open(FileOptions{FilePath: path, MaxLeafSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if chillCurrentWarmEligible(t, g) == 0 {
		t.Fatal("expected warm leaves")
	}

	// Dirty a region: those bytes now have no file backing and no
	// cold backend - only a Save can make them evictable.
	c := g.NewCursor()
	if err := c.SeekByte(3000); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString("unsaved edit that lives only in memory", nil, false); err != nil {
		t.Fatal(err)
	}

	info := g.MemoryPressure()
	if info.SaveableBytes == 0 {
		t.Fatalf("expected saveable (save-to-evict) bytes, got %+v", info)
	}
	if info.HardLimitBytes != 1<<20 {
		t.Errorf("HardLimitBytes = %d, want %d", info.HardLimitBytes, 1<<20)
	}

	if _, err := g.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	after := g.MemoryPressure()
	if after.SaveableBytes != 0 {
		t.Fatalf("after save: still %d saveable bytes (%+v)", after.SaveableBytes, after)
	}
	if after.ResidentBytes > 0 && after.EvictableBytes == 0 {
		t.Fatalf("after save: resident bytes not evictable (%+v)", after)
	}
}
