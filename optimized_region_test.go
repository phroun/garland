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
