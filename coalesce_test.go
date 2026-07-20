package garland

import (
	"testing"
	"time"
)

// coalesce_test.go - undo coalescing: runs of adjacent inserts,
// deletes, and overwrites each collapse into one revision (the three
// kinds never merge with each other); Bake(), AutoBakeTime, seeks,
// saves, and transactions are hard edges.

func coalesceFixture(t *testing.T, content string) (*Garland, *Cursor) {
	t.Helper()
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// DataBytes rather than DataString: an empty document is a valid
	// fixture here, and an empty DataString reads as "no source".
	g, err := lib.Open(FileOptions{DataBytes: []byte(content)})
	if err != nil {
		t.Fatal(err)
	}
	g.SetUndoCoalescing(true, 0)
	return g, g.NewCursor()
}

func typeString(t *testing.T, c *Cursor, pos int64, s string) ChangeResult {
	t.Helper()
	if err := c.SeekByte(pos); err != nil {
		t.Fatal(err)
	}
	res, err := c.InsertString(s, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// overwriteAt overwrites `length` bytes at pos with s (replace-mode edit).
func overwriteAt(t *testing.T, c *Cursor, pos, length int64, s string) ChangeResult {
	t.Helper()
	if err := c.SeekByte(pos); err != nil {
		t.Fatal(err)
	}
	_, res, err := c.OverwriteBytes(length, []byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestCoalesceTypingRun: keystrokes at an advancing caret collapse to
// ONE revision; undo removes the whole run, redo restores it whole.
func TestCoalesceTypingRun(t *testing.T) {
	g, c := coalesceFixture(t, "base\n")

	r1 := typeString(t, c, 5, "h")
	r2 := typeString(t, c, 6, "e") // continues at the run's end
	r3 := typeString(t, c, 7, "y")
	if r1.Revision != 1 || r2.Revision != 1 || r3.Revision != 1 {
		t.Fatalf("revisions = %d,%d,%d, want all 1", r1.Revision, r2.Revision, r3.Revision)
	}
	if g.CurrentRevision() != 1 {
		t.Fatalf("current revision = %d, want 1", g.CurrentRevision())
	}
	if got := contentOf(t, g, c); got != "base\nhey" {
		t.Fatalf("content = %q", got)
	}

	if err := g.UndoSeek(0); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != "base\n" {
		t.Fatalf("after undo content = %q, want run fully removed", got)
	}
	if err := g.UndoSeek(1); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != "base\nhey" {
		t.Fatalf("after redo content = %q, want run fully restored", got)
	}
}

// TestCoalescePrepend: inserting at the BEGINNING of the run's chunk
// coalesces too (per the ruling: beginning or end, not interior).
func TestCoalescePrepend(t *testing.T) {
	g, c := coalesceFixture(t, "xyz")

	typeString(t, c, 0, "bc")
	res := typeString(t, c, 0, "a") // prepend at the chunk start
	if res.Revision != 1 || g.CurrentRevision() != 1 {
		t.Fatalf("prepend minted revision %d, want coalesced into 1", res.Revision)
	}
	if got := contentOf(t, g, c); got != "abcxyz" {
		t.Fatalf("content = %q", got)
	}

	// Interior insertion is navigation - it bakes.
	res = typeString(t, c, 2, "Q") // inside [0,3)
	if res.Revision != 2 {
		t.Fatalf("interior insert revision = %d, want new revision 2", res.Revision)
	}
}

// TestCoalesceNonAdjacentBakes: an insert away from the run's chunk
// starts a new revision (and a new run there).
func TestCoalesceNonAdjacentBakes(t *testing.T) {
	g, c := coalesceFixture(t, "0123456789")

	typeString(t, c, 0, "a")
	res := typeString(t, c, 7, "b") // far away
	if res.Revision != 2 {
		t.Fatalf("non-adjacent insert revision = %d, want 2", res.Revision)
	}
	// ...and the new location is itself a live run.
	res = typeString(t, c, 8, "c")
	if res.Revision != 2 {
		t.Fatalf("continuation at new location = %d, want coalesced into 2", res.Revision)
	}
	if g.CurrentRevision() != 2 {
		t.Fatalf("current revision = %d, want 2", g.CurrentRevision())
	}
}

// TestCoalesceForwardDeleteRun: repeated Delete at one caret is one
// revision; undo restores everything.
func TestCoalesceForwardDeleteRun(t *testing.T) {
	g, c := coalesceFixture(t, "abcdef")

	for i := 0; i < 3; i++ {
		if err := c.SeekByte(1); err != nil {
			t.Fatal(err)
		}
		if _, _, err := c.DeleteBytes(1, false); err != nil {
			t.Fatal(err)
		}
	}
	if g.CurrentRevision() != 1 {
		t.Fatalf("current revision = %d, want 1", g.CurrentRevision())
	}
	if got := contentOf(t, g, c); got != "aef" {
		t.Fatalf("content = %q, want aef", got)
	}
	if err := g.UndoSeek(0); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != "abcdef" {
		t.Fatalf("undo content = %q", got)
	}
}

// TestCoalesceBackspaceRun: backspacing walks the caret left; each
// delete ends exactly where the previous one began - one revision.
func TestCoalesceBackspaceRun(t *testing.T) {
	g, c := coalesceFixture(t, "abcdef")

	for pos := int64(5); pos >= 3; pos-- {
		if err := c.SeekByte(pos); err != nil {
			t.Fatal(err)
		}
		if _, _, err := c.DeleteBytes(1, false); err != nil {
			t.Fatal(err)
		}
	}
	if g.CurrentRevision() != 1 {
		t.Fatalf("current revision = %d, want 1", g.CurrentRevision())
	}
	if got := contentOf(t, g, c); got != "abc" {
		t.Fatalf("content = %q, want abc", got)
	}
}

// TestCoalesceKindSwitchBakes: switching between inserting and
// deleting is a hard edge even at an adjacent position.
func TestCoalesceKindSwitchBakes(t *testing.T) {
	g, c := coalesceFixture(t, "base")

	typeString(t, c, 4, "xy") // rev 1, run [4,6)
	if err := c.SeekByte(5); err != nil {
		t.Fatal(err)
	}
	if _, res, err := c.DeleteBytes(1, false); err != nil {
		t.Fatal(err)
	} else if res.Revision != 2 {
		t.Fatalf("delete after insert run: revision = %d, want 2", res.Revision)
	}
	if g.CurrentRevision() != 2 {
		t.Fatalf("current revision = %d, want 2", g.CurrentRevision())
	}
}

// TestCoalesceOverwriteRun: replace-mode typing (overwrite one char,
// caret advances, overwrite the next) collapses to ONE revision; undo
// removes the whole run and restores the original bytes.
func TestCoalesceOverwriteRun(t *testing.T) {
	g, c := coalesceFixture(t, "0123456789")

	// Overwrite "abc" over "012", one char at a time, caret advancing.
	r0 := overwriteAt(t, c, 0, 1, "a")
	r1 := overwriteAt(t, c, 1, 1, "b") // continues at the run's end
	r2 := overwriteAt(t, c, 2, 1, "c")
	if r0.Revision != 1 || r1.Revision != 1 || r2.Revision != 1 {
		t.Fatalf("revisions = %d,%d,%d, want all 1", r0.Revision, r1.Revision, r2.Revision)
	}
	if g.CurrentRevision() != 1 {
		t.Fatalf("current revision = %d, want 1", g.CurrentRevision())
	}
	if got := contentOf(t, g, c); got != "abc3456789" {
		t.Fatalf("content = %q", got)
	}

	if err := g.UndoSeek(0); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != "0123456789" {
		t.Fatalf("after undo content = %q, want the run fully removed", got)
	}
	if err := g.UndoSeek(1); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != "abc3456789" {
		t.Fatalf("after redo content = %q, want the run fully restored", got)
	}
}

// TestCoalesceOverwriteGrows: overwrites whose replacement is longer
// than what they replace still coalesce, the run's written span growing
// by the written length each time.
func TestCoalesceOverwriteGrows(t *testing.T) {
	g, c := coalesceFixture(t, "xyz")

	overwriteAt(t, c, 0, 1, "AA")       // [0,1)->"AA": span [0,2), content "AAyz"
	res := overwriteAt(t, c, 2, 1, "B") // caret at 2 == run end; "AAByz"... wait span
	if res.Revision != 1 || g.CurrentRevision() != 1 {
		t.Fatalf("second overwrite revision = %d, want coalesced into 1", res.Revision)
	}
	if got := contentOf(t, g, c); got != "AABz" {
		t.Fatalf("content = %q, want AABz", got)
	}
}

// TestCoalesceOverwriteNonAdjacentBakes: an overwrite away from the run
// starts a new revision (and a fresh run there).
func TestCoalesceOverwriteNonAdjacentBakes(t *testing.T) {
	g, c := coalesceFixture(t, "0123456789")

	overwriteAt(t, c, 0, 1, "a")
	res := overwriteAt(t, c, 7, 1, "b") // far from the run
	if res.Revision != 2 {
		t.Fatalf("non-adjacent overwrite revision = %d, want 2", res.Revision)
	}
	res = overwriteAt(t, c, 8, 1, "c") // continues the new run
	if res.Revision != 2 || g.CurrentRevision() != 2 {
		t.Fatalf("continuation revision = %d, want coalesced into 2", res.Revision)
	}
}

// TestCoalesceOverwriteInteriorBakes: re-overwriting inside the run
// (navigating back into what you wrote) bakes, mirroring insert's
// interior rule.
func TestCoalesceOverwriteInteriorBakes(t *testing.T) {
	g, c := coalesceFixture(t, "0123456789")

	overwriteAt(t, c, 0, 1, "a")
	overwriteAt(t, c, 1, 1, "b")        // run [0,2)
	res := overwriteAt(t, c, 0, 1, "Z") // back at the start - interior
	if res.Revision != 2 {
		t.Fatalf("interior overwrite revision = %d, want new revision 2", res.Revision)
	}
	if g.CurrentRevision() != 2 {
		t.Fatalf("current revision = %d, want 2", g.CurrentRevision())
	}
}

// TestCoalesceOverwriteKindSeparate: overwrite runs never merge with
// insert or delete runs, even at an adjacent position - each kind is
// its own run.
func TestCoalesceOverwriteKindSeparate(t *testing.T) {
	g, c := coalesceFixture(t, "0123456789")

	// insert then adjacent overwrite: different kinds, so the overwrite
	// starts a fresh revision.
	typeString(t, c, 0, "AB") // rev 1, insert run [0,2)
	res := overwriteAt(t, c, 2, 1, "C")
	if res.Revision != 2 {
		t.Fatalf("overwrite after insert run: revision = %d, want 2", res.Revision)
	}
	// overwrite then adjacent delete: different kinds again.
	if err := c.SeekByte(3); err != nil {
		t.Fatal(err)
	}
	if _, dres, err := c.DeleteBytes(1, false); err != nil {
		t.Fatal(err)
	} else if dres.Revision != 3 {
		t.Fatalf("delete after overwrite run: revision = %d, want 3", dres.Revision)
	}
	if g.CurrentRevision() != 3 {
		t.Fatalf("current revision = %d, want 3", g.CurrentRevision())
	}
}

// TestCoalesceOverwriteSeekBakes: history navigation ends an overwrite
// run, like the other kinds.
func TestCoalesceOverwriteSeekBakes(t *testing.T) {
	g, c := coalesceFixture(t, "0123456789")

	overwriteAt(t, c, 0, 1, "a") // rev 1, run [0,1)
	if err := g.UndoSeek(0); err != nil {
		t.Fatal(err)
	}
	if err := g.UndoSeek(1); err != nil {
		t.Fatal(err)
	}
	res := overwriteAt(t, c, 1, 1, "b") // adjacent to the old run
	if res.Revision != 2 {
		t.Fatalf("post-seek overwrite revision = %d, want 2 (run baked by seek)", res.Revision)
	}
}

// TestCoalesceOvertypeAppend: "overtype mode" - a run of overwrites
// within a line, then inserts appended at the end when the line runs
// out - stays ONE revision. The overwrite->insert switch coalesces
// (one-directional), so the whole overtype gesture is a single undo.
func TestCoalesceOvertypeAppend(t *testing.T) {
	g, c := coalesceFixture(t, "0123456789")

	// Overtype "abc" over "012".
	overwriteAt(t, c, 0, 1, "a")
	overwriteAt(t, c, 1, 1, "b")
	overwriteAt(t, c, 2, 1, "c") // overwrite run [0,3), "abc3456789"

	// Reached the switch point: now APPEND via inserts at the run's end.
	r1 := typeString(t, c, 3, "X") // insert at pos == run end
	r2 := typeString(t, c, 4, "Y") // continues as an insert run
	if r1.Revision != 1 || r2.Revision != 1 {
		t.Fatalf("append inserts minted revisions %d,%d, want coalesced into 1", r1.Revision, r2.Revision)
	}
	if g.CurrentRevision() != 1 {
		t.Fatalf("current revision = %d, want 1", g.CurrentRevision())
	}
	if got := contentOf(t, g, c); got != "abcXY3456789" {
		t.Fatalf("content = %q", got)
	}

	// One undo removes the entire overtype gesture.
	if err := g.UndoSeek(0); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != "0123456789" {
		t.Fatalf("after undo content = %q, want the whole gesture removed", got)
	}
}

// TestCoalesceOvertypeSwitchIsOneWay: after the overwrite->insert switch
// the run is an insert run, so a subsequent overwrite bakes (the reverse
// transition never coalesces).
func TestCoalesceOvertypeSwitchIsOneWay(t *testing.T) {
	g, c := coalesceFixture(t, "0123456789")

	overwriteAt(t, c, 0, 1, "a")        // overwrite run [0,1)
	typeString(t, c, 1, "X")            // insert continues it -> insert run [0,2)
	res := overwriteAt(t, c, 2, 1, "b") // overwrite after insert run: bakes
	if res.Revision != 2 {
		t.Fatalf("overwrite after the switch: revision = %d, want 2 (one-way)", res.Revision)
	}
	if g.CurrentRevision() != 2 {
		t.Fatalf("current revision = %d, want 2", g.CurrentRevision())
	}
}

// TestCoalesceInsertBeforeOverwriteBakes: the switch is only at the END
// of the overwrite run. An insert at the START (prepend) is not the
// overtype-append gesture, so it bakes.
func TestCoalesceInsertBeforeOverwriteBakes(t *testing.T) {
	g, c := coalesceFixture(t, "0123456789")

	overwriteAt(t, c, 1, 1, "a")
	overwriteAt(t, c, 2, 1, "b")    // overwrite run [1,3)
	res := typeString(t, c, 1, "Z") // insert at the run START
	if res.Revision != 2 {
		t.Fatalf("insert before overwrite run: revision = %d, want 2 (only end appends)", res.Revision)
	}
	_ = g
}

// TestBakeHardEdge: Bake() ends the run; the next perfectly adjacent
// keystroke starts a fresh revision.
func TestBakeHardEdge(t *testing.T) {
	g, c := coalesceFixture(t, "")

	typeString(t, c, 0, "a")
	g.Bake()
	res := typeString(t, c, 1, "b")
	if res.Revision != 2 {
		t.Fatalf("post-bake insert revision = %d, want 2", res.Revision)
	}
	// Each run undoes independently.
	if err := g.UndoSeek(1); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != "a" {
		t.Fatalf("undo to rev1 content = %q, want a", got)
	}
}

// TestAutoBakeTime: a pause longer than the window bakes the run
// automatically; edits inside the window keep coalescing.
func TestAutoBakeTime(t *testing.T) {
	g, c := coalesceFixture(t, "")
	g.SetUndoCoalescing(true, 25*time.Millisecond)

	typeString(t, c, 0, "a")
	res := typeString(t, c, 1, "b") // immediate: coalesces
	if res.Revision != 1 {
		t.Fatalf("fast follow-up revision = %d, want 1", res.Revision)
	}

	time.Sleep(80 * time.Millisecond)
	res = typeString(t, c, 2, "c") // stale: auto-baked
	if res.Revision != 2 {
		t.Fatalf("post-pause insert revision = %d, want 2", res.Revision)
	}
}

// TestCoalesceSeekBakes: undo/redo navigation ends the run - typing
// after inspecting history never rewrites the inspected revision.
func TestCoalesceSeekBakes(t *testing.T) {
	g, c := coalesceFixture(t, "")

	typeString(t, c, 0, "ab") // rev 1
	if err := g.UndoSeek(0); err != nil {
		t.Fatal(err)
	}
	if err := g.UndoSeek(1); err != nil {
		t.Fatal(err)
	}
	res := typeString(t, c, 2, "c") // adjacent to the old run
	if res.Revision != 2 {
		t.Fatalf("post-seek insert revision = %d, want 2 (run baked by seek)", res.Revision)
	}
	if err := g.UndoSeek(1); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != "ab" {
		t.Fatalf("rev1 content = %q, want ab intact", got)
	}
}

// TestCoalesceSaveBakes: a save pins its revision; later keystrokes
// must not amend it, and RevertToLastSave lands on exactly what was
// written.
func TestCoalesceSaveBakes(t *testing.T) {
	lib, path := metaFixture(t, "doc: ")
	g, err := lib.Open(FileOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	g.SetUndoCoalescing(true, 0)
	c := g.NewCursor()

	typeString(t, c, 5, "sa")
	typeString(t, c, 7, "ved") // same run
	savedRev := g.CurrentRevision()
	if _, err := g.Save(); err != nil {
		t.Fatal(err)
	}
	savedContent := contentOf(t, g, c)

	res := typeString(t, c, 10, "X") // adjacent to the pre-save run
	if res.Revision != savedRev+1 {
		t.Fatalf("post-save insert revision = %d, want %d (save is a hard edge)",
			res.Revision, savedRev+1)
	}

	if err := g.RevertToLastSave(); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != savedContent {
		t.Fatalf("revert content = %q, want %q", got, savedContent)
	}
}

// TestCoalesceInsideTransaction: a transaction is its own grouping -
// coalescing runs dissolve into it without errors, and a fresh run
// works after it commits.
func TestCoalesceInsideTransaction(t *testing.T) {
	g, c := coalesceFixture(t, "")

	typeString(t, c, 0, "a") // rev 1 (open run)
	if err := g.TransactionStart("group"); err != nil {
		t.Fatal(err)
	}
	typeString(t, c, 1, "b")
	typeString(t, c, 2, "c")
	if _, err := g.TransactionCommit(); err != nil {
		t.Fatal(err)
	}
	txRev := g.CurrentRevision()
	if txRev != 2 {
		t.Fatalf("transaction revision = %d, want 2", txRev)
	}

	// Fresh run after the transaction.
	typeString(t, c, 3, "d")
	res := typeString(t, c, 4, "e")
	if res.Revision != 3 || g.CurrentRevision() != 3 {
		t.Fatalf("post-transaction run revision = %d, want 3", res.Revision)
	}
	if got := contentOf(t, g, c); got != "abcde" {
		t.Fatalf("content = %q", got)
	}
	// Undo layers: run / transaction / run.
	if err := g.UndoSeek(2); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != "abc" {
		t.Fatalf("rev2 content = %q, want abc", got)
	}
	if err := g.UndoSeek(1); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(t, g, c); got != "a" {
		t.Fatalf("rev1 content = %q, want a", got)
	}
}

// TestCoalesceDisabledByDefault: without opting in, adjacent inserts
// stay separate revisions (existing behavior preserved).
func TestCoalesceDisabledByDefault(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataBytes: []byte{}})
	if err != nil {
		t.Fatal(err)
	}
	c := g.NewCursor()

	typeString(t, c, 0, "a")
	res := typeString(t, c, 1, "b")
	if res.Revision != 2 {
		t.Fatalf("adjacent inserts without coalescing: revision = %d, want 2", res.Revision)
	}
}

// TestCoalesceCursorRestore: undoing past a coalesced run restores the
// pre-run cursor position; undoing onto it restores the end-of-run
// position (recorded when the run baked).
func TestCoalesceCursorRestore(t *testing.T) {
	g, c := coalesceFixture(t, "0123")

	if err := c.SeekByte(2); err != nil {
		t.Fatal(err)
	}
	typeString(t, c, 2, "a")
	typeString(t, c, 3, "b") // run [2,4), cursor at 4
	g.Bake()
	typeString(t, c, 4, "Z") // rev 2 - baking rev1 recorded its end positions

	if err := g.UndoSeek(1); err != nil {
		t.Fatal(err)
	}
	if got := c.posByte(); got != 4 {
		t.Fatalf("cursor at rev1 = %d, want end-of-run 4", got)
	}
	if err := g.UndoSeek(0); err != nil {
		t.Fatal(err)
	}
	if got := c.posByte(); got != 2 {
		t.Fatalf("cursor at rev0 = %d, want pre-run 2", got)
	}
}
