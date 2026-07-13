package garland

import "testing"

// History destruction contract: Prune and DeleteFork remove revisions
// from ONE fork's view, but shared history survives while any other
// live fork can still reach it - and dies together (revisionInfo,
// cursor records, and node snapshots stay consistent) once nothing can.

func openWithRevisions(t *testing.T, edits []string) (*Garland, *Cursor) {
	t.Helper()
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g, err := lib.Open(FileOptions{DataString: "rev0 content\n"})
	if err != nil {
		t.Fatal(err)
	}
	c := g.NewCursor()
	for _, e := range edits {
		if err := c.SeekByte(g.ByteCount().Value); err != nil {
			t.Fatal(err)
		}
		if _, err := c.InsertString(e, nil, false); err != nil {
			t.Fatal(err)
		}
	}
	return g, c
}

func contentOf(t *testing.T, g *Garland, c *Cursor) string {
	t.Helper()
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	data, err := c.ReadBytes(g.ByteCount().Value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestPrunePreservesDescendantHistory: pruning a parent fork must not
// destroy revisions a child fork (branched below the watermark) still
// reaches through the parent's lineage.
func TestPrunePreservesDescendantHistory(t *testing.T) {
	g, c := openWithRevisions(t, []string{"one\n", "two\n", "three\n"}) // revs 1..3

	// Branch fork 1 at revision 2.
	if err := g.UndoSeek(2); err != nil {
		t.Fatal(err)
	}
	if err := c.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertString("FORK\n", nil, false); err != nil {
		t.Fatal(err)
	}
	if g.CurrentFork() == 0 {
		t.Fatal("expected a fork branch")
	}
	child := g.CurrentFork()
	wantRev2 := "rev0 content\none\ntwo\n"

	// Back on fork 0, prune everything below its head.
	if err := g.ForkSeek(0); err != nil {
		t.Fatal(err)
	}
	if err := g.UndoSeek(3); err != nil {
		t.Fatal(err)
	}
	if err := g.Prune(3); err != nil {
		t.Fatal(err)
	}

	// Fork 0's own view below the watermark is gone...
	if err := g.UndoSeek(2); err == nil {
		t.Error("UndoSeek(2) on pruned fork 0 should fail")
	}

	// ...but the child still reaches its inherited revision 2.
	if err := g.ForkSeek(child); err != nil {
		t.Fatalf("ForkSeek(child): %v", err)
	}
	if err := g.UndoSeek(2); err != nil {
		t.Fatalf("child UndoSeek(2) after parent prune: %v", err)
	}
	if got := contentOf(t, g, c); got != wantRev2 {
		t.Errorf("child rev 2 content = %q, want %q", got, wantRev2)
	}
}

// TestDeleteForkChainKeepsGrandchild: deleting a middle fork and then
// its parent must leave a live grandchild's inherited revisions intact
// (the need computation is transitive, passing through deleted forks).
func TestDeleteForkChainKeepsGrandchild(t *testing.T) {
	g, c := openWithRevisions(t, []string{"one\n", "two\n", "three\n"}) // fork 0, revs 1..3

	branch := func(atRev RevisionID, text string) ForkID {
		t.Helper()
		if err := g.UndoSeek(atRev); err != nil {
			t.Fatal(err)
		}
		if err := c.SeekByte(0); err != nil {
			t.Fatal(err)
		}
		if _, err := c.InsertString(text, nil, false); err != nil {
			t.Fatal(err)
		}
		return g.CurrentFork()
	}

	middle := branch(2, "MID\n") // middle branches from fork 0 at rev 2
	grand := branch(2, "GRAND\n")
	if middle == 0 || grand == middle {
		t.Fatalf("unexpected fork ids: middle=%d grand=%d", middle, grand)
	}
	// grand branched from middle at rev 2 (middle's inherited region),
	// so its lineage passes through middle into fork 0.

	// Delete middle first, then verify the grandchild still works.
	if err := g.DeleteFork(middle); err != nil {
		t.Fatal(err)
	}
	if err := g.UndoSeek(1); err != nil {
		t.Fatalf("grandchild UndoSeek(1) after deleting middle: %v", err)
	}
	if got := contentOf(t, g, c); got != "rev0 content\none\n" {
		t.Errorf("grandchild rev 1 content = %q", got)
	}
	if err := g.UndoSeek(3); err != nil {
		t.Fatalf("grandchild redo: %v", err)
	}
	if got := contentOf(t, g, c); got != "GRAND\nrev0 content\none\ntwo\n" {
		t.Errorf("grandchild rev 3 content = %q", got)
	}

	// Now delete the root fork too. The grandchild's need for fork 0's
	// revisions 0..2 is computed by walking THROUGH the deleted middle
	// fork - the truly transitive case.
	if err := g.DeleteFork(0); err != nil {
		t.Fatal(err)
	}
	if err := g.UndoSeek(2); err != nil {
		t.Fatalf("grandchild UndoSeek(2) after deleting root fork: %v", err)
	}
	if got := contentOf(t, g, c); got != "rev0 content\none\ntwo\n" {
		t.Errorf("grandchild rev 2 content after root delete = %q", got)
	}
}
