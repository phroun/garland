package garland

import (
	"testing"
)

func TestInit(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if lib == nil {
		t.Fatal("Init returned nil library")
	}
}

func TestOpenWithString(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{
		DataString: "Hello, World!",
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if g == nil {
		t.Fatal("Open returned nil garland")
	}
	defer g.Close()

	// Check counts
	bc := g.ByteCount()
	if bc.Value != 13 {
		t.Errorf("Expected 13 bytes, got %d", bc.Value)
	}
	if !bc.Complete {
		t.Error("Expected count to be complete")
	}
}

func TestOpenWithBytes(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	data := []byte("Hello\nWorld\n")
	g, err := lib.Open(FileOptions{
		DataBytes: data,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	// Check counts
	bc := g.ByteCount()
	if bc.Value != 12 {
		t.Errorf("Expected 12 bytes, got %d", bc.Value)
	}

	rc := g.RuneCount()
	if rc.Value != 12 {
		t.Errorf("Expected 12 runes, got %d", rc.Value)
	}

	lc := g.LineCount()
	if lc.Value != 2 {
		t.Errorf("Expected 2 newlines, got %d", lc.Value)
	}
}

func TestCursor(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{
		DataString: "Hello",
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	c := g.NewCursor()
	if c == nil {
		t.Fatal("NewCursor returned nil")
	}

	if c.BytePos() != 0 {
		t.Errorf("Expected byte pos 0, got %d", c.BytePos())
	}

	// Remove cursor
	err = g.RemoveCursor(c)
	if err != nil {
		t.Errorf("RemoveCursor failed: %v", err)
	}

	// Removing again should fail
	err = g.RemoveCursor(c)
	if err != ErrCursorNotFound {
		t.Errorf("Expected ErrCursorNotFound, got %v", err)
	}
}

func TestTransaction(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{
		DataString: "Hello",
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	// Not in transaction
	if g.InTransaction() {
		t.Error("Should not be in transaction")
	}
	if g.TransactionDepth() != 0 {
		t.Error("Transaction depth should be 0")
	}

	// Start transaction
	err = g.TransactionStart("Test transaction")
	if err != nil {
		t.Fatalf("TransactionStart failed: %v", err)
	}

	if !g.InTransaction() {
		t.Error("Should be in transaction")
	}
	if g.TransactionDepth() != 1 {
		t.Error("Transaction depth should be 1")
	}

	// Nested transaction
	err = g.TransactionStart("Nested")
	if err != nil {
		t.Fatalf("Nested TransactionStart failed: %v", err)
	}

	if g.TransactionDepth() != 2 {
		t.Error("Transaction depth should be 2")
	}

	// Inner commit
	_, err = g.TransactionCommit()
	if err != nil {
		t.Fatalf("Inner commit failed: %v", err)
	}

	if g.TransactionDepth() != 1 {
		t.Error("Transaction depth should be 1 after inner commit")
	}

	// Outer commit
	result, err := g.TransactionCommit()
	if err != nil {
		t.Fatalf("Outer commit failed: %v", err)
	}

	if g.InTransaction() {
		t.Error("Should not be in transaction after commit")
	}

	// Check revision info
	info, err := g.GetRevisionInfo(result.Revision)
	if err != nil {
		t.Fatalf("GetRevisionInfo failed: %v", err)
	}
	if info.Name != "Test transaction" {
		t.Errorf("Expected name 'Test transaction', got '%s'", info.Name)
	}
	if info.HasChanges {
		t.Error("HasChanges should be false for empty transaction")
	}
}

func TestTransactionRollback(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{
		DataString: "Hello",
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	initialRev := g.CurrentRevision()

	// Start transaction and rollback
	g.TransactionStart("To rollback")
	err = g.TransactionRollback()
	if err != nil {
		t.Fatalf("TransactionRollback failed: %v", err)
	}

	if g.InTransaction() {
		t.Error("Should not be in transaction after rollback")
	}
	if g.CurrentRevision() != initialRev {
		t.Error("Revision should not change after rollback")
	}
}

func TestPoisonedTransaction(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{
		DataString: "Hello",
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	initialRev := g.CurrentRevision()

	// Outer transaction
	g.TransactionStart("Outer")

	// Inner transaction that rolls back
	g.TransactionStart("Inner")
	g.TransactionRollback() // This poisons the outer transaction

	// Outer commit should fail
	_, err = g.TransactionCommit()
	if err != ErrTransactionPoisoned {
		t.Errorf("Expected ErrTransactionPoisoned, got %v", err)
	}

	if g.InTransaction() {
		t.Error("Should not be in transaction after poisoned commit")
	}
	if g.CurrentRevision() != initialRev {
		t.Error("Revision should not change after poisoned transaction")
	}
}

func TestNodeSnapshot(t *testing.T) {
	data := []byte("Hello\nWorld")
	snap := createLeafSnapshot(data, nil, 0)

	if snap.ByteCount() != 11 {
		t.Errorf("Expected 11 bytes, got %d", snap.ByteCount())
	}

	if snap.RuneCount() != 11 {
		t.Errorf("Expected 11 runes, got %d", snap.RuneCount())
	}

	if snap.LineCount() != 1 {
		t.Errorf("Expected 1 newline, got %d", snap.LineCount())
	}

	if len(snap.lineStarts) != 2 {
		t.Errorf("Expected 2 line starts, got %d", len(snap.lineStarts))
	}
}

func TestPartitionDecorations(t *testing.T) {
	decorations := []Decoration{
		{Key: "a", Position: 5},
		{Key: "b", Position: 10},
		{Key: "c", Position: 15},
	}

	left, right := partitionDecorations(decorations, 10)

	if len(left) != 1 {
		t.Errorf("Expected 1 left decoration, got %d", len(left))
	}
	if left[0].Key != "a" || left[0].Position != 5 {
		t.Error("Left decoration incorrect")
	}

	if len(right) != 2 {
		t.Errorf("Expected 2 right decorations, got %d", len(right))
	}
	if right[0].Key != "b" || right[0].Position != 0 {
		t.Error("Right decoration 0 incorrect (should be adjusted to 0)")
	}
	if right[1].Key != "c" || right[1].Position != 5 {
		t.Error("Right decoration 1 incorrect (should be adjusted to 5)")
	}
}

func TestAbsoluteAddress(t *testing.T) {
	ba := ByteAddress(100)
	if ba.Mode != ByteMode || ba.Byte != 100 {
		t.Error("ByteAddress incorrect")
	}

	ra := RuneAddress(50)
	if ra.Mode != RuneMode || ra.Rune != 50 {
		t.Error("RuneAddress incorrect")
	}

	la := LineAddress(10, 5)
	if la.Mode != LineRuneMode || la.Line != 10 || la.LineRune != 5 {
		t.Error("LineAddress incorrect")
	}
}
