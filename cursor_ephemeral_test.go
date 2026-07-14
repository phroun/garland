package garland

import "testing"

// TestEphemeralCursorNoHistory: an ephemeral cursor adjusts to edits
// but accrues no per-revision history and is never teleported to a
// historical position on undo - unlike a tracked cursor.
func TestEphemeralCursorNoHistory(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	tracked := g.NewCursor()
	ephemeral := g.NewEphemeralCursor()
	if ephemeral.TracksHistory() {
		t.Fatal("ephemeral cursor reports TracksHistory() = true")
	}
	if len(ephemeral.positionHistory) != 0 {
		t.Fatalf("ephemeral cursor allocated %d history entries at birth", len(ephemeral.positionHistory))
	}

	// Both sit at byte 5 in rev 0.
	if err := tracked.SeekByte(5); err != nil {
		t.Fatal(err)
	}
	if err := ephemeral.SeekByte(5); err != nil {
		t.Fatal(err)
	}

	// A mutation creates rev 1 and shifts both cursors past the insert.
	edit := g.NewCursor()
	if err := edit.SeekByte(0); err != nil {
		t.Fatal(err)
	}
	if _, err := edit.InsertString("ABC", nil, false); err != nil {
		t.Fatal(err)
	}
	if tracked.BytePos() != 8 || ephemeral.BytePos() != 8 {
		t.Fatalf("post-insert positions: tracked=%d ephemeral=%d, want 8/8 (both adjust to edits)",
			tracked.BytePos(), ephemeral.BytePos())
	}

	// The ephemeral cursor recorded NOTHING; the tracked one did.
	if len(ephemeral.positionHistory) != 0 {
		t.Fatalf("ephemeral cursor accrued %d history entries after an edit", len(ephemeral.positionHistory))
	}
	if len(tracked.positionHistory) == 0 {
		t.Fatal("tracked cursor recorded no history")
	}

	// Move both to 2, then undo to rev 0. The tracked cursor is
	// restored to its recorded rev-0 position (5); the ephemeral one
	// is NOT teleported - it keeps its live position (clamped).
	if err := tracked.SeekByte(2); err != nil {
		t.Fatal(err)
	}
	if err := ephemeral.SeekByte(2); err != nil {
		t.Fatal(err)
	}
	if err := g.UndoSeek(0); err != nil {
		t.Fatalf("UndoSeek(0): %v", err)
	}
	if tracked.BytePos() != 5 {
		t.Errorf("tracked cursor not restored to rev-0 position: got %d, want 5", tracked.BytePos())
	}
	if ephemeral.BytePos() != 2 {
		t.Errorf("ephemeral cursor was teleported on undo: got %d, want 2 (kept live position)", ephemeral.BytePos())
	}

	// SetTracksHistory(false) drops accumulated history and stops recording.
	tracked.SetTracksHistory(false)
	if len(tracked.positionHistory) != 0 {
		t.Fatal("SetTracksHistory(false) did not drop accumulated history")
	}
}
