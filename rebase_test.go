package garland

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRebaseAdoptsResizedBlock: an external edit INSIDE a block that
// changes its length. Read-time triage must diagnose it as resized
// (pointing at Rebase) rather than a bare loss, and Rebase must then
// adopt the new content, keeping every other block.
func TestRebaseAdoptsResizedBlock(t *testing.T) {
	content := integrityDoc(4096)
	g, _, path := openSaveFixture(t, content)
	defer g.Close()
	if chillCurrentWarmEligible(t, g) == 0 {
		t.Fatal("expected warm leaves")
	}
	spans := integritySpans(g)
	if len(spans) < 3 {
		t.Skipf("need >=3 leaves, got %d", len(spans))
	}
	target := spans[1]
	off, n := target.snap.originalFileOffset, target.snap.byteCount

	// Replace the block's file region with LONGER foreign content.
	foreign := bytes.Repeat([]byte("<external insert>\n"), 60) // 1080 bytes
	var modified string
	mutateFile(t, path, func(d []byte) []byte {
		out := append([]byte(nil), d[:off]...)
		out = append(out, foreign...)
		out = append(out, d[off+n:]...)
		modified = string(out)
		return out
	})

	// Read-time: the resized block is diagnosed, not silently lost.
	c := g.NewCursor()
	if err := c.SeekByte(target.bufOff + 5); err == nil {
		if _, err := c.ReadBytes(10); err == nil {
			t.Fatal("read succeeded over externally resized block")
		}
	}
	kinds := countKinds(g.IntegrityEvents())
	if kinds[IntegrityBlockResized] != 1 {
		t.Fatalf("want 1 resized event, got %v", kinds)
	}
	for _, ev := range g.IntegrityEvents() {
		if ev.Kind == IntegrityBlockResized && !strings.Contains(ev.Detail, "Rebase") {
			t.Fatalf("resized detail does not point at Rebase: %q", ev.Detail)
		}
	}

	// Deliberate rebase adopts the resized content.
	report, err := g.RebaseOnSource()
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if report.NoChange {
		t.Fatal("rebase reported NoChange over a resized block")
	}
	if got := readBack(t, g); got != modified {
		t.Fatal("buffer != file after rebase")
	}
	if report.NewSize != int64(len(modified)) || report.OldSize != int64(len(content)) {
		t.Fatalf("sizes %d->%d, want %d->%d",
			report.OldSize, report.NewSize, len(content), len(modified))
	}
	if len(report.Adopted) == 0 || report.BytesAdopted == 0 {
		t.Fatal("rebase reported nothing adopted")
	}
	if report.BytesKept == 0 || report.BlocksKept == 0 {
		t.Fatal("rebase kept nothing - anchoring failed entirely")
	}
	// The adopted region must cover the foreign bytes.
	covered := false
	for _, r := range report.Adopted {
		if r.Offset <= off && off+int64(len(foreign)) <= r.Offset+r.Length {
			covered = true
		}
	}
	if !covered {
		t.Fatalf("adopted regions %+v do not cover the resized block [%d,%d)",
			report.Adopted, off, off+int64(len(foreign)))
	}
}

// TestRebaseNoChange: rebasing when the file already matches is a
// reported no-op - no new revision, content untouched.
func TestRebaseNoChange(t *testing.T) {
	content := integrityDoc(4096)
	g, _, _ := openSaveFixture(t, content)
	defer g.Close()
	if chillCurrentWarmEligible(t, g) == 0 {
		t.Fatal("expected warm leaves")
	}
	revBefore := g.CurrentRevision()

	report, err := g.RebaseOnSource()
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if !report.NoChange {
		t.Fatalf("want NoChange, got %+v", report)
	}
	if g.CurrentRevision() != revBefore {
		t.Fatal("no-change rebase recorded a mutation")
	}
	if got := readBack(t, g); got != content {
		t.Fatal("no-change rebase altered the buffer")
	}
}

// TestRebaseHealsPlaceholder: cold storage destroyed but the source
// file intact - the placeholder block's bytes are found right where
// they belong and the block heals without any adoption.
func TestRebaseHealsPlaceholder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	coldDir := filepath.Join(dir, "cold")
	content := integrityDoc(4096)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	lib, _ := Init(LibraryOptions{ColdStoragePath: coldDir})
	g, err := lib.Open(FileOptions{FilePath: path, MaxLeafSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	if err := g.Chill(ChillEverything); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(coldDir); err != nil {
		t.Fatal(err)
	}
	// Trip a placeholder.
	c := g.NewCursor()
	if err := c.SeekByte(0); err == nil {
		if _, err := c.ReadBytes(100); err == nil {
			t.Skip("cold data unexpectedly still readable")
		}
	}

	report, err := g.RebaseOnSource()
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if report.BlocksHealed == 0 {
		t.Fatal("rebase healed nothing")
	}
	if got := readBack(t, g); got != content {
		t.Fatal("buffer wrong after healing rebase")
	}
}

// TestRebaseDiscardsLocalEdits: rebase means the DISK wins - unsaved
// local edits are displaced by the file's content, but the pre-rebase
// buffer stays one UndoSeek away ("keep your version" after the fact).
func TestRebaseDiscardsLocalEdits(t *testing.T) {
	content := integrityDoc(4096)
	g, _, _ := openSaveFixture(t, content)
	defer g.Close()
	if chillCurrentWarmEligible(t, g) == 0 {
		t.Fatal("expected warm leaves")
	}

	c := g.NewCursor()
	if err := c.SeekByte(2000); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString("<LOCAL EDIT>", nil, false); err != nil {
		t.Fatal(err)
	}
	edited := readBack(t, g)
	if edited == content {
		t.Fatal("edit did not take")
	}

	report, err := g.RebaseOnSource()
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if report.NoChange {
		t.Fatal("rebase ignored the divergence")
	}
	if got := readBack(t, g); got != content {
		t.Fatal("rebase did not restore the file's content")
	}

	// Keep-your-version escape hatch.
	if err := g.UndoSeek(report.PreviousRevision); err != nil {
		t.Fatalf("UndoSeek(%d): %v", report.PreviousRevision, err)
	}
	if got := readBack(t, g); got != edited {
		t.Fatal("undo after rebase did not restore the local edit")
	}
}

// TestRebasePiecewiseShifts: TWO separate external inserts create
// piecewise offsets that single-delta slide triage cannot fully
// follow, but the rebase anchor tracker follows each shift and keeps
// every untouched block.
func TestRebasePiecewiseShifts(t *testing.T) {
	content := integrityDoc(4096)
	g, _, path := openSaveFixture(t, content)
	defer g.Close()
	if chillCurrentWarmEligible(t, g) == 0 {
		t.Fatal("expected warm leaves")
	}
	spans := integritySpans(g)
	if len(spans) < 4 {
		t.Skipf("need >=4 leaves, got %d", len(spans))
	}
	// Insert in the gaps BEFORE block 1 and BEFORE block 3 (at their
	// exact boundaries, so no block's own content is damaged).
	b1, b3 := spans[1].snap.originalFileOffset, spans[3].snap.originalFileOffset
	ins1, ins2 := []byte("[first insert]\n"), []byte("[second insert]\n")
	var modified string
	mutateFile(t, path, func(d []byte) []byte {
		out := append([]byte(nil), d[:b1]...)
		out = append(out, ins1...)
		out = append(out, d[b1:b3]...)
		out = append(out, ins2...)
		out = append(out, d[b3:]...)
		modified = string(out)
		return out
	})

	report, err := g.RebaseOnSource()
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if got := readBack(t, g); got != modified {
		t.Fatal("buffer != file after piecewise rebase")
	}
	// Every original block survives (the inserts landed between
	// blocks), so kept bytes == the whole original content.
	if report.BytesKept != int64(len(content)) {
		t.Fatalf("kept %d bytes, want all %d (piecewise tracking failed)",
			report.BytesKept, len(content))
	}
	if report.BytesAdopted != int64(len(ins1)+len(ins2)) {
		t.Fatalf("adopted %d bytes, want %d", report.BytesAdopted, len(ins1)+len(ins2))
	}
	if len(report.Adopted) != 2 {
		t.Fatalf("want 2 adopted regions, got %+v", report.Adopted)
	}
}

// TestRebaseOnForeignFile: rebasing onto a different file adopts its
// content (keeping blocks common with the buffer) and SWITCHES the
// source - subsequent saves go to the new file.
func TestRebaseOnForeignFile(t *testing.T) {
	content := integrityDoc(4096)
	g, _, path := openSaveFixture(t, content)
	defer g.Close()
	if chillCurrentWarmEligible(t, g) == 0 {
		t.Fatal("expected warm leaves")
	}

	// Foreign file: original content with a middle chunk replaced and
	// some extra appended.
	foreign := []byte(content)
	copy(foreign[1500:1600], bytes.Repeat([]byte("Q"), 100))
	foreign = append(foreign, []byte("appended tail\n")...)
	otherPath := filepath.Join(filepath.Dir(path), "other.txt")
	if err := os.WriteFile(otherPath, foreign, 0644); err != nil {
		t.Fatal(err)
	}

	report, err := g.RebaseOnFile(nil, otherPath)
	if err != nil {
		t.Fatalf("RebaseOn: %v", err)
	}
	if got := readBack(t, g); got != string(foreign) {
		t.Fatal("buffer != foreign file after RebaseOn")
	}
	if report.BytesKept == 0 {
		t.Fatal("RebaseOn kept nothing despite common blocks")
	}
	if g.sourcePath != otherPath {
		t.Fatalf("source not switched: %q", g.sourcePath)
	}

	// Saving now goes to the new source, in place.
	if _, err := g.Save(); err != nil {
		t.Fatalf("Save after RebaseOn: %v", err)
	}
	onDisk, err := os.ReadFile(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != string(foreign) {
		t.Fatal("save after RebaseOn wrote wrong content")
	}
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(orig) != content {
		t.Fatal("save after RebaseOn touched the ORIGINAL file")
	}
}
