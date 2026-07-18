package garland

import (
	"testing"
	"time"
)

// coalesce_test.go - undo coalescing: runs of adjacent inserts/deletes
// collapse into one revision; Bake(), AutoBakeTime, seeks, saves, and
// transactions are hard edges.

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
