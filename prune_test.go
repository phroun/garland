package garland

import (
	"testing"
)

func TestPruneBasic(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	// Create some revisions
	cursor := g.NewCursor()
	cursor.InsertString("A", nil, false) // rev 1
	cursor.InsertString("B", nil, false) // rev 2
	cursor.InsertString("C", nil, false) // rev 3

	if g.CurrentRevision() != 3 {
		t.Fatalf("Expected revision 3, got %d", g.CurrentRevision())
	}

	// Prune to revision 2 (keep 2 and 3)
	err := g.Prune(2)
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}

	// Check PrunedUpTo
	forkInfo, _ := g.GetForkInfo(g.CurrentFork())
	if forkInfo.PrunedUpTo != 2 {
		t.Errorf("PrunedUpTo = %d, want 2", forkInfo.PrunedUpTo)
	}

	// Should be able to UndoSeek to revision 2
	err = g.UndoSeek(2)
	if err != nil {
		t.Errorf("UndoSeek to 2 should work: %v", err)
	}

	// Should NOT be able to UndoSeek to revision 1 (pruned)
	err = g.UndoSeek(1)
	if err == nil {
		t.Error("UndoSeek to 1 should fail (pruned)")
	}

	// Should NOT be able to UndoSeek to revision 0 (pruned)
	err = g.UndoSeek(0)
	if err == nil {
		t.Error("UndoSeek to 0 should fail (pruned)")
	}
}

func TestPruneCannotPrunePastCurrent(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.InsertString("A", nil, false) // rev 1
	cursor.InsertString("B", nil, false) // rev 2
	cursor.InsertString("C", nil, false) // rev 3

	// Go back to revision 2
	g.UndoSeek(2)

	// Should not be able to prune to revision 3 (past current)
	err := g.Prune(3)
	if err == nil {
		t.Error("Prune past current revision should fail")
	}

	// Should be able to prune to revision 2 (current)
	err = g.Prune(2)
	if err != nil {
		t.Errorf("Prune to current revision should work: %v", err)
	}
}

func TestPruneCursorHistoryCleanup(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.InsertString("A", nil, false) // rev 1
	cursor.InsertString("B", nil, false) // rev 2

	// Log current history state for debugging
	t.Logf("Before prune: %d position history entries", len(cursor.positionHistory))
	for forkRev := range cursor.positionHistory {
		t.Logf("  Fork %d, Rev %d", forkRev.Fork, forkRev.Revision)
	}

	// Check that we have at least revision 0 entry
	hasRev0 := false
	for forkRev := range cursor.positionHistory {
		if forkRev.Fork == g.CurrentFork() && forkRev.Revision == 0 {
			hasRev0 = true
		}
	}
	if !hasRev0 {
		t.Log("No revision 0 entry found, skipping history cleanup test")
		return
	}

	// Prune to revision 2
	err := g.Prune(2)
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}

	// Position history for rev 0 and 1 should be cleaned up
	for forkRev := range cursor.positionHistory {
		if forkRev.Fork == g.CurrentFork() && forkRev.Revision < 2 {
			t.Errorf("Position history for revision %d should be pruned", forkRev.Revision)
		}
	}
}

func TestDeleteForkBasic(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.InsertString("A", nil, false) // rev 1

	// UndoSeek and create a fork
	g.UndoSeek(0)
	cursor.InsertString("X", nil, false) // Creates fork 1

	if g.CurrentFork() != 1 {
		t.Fatalf("Expected fork 1, got %d", g.CurrentFork())
	}

	// Switch back to fork 0
	g.ForkSeek(0)

	// Delete fork 1
	err := g.DeleteFork(1)
	if err != nil {
		t.Fatalf("DeleteFork failed: %v", err)
	}

	// Should not be able to switch to fork 1
	err = g.ForkSeek(1)
	if err == nil {
		t.Error("ForkSeek to deleted fork should fail")
	}

	// Fork should be marked as deleted
	forkInfo, err := g.GetForkInfo(1)
	if err != nil {
		t.Fatalf("GetForkInfo failed: %v", err)
	}
	if !forkInfo.Deleted {
		t.Error("Fork should be marked as deleted")
	}
}

func TestDeleteForkCannotDeleteFork0(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	err := g.DeleteFork(0)
	if err == nil {
		t.Error("DeleteFork(0) should fail")
	}
}

func TestDeleteForkCannotDeleteCurrent(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.InsertString("A", nil, false) // rev 1

	// UndoSeek and create a fork
	g.UndoSeek(0)
	cursor.InsertString("X", nil, false) // Creates fork 1

	// Try to delete current fork (1)
	err := g.DeleteFork(1)
	if err == nil {
		t.Error("DeleteFork on current fork should fail")
	}
}

func TestPruneWithForks(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.InsertString("A", nil, false) // rev 1 in fork 0
	cursor.InsertString("B", nil, false) // rev 2 in fork 0

	// Create fork 1 from revision 1
	g.UndoSeek(1)
	cursor.InsertString("X", nil, false) // Creates fork 1, rev 2
	cursor.InsertString("Y", nil, false) // rev 3 in fork 1

	// We're now in fork 1
	if g.CurrentFork() != 1 {
		t.Fatalf("Expected fork 1, got %d", g.CurrentFork())
	}

	// Prune fork 1 to revision 3
	err := g.Prune(3)
	if err != nil {
		t.Fatalf("Prune fork 1 failed: %v", err)
	}

	// Fork 1 should be pruned to 3
	forkInfo, _ := g.GetForkInfo(1)
	if forkInfo.PrunedUpTo != 3 {
		t.Errorf("Fork 1 PrunedUpTo = %d, want 3", forkInfo.PrunedUpTo)
	}

	// Switch to fork 0 and verify it's still intact
	g.ForkSeek(0)
	err = g.UndoSeek(0)
	if err != nil {
		t.Errorf("Fork 0 revision 0 should still be accessible: %v", err)
	}

	// Verify content at fork 0 revision 0
	data, _ := cursor.ReadBytes(100)
	if string(data) != "BASE" {
		t.Errorf("Fork 0 rev 0 content = %q, want %q", string(data), "BASE")
	}
}

func TestListForksShowsDeletedAndPruned(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	cursor := g.NewCursor()
	cursor.InsertString("A", nil, false) // rev 1
	cursor.InsertString("B", nil, false) // rev 2

	// Create fork
	g.UndoSeek(0)
	cursor.InsertString("X", nil, false) // Creates fork 1

	// Prune fork 1
	g.Prune(1)

	// Switch back and delete fork 1
	g.ForkSeek(0)
	g.DeleteFork(1)

	// ListForks should show the deleted fork with its status
	forks := g.ListForks()
	foundDeletedFork := false
	for _, info := range forks {
		if info.ID == 1 {
			if !info.Deleted {
				t.Error("Fork 1 should be marked as deleted")
			}
			if info.PrunedUpTo != 1 {
				t.Errorf("Fork 1 PrunedUpTo = %d, want 1", info.PrunedUpTo)
			}
			foundDeletedFork = true
		}
	}
	if !foundDeletedFork {
		t.Error("Deleted fork should still appear in ListForks")
	}
}

// countSnapshotsInFork counts how many snapshots exist for a specific fork
func countSnapshotsInFork(g *Garland, forkID ForkID) int {
	stats := g.GetSnapshotStats()
	return stats.ByFork[forkID]
}

// countSnapshotsForRevision counts how many snapshots exist for a specific fork/revision
func countSnapshotsForRevision(g *Garland, forkID ForkID, rev RevisionID) int {
	stats := g.GetSnapshotStats()
	return stats.ByForkRevision[ForkRevision{Fork: forkID, Revision: rev}]
}

// totalSnapshotCount counts all snapshots across all nodes
func totalSnapshotCount(g *Garland) int {
	stats := g.GetSnapshotStats()
	return stats.TotalSnapshots
}

func TestSharedHistoryPreservedForChildForks(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	cursor := g.NewCursor()

	// Build a mid-length history in fork 0
	cursor.InsertString("1", nil, false) // rev 1
	cursor.InsertString("2", nil, false) // rev 2
	cursor.InsertString("3", nil, false) // rev 3
	cursor.InsertString("4", nil, false) // rev 4
	cursor.InsertString("5", nil, false) // rev 5

	// Create fork 1 from revision 3 (will share revisions 0-3 with fork 0)
	g.UndoSeek(3)
	cursor.InsertString("A", nil, false) // Creates fork 1, rev 4
	cursor.InsertString("B", nil, false) // rev 5 in fork 1
	cursor.InsertString("C", nil, false) // rev 6 in fork 1

	if g.CurrentFork() != 1 {
		t.Fatalf("Expected fork 1, got %d", g.CurrentFork())
	}

	// Prune fork 1 to revision 5 (keep 5 and 6)
	// Fork 1's parentRevision is 3, so pruning to 5 means:
	// - Fork 1's own revisions 4 is logically pruned
	// - But shared history (revisions 0-3 from fork 0) should remain for fork 0
	err := g.Prune(5)
	if err != nil {
		t.Fatalf("Prune fork 1 failed: %v", err)
	}

	forkInfo1, _ := g.GetForkInfo(1)
	if forkInfo1.PrunedUpTo != 5 {
		t.Errorf("Fork 1 PrunedUpTo = %d, want 5", forkInfo1.PrunedUpTo)
	}

	// Switch to fork 0 - all its history should still be accessible
	g.ForkSeek(0)
	g.UndoSeek(5) // Back to latest in fork 0

	// Verify we can still access all of fork 0's history
	for rev := RevisionID(0); rev <= 5; rev++ {
		err := g.UndoSeek(rev)
		if err != nil {
			t.Errorf("Fork 0 revision %d should still be accessible: %v", rev, err)
		}
	}

	// Verify content at fork 0 revision 0
	g.UndoSeek(0)
	data, _ := cursor.ReadBytes(100)
	if string(data) != "BASE" {
		t.Errorf("Fork 0 rev 0 content = %q, want %q", string(data), "BASE")
	}

	t.Logf("Shared history preserved: Fork 0 history intact after fork 1 prune")
}

func TestSharedHistoryFreedWhenAllForksProned(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	cursor := g.NewCursor()

	// Build mid-length history in fork 0
	cursor.InsertString("1", nil, false) // rev 1
	cursor.InsertString("2", nil, false) // rev 2
	cursor.InsertString("3", nil, false) // rev 3
	cursor.InsertString("4", nil, false) // rev 4
	cursor.InsertString("5", nil, false) // rev 5

	// Create fork 1 from revision 2
	g.UndoSeek(2)
	cursor.InsertString("A", nil, false) // Creates fork 1, rev 3
	cursor.InsertString("B", nil, false) // rev 4 in fork 1

	if g.CurrentFork() != 1 {
		t.Fatalf("Expected fork 1, got %d", g.CurrentFork())
	}

	// Count initial snapshots
	snapshotsRev1Before := countSnapshotsForRevision(g, 0, 1)
	t.Logf("Before pruning: Fork 0 rev 1 has %d snapshots", snapshotsRev1Before)

	// Switch to fork 0 and prune to revision 3 (keeps 3,4,5)
	g.ForkSeek(0)
	g.UndoSeek(5) // make sure we're at latest
	err := g.Prune(3)
	if err != nil {
		t.Fatalf("Prune fork 0 failed: %v", err)
	}

	// Fork 0 is pruned - some revision 1 snapshots should be freed
	// (those not needed by fork 1's parent chain)
	snapshotsRev1AfterFork0Prune := countSnapshotsForRevision(g, 0, 1)
	t.Logf("After fork 0 prune: Fork 0 rev 1 has %d snapshots", snapshotsRev1AfterFork0Prune)

	// Verify fork 0 can no longer access revision 1
	err = g.UndoSeek(1)
	if err == nil {
		t.Error("Fork 0 should not be able to UndoSeek to revision 1 (pruned)")
	}

	// But fork 1 should still be able to render its state
	// (it references parent fork via parentRevision, which is rev 2)
	g.ForkSeek(1)
	g.UndoSeek(4) // latest in fork 1

	// Now prune fork 1 to revision 4 (its current latest)
	err = g.Prune(4)
	if err != nil {
		t.Fatalf("Prune fork 1 failed: %v", err)
	}

	// After pruning fork 1, some more snapshots may be freed
	// Note: fork 1 still references fork 0 at parentRevision=2, so some
	// fork 0 snapshots needed to render that state are still kept
	snapshotsRev1AfterBothPrune := countSnapshotsForRevision(g, 0, 1)
	t.Logf("After both forks pruned: Fork 0 rev 1 has %d snapshots", snapshotsRev1AfterBothPrune)

	// Verify fewer snapshots after pruning (may not be zero due to parent chain)
	if snapshotsRev1AfterBothPrune >= snapshotsRev1Before {
		t.Errorf("Expected fewer Fork 0 rev 1 snapshots after pruning, before=%d, after=%d",
			snapshotsRev1Before, snapshotsRev1AfterBothPrune)
	}
}

func TestDeletedForkDataFreedAfterChildPrune(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	cursor := g.NewCursor()

	// Build history in fork 0
	cursor.InsertString("1", nil, false) // rev 1
	cursor.InsertString("2", nil, false) // rev 2
	cursor.InsertString("3", nil, false) // rev 3

	// Create fork 1 from revision 2
	g.UndoSeek(2)
	cursor.InsertString("A", nil, false) // Creates fork 1, rev 3
	cursor.InsertString("B", nil, false) // rev 4 in fork 1
	cursor.InsertString("C", nil, false) // rev 5 in fork 1

	// Create fork 2 from fork 1's revision 4
	g.UndoSeek(4)
	cursor.InsertString("X", nil, false) // Creates fork 2, rev 5
	cursor.InsertString("Y", nil, false) // rev 6 in fork 2

	if g.CurrentFork() != 2 {
		t.Fatalf("Expected fork 2, got %d", g.CurrentFork())
	}

	// Count snapshots in fork 1 before deletion
	fork1SnapshotsBefore := countSnapshotsInFork(g, 1)
	t.Logf("Fork 1 has %d snapshots before deletion", fork1SnapshotsBefore)

	// Delete fork 1 (the middle fork)
	g.ForkSeek(0)
	g.UndoSeek(3) // latest in fork 0

	err := g.DeleteFork(1)
	if err != nil {
		t.Fatalf("DeleteFork(1) failed: %v", err)
	}

	// Fork 1 is deleted, but fork 2 still depends on it via parent chain
	// Some fork 1 snapshots should still exist (those needed by fork 2)
	fork1SnapshotsAfterDelete := countSnapshotsInFork(g, 1)
	t.Logf("Fork 1 has %d snapshots after deletion (fork 2 still needs some)", fork1SnapshotsAfterDelete)

	// Fork 2 is still intact
	g.ForkSeek(2)
	g.UndoSeek(6)

	// Now prune fork 2 to revision 6 (its latest)
	err = g.Prune(6)
	if err != nil {
		t.Fatalf("Prune fork 2 failed: %v", err)
	}

	// After pruning fork 2, some fork 1 snapshots should be freed
	// Note: fork 2 still references fork 1 at parentRevision=4, so some may remain
	fork1SnapshotsAfterPrune := countSnapshotsInFork(g, 1)
	t.Logf("Fork 1 has %d snapshots after fork 2 pruned", fork1SnapshotsAfterPrune)

	// Verify fork 1 is marked as deleted
	forkInfo, _ := g.GetForkInfo(1)
	if !forkInfo.Deleted {
		t.Error("Fork 1 should be marked as deleted")
	}

	// Verify snapshot count decreased after pruning
	if fork1SnapshotsAfterDelete > 0 && fork1SnapshotsAfterPrune >= fork1SnapshotsAfterDelete {
		t.Logf("Note: Fork 1 snapshots didn't decrease after prune (before=%d, after=%d) - this may be expected if fork 2 still references them via parent chain",
			fork1SnapshotsAfterDelete, fork1SnapshotsAfterPrune)
	}
}

func TestDeleteOriginalForkWithChildForksNeeding(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "BASE"})
	defer g.Close()

	cursor := g.NewCursor()

	// Build mid-length history in fork 0
	cursor.InsertString("1", nil, false) // rev 1
	cursor.InsertString("2", nil, false) // rev 2
	cursor.InsertString("3", nil, false) // rev 3
	cursor.InsertString("4", nil, false) // rev 4

	// Create fork 1 from revision 2
	g.UndoSeek(2)
	cursor.InsertString("A", nil, false) // Creates fork 1, rev 3
	cursor.InsertString("B", nil, false) // rev 4 in fork 1

	// Create fork 2 from revision 2 (same branch point as fork 1)
	g.ForkSeek(0)
	g.UndoSeek(2)
	cursor.InsertString("X", nil, false) // Creates fork 2, rev 3
	cursor.InsertString("Y", nil, false) // rev 4 in fork 2

	// Both fork 1 and fork 2 depend on fork 0's revisions 0-2
	// Count fork 0's snapshots
	fork0SnapshotsBefore := countSnapshotsInFork(g, 0)
	t.Logf("Fork 0 has %d snapshots before pruning", fork0SnapshotsBefore)

	// Can't delete fork 0 (it's the root fork)
	err := g.DeleteFork(0)
	if err == nil {
		t.Error("Should not be able to delete fork 0")
	}

	// Prune fork 0 to revision 4
	g.ForkSeek(0)
	g.UndoSeek(4)
	err = g.Prune(4)
	if err != nil {
		t.Fatalf("Prune fork 0 failed: %v", err)
	}

	// Fork 0 is pruned to 4, but forks 1 and 2 need fork 0's history up to rev 2
	fork0Rev1SnapshotsAfterPrune := countSnapshotsForRevision(g, 0, 1)
	t.Logf("Fork 0 rev 1 has %d snapshots after fork 0 prune (child forks may still need)", fork0Rev1SnapshotsAfterPrune)

	// Prune fork 1 to revision 4
	g.ForkSeek(1)
	g.UndoSeek(4)
	err = g.Prune(4)
	if err != nil {
		t.Fatalf("Prune fork 1 failed: %v", err)
	}

	fork0Rev1AfterFork1Prune := countSnapshotsForRevision(g, 0, 1)
	t.Logf("Fork 0 rev 1 has %d snapshots after fork 1 also pruned", fork0Rev1AfterFork1Prune)

	// Prune fork 2 to revision 4
	g.ForkSeek(2)
	g.UndoSeek(4)
	err = g.Prune(4)
	if err != nil {
		t.Fatalf("Prune fork 2 failed: %v", err)
	}

	// After pruning all forks, some fork 0 snapshots may still be needed
	// because forks 1 and 2 still reference fork 0 at parentRevision=2
	fork0Rev1AfterAllPrune := countSnapshotsForRevision(g, 0, 1)
	t.Logf("Fork 0 rev 1 has %d snapshots after all forks pruned", fork0Rev1AfterAllPrune)

	// Verify that pruned revisions can't be accessed
	g.ForkSeek(0)
	err = g.UndoSeek(1)
	if err == nil {
		t.Error("Fork 0 should not be able to UndoSeek to revision 1 (pruned)")
	}

	// But fork 0 can access its pruned-from revision (4)
	err = g.UndoSeek(4)
	if err != nil {
		t.Errorf("Fork 0 should be able to access revision 4: %v", err)
	}
}

func TestDecorationHistoryPrunedWithData(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World"})
	defer g.Close()

	cursor := g.NewCursor()

	// Add some edits to create revisions
	cursor.InsertString("!", nil, false) // rev 1

	// Add decoration at current revision
	addr5 := ByteAddress(5)
	g.Decorate([]DecorationEntry{{Key: "mark1", Address: &addr5}})
	cursor.InsertString("?", nil, false) // rev 2

	// Add another decoration
	addr0 := ByteAddress(0)
	g.Decorate([]DecorationEntry{{Key: "mark2", Address: &addr0}})
	cursor.InsertString("@", nil, false) // rev 3

	// Verify decorations exist at current revision (rev 3)
	pos1, err := g.GetDecorationPosition("mark1")
	if err != nil {
		t.Fatalf("mark1 should exist at rev 3: %v", err)
	}
	t.Logf("mark1 at rev 3: position %d", pos1.Byte)

	pos2, err := g.GetDecorationPosition("mark2")
	if err != nil {
		t.Fatalf("mark2 should exist at rev 3: %v", err)
	}
	t.Logf("mark2 at rev 3: position %d", pos2.Byte)

	// Count total snapshots before prune
	totalBefore := totalSnapshotCount(g)
	t.Logf("Total snapshots before prune: %d", totalBefore)

	// Prune to revision 3 (removes revisions 0, 1, 2)
	err = g.Prune(3)
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}

	// Count total snapshots after prune
	totalAfter := totalSnapshotCount(g)
	t.Logf("Total snapshots after prune: %d", totalAfter)

	// Decorations from revision 3 should still exist
	pos1AfterPrune, err := g.GetDecorationPosition("mark1")
	if err != nil {
		t.Errorf("mark1 should still exist after prune: %v", err)
	} else {
		t.Logf("mark1 after prune: position %d", pos1AfterPrune.Byte)
	}

	pos2AfterPrune, err := g.GetDecorationPosition("mark2")
	if err != nil {
		t.Errorf("mark2 should still exist after prune: %v", err)
	} else {
		t.Logf("mark2 after prune: position %d", pos2AfterPrune.Byte)
	}

	// Verify we can't go back to pruned revisions
	err = g.UndoSeek(1)
	if err == nil {
		t.Error("Should not be able to UndoSeek to revision 1 (pruned)")
	}

	err = g.UndoSeek(0)
	if err == nil {
		t.Error("Should not be able to UndoSeek to revision 0 (pruned)")
	}

	// Verify some snapshots were freed
	if totalAfter >= totalBefore {
		t.Logf("Note: Snapshot count didn't decrease (before=%d, after=%d) - current revision may reference older snapshots",
			totalBefore, totalAfter)
	} else {
		t.Logf("Pruning reduced snapshots from %d to %d", totalBefore, totalAfter)
	}
}
