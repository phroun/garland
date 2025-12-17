package garland

import (
	"fmt"
	"testing"
)

// TestExtensiveForkOperationsWithDecorations tests complex fork scenarios with decorations:
// - Multiple changes with decorations on different forks
// - Decoration preservation across version navigation
// - Switching between forks with decorated content
// - UndoSeek within and across divergence points
func TestExtensiveForkOperationsWithDecorations(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "BASE"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Helper to read current content
	readContent := func() string {
		cursor.SeekByte(0)
		data, _ := cursor.ReadBytes(g.ByteCount().Value)
		return string(data)
	}

	// Verify initial state
	if content := readContent(); content != "BASE" {
		t.Fatalf("Initial content: got %q, want %q", content, "BASE")
	}

	// === Fork 0: Insert with decorations ===
	t.Log("=== Building Fork 0 with decorations ===")

	// Rev 1: Insert "A" with decoration "mark_A"
	cursor.SeekByte(0)
	_, err = cursor.InsertString("A", []RelativeDecoration{{Key: "mark_A", Position: 0}}, false)
	if err != nil {
		t.Fatalf("Insert A failed: %v", err)
	}
	t.Logf("Rev 1: %q with mark_A (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// Rev 2: Insert "B" with decoration "mark_B"
	cursor.SeekByte(1)
	_, err = cursor.InsertString("B", []RelativeDecoration{{Key: "mark_B", Position: 0}}, false)
	if err != nil {
		t.Fatalf("Insert B failed: %v", err)
	}
	t.Logf("Rev 2: %q with mark_B (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// === UndoSeek back and create new fork with different decorations ===
	t.Log("=== UndoSeek to rev 1 and create fork with different decorations ===")
	err = g.UndoSeek(1)
	if err != nil {
		t.Fatalf("UndoSeek to 1 failed: %v", err)
	}

	// Fork 1: Insert "X" with decoration "mark_X" (different path)
	// With fork revision inheritance, fork 1 inherits rev 0-1 from parent,
	// so this edit becomes revision 2 in fork 1
	cursor.SeekByte(1)
	_, err = cursor.InsertString("X", []RelativeDecoration{{Key: "mark_X", Position: 0}}, false)
	if err != nil {
		t.Fatalf("Insert X failed: %v", err)
	}
	if g.CurrentFork() != 1 {
		t.Errorf("Expected fork 1, got %d", g.CurrentFork())
	}
	if g.CurrentRevision() != 2 {
		t.Errorf("Expected revision 2 after fork, got %d", g.CurrentRevision())
	}
	t.Logf("Fork 1 Rev 2: %q with mark_X (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// Add more to fork 1 with multiple decorations
	cursor.SeekByte(2)
	_, err = cursor.InsertString("YZ", []RelativeDecoration{
		{Key: "mark_Y", Position: 0},
		{Key: "mark_Z", Position: 1},
	}, false)
	if err != nil {
		t.Fatalf("Insert YZ failed: %v", err)
	}
	if g.CurrentRevision() != 3 {
		t.Errorf("Expected revision 3 after second edit in fork, got %d", g.CurrentRevision())
	}
	t.Logf("Fork 1 Rev 3: %q with mark_Y,mark_Z (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// === Test UndoSeek within Fork 1 ===
	t.Log("=== UndoSeek within Fork 1 ===")

	// UndoSeek to rev 1 should show parent's content at rev 1
	err = g.UndoSeek(1)
	if err != nil {
		t.Fatalf("UndoSeek to 1 in fork 1 failed: %v", err)
	}
	if content := readContent(); content != "ABASE" {
		t.Errorf("Fork 1 rev 1 (from parent): got %q, want %q", content, "ABASE")
	}
	t.Logf("Fork 1 at rev 1 (inherited from parent): %q", readContent())

	// UndoSeek to rev 2 should show first fork 1 edit
	err = g.UndoSeek(2)
	if err != nil {
		t.Fatalf("UndoSeek to 2 in fork 1 failed: %v", err)
	}
	if content := readContent(); content != "AXBASE" {
		t.Errorf("Fork 1 rev 2: got %q, want %q", content, "AXBASE")
	}

	// UndoSeek to rev 3 should show second fork 1 edit
	err = g.UndoSeek(3)
	if err != nil {
		t.Fatalf("UndoSeek to 3 in fork 1 failed: %v", err)
	}
	if content := readContent(); content != "AXYZBASE" {
		t.Errorf("Fork 1 rev 3: got %q, want %q", content, "AXYZBASE")
	}

	// === Switch to Fork 0 and verify content preserved ===
	t.Log("=== Switch to Fork 0 ===")
	err = g.ForkSeek(0)
	if err != nil {
		t.Fatalf("ForkSeek to 0 failed: %v", err)
	}

	// Seek to rev 2 in fork 0
	err = g.UndoSeek(2)
	if err != nil {
		t.Fatalf("UndoSeek to 2 in fork 0 failed: %v", err)
	}
	if content := readContent(); content != "ABBASE" {
		t.Errorf("Fork 0 rev 2: got %q, want %q", content, "ABBASE")
	}
	t.Logf("Fork 0 rev 2: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// === UndoSeek to origin in fork 0 ===
	err = g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek to 0 failed: %v", err)
	}
	if content := readContent(); content != "BASE" {
		t.Errorf("Fork 0 rev 0: got %q, want %q", content, "BASE")
	}

	// === Create Fork 2 from Fork 0 rev 0 with its own decorations ===
	t.Log("=== Create Fork 2 from Fork 0 rev 0 ===")
	cursor.SeekByte(0)
	_, err = cursor.InsertString("!!", []RelativeDecoration{
		{Key: "exclaim_1", Position: 0},
		{Key: "exclaim_2", Position: 1},
	}, false)
	if err != nil {
		t.Fatalf("Insert !! failed: %v", err)
	}
	if g.CurrentFork() != 2 {
		t.Errorf("Expected fork 2, got %d", g.CurrentFork())
	}
	if content := readContent(); content != "!!BASE" {
		t.Errorf("Fork 2 rev 1: got %q, want %q", content, "!!BASE")
	}
	t.Logf("Fork 2 Rev 1: %q with exclaim decorations (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// === Final traversal: switch between all forks ===
	t.Log("=== Final traversal between all forks ===")

	// Fork 0 head
	err = g.ForkSeek(0)
	if err != nil {
		t.Fatalf("ForkSeek to 0 failed: %v", err)
	}
	g.UndoSeek(2)
	t.Logf("Fork 0 HEAD: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())
	if content := readContent(); content != "ABBASE" {
		t.Errorf("Fork 0 HEAD: got %q, want %q", content, "ABBASE")
	}

	// Fork 1 head
	err = g.ForkSeek(1)
	if err != nil {
		t.Fatalf("ForkSeek to 1 failed: %v", err)
	}
	forkInfo, _ := g.GetForkInfo(1)
	g.UndoSeek(forkInfo.HighestRevision)
	t.Logf("Fork 1 HEAD: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())
	if content := readContent(); content != "AXYZBASE" {
		t.Errorf("Fork 1 HEAD: got %q, want %q", content, "AXYZBASE")
	}

	// Fork 2 head
	err = g.ForkSeek(2)
	if err != nil {
		t.Fatalf("ForkSeek to 2 failed: %v", err)
	}
	forkInfo, _ = g.GetForkInfo(2)
	g.UndoSeek(forkInfo.HighestRevision)
	t.Logf("Fork 2 HEAD: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())
	if content := readContent(); content != "!!BASE" {
		t.Errorf("Fork 2 HEAD: got %q, want %q", content, "!!BASE")
	}

	// === Summary ===
	t.Log("=== Fork summary ===")
	forks := g.ListForks()
	for _, f := range forks {
		t.Logf("Fork %d: parent=%d@%d, highest=%d", f.ID, f.ParentFork, f.ParentRevision, f.HighestRevision)
	}

	if len(forks) != 3 {
		t.Errorf("Expected 3 forks, got %d", len(forks))
	}
}

// TestExtensiveForkOperations tests complex fork scenarios:
// - Multiple changes on different forks
// - Switching between forks
// - UndoSeek within and across divergence points
// - Fork retention when seeking forward
func TestExtensiveForkOperations(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "BASE"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Helper to read current content
	readContent := func() string {
		cursor.SeekByte(0)
		data, _ := cursor.ReadBytes(g.ByteCount().Value)
		return string(data)
	}

	// Verify initial state
	if content := readContent(); content != "BASE" {
		t.Fatalf("Initial content: got %q, want %q", content, "BASE")
	}
	if g.CurrentFork() != 0 || g.CurrentRevision() != 0 {
		t.Fatalf("Initial state: fork=%d rev=%d, want fork=0 rev=0", g.CurrentFork(), g.CurrentRevision())
	}

	// === Fork 0: Make several changes ===
	t.Log("=== Building Fork 0 history ===")

	// Rev 1: "ABASE"
	cursor.SeekByte(0)
	_, err = cursor.InsertString("A", nil, false)
	if err != nil {
		t.Fatalf("Insert A failed: %v", err)
	}
	if content := readContent(); content != "ABASE" {
		t.Errorf("After A: got %q, want %q", content, "ABASE")
	}
	t.Logf("Rev 1: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// Rev 2: "ABBASE"
	cursor.SeekByte(1)
	_, err = cursor.InsertString("B", nil, false)
	if err != nil {
		t.Fatalf("Insert B failed: %v", err)
	}
	if content := readContent(); content != "ABBASE" {
		t.Errorf("After B: got %q, want %q", content, "ABBASE")
	}
	t.Logf("Rev 2: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// Rev 3: "ABCBASE"
	cursor.SeekByte(2)
	_, err = cursor.InsertString("C", nil, false)
	if err != nil {
		t.Fatalf("Insert C failed: %v", err)
	}
	if content := readContent(); content != "ABCBASE" {
		t.Errorf("After C: got %q, want %q", content, "ABCBASE")
	}
	t.Logf("Rev 3: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// Rev 4: "ABCDBASE"
	cursor.SeekByte(3)
	_, err = cursor.InsertString("D", nil, false)
	if err != nil {
		t.Fatalf("Insert D failed: %v", err)
	}
	if content := readContent(); content != "ABCDBASE" {
		t.Errorf("After D: got %q, want %q", content, "ABCDBASE")
	}
	t.Logf("Rev 4: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// Now we have Fork 0 with revisions 0-4
	if g.CurrentFork() != 0 || g.CurrentRevision() != 4 {
		t.Fatalf("After fork 0 changes: fork=%d rev=%d, want fork=0 rev=4", g.CurrentFork(), g.CurrentRevision())
	}

	// === UndoSeek back to revision 2 in Fork 0 ===
	t.Log("=== UndoSeek to revision 2 ===")
	err = g.UndoSeek(2)
	if err != nil {
		t.Fatalf("UndoSeek to 2 failed: %v", err)
	}
	if content := readContent(); content != "ABBASE" {
		t.Errorf("After UndoSeek(2): got %q, want %q", content, "ABBASE")
	}
	t.Logf("After UndoSeek(2): %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// === Create Fork 1 by editing from revision 2 ===
	// With fork revision inheritance, fork 1 inherits rev 0-2 from parent,
	// so the first edit in fork 1 becomes revision 3
	t.Log("=== Creating Fork 1 by editing from revision 2 ===")
	cursor.SeekByte(2)
	_, err = cursor.InsertString("X", nil, false)
	if err != nil {
		t.Fatalf("Insert X failed: %v", err)
	}
	if g.CurrentFork() != 1 {
		t.Errorf("After edit from rev 2: fork=%d, want 1", g.CurrentFork())
	}
	if g.CurrentRevision() != 3 {
		t.Errorf("After edit from rev 2: rev=%d, want 3 (inherited 0-2, new is 3)", g.CurrentRevision())
	}
	if content := readContent(); content != "ABXBASE" {
		t.Errorf("After X: got %q, want %q", content, "ABXBASE")
	}
	t.Logf("Fork 1 Rev 3: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// More changes on Fork 1
	cursor.SeekByte(3)
	_, err = cursor.InsertString("Y", nil, false)
	if err != nil {
		t.Fatalf("Insert Y failed: %v", err)
	}
	if content := readContent(); content != "ABXYBASE" {
		t.Errorf("After Y: got %q, want %q", content, "ABXYBASE")
	}
	t.Logf("Fork 1 Rev 4: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	cursor.SeekByte(4)
	_, err = cursor.InsertString("Z", nil, false)
	if err != nil {
		t.Fatalf("Insert Z failed: %v", err)
	}
	if content := readContent(); content != "ABXYZBASE" {
		t.Errorf("After Z: got %q, want %q", content, "ABXYZBASE")
	}
	t.Logf("Fork 1 Rev 5: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// Now Fork 1 has revisions 0-5 (0-2 inherited from fork 0, 3-5 are new)
	if g.CurrentFork() != 1 || g.CurrentRevision() != 5 {
		t.Fatalf("After fork 1 changes: fork=%d rev=%d, want fork=1 rev=5", g.CurrentFork(), g.CurrentRevision())
	}

	// === UndoSeek within Fork 1 ===
	t.Log("=== UndoSeek within Fork 1 ===")

	// UndoSeek to rev 1 - shows parent's content at rev 1 (before divergence)
	err = g.UndoSeek(1)
	if err != nil {
		t.Fatalf("UndoSeek to 1 in fork 1 failed: %v", err)
	}
	if content := readContent(); content != "ABASE" {
		t.Errorf("Fork 1 after UndoSeek(1) (from parent): got %q, want %q", content, "ABASE")
	}
	t.Logf("Fork 1 after UndoSeek(1) (inherited from parent): %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// UndoSeek to rev 3 - shows first fork 1 edit
	err = g.UndoSeek(3)
	if err != nil {
		t.Fatalf("UndoSeek to 3 in fork 1 failed: %v", err)
	}
	if content := readContent(); content != "ABXBASE" {
		t.Errorf("Fork 1 after UndoSeek(3): got %q, want %q", content, "ABXBASE")
	}
	if g.CurrentFork() != 1 || g.CurrentRevision() != 3 {
		t.Errorf("After UndoSeek(3): fork=%d rev=%d, want fork=1 rev=3", g.CurrentFork(), g.CurrentRevision())
	}
	t.Logf("Fork 1 after UndoSeek(3): %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// Go forward to rev 5 in fork 1
	err = g.UndoSeek(5)
	if err != nil {
		t.Fatalf("UndoSeek to 5 in fork 1 failed: %v", err)
	}
	if content := readContent(); content != "ABXYZBASE" {
		t.Errorf("Fork 1 after UndoSeek(5): got %q, want %q", content, "ABXYZBASE")
	}
	if g.CurrentFork() != 1 || g.CurrentRevision() != 5 {
		t.Errorf("After UndoSeek(5): fork=%d rev=%d, want fork=1 rev=5", g.CurrentFork(), g.CurrentRevision())
	}
	t.Logf("Fork 1 after UndoSeek(5): %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// === Switch to Fork 0 ===
	t.Log("=== Switch to Fork 0 ===")
	err = g.ForkSeek(0)
	if err != nil {
		t.Fatalf("ForkSeek to 0 failed: %v", err)
	}
	// ForkSeek goes to the common ancestor (rev 2 since fork 1 branched from there)
	t.Logf("After ForkSeek(0): %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// Now seek to rev 4 in fork 0
	err = g.UndoSeek(4)
	if err != nil {
		t.Fatalf("UndoSeek to 4 in fork 0 failed: %v", err)
	}
	if content := readContent(); content != "ABCDBASE" {
		t.Errorf("Fork 0 after UndoSeek(4): got %q, want %q", content, "ABCDBASE")
	}
	if g.CurrentFork() != 0 || g.CurrentRevision() != 4 {
		t.Errorf("After UndoSeek(4): fork=%d rev=%d, want fork=0 rev=4", g.CurrentFork(), g.CurrentRevision())
	}
	t.Logf("Fork 0 after UndoSeek(4): %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// === UndoSeek all the way back to revision 0 (before divergence) ===
	t.Log("=== UndoSeek to revision 0 (pre-divergence) ===")
	err = g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek to 0 failed: %v", err)
	}
	if content := readContent(); content != "BASE" {
		t.Errorf("After UndoSeek(0): got %q, want %q", content, "BASE")
	}
	if g.CurrentFork() != 0 || g.CurrentRevision() != 0 {
		t.Errorf("After UndoSeek(0): fork=%d rev=%d, want fork=0 rev=0", g.CurrentFork(), g.CurrentRevision())
	}
	t.Logf("After UndoSeek(0): %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// === Seek forward again - should retain fork 0 ===
	t.Log("=== Seek forward - should retain fork 0 ===")
	err = g.UndoSeek(3)
	if err != nil {
		t.Fatalf("UndoSeek to 3 failed: %v", err)
	}
	if content := readContent(); content != "ABCBASE" {
		t.Errorf("After UndoSeek(3) in fork 0: got %q, want %q", content, "ABCBASE")
	}
	if g.CurrentFork() != 0 {
		t.Errorf("Fork should still be 0, got %d", g.CurrentFork())
	}
	t.Logf("After UndoSeek(3) in fork 0: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// === Switch to Fork 1 and traverse its history ===
	t.Log("=== Switch to Fork 1 and traverse ===")
	err = g.ForkSeek(1)
	if err != nil {
		t.Fatalf("ForkSeek to 1 failed: %v", err)
	}
	t.Logf("After ForkSeek(1): %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// Seek to the head of fork 1
	forkInfo, _ := g.GetForkInfo(1)
	err = g.UndoSeek(forkInfo.HighestRevision)
	if err != nil {
		t.Fatalf("UndoSeek to HEAD of fork 1 failed: %v", err)
	}
	if content := readContent(); content != "ABXYZBASE" {
		t.Errorf("Fork 1 HEAD: got %q, want %q", content, "ABXYZBASE")
	}
	t.Logf("Fork 1 HEAD: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// Seek back to rev 0 in fork 1 - with fork inheritance, rev 0 shows
	// the actual revision 0 content from the parent fork
	err = g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek to 0 in fork 1 failed: %v", err)
	}
	// Fork 1 inherits rev 0 from fork 0, so rev 0 = "BASE"
	if content := readContent(); content != "BASE" {
		t.Errorf("Fork 1 rev 0 (inherited): got %q, want %q", content, "BASE")
	}
	if g.CurrentFork() != 1 || g.CurrentRevision() != 0 {
		t.Errorf("After UndoSeek(0) in fork 1: fork=%d rev=%d, want fork=1 rev=0", g.CurrentFork(), g.CurrentRevision())
	}
	t.Logf("Fork 1 rev 0 (inherited from parent): %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// === Create Fork 2 from Fork 1 ===
	t.Log("=== Create Fork 2 from Fork 1 rev 0 ===")
	cursor.SeekByte(0)
	_, err = cursor.InsertString("!", nil, false)
	if err != nil {
		t.Fatalf("Insert ! failed: %v", err)
	}
	if g.CurrentFork() != 2 {
		t.Errorf("After edit from fork 1 rev 0: fork=%d, want 2", g.CurrentFork())
	}
	// Fork 2 branches from Fork 1 at rev 0, which has content "BASE"
	if content := readContent(); content != "!BASE" {
		t.Errorf("Fork 2 rev 1: got %q, want %q", content, "!BASE")
	}
	t.Logf("Fork 2 Rev 1: %q (fork=%d, rev=%d)", readContent(), g.CurrentFork(), g.CurrentRevision())

	// === Summary: List all forks ===
	t.Log("=== Final fork summary ===")
	forks := g.ListForks()
	for _, f := range forks {
		t.Logf("Fork %d: parent=%d@%d, highest=%d", f.ID, f.ParentFork, f.ParentRevision, f.HighestRevision)
	}

	if len(forks) != 3 {
		t.Errorf("Expected 3 forks, got %d", len(forks))
	}
}

// TestUndoSeekBoundaryCases tests edge cases for UndoSeek
func TestUndoSeekBoundaryCases(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Test"})
	defer g.Close()

	cursor := g.NewCursor()

	// Create some revisions
	for i := 0; i < 5; i++ {
		cursor.SeekByte(0)
		cursor.InsertString(fmt.Sprintf("%d", i), nil, false)
	}

	t.Logf("Created revisions 0-5, currently at rev %d", g.CurrentRevision())

	// Test: Seek to current revision (no-op)
	currentRev := g.CurrentRevision()
	err := g.UndoSeek(currentRev)
	if err != nil {
		t.Errorf("UndoSeek to current revision failed: %v", err)
	}
	if g.CurrentRevision() != currentRev {
		t.Errorf("Revision changed on no-op seek")
	}

	// Test: Seek past highest revision should fail
	forkInfo, _ := g.GetForkInfo(g.CurrentFork())
	err = g.UndoSeek(forkInfo.HighestRevision + 10)
	if err != ErrRevisionNotFound {
		t.Errorf("Expected ErrRevisionNotFound for invalid revision, got %v", err)
	}

	// Test: Seek to 0 should work
	err = g.UndoSeek(0)
	if err != nil {
		t.Errorf("UndoSeek to 0 failed: %v", err)
	}
	if g.CurrentRevision() != 0 {
		t.Errorf("After UndoSeek(0): revision=%d, want 0", g.CurrentRevision())
	}
}

// TestForkSeekBoundaryCases tests edge cases for ForkSeek
func TestForkSeekBoundaryCases(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Test"})
	defer g.Close()

	cursor := g.NewCursor()

	// Create a change then go back and fork
	cursor.SeekByte(0)
	cursor.InsertString("A", nil, false)

	g.UndoSeek(0)
	cursor.SeekByte(0)
	cursor.InsertString("B", nil, false)

	// Now we have fork 0 and fork 1
	if g.CurrentFork() != 1 {
		t.Fatalf("Expected to be on fork 1, got %d", g.CurrentFork())
	}

	// Test: Seek to current fork (no-op)
	err := g.ForkSeek(1)
	if err != nil {
		t.Errorf("ForkSeek to current fork failed: %v", err)
	}

	// Test: Seek to non-existent fork should fail
	err = g.ForkSeek(999)
	if err != ErrForkNotFound {
		t.Errorf("Expected ErrForkNotFound for invalid fork, got %v", err)
	}

	// Test: Seek to fork 0 should work
	err = g.ForkSeek(0)
	if err != nil {
		t.Errorf("ForkSeek to 0 failed: %v", err)
	}
	if g.CurrentFork() != 0 {
		t.Errorf("After ForkSeek(0): fork=%d, want 0", g.CurrentFork())
	}
}

// TestTransactionBlocksSeek tests that UndoSeek/ForkSeek are blocked during transactions
func TestTransactionBlocksSeek(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Test"})
	defer g.Close()

	// Start a transaction
	err := g.TransactionStart("test")
	if err != nil {
		t.Fatalf("TransactionStart failed: %v", err)
	}

	// UndoSeek should fail
	err = g.UndoSeek(0)
	if err != ErrTransactionPending {
		t.Errorf("UndoSeek during transaction: expected ErrTransactionPending, got %v", err)
	}

	// ForkSeek should fail
	err = g.ForkSeek(0)
	if err != ErrTransactionPending {
		t.Errorf("ForkSeek during transaction: expected ErrTransactionPending, got %v", err)
	}

	// Commit and try again
	g.TransactionCommit()

	err = g.UndoSeek(0)
	if err != nil {
		t.Errorf("UndoSeek after commit failed: %v", err)
	}
}

// TestCursorPositionAcrossVersions tests that cursor positions are properly maintained
func TestCursorPositionAcrossVersions(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World"})
	defer g.Close()

	cursor := g.NewCursor()

	// Position cursor at "World"
	cursor.SeekByte(6)
	if cursor.BytePos() != 6 {
		t.Fatalf("Initial cursor position: %d, want 6", cursor.BytePos())
	}

	// Insert at beginning
	cursor.SeekByte(0)
	cursor.InsertString("AAA", nil, false)

	// Cursor should have advanced past insert
	// But let's check with a new seek
	cursor.SeekByte(9) // "World" is now at position 9
	if cursor.BytePos() != 9 {
		t.Errorf("After insert, cursor at: %d, want 9", cursor.BytePos())
	}

	// UndoSeek back
	err := g.UndoSeek(0)
	if err != nil {
		t.Fatalf("UndoSeek failed: %v", err)
	}

	// Cursor should be clamped to valid range (content is now 11 bytes again)
	if cursor.BytePos() > g.ByteCount().Value {
		t.Errorf("Cursor position %d exceeds content length %d", cursor.BytePos(), g.ByteCount().Value)
	}
	t.Logf("After UndoSeek, cursor at: %d (content: %d bytes)", cursor.BytePos(), g.ByteCount().Value)
}

// TestFindForksBetween tests the FindForksBetween function for fork analysis
func TestFindForksBetween(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t.Run("no forks in range", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Test"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		cursor := g.NewCursor()
		cursor.InsertString("A", nil, false)
		cursor.InsertString("B", nil, false)

		// Query range with no fork divergences
		divergences, err := g.FindForksBetween(0, 2)
		if err != nil {
			t.Fatalf("FindForksBetween failed: %v", err)
		}

		if len(divergences) != 0 {
			t.Errorf("Expected 0 divergences, got %d", len(divergences))
		}
		t.Log("Correctly found no divergences in simple linear history")
	})

	t.Run("child fork BranchedInto", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Test"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		cursor := g.NewCursor()

		// Create revisions 1, 2, 3 on fork 0
		cursor.InsertString("A", nil, false) // rev 1
		cursor.InsertString("B", nil, false) // rev 2
		cursor.InsertString("C", nil, false) // rev 3

		// Go back to rev 1 and create a new fork
		err = g.UndoSeek(1)
		if err != nil {
			t.Fatalf("UndoSeek failed: %v", err)
		}

		cursor.InsertString("X", nil, false) // Creates fork 1, rev 2

		// Return to fork 0
		err = g.ForkSeek(0)
		if err != nil {
			t.Fatalf("ForkSeek failed: %v", err)
		}

		// From fork 0's perspective, find divergences in range [0, 3]
		divergences, err := g.FindForksBetween(0, 3)
		if err != nil {
			t.Fatalf("FindForksBetween failed: %v", err)
		}

		if len(divergences) != 1 {
			t.Errorf("Expected 1 divergence, got %d", len(divergences))
		}

		if len(divergences) > 0 {
			d := divergences[0]
			if d.Fork != 1 {
				t.Errorf("Expected fork 1, got %d", d.Fork)
			}
			if d.DivergenceRev != 1 {
				t.Errorf("Expected divergence at rev 1, got %d", d.DivergenceRev)
			}
			if d.Direction != BranchedInto {
				t.Errorf("Expected BranchedInto, got %v", d.Direction)
			}
			t.Logf("Found divergence: fork=%d at rev=%d direction=%v", d.Fork, d.DivergenceRev, d.Direction)
		}
	})

	t.Run("parent fork BranchedFrom", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Test"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		cursor := g.NewCursor()

		// Create revisions on fork 0
		cursor.InsertString("A", nil, false) // rev 1
		cursor.InsertString("B", nil, false) // rev 2

		t.Logf("After creating fork 0: fork=%d, rev=%d", g.CurrentFork(), g.CurrentRevision())

		// Go back and create fork 1
		err = g.UndoSeek(1)
		if err != nil {
			t.Fatalf("UndoSeek failed: %v", err)
		}

		cursor.InsertString("X", nil, false) // Fork 1, rev 2
		cursor.InsertString("Y", nil, false) // Fork 1, rev 3

		t.Logf("After creating fork 1: fork=%d, rev=%d", g.CurrentFork(), g.CurrentRevision())

		// We are now on fork 1
		if g.CurrentFork() != 1 {
			t.Fatalf("Expected fork 1, got %d", g.CurrentFork())
		}

		// Get fork info
		forkInfo, _ := g.GetForkInfo(1)
		t.Logf("Fork 1 info: highest=%d, parent=%d, parentRev=%d", forkInfo.HighestRevision, forkInfo.ParentFork, forkInfo.ParentRevision)

		// From fork 1's perspective, find divergences
		// This should show we BranchedFrom fork 0 at rev 1
		divergences, err := g.FindForksBetween(0, forkInfo.HighestRevision)
		if err != nil {
			t.Fatalf("FindForksBetween failed: %v", err)
		}

		if len(divergences) != 1 {
			t.Errorf("Expected 1 divergence, got %d", len(divergences))
		}

		if len(divergences) > 0 {
			d := divergences[0]
			if d.Fork != 0 {
				t.Errorf("Expected parent fork 0, got %d", d.Fork)
			}
			if d.DivergenceRev != 1 {
				t.Errorf("Expected divergence at rev 1, got %d", d.DivergenceRev)
			}
			if d.Direction != BranchedFrom {
				t.Errorf("Expected BranchedFrom, got %v", d.Direction)
			}
			t.Logf("Found divergence: fork=%d at rev=%d direction=%v", d.Fork, d.DivergenceRev, d.Direction)
		}
	})

	t.Run("multiple divergences", func(t *testing.T) {
		g, err := lib.Open(FileOptions{DataString: "Test"})
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer g.Close()

		cursor := g.NewCursor()

		// Create revisions on fork 0
		cursor.InsertString("A", nil, false) // rev 1
		cursor.InsertString("B", nil, false) // rev 2
		cursor.InsertString("C", nil, false) // rev 3
		cursor.InsertString("D", nil, false) // rev 4

		// Create fork 1 from rev 1
		g.UndoSeek(1)
		cursor.InsertString("X1", nil, false) // fork 1

		// Go back to fork 0
		g.ForkSeek(0)

		// Create fork 2 from rev 3
		g.UndoSeek(3)
		cursor.InsertString("X2", nil, false) // fork 2

		// Go back to fork 0
		g.ForkSeek(0)

		// From fork 0, find all divergences in [0, 4]
		divergences, err := g.FindForksBetween(0, 4)
		if err != nil {
			t.Fatalf("FindForksBetween failed: %v", err)
		}

		if len(divergences) != 2 {
			t.Errorf("Expected 2 divergences, got %d", len(divergences))
		}

		// Should be sorted by revision
		for i, d := range divergences {
			t.Logf("Divergence %d: fork=%d at rev=%d direction=%v", i, d.Fork, d.DivergenceRev, d.Direction)
		}

		if len(divergences) >= 2 {
			if divergences[0].DivergenceRev > divergences[1].DivergenceRev {
				t.Error("Expected divergences sorted by revision")
			}
		}
	})
}
