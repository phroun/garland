package garland

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordingFS wraps a FileSystemInterface and records the operations
// that matter for the in-place save contract: which files get opened
// (and how), and when truncation happens.
type recordingFS struct {
	FileSystemInterface
	opens     []string
	openModes []OpenMode
	truncates []int64
	writes    int
}

func (r *recordingFS) Open(name string, mode OpenMode) (FileHandle, error) {
	r.opens = append(r.opens, name)
	r.openModes = append(r.openModes, mode)
	return r.FileSystemInterface.Open(name, mode)
}

func (r *recordingFS) Truncate(h FileHandle, size int64) error {
	r.truncates = append(r.truncates, size)
	return r.FileSystemInterface.Truncate(h, size)
}

func (r *recordingFS) WriteBytes(h FileHandle, data []byte) error {
	r.writes++
	return r.FileSystemInterface.WriteBytes(h, data)
}

// openSaveFixture writes content to a file and opens it with small
// leaves (1KB) through a recording FS.
func openSaveFixture(t *testing.T, content string) (*Garland, *recordingFS, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	rfs := &recordingFS{FileSystemInterface: &localFileSystem{}}
	lib, err := Init(LibraryOptions{ColdStoragePath: filepath.Join(dir, "cold")})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{
		FilePath:    path,
		FileSystem:  rfs,
		MaxLeafSize: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Reset the recording so tests see only save-time operations.
	rfs.opens = nil
	rfs.openModes = nil
	rfs.truncates = nil
	rfs.writes = 0
	return g, rfs, path
}

// chillCurrentWarmEligible pushes every warm-eligible leaf of the
// current revision to warm storage (drops its bytes from memory).
func chillCurrentWarmEligible(t *testing.T, g *Garland) int {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	chilled := 0
	seen := map[NodeID]bool{}
	var walk func(id NodeID)
	walk = func(id NodeID) {
		node := g.nodeRegistry[id]
		if node == nil || seen[id] {
			return
		}
		seen[id] = true
		snap := node.snapshotAt(g.currentFork, g.currentRevision)
		if snap == nil {
			return
		}
		if !snap.isLeaf {
			walk(snap.leftID)
			walk(snap.rightID)
			return
		}
		if snap.storageState == StorageMemory && snap.originalFileOffset >= 0 && snap.byteCount > 0 {
			if err := g.chillToWarmStorage(node.id, snap); err != nil {
				t.Fatalf("chillToWarmStorage: %v", err)
			}
			chilled++
		}
	}
	walk(g.root.id)
	return chilled
}

func readBack(t *testing.T, g *Garland) string {
	t.Helper()
	c := g.NewCursor()
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	data, err := c.ReadBytes(g.ByteCount().Value)
	if err != nil {
		t.Fatalf("ReadBytes: %v", err)
	}
	return string(data)
}

func saveDoc(size int) string {
	line := "0123456789abcdefghijklmnopqrstuvwxyz ABCDEFGHIJKLMNOPQRSTUVWXYZ\n"
	buf := make([]byte, 0, size+len(line))
	for len(buf) < size {
		buf = append(buf, line...)
	}
	return string(buf[:size])
}

// TestSaveInPlaceContract: the floppy-disk constraints. Saving must
// not create any other file, must not open the source with a
// truncating mode, and must truncate only once - downward, at the end.
func TestSaveInPlaceContract(t *testing.T) {
	content := saveDoc(8192)
	g, rfs, path := openSaveFixture(t, content)
	defer g.Close()

	// Chill everything warm, then edit: insert near the start (forces
	// right-moving warm spans) and delete a bit near the end (shrink).
	if n := chillCurrentWarmEligible(t, g); n == 0 {
		t.Fatal("expected warm leaves")
	}
	c := g.NewCursor()
	if err := c.SeekByte(10); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString("<INSERTED>", nil, false); err != nil {
		t.Fatal(err)
	}
	if err := c.SeekByte(g.ByteCount().Value - 100); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.DeleteBytes(50, false); err != nil {
		t.Fatal(err)
	}
	want := readBack(t, g)

	if _, err := g.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File content matches the buffer.
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != want {
		t.Fatalf("file/buffer mismatch after save:\n file %d bytes\n want %d bytes", len(onDisk), len(want))
	}

	// No file other than the source was ever opened.
	for _, name := range rfs.opens {
		if name != path {
			t.Errorf("save opened unexpected file %q", name)
		}
	}
	// The source was never opened with the truncating write mode.
	for i, mode := range rfs.openModes {
		if mode == OpenModeWrite {
			t.Errorf("save opened %q with truncating OpenModeWrite", rfs.opens[i])
		}
	}
	// Exactly one truncation, downward, to the final size.
	if len(rfs.truncates) != 1 || rfs.truncates[0] != int64(len(want)) {
		t.Errorf("truncates = %v, want exactly [%d]", rfs.truncates, len(want))
	}

	// Warm storage survived: re-read through the (re-homed) warm spans.
	if got := readBack(t, g); got != want {
		t.Errorf("buffer mismatch after save (re-homed warm reads)")
	}
}

// TestSaveGrowNoTruncate: a pure append/grow save must not truncate at all.
func TestSaveGrowNoTruncate(t *testing.T) {
	g, rfs, path := openSaveFixture(t, saveDoc(4096))
	defer g.Close()
	chillCurrentWarmEligible(t, g)
	c := g.NewCursor()
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString("HEAD:", nil, false); err != nil {
		t.Fatal(err)
	}
	want := readBack(t, g)
	if _, err := g.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(rfs.truncates) != 0 {
		t.Errorf("grow-only save truncated: %v", rfs.truncates)
	}
	onDisk, _ := os.ReadFile(path)
	if string(onDisk) != want {
		t.Error("file/buffer mismatch after grow save")
	}
}

// TestSavePreservesHistoryWarm: history that depends on warm bytes the
// save overwrites must survive via cold migration and remain
// undo-reachable afterwards.
func TestSavePreservesHistoryWarm(t *testing.T) {
	g, _, _ := openSaveFixture(t, saveDoc(4096))
	defer g.Close()
	chillCurrentWarmEligible(t, g)
	original := readBack(t, g)
	preRev := g.CurrentRevision()

	// Overwrite the first 1KB region so history's warm leaf for it is
	// clobbered by the save.
	c := g.NewCursor()
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.OverwriteBytes(1500, []byte(saveDoc(1500))); err != nil {
		t.Fatal(err)
	}
	if _, err := g.SaveWith(SaveOptions{PreserveHistory: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := g.UndoSeek(preRev); err != nil {
		t.Fatalf("UndoSeek: %v", err)
	}
	if got := readBack(t, g); got != original {
		t.Errorf("history content corrupted after preserving save")
	}
}

// TestSaveRandomizedWarmRoundTrips: mini storage harness. Random edits
// mirrored to a flat model, random warm chills, random saves - after
// every save the file on disk must equal the model, and the buffer
// must stay readable through re-homed warm spans.
func TestSaveRandomizedWarmRoundTrips(t *testing.T) {
	for seed := int64(1); seed <= 6; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rnd := rand.New(rand.NewSource(seed))
			content := saveDoc(6144)
			g, _, path := openSaveFixture(t, content)
			defer g.Close()
			model := []byte(content)
			c := g.NewCursor()

			for i := 0; i < 60; i++ {
				switch rnd.Intn(5) {
				case 0, 1: // insert
					pos := int64(rnd.Intn(len(model) + 1))
					piece := []byte(fmt.Sprintf("[i%d]", i))
					if err := c.SeekByte(pos); err != nil {
						t.Fatal(err)
					}
					if _, err := c.InsertString(string(piece), nil, false); err != nil {
						t.Fatal(err)
					}
					model = append(model[:pos:pos], append(append([]byte(nil), piece...), model[pos:]...)...)
				case 2: // delete
					if len(model) < 10 {
						continue
					}
					pos := int64(rnd.Intn(len(model) - 8))
					n := int64(1 + rnd.Intn(7))
					if err := c.SeekByte(pos); err != nil {
						t.Fatal(err)
					}
					if _, _, err := c.DeleteBytes(n, false); err != nil {
						t.Fatal(err)
					}
					model = append(model[:pos:pos], model[pos+n:]...)
				case 3: // chill whatever is warm-eligible
					chillCurrentWarmEligible(t, g)
				case 4: // save + verify disk == model
					if _, err := g.Save(); err != nil {
						t.Fatalf("op %d Save: %v", i, err)
					}
					onDisk, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(onDisk, model) {
						t.Fatalf("op %d: disk (%d bytes) != model (%d bytes)", i, len(onDisk), len(model))
					}
				}
				if got := g.ByteCount().Value; got != int64(len(model)) {
					t.Fatalf("op %d: ByteCount %d != model %d", i, got, len(model))
				}
			}
			// Final save + full read-back through whatever tiers remain.
			if _, err := g.Save(); err != nil {
				t.Fatalf("final Save: %v", err)
			}
			onDisk, _ := os.ReadFile(path)
			if !bytes.Equal(onDisk, model) {
				t.Fatal("final disk != model")
			}
			if got := readBack(t, g); got != string(model) {
				t.Fatal("final buffer != model")
			}
		})
	}
}

// TestScarBytesFormat pins the exact scar layout ruling:
//   - marker fits (with leading+trailing newline): "\n" + marker +
//     "="-padding + "\n", exactly blockLen bytes, no appendix;
//   - no room: "\n" + "="s + "\n" (or "\n\n" / "\n" for 2/1 bytes) and
//     the marker moves to an appendix with a leading newline.
func TestScarBytesFormat(t *testing.T) {
	marker := "[Missing 42 bytes from original file address 7]"
	ml := int64(len(marker))

	// Exact fit: marker + 2 newlines.
	block, app := scarBytes(ml+2, marker)
	if app != nil {
		t.Errorf("exact fit produced appendix %q", app)
	}
	if string(block) != "\n"+marker+"\n" {
		t.Errorf("exact fit block = %q", block)
	}

	// Roomy fit: padding is '=', final byte newline.
	block, app = scarBytes(ml+10, marker)
	if app != nil {
		t.Errorf("roomy fit produced appendix")
	}
	if int64(len(block)) != ml+10 {
		t.Fatalf("block len %d, want %d", len(block), ml+10)
	}
	if block[0] != '\n' || block[len(block)-1] != '\n' {
		t.Error("block must start and end with newline")
	}
	if string(block[1:1+ml]) != marker {
		t.Error("marker not at start of block")
	}
	for _, b := range block[1+ml : int64(len(block))-1] {
		if b != '=' {
			t.Errorf("padding byte %q, want '='", b)
		}
	}

	// One byte short of fitting: filler + appendix.
	block, app = scarBytes(ml+1, marker)
	if string(app) != "\n"+marker {
		t.Errorf("appendix = %q", app)
	}
	if block[0] != '\n' || block[len(block)-1] != '\n' {
		t.Error("filler must start and end with newline")
	}
	for _, b := range block[1 : len(block)-1] {
		if b != '=' {
			t.Errorf("filler byte %q, want '='", b)
		}
	}

	// Tiny blocks.
	if block, _ := scarBytes(2, marker); string(block) != "\n\n" {
		t.Errorf("2-byte filler = %q, want \\n\\n", block)
	}
	if block, _ := scarBytes(1, marker); string(block) != "\n" {
		t.Errorf("1-byte filler = %q, want \\n", block)
	}
	if block, _ := scarBytes(5, marker); string(block) != "\n===\n" {
		t.Errorf("5-byte filler = %q, want \\n===\\n", block)
	}
}

// TestSaveNeverRefusesOnPlaceholder: destroy a cold block behind the
// buffer's back, then Save. The save must succeed, the hole must be a
// visible scar of the same size, and everything else must survive.
func TestSaveNeverRefusesOnPlaceholder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	coldDir := filepath.Join(dir, "cold")
	content := saveDoc(4096)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	lib, _ := Init(LibraryOptions{ColdStoragePath: coldDir})
	g, err := lib.Open(FileOptions{FilePath: path, MaxLeafSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Chill everything to cold, then destroy the cold store.
	if err := g.Chill(ChillEverything); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(coldDir); err != nil {
		t.Fatal(err)
	}

	// Reads now fail and mark leaves as placeholders...
	c := g.NewCursor()
	if err := c.SeekByte(0); err == nil {
		if _, err := c.ReadBytes(100); err == nil {
			t.Skip("cold data unexpectedly still readable")
		}
	}

	// ...but Save must still succeed, and the report must tell the app
	// exactly what was lost, where, and why.
	report, err := g.Save()
	if err != nil {
		t.Fatalf("Save refused on placeholder: %v", err)
	}
	if len(report.Scars) == 0 {
		t.Fatal("scarred save reported no ScarWarnings")
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Same total size (every scar is byte-for-byte the lost block).
	if len(onDisk) != len(content) {
		t.Fatalf("file size %d after scarred save, want %d", len(onDisk), len(content))
	}
	// The file must carry the visible marker and the buffer must match
	// the file exactly.
	if !bytes.Contains(onDisk, []byte("[Missing ")) {
		t.Error("scarred file carries no visible marker")
	}
	if got := readBack(t, g); got != string(onDisk) {
		t.Error("buffer != file after scarred save")
	}

	// Each warning must be structurally coherent with the saved file:
	// sane bounds, marker text, and the reason captured at the moment
	// the loss was discovered (here: cold storage destroyed).
	for i, s := range report.Scars {
		if s.Offset < 0 || s.Length <= 0 || s.Offset+s.Length > int64(len(onDisk)) {
			t.Errorf("scar %d: bad bounds offset=%d length=%d (file %d bytes)",
				i, s.Offset, s.Length, len(onDisk))
			continue
		}
		if s.Marker == "" {
			t.Errorf("scar %d: empty marker", i)
		}
		if s.Appended {
			// Marker lives at EOF instead of inside the block.
			if !bytes.Contains(onDisk, []byte("\n"+s.Marker)) {
				t.Errorf("scar %d: appended marker %q not found at EOF region", i, s.Marker)
			}
		} else {
			blockRegion := onDisk[s.Offset : s.Offset+s.Length]
			if !bytes.Contains(blockRegion, []byte(s.Marker)) {
				t.Errorf("scar %d: marker %q not inside its block region", i, s.Marker)
			}
		}
		if !strings.Contains(s.Reason, "cold storage") {
			t.Errorf("scar %d: reason %q does not identify the cold storage failure", i, s.Reason)
		}
	}
}
