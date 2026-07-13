package garland

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

// integrity_test.go - external-modification forensics: slide, swap,
// soft-adopt (plain and duplicate-flagged), and hard loss.

// integrityDoc builds content where every region is distinct (unlike
// saveDoc's repeated line), so hashes discriminate between blocks.
func integrityDoc(size int) string {
	var b []byte
	for i := 0; len(b) < size; i++ {
		b = append(b, []byte(fmt.Sprintf("line %08d abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJK\n", i))...)
	}
	return string(b[:size])
}

// integritySpans snapshots the current leaf layout (buffer offset,
// file offset, length) for tests to aim external edits at.
func integritySpans(g *Garland) []leafSpan {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.currentLeafSpans()
}

// mutateFile rewrites the file at path through f. os.WriteFile
// truncates in place (same inode), so the buffer's open source handle
// keeps working - exactly like an external editor.
func mutateFile(t *testing.T, path string, f func([]byte) []byte) {
	t.Helper()
	d, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, f(d), 0644); err != nil {
		t.Fatal(err)
	}
}

func countKinds(evs []IntegrityEvent) map[IntegrityKind]int {
	m := map[IntegrityKind]int{}
	for _, ev := range evs {
		m[ev.Kind]++
	}
	return m
}

// TestIntegritySlideRecovers: an external insert BEFORE our warm
// blocks slides them all. The shift (+7) is deliberately not a
// multiple of the block size - slide detection is byte-granular, the
// block layout never enters into it. Every block must be found intact
// at its shifted offset and re-homed; nothing is lost and the buffer
// still reads as the original content.
func TestIntegritySlideRecovers(t *testing.T) {
	content := integrityDoc(4096)
	g, _, path := openSaveFixture(t, content)
	defer g.Close()
	if chillCurrentWarmEligible(t, g) == 0 {
		t.Fatal("expected warm leaves")
	}

	mutateFile(t, path, func(d []byte) []byte {
		return append([]byte("EXTERN\n"), d...) // +7 bytes at the front
	})

	if got := readBack(t, g); got != content {
		t.Fatalf("buffer changed after external slide: %d bytes vs %d", len(got), len(content))
	}
	kinds := countKinds(g.IntegrityEvents())
	if kinds[IntegrityBlockSlid] == 0 {
		t.Fatal("no slide events recorded")
	}
	if kinds[IntegrityBlockLost] != 0 || kinds[IntegrityBlockAdopted] != 0 {
		t.Fatalf("slide misclassified: %v", kinds)
	}
}

// TestIntegrityAdoptExternalEdit: a same-size external edit confined
// to one block, with intact neighbors, is adopted - the buffer takes
// the file's bytes (with correct rune/line aggregates), the event says
// so, and a subsequent save does NOT destroy the external edit.
func TestIntegrityAdoptExternalEdit(t *testing.T) {
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

	// Foreign content with a DIFFERENT line structure (no newlines),
	// so the adoption must recompute rune/line aggregates.
	foreign := bytes.Repeat([]byte("z"), int(n))
	var modified string
	mutateFile(t, path, func(d []byte) []byte {
		copy(d[off:off+n], foreign)
		modified = string(d)
		return d
	})

	if got := readBack(t, g); got != modified {
		t.Fatal("buffer did not adopt the external edit")
	}
	kinds := countKinds(g.IntegrityEvents())
	if kinds[IntegrityBlockAdopted] != 1 {
		t.Fatalf("want exactly 1 adopted event, got %v", kinds)
	}
	if kinds[IntegrityBlockLost] != 0 {
		t.Fatalf("adoption misclassified as loss: %v", kinds)
	}
	// Aggregates must match a fresh buffer holding the same content.
	fresh, err := g.lib.Open(FileOptions{DataBytes: []byte(modified)})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if g.LineCount().Value != fresh.LineCount().Value {
		t.Fatalf("line count %d after adopt, want %d", g.LineCount().Value, fresh.LineCount().Value)
	}
	if g.RuneCount().Value != fresh.RuneCount().Value {
		t.Fatalf("rune count %d after adopt, want %d", g.RuneCount().Value, fresh.RuneCount().Value)
	}

	// Saving must preserve the adopted (external) bytes and report the
	// event; the pending log drains into the report.
	report, err := g.Save()
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(report.Scars) != 0 {
		t.Fatalf("clean adopt produced scars: %+v", report.Scars)
	}
	found := false
	for _, ev := range report.Integrity {
		if ev.Kind == IntegrityBlockAdopted {
			found = true
		}
	}
	if !found {
		t.Fatal("SaveReport.Integrity missing the adoption event")
	}
	if len(g.IntegrityEvents()) != 0 {
		t.Fatal("integrity log not drained by save")
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != modified {
		t.Fatal("save destroyed the external edit")
	}
}

// TestIntegritySwapRecovers: two blocks' file regions exchanged by an
// external program. Triage finds our bytes at the other block's
// position (and its bytes at ours) - proof of a move - and recovers
// BOTH by exchanging backing offsets. Buffer content is untouched, and
// a save (exercising the out-of-order warm rescue) restores the file.
func TestIntegritySwapRecovers(t *testing.T) {
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
	a, b := spans[1], spans[2]
	if a.snap.byteCount != b.snap.byteCount {
		t.Skipf("unequal leaf sizes %d/%d", a.snap.byteCount, b.snap.byteCount)
	}
	aOff, bOff, n := a.snap.originalFileOffset, b.snap.originalFileOffset, a.snap.byteCount

	mutateFile(t, path, func(d []byte) []byte {
		tmp := append([]byte(nil), d[aOff:aOff+n]...)
		copy(d[aOff:aOff+n], d[bOff:bOff+n])
		copy(d[bOff:bOff+n], tmp)
		return d
	})

	if got := readBack(t, g); got != content {
		t.Fatal("buffer changed after external swap")
	}
	kinds := countKinds(g.IntegrityEvents())
	if kinds[IntegrityBlockSwapped] != 1 {
		t.Fatalf("want exactly 1 swap event, got %v", kinds)
	}
	if kinds[IntegrityBlockLost] != 0 || kinds[IntegrityBlockAdopted] != 0 {
		t.Fatalf("swap misclassified: %v", kinds)
	}

	// The swapped blocks now have out-of-order backing offsets; the
	// save engine must rescue them and write the buffer faithfully.
	if _, err := g.Save(); err != nil {
		t.Fatalf("Save after swap: %v", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != content {
		t.Fatal("file wrong after saving a swapped buffer")
	}
}

// TestIntegrityAdoptDuplicateFlagged: an external COPY of one block's
// region over another's. The adopted content exactly duplicates a
// preserved block, so the event is flagged as a suspected move/copy
// (the duplicate's own region still verifies, so it is not a swap).
func TestIntegrityAdoptDuplicateFlagged(t *testing.T) {
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
	src, dst := spans[1], spans[3]
	if src.snap.byteCount != dst.snap.byteCount {
		t.Skipf("unequal leaf sizes")
	}
	sOff, dOff, n := src.snap.originalFileOffset, dst.snap.originalFileOffset, src.snap.byteCount

	var modified string
	mutateFile(t, path, func(d []byte) []byte {
		copy(d[dOff:dOff+n], d[sOff:sOff+n])
		modified = string(d)
		return d
	})

	if got := readBack(t, g); got != modified {
		t.Fatal("buffer did not adopt the copied content")
	}
	kinds := countKinds(g.IntegrityEvents())
	if kinds[IntegrityBlockAdoptedDuplicate] != 1 {
		t.Fatalf("want exactly 1 adopted-duplicate event, got %v", kinds)
	}
	if kinds[IntegrityBlockSwapped] != 0 || kinds[IntegrityBlockLost] != 0 {
		t.Fatalf("duplicate copy misclassified: %v", kinds)
	}
	for _, ev := range g.IntegrityEvents() {
		if ev.Kind == IntegrityBlockAdoptedDuplicate &&
			!strings.Contains(ev.Detail, "duplicates") {
			t.Fatalf("duplicate event detail missing the duplicate hint: %q", ev.Detail)
		}
	}
}

// TestIntegrityHardLossStillScars: garbage across two adjacent blocks
// defeats every recovery (no slide, no duplicate, failing neighbors).
// Both become placeholders with Lost events, and the next save scars
// them with the surrounding-verification reason.
func TestIntegrityHardLossStillScars(t *testing.T) {
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
	x, y := spans[1], spans[2]
	xOff, xN := x.snap.originalFileOffset, x.snap.byteCount
	yOff, yN := y.snap.originalFileOffset, y.snap.byteCount

	mutateFile(t, path, func(d []byte) []byte {
		for i := xOff; i < xOff+xN; i++ {
			d[i] = byte(31 + (i*7)%89) // deterministic garbage
		}
		for i := yOff; i < yOff+yN; i++ {
			d[i] = byte(31 + (i*11)%89)
		}
		return d
	})

	// Touch both corrupted regions: reads must fail.
	c := g.NewCursor()
	for _, off := range []int64{x.bufOff + 5, y.bufOff + 5} {
		if err := c.SeekByte(off); err != nil {
			continue // seek itself may surface the loss
		}
		if _, err := c.ReadBytes(10); err == nil {
			t.Fatalf("read at %d succeeded over hard-corrupted block", off)
		}
	}
	kinds := countKinds(g.IntegrityEvents())
	if kinds[IntegrityBlockLost] != 2 {
		t.Fatalf("want 2 lost events, got %v", kinds)
	}
	if kinds[IntegrityBlockAdopted] != 0 || kinds[IntegrityBlockSlid] != 0 {
		t.Fatalf("hard corruption misclassified: %v", kinds)
	}

	report, err := g.Save()
	if err != nil {
		t.Fatalf("Save refused on hard loss: %v", err)
	}
	if len(report.Scars) != 2 {
		t.Fatalf("want 2 scars, got %d", len(report.Scars))
	}
	for i, s := range report.Scars {
		if !strings.Contains(s.Reason, "surrounding") {
			t.Errorf("scar %d reason %q missing surrounding-verification cause", i, s.Reason)
		}
	}
	lost := 0
	for _, ev := range report.Integrity {
		if ev.Kind == IntegrityBlockLost {
			lost++
		}
	}
	if lost != 2 {
		t.Fatalf("SaveReport.Integrity carries %d lost events, want 2", lost)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(onDisk)) != int64(len(content)) {
		t.Fatalf("file size %d after scarred save, want %d", len(onDisk), len(content))
	}
}
