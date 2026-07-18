package garland

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// source_meta_test.go - source metadata capture, consistency queries,
// volunteered metadata, save history / revert, source adoption and
// recovery, and device info.

func metaFixture(t *testing.T, content string) (*Library, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	lib, err := Init(LibraryOptions{ColdStoragePath: filepath.Join(dir, "cold")})
	if err != nil {
		t.Fatal(err)
	}
	return lib, path
}

// TestMetadataCapturedOnOpenAllStyles: opening a file captures its
// metadata whatever the loading style, so the app can always ask about
// external modification later.
func TestMetadataCapturedOnOpenAllStyles(t *testing.T) {
	styles := map[string]LoadingStyle{
		"AllStorage":    AllStorage,
		"ColdAndMemory": ColdAndMemory,
		"MemoryOnly":    MemoryOnly,
	}
	content := "metadata capture test content\n"
	for name, style := range styles {
		t.Run(name, func(t *testing.T) {
			lib, path := metaFixture(t, content)
			g, err := lib.Open(FileOptions{FilePath: path, LoadingStyle: style})
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()

			rep, err := g.SourceConsistency()
			if err != nil {
				t.Fatal(err)
			}
			if rep.State != ConsistencyClean {
				t.Fatalf("state = %v, want clean", rep.State)
			}
			if rep.Baseline.Size != int64(len(content)) {
				t.Fatalf("baseline size = %d, want %d", rep.Baseline.Size, len(content))
			}

			// External append is visible in every style.
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString("tail")
			f.Close()

			rep, err = g.SourceConsistency()
			if err != nil {
				t.Fatal(err)
			}
			if rep.State != ConsistencyAppended {
				t.Fatalf("state after append = %v, want appended", rep.State)
			}
			if rep.Observed.Size != int64(len(content)+4) {
				t.Fatalf("observed size = %d", rep.Observed.Size)
			}
		})
	}
}

// TestConsistencyTransitions: modified, truncated, replaced, missing.
func TestConsistencyTransitions(t *testing.T) {
	content := "0123456789abcdef\n"
	check := func(t *testing.T, g *Garland, want SourceConsistencyState) {
		t.Helper()
		rep, err := g.SourceConsistency()
		if err != nil {
			t.Fatal(err)
		}
		if rep.State != want {
			t.Fatalf("state = %v, want %v", rep.State, want)
		}
		// The cached query must agree without touching the disk.
		if got := g.SourceConsistencyCached().State; got != want {
			t.Fatalf("cached state = %v, want %v", got, want)
		}
	}

	t.Run("modified", func(t *testing.T) {
		lib, path := metaFixture(t, content)
		g, err := lib.Open(FileOptions{FilePath: path})
		if err != nil {
			t.Fatal(err)
		}
		defer g.Close()
		// Same size, different mtime.
		if err := os.Chtimes(path, time.Now(), time.Now().Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		check(t, g, ConsistencyModified)
	})

	t.Run("truncated", func(t *testing.T) {
		lib, path := metaFixture(t, content)
		g, err := lib.Open(FileOptions{FilePath: path})
		if err != nil {
			t.Fatal(err)
		}
		defer g.Close()
		if err := os.Truncate(path, 4); err != nil {
			t.Fatal(err)
		}
		check(t, g, ConsistencyTruncated)
	})

	t.Run("replaced", func(t *testing.T) {
		lib, path := metaFixture(t, content)
		g, err := lib.Open(FileOptions{FilePath: path})
		if err != nil {
			t.Fatal(err)
		}
		defer g.Close()
		// Classic replace: write a sibling (guaranteed different
		// identity while the original exists), rename over. Same size,
		// so only identity can catch it.
		tmp := path + ".new"
		if err := os.WriteFile(tmp, []byte(strings.ToUpper(content)), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
		check(t, g, ConsistencyReplaced)
	})

	t.Run("missing", func(t *testing.T) {
		lib, path := metaFixture(t, content)
		g, err := lib.Open(FileOptions{FilePath: path})
		if err != nil {
			t.Fatal(err)
		}
		defer g.Close()
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		check(t, g, ConsistencyMissing)
	})
}

// noStatFS hides metadata: a stand-in for a virtualized filesystem
// that cannot answer Stat, forcing the volunteer path.
type noStatFS struct {
	FileSystemInterface
}

func (fs *noStatFS) Stat(name string) (FileMetadata, error) {
	return FileMetadata{}, ErrNotSupported
}

// TestVolunteeredMetadata: with a metadata-less filesystem the app
// feeds observations in; the first becomes the baseline, later ones
// classify against it, and queries answer from tracked state.
func TestVolunteeredMetadata(t *testing.T) {
	content := "volunteer style tracking\n"
	lib, path := metaFixture(t, content)
	g, err := lib.Open(FileOptions{
		FilePath:   path,
		FileSystem: &noStatFS{FileSystemInterface: NewLocalFileSystem()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	if got := g.SourceConsistencyCached().State; got != ConsistencyUntracked {
		t.Fatalf("pre-volunteer state = %v, want untracked", got)
	}

	base := FileMetadata{Exists: true, Size: int64(len(content)), ModTime: time.Now()}
	if got := g.ReportSourceMetadata(base); got != ConsistencyClean {
		t.Fatalf("first report = %v, want clean (becomes baseline)", got)
	}

	grown := base
	grown.Size += 7
	if got := g.ReportSourceMetadata(grown); got != ConsistencyAppended {
		t.Fatalf("grown report = %v, want appended", got)
	}
	if got := g.SourceConsistencyCached().State; got != ConsistencyAppended {
		t.Fatalf("cached = %v, want appended", got)
	}

	touched := base
	touched.ModTime = base.ModTime.Add(time.Minute)
	if got := g.ReportSourceMetadata(touched); got != ConsistencyModified {
		t.Fatalf("touched report = %v, want modified", got)
	}

	// The pull-style query cannot stat, but still answers from the
	// volunteered observation without error.
	rep, err := g.SourceConsistency()
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != ConsistencyModified {
		t.Fatalf("pull state = %v, want modified", rep.State)
	}
}

// TestSaveHistoryAndRevert: each save anchors a SavePoint;
// RevertToLastSave is a pure history seek back to it, with redo intact.
func TestSaveHistoryAndRevert(t *testing.T) {
	lib, path := metaFixture(t, "base\n")
	g, err := lib.Open(FileOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c := g.NewCursor()

	if _, ok := g.LastSave(); ok {
		t.Fatal("save history should start empty")
	}
	if err := g.RevertToLastSave(); err != ErrRevisionNotFound {
		t.Fatalf("revert with no saves = %v, want ErrRevisionNotFound", err)
	}

	if _, err := c.InsertString("one ", nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Save(); err != nil {
		t.Fatal(err)
	}
	savedContent := contentOf(t, g, c)
	savedRev := g.CurrentRevision()

	sp, ok := g.LastSave()
	if !ok {
		t.Fatal("no save point recorded")
	}
	if sp.Path != path || sp.Revision != savedRev || !sp.AdoptedAsSource {
		t.Fatalf("save point = %+v", sp)
	}
	if !sp.Meta.Exists || sp.Meta.Size != int64(len(savedContent)) {
		t.Fatalf("save point meta = %+v, want size %d", sp.Meta, len(savedContent))
	}

	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString("two ", nil, false); err != nil {
		t.Fatal(err)
	}
	dirtyContent := contentOf(t, g, c)
	dirtyRev := g.CurrentRevision()

	if err := g.RevertToLastSave(); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != savedContent {
		t.Fatalf("after revert content = %q, want %q", got, savedContent)
	}

	// Nothing destroyed: the abandoned edit is still reachable as redo.
	if err := g.UndoSeek(dirtyRev); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != dirtyContent {
		t.Fatalf("after redo content = %q, want %q", got, dirtyContent)
	}
}

// TestSaveAsWithAdopt: adopting the destination re-homes the buffer
// onto it so completely that an immediate in-place Save writes NOTHING
// (every span skips), and the new path is the source for consistency
// tracking and future saves.
func TestSaveAsWithAdopt(t *testing.T) {
	lib, path := metaFixture(t, strings.Repeat("adopt me, five-lines-a-piece!\n", 200))
	rfs := &recordingFS{FileSystemInterface: NewLocalFileSystem()}
	g, err := lib.Open(FileOptions{FilePath: path, FileSystem: rfs, MaxLeafSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c := g.NewCursor()
	if _, err := c.InsertString("edited: ", nil, false); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(filepath.Dir(path), "adopted.txt")
	if _, err := g.SaveAsWith(rfs, dest, SaveAsOptions{
		AdoptAsSource:   true,
		PreserveHistory: true,
	}); err != nil {
		t.Fatal(err)
	}

	if g.SourcePath() != dest {
		t.Fatalf("source path = %q, want %q", g.SourcePath(), dest)
	}
	sp, ok := g.LastSave()
	if !ok || !sp.AdoptedAsSource || sp.Path != dest {
		t.Fatalf("save point = %+v ok=%v", sp, ok)
	}

	// Re-home proof: an immediate in-place save finds every span
	// already at its offset in the adopted file.
	before := rfs.writes
	if _, err := g.Save(); err != nil {
		t.Fatal(err)
	}
	if rfs.writes != before {
		t.Fatalf("in-place save after adoption wrote %d spans, want 0", rfs.writes-before)
	}

	// Consistency tracking follows the new source.
	if err := os.Chtimes(dest, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rep, err := g.SourceConsistency()
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != ConsistencyModified {
		t.Fatalf("state = %v, want modified (of adopted file)", rep.State)
	}
}

// TestSaveAsWithoutAdopt: a plain SaveAs (export / removable media)
// leaves the buffer working from its original source.
func TestSaveAsWithoutAdopt(t *testing.T) {
	lib, path := metaFixture(t, "stay home\n")
	g, err := lib.Open(FileOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	dest := filepath.Join(filepath.Dir(path), "export.txt")
	if _, err := g.SaveAs(nil, dest); err != nil {
		t.Fatal(err)
	}
	if g.SourcePath() != path {
		t.Fatalf("source path = %q, want original %q", g.SourcePath(), path)
	}
	sp, ok := g.LastSave()
	if !ok || sp.AdoptedAsSource || sp.Path != dest {
		t.Fatalf("save point = %+v ok=%v", sp, ok)
	}
}

// TestAdoptWarmSource: the swift metadata-level switch works when a
// save point proves the candidate holds the current buffer state, and
// full verification refuses a file that does not match the buffer.
func TestAdoptWarmSource(t *testing.T) {
	lib, path := metaFixture(t, "switchable content, reasonably long\n")
	g, err := lib.Open(FileOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Save a copy; its save point (current fork/revision + metadata)
	// is the evidence VerifyMetadata needs.
	twin := filepath.Join(filepath.Dir(path), "twin.txt")
	if _, err := g.SaveAs(nil, twin); err != nil {
		t.Fatal(err)
	}
	if err := g.AdoptWarmSource(nil, twin, VerifyMetadata); err != nil {
		t.Fatal(err)
	}
	if g.SourcePath() != twin {
		t.Fatalf("source path = %q, want %q", g.SourcePath(), twin)
	}

	// Reading still works after the switch.
	c := g.NewCursor()
	if got := contentOf(t, g, c); !strings.HasPrefix(got, "switchable") {
		t.Fatalf("content after adoption = %q", got)
	}

	// Now dirty the buffer; the ORIGINAL file no longer matches it, so
	// full verification must refuse to adopt it back.
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString("DIRTY ", nil, false); err != nil {
		t.Fatal(err)
	}
	if err := g.AdoptWarmSource(nil, path, VerifyFull); err != ErrWarmStorageMismatch {
		t.Fatalf("adopt of mismatched file = %v, want ErrWarmStorageMismatch", err)
	}
	// Metadata level must also refuse: no save point matches this
	// buffer state for that path.
	if err := g.AdoptWarmSource(nil, path, VerifyMetadata); err != ErrWarmStorageMismatch {
		t.Fatalf("metadata adopt of mismatched file = %v, want ErrWarmStorageMismatch", err)
	}
}

// TestTryRecoverSource: when the current source goes bad, recovery
// explores alternate known save locations and adopts one that
// verifies.
func TestTryRecoverSource(t *testing.T) {
	lib, path := metaFixture(t, "precious data that must survive\n")
	g, err := lib.Open(FileOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	c := g.NewCursor()
	if _, err := c.InsertString("v2: ", nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Save(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(filepath.Dir(path), "backup.txt")
	if _, err := g.SaveAs(nil, backup); err != nil {
		t.Fatal(err)
	}
	want := contentOf(t, g, c)

	// The current source becomes garbage.
	if err := os.WriteFile(path, []byte(strings.Repeat("X", len(want))), 0644); err != nil {
		t.Fatal(err)
	}

	sp, err := g.TryRecoverSource(VerifyFull)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Path != backup {
		t.Fatalf("recovered from %q, want %q", sp.Path, backup)
	}
	if g.SourcePath() != backup {
		t.Fatalf("source path = %q, want %q", g.SourcePath(), backup)
	}
	if got := contentOf(t, g, c); got != want {
		t.Fatalf("content after recovery = %q, want %q", got, want)
	}
}

// TestDeviceInfo: the local filesystem reports device identity and
// free space (used for "will this save fit" warnings).
func TestDeviceInfo(t *testing.T) {
	lib, path := metaFixture(t, "device info\n")
	info, err := lib.DeviceInfoFor(nil, path)
	if err == ErrNotSupported {
		t.Skip("device info not supported on this platform")
	}
	if err != nil {
		t.Fatal(err)
	}
	if info.TotalBytes <= 0 || info.FreeBytes < 0 {
		t.Fatalf("device info = %+v", info)
	}
	if info.DeviceID == "" {
		t.Fatal("empty device id")
	}

	g, err := lib.Open(FileOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	sinfo, err := g.SourceDeviceInfo()
	if err != nil {
		t.Fatal(err)
	}
	if sinfo.DeviceID != info.DeviceID {
		t.Fatalf("source device %q != path device %q", sinfo.DeviceID, info.DeviceID)
	}
}
