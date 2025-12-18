package garland

import (
	"testing"
)

func TestByteBufferRegion(t *testing.T) {
	t.Run("basic operations", func(t *testing.T) {
		r := NewByteBufferRegion([]byte("Hello World"))

		if r.ByteCount() != 11 {
			t.Errorf("ByteCount() = %d, want 11", r.ByteCount())
		}
		if r.RuneCount() != 11 {
			t.Errorf("RuneCount() = %d, want 11", r.RuneCount())
		}
		if r.LineCount() != 0 {
			t.Errorf("LineCount() = %d, want 0", r.LineCount())
		}

		// Test read
		data, err := r.ReadBytes(0, 5)
		if err != nil {
			t.Fatalf("ReadBytes failed: %v", err)
		}
		if string(data) != "Hello" {
			t.Errorf("ReadBytes(0, 5) = %q, want %q", data, "Hello")
		}

		// Test content
		content := r.Content()
		if string(content) != "Hello World" {
			t.Errorf("Content() = %q, want %q", content, "Hello World")
		}
	})

	t.Run("insert", func(t *testing.T) {
		r := NewByteBufferRegion([]byte("Hello World"))

		// Insert in middle
		err := r.InsertBytes(5, []byte(" Beautiful"))
		if err != nil {
			t.Fatalf("InsertBytes failed: %v", err)
		}

		if string(r.Content()) != "Hello Beautiful World" {
			t.Errorf("Content() = %q, want %q", r.Content(), "Hello Beautiful World")
		}
		if r.ByteCount() != 21 {
			t.Errorf("ByteCount() = %d, want 21", r.ByteCount())
		}
	})

	t.Run("delete", func(t *testing.T) {
		r := NewByteBufferRegion([]byte("Hello Beautiful World"))

		// Delete "Beautiful "
		err := r.DeleteBytes(6, 10)
		if err != nil {
			t.Fatalf("DeleteBytes failed: %v", err)
		}

		if string(r.Content()) != "Hello World" {
			t.Errorf("Content() = %q, want %q", r.Content(), "Hello World")
		}
	})

	t.Run("line counting", func(t *testing.T) {
		r := NewByteBufferRegion([]byte("line1\nline2\nline3"))

		if r.LineCount() != 2 {
			t.Errorf("LineCount() = %d, want 2", r.LineCount())
		}

		// Insert newline
		r.InsertBytes(17, []byte("\nline4"))
		if r.LineCount() != 3 {
			t.Errorf("After insert, LineCount() = %d, want 3", r.LineCount())
		}

		// Delete a line
		r.DeleteBytes(0, 6) // Delete "line1\n"
		if r.LineCount() != 2 {
			t.Errorf("After delete, LineCount() = %d, want 2", r.LineCount())
		}
	})

	t.Run("unicode", func(t *testing.T) {
		r := NewByteBufferRegion([]byte("héllo 世界"))

		if r.RuneCount() != 8 { // h é l l o space 世 界
			t.Errorf("RuneCount() = %d, want 8", r.RuneCount())
		}

		// Insert unicode
		r.InsertBytes(0, []byte("🎉 "))
		if r.RuneCount() != 10 { // emoji + space + original 8
			t.Errorf("After insert, RuneCount() = %d, want 10", r.RuneCount())
		}
	})
}

func TestCursorMode(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Default mode should be Human
	if cursor.Mode() != CursorModeHuman {
		t.Errorf("Default mode = %v, want CursorModeHuman", cursor.Mode())
	}

	// Change to Process
	cursor.SetMode(CursorModeProcess)
	if cursor.Mode() != CursorModeProcess {
		t.Errorf("After SetMode, mode = %v, want CursorModeProcess", cursor.Mode())
	}
}

func TestCursorOptimizedRegionSerial(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// No region initially
	if cursor.OptimizedRegionSerial() != -1 {
		t.Errorf("Initial serial = %d, want -1", cursor.OptimizedRegionSerial())
	}

	if cursor.HasOptimizedRegion() {
		t.Error("HasOptimizedRegion() should be false initially")
	}
}

func TestBeginOptimizedRegion(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Explicitly begin a region
	err = cursor.BeginOptimizedRegion(0, 5)
	if err != nil {
		t.Fatalf("BeginOptimizedRegion failed: %v", err)
	}

	if !cursor.HasOptimizedRegion() {
		t.Error("HasOptimizedRegion() should be true after BeginOptimizedRegion")
	}

	serial := cursor.OptimizedRegionSerial()
	if serial == -1 {
		t.Error("OptimizedRegionSerial() should not be -1 after BeginOptimizedRegion")
	}

	// Check bounds
	start, end, ok := cursor.OptimizedRegionBounds()
	if !ok {
		t.Error("OptimizedRegionBounds() should return ok=true")
	}
	if start != 0 || end != 5 {
		t.Errorf("OptimizedRegionBounds() = (%d, %d), want (0, 5)", start, end)
	}

	// Begin another region (should dissolve the first)
	err = cursor.BeginOptimizedRegion(5, 11)
	if err != nil {
		t.Fatalf("Second BeginOptimizedRegion failed: %v", err)
	}

	newSerial := cursor.OptimizedRegionSerial()
	if newSerial == serial {
		t.Error("New region should have different serial")
	}
}

func TestCheckpoint(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Begin a region
	err = cursor.BeginOptimizedRegion(0, 5)
	if err != nil {
		t.Fatalf("BeginOptimizedRegion failed: %v", err)
	}

	if !cursor.HasOptimizedRegion() {
		t.Error("Should have region after BeginOptimizedRegion")
	}

	// Checkpoint should dissolve it
	err = g.Checkpoint()
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	if cursor.HasOptimizedRegion() {
		t.Error("Should not have region after Checkpoint")
	}
}

func TestDecorationAdjustment(t *testing.T) {
	handle := &OptimizedRegionHandle{
		serial:       1,
		graceStart:   0,
		graceEnd:     100,
		contentStart: 0,
		buffer:       NewByteBufferRegion([]byte("Hello World")),
		decorations: []Decoration{
			{Key: "a", Position: 0},
			{Key: "b", Position: 5},
			{Key: "c", Position: 10},
		},
	}

	// Insert at position 5 with insertBefore=true
	handle.adjustDecorationsForInsert(5, 3, true)

	// "a" at 0 should stay
	// "b" at 5 should move to 8 (insertBefore=true means it moves)
	// "c" at 10 should move to 13
	expected := map[string]int64{"a": 0, "b": 8, "c": 13}
	for _, d := range handle.decorations {
		if want, ok := expected[d.Key]; ok {
			if d.Position != want {
				t.Errorf("Decoration %q position = %d, want %d", d.Key, d.Position, want)
			}
		}
	}
}

func TestDecorationAdjustmentInsertAfter(t *testing.T) {
	handle := &OptimizedRegionHandle{
		serial:       1,
		graceStart:   0,
		graceEnd:     100,
		contentStart: 0,
		buffer:       NewByteBufferRegion([]byte("Hello World")),
		decorations: []Decoration{
			{Key: "a", Position: 0},
			{Key: "b", Position: 5},
			{Key: "c", Position: 10},
		},
	}

	// Insert at position 5 with insertBefore=false
	handle.adjustDecorationsForInsert(5, 3, false)

	// "a" at 0 should stay
	// "b" at 5 should stay (insertBefore=false means it doesn't move)
	// "c" at 10 should move to 13
	expected := map[string]int64{"a": 0, "b": 5, "c": 13}
	for _, d := range handle.decorations {
		if want, ok := expected[d.Key]; ok {
			if d.Position != want {
				t.Errorf("Decoration %q position = %d, want %d", d.Key, d.Position, want)
			}
		}
	}
}

func TestDecorationAdjustmentDelete(t *testing.T) {
	handle := &OptimizedRegionHandle{
		serial:       1,
		graceStart:   0,
		graceEnd:     100,
		contentStart: 0,
		buffer:       NewByteBufferRegion([]byte("Hello World")),
		decorations: []Decoration{
			{Key: "a", Position: 0},
			{Key: "b", Position: 5},
			{Key: "c", Position: 10},
		},
	}

	// Delete from position 3 to 7 (4 bytes)
	removed := handle.adjustDecorationsForDelete(3, 4)

	// "a" at 0 should stay
	// "b" at 5 should be removed (in deleted range 3-7)
	// "c" at 10 should move to 6
	if len(removed) != 1 || removed[0].Key != "b" {
		t.Errorf("Removed decorations = %v, want [{b 5}]", removed)
	}

	expected := map[string]int64{"a": 0, "c": 6}
	for _, d := range handle.decorations {
		if want, ok := expected[d.Key]; ok {
			if d.Position != want {
				t.Errorf("Decoration %q position = %d, want %d", d.Key, d.Position, want)
			}
		}
	}

	if len(handle.decorations) != 2 {
		t.Errorf("Remaining decorations = %d, want 2", len(handle.decorations))
	}
}

func TestRegionSerialIncrement(t *testing.T) {
	// Get two serials and verify they increment
	s1 := nextRegionSerial()
	s2 := nextRegionSerial()

	if s2 <= s1 {
		t.Errorf("Serial numbers should increment: s1=%d, s2=%d", s1, s2)
	}
}

// Integration tests

func TestTransactionStartCheckpointsRegions(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Begin a region
	err = cursor.BeginOptimizedRegion(0, 5)
	if err != nil {
		t.Fatalf("BeginOptimizedRegion failed: %v", err)
	}

	if !cursor.HasOptimizedRegion() {
		t.Fatal("Should have region after BeginOptimizedRegion")
	}

	// TransactionStart should checkpoint (dissolve) the region
	err = g.TransactionStart("test")
	if err != nil {
		t.Fatalf("TransactionStart failed: %v", err)
	}

	if cursor.HasOptimizedRegion() {
		t.Error("TransactionStart should have dissolved the region")
	}

	g.TransactionCommit()
}

func TestTransactionCommitDissolvesRegions(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()
	cursor.SetMode(CursorModeProcess) // Process mode to avoid auto-region on transaction start

	// Start transaction first
	err = g.TransactionStart("test")
	if err != nil {
		t.Fatalf("TransactionStart failed: %v", err)
	}

	// Explicitly begin a region inside the transaction
	err = cursor.BeginOptimizedRegion(0, 5)
	if err != nil {
		t.Fatalf("BeginOptimizedRegion failed: %v", err)
	}

	if !cursor.HasOptimizedRegion() {
		t.Fatal("Should have region after BeginOptimizedRegion")
	}

	// Commit should dissolve the region
	_, err = g.TransactionCommit()
	if err != nil {
		t.Fatalf("TransactionCommit failed: %v", err)
	}

	if cursor.HasOptimizedRegion() {
		t.Error("TransactionCommit should have dissolved the region")
	}
}

func TestTransactionRollbackDiscardsRegions(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()
	cursor.SetMode(CursorModeProcess)

	// Start transaction
	err = g.TransactionStart("test")
	if err != nil {
		t.Fatalf("TransactionStart failed: %v", err)
	}

	// Begin a region
	err = cursor.BeginOptimizedRegion(0, 5)
	if err != nil {
		t.Fatalf("BeginOptimizedRegion failed: %v", err)
	}

	// Rollback should discard the region
	err = g.TransactionRollback()
	if err != nil {
		t.Fatalf("TransactionRollback failed: %v", err)
	}

	if cursor.HasOptimizedRegion() {
		t.Error("TransactionRollback should have discarded the region")
	}
}

func TestMultipleCursorsIndependentRegions(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World Test Data"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor1 := g.NewCursor()
	cursor2 := g.NewCursor()

	// Both start in Human mode
	if cursor1.Mode() != CursorModeHuman || cursor2.Mode() != CursorModeHuman {
		t.Error("Cursors should default to Human mode")
	}

	// Begin regions on both cursors
	err = cursor1.BeginOptimizedRegion(0, 5)
	if err != nil {
		t.Fatalf("cursor1.BeginOptimizedRegion failed: %v", err)
	}

	err = cursor2.BeginOptimizedRegion(10, 15)
	if err != nil {
		t.Fatalf("cursor2.BeginOptimizedRegion failed: %v", err)
	}

	// Both should have independent regions
	if !cursor1.HasOptimizedRegion() || !cursor2.HasOptimizedRegion() {
		t.Error("Both cursors should have regions")
	}

	// Serial numbers should be different
	if cursor1.OptimizedRegionSerial() == cursor2.OptimizedRegionSerial() {
		t.Error("Region serial numbers should be different")
	}

	// Bounds should be independent
	s1, e1, _ := cursor1.OptimizedRegionBounds()
	s2, e2, _ := cursor2.OptimizedRegionBounds()

	if s1 != 0 || e1 != 5 {
		t.Errorf("cursor1 bounds = (%d, %d), want (0, 5)", s1, e1)
	}
	if s2 != 10 || e2 != 15 {
		t.Errorf("cursor2 bounds = (%d, %d), want (10, 15)", s2, e2)
	}

	// Checkpoint should dissolve both
	err = g.Checkpoint()
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	if cursor1.HasOptimizedRegion() || cursor2.HasOptimizedRegion() {
		t.Error("Checkpoint should have dissolved both regions")
	}
}

func TestGraceWindowBounds(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World Test Data Here"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Begin region at position 10-15
	err = cursor.BeginOptimizedRegion(10, 15)
	if err != nil {
		t.Fatalf("BeginOptimizedRegion failed: %v", err)
	}

	// Check grace window is larger than content bounds
	graceStart, graceEnd, ok := cursor.OptimizedRegionGraceWindow()
	if !ok {
		t.Fatal("Should have grace window")
	}

	contentStart, contentEnd, _ := cursor.OptimizedRegionBounds()

	// Grace window should encompass content bounds
	if graceStart > contentStart || graceEnd < contentEnd {
		t.Errorf("Grace window (%d, %d) should encompass content bounds (%d, %d)",
			graceStart, graceEnd, contentStart, contentEnd)
	}

	// Grace window should be larger (by graceWindowSize)
	if graceEnd-graceStart <= contentEnd-contentStart {
		t.Error("Grace window should be larger than content bounds")
	}
}

func TestRegionContentPreservation(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Create region covering "Hello"
	err = cursor.BeginOptimizedRegion(0, 5)
	if err != nil {
		t.Fatalf("BeginOptimizedRegion failed: %v", err)
	}

	// Read original content
	originalBytes := g.ByteCount().Value

	// Checkpoint to dissolve back to tree
	err = g.Checkpoint()
	if err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	// Verify content is preserved
	afterBytes := g.ByteCount().Value

	if afterBytes != originalBytes {
		t.Errorf("ByteCount after checkpoint = %d, want %d", afterBytes, originalBytes)
	}

	// Read actual content
	data, err := g.readBytesAt(0, afterBytes)
	if err != nil {
		t.Fatalf("readBytesAt failed: %v", err)
	}

	if string(data) != "Hello World" {
		t.Errorf("Content after checkpoint = %q, want %q", data, "Hello World")
	}
}

func TestRegionBeginDissolvesExisting(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World Test"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()

	// Begin first region
	err = cursor.BeginOptimizedRegion(0, 5)
	if err != nil {
		t.Fatalf("First BeginOptimizedRegion failed: %v", err)
	}

	firstSerial := cursor.OptimizedRegionSerial()

	// Begin second region (should dissolve first)
	err = cursor.BeginOptimizedRegion(10, 15)
	if err != nil {
		t.Fatalf("Second BeginOptimizedRegion failed: %v", err)
	}

	secondSerial := cursor.OptimizedRegionSerial()

	// Should have a new region with different serial
	if secondSerial == firstSerial {
		t.Error("Second region should have different serial")
	}

	// Bounds should be for second region
	start, end, _ := cursor.OptimizedRegionBounds()
	if start != 10 || end != 15 {
		t.Errorf("Bounds = (%d, %d), want (10, 15)", start, end)
	}
}

func TestProcessCursorNoAutoRegion(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{DataString: "Hello World"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer g.Close()

	cursor := g.NewCursor()
	cursor.SetMode(CursorModeProcess)

	// Process cursor should not have region initially
	if cursor.HasOptimizedRegion() {
		t.Error("Process cursor should not have auto-region")
	}

	// Can still explicitly create region
	err = cursor.BeginOptimizedRegion(0, 5)
	if err != nil {
		t.Fatalf("BeginOptimizedRegion failed: %v", err)
	}

	if !cursor.HasOptimizedRegion() {
		t.Error("Process cursor should have region after explicit Begin")
	}
}

func TestByteBufferRegionBoundaryConditions(t *testing.T) {
	t.Run("empty region", func(t *testing.T) {
		r := NewByteBufferRegion([]byte{})
		if r.ByteCount() != 0 {
			t.Errorf("Empty region ByteCount = %d, want 0", r.ByteCount())
		}
		if r.RuneCount() != 0 {
			t.Errorf("Empty region RuneCount = %d, want 0", r.RuneCount())
		}
		if r.LineCount() != 0 {
			t.Errorf("Empty region LineCount = %d, want 0", r.LineCount())
		}

		// Insert into empty
		err := r.InsertBytes(0, []byte("test"))
		if err != nil {
			t.Fatalf("InsertBytes failed: %v", err)
		}
		if string(r.Content()) != "test" {
			t.Errorf("Content = %q, want %q", r.Content(), "test")
		}
	})

	t.Run("insert at start", func(t *testing.T) {
		r := NewByteBufferRegion([]byte("world"))
		err := r.InsertBytes(0, []byte("hello "))
		if err != nil {
			t.Fatalf("InsertBytes failed: %v", err)
		}
		if string(r.Content()) != "hello world" {
			t.Errorf("Content = %q, want %q", r.Content(), "hello world")
		}
	})

	t.Run("insert at end", func(t *testing.T) {
		r := NewByteBufferRegion([]byte("hello"))
		err := r.InsertBytes(5, []byte(" world"))
		if err != nil {
			t.Fatalf("InsertBytes failed: %v", err)
		}
		if string(r.Content()) != "hello world" {
			t.Errorf("Content = %q, want %q", r.Content(), "hello world")
		}
	})

	t.Run("delete all", func(t *testing.T) {
		r := NewByteBufferRegion([]byte("hello"))
		err := r.DeleteBytes(0, 5)
		if err != nil {
			t.Fatalf("DeleteBytes failed: %v", err)
		}
		if r.ByteCount() != 0 {
			t.Errorf("ByteCount = %d, want 0", r.ByteCount())
		}
	})

	t.Run("invalid positions", func(t *testing.T) {
		r := NewByteBufferRegion([]byte("hello"))

		// Insert at negative position
		err := r.InsertBytes(-1, []byte("x"))
		if err != ErrInvalidPosition {
			t.Errorf("InsertBytes(-1) error = %v, want ErrInvalidPosition", err)
		}

		// Insert past end
		err = r.InsertBytes(100, []byte("x"))
		if err != ErrInvalidPosition {
			t.Errorf("InsertBytes(100) error = %v, want ErrInvalidPosition", err)
		}

		// Delete at negative position
		err = r.DeleteBytes(-1, 1)
		if err != ErrInvalidPosition {
			t.Errorf("DeleteBytes(-1) error = %v, want ErrInvalidPosition", err)
		}

		// Delete past end
		err = r.DeleteBytes(0, 100)
		if err != ErrInvalidPosition {
			t.Errorf("DeleteBytes(0, 100) error = %v, want ErrInvalidPosition", err)
		}

		// Read at invalid position
		_, err = r.ReadBytes(-1, 1)
		if err != ErrInvalidPosition {
			t.Errorf("ReadBytes(-1) error = %v, want ErrInvalidPosition", err)
		}

		_, err = r.ReadBytes(0, 100)
		if err != ErrInvalidPosition {
			t.Errorf("ReadBytes(0, 100) error = %v, want ErrInvalidPosition", err)
		}
	})
}
