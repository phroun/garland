package garland

import (
	"path/filepath"
	"testing"
)

func TestChillMemoryOnlyNoOp(t *testing.T) {
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{
		DataString:   "Hello World",
		LoadingStyle: MemoryOnly,
	})
	defer g.Close()

	// Chill should be a no-op for MemoryOnly files
	err := g.Chill(ChillEverything)
	if err != nil {
		t.Errorf("Chill on MemoryOnly returned error: %v", err)
	}

	// Data should still be accessible
	cursor := g.NewCursor()
	data, _ := cursor.ReadBytes(11)
	if string(data) != "Hello World" {
		t.Errorf("After Chill: %q, want %q", string(data), "Hello World")
	}
}

func TestChillNoColdStorage(t *testing.T) {
	// Init without cold storage
	lib, _ := Init(LibraryOptions{})
	g, _ := lib.Open(FileOptions{DataString: "Hello World"})
	defer g.Close()

	// Chill should be a no-op without cold storage
	err := g.Chill(ChillEverything)
	if err != nil {
		t.Errorf("Chill without cold storage returned error: %v", err)
	}

	// Data should still be accessible
	cursor := g.NewCursor()
	data, _ := cursor.ReadBytes(11)
	if string(data) != "Hello World" {
		t.Errorf("After Chill: %q, want %q", string(data), "Hello World")
	}
}

func TestChillWithColdStorage(t *testing.T) {
	tmpDir := t.TempDir()
	lib, _ := Init(LibraryOptions{ColdStoragePath: tmpDir})

	g, _ := lib.Open(FileOptions{DataString: "Hello World"})
	defer g.Close()

	t.Logf("After open: rev=%d, bytes=%d, nodes=%d", g.currentRevision, g.totalBytes, len(g.nodeRegistry))

	// Make some edits to create multiple revisions
	cursor := g.NewCursor()
	cursor.SeekByte(5)
	cursor.InsertString(" Beautiful", nil, true)

	t.Logf("After insert: rev=%d, bytes=%d, nodes=%d", g.currentRevision, g.totalBytes, len(g.nodeRegistry))

	// Log nodes before chill
	for id, node := range g.nodeRegistry {
		for forkRev, snap := range node.history {
			if snap.isLeaf && snap.byteCount > 0 {
				t.Logf("  Node %d @ {%d,%d}: %d bytes, data=%q, storage=%d",
					id, forkRev.Fork, forkRev.Revision, snap.byteCount, string(snap.data), snap.storageState)
			}
		}
	}

	// Chill unused data
	err := g.Chill(ChillUnusedData)
	if err != nil {
		t.Errorf("Chill returned error: %v", err)
	}

	t.Logf("After chill:")
	for id, node := range g.nodeRegistry {
		for forkRev, snap := range node.history {
			if snap.isLeaf && snap.byteCount > 0 {
				t.Logf("  Node %d @ {%d,%d}: %d bytes, data=%q, storage=%d",
					id, forkRev.Fork, forkRev.Revision, snap.byteCount, string(snap.data), snap.storageState)
			}
		}
	}

	// Current data should still be accessible
	cursor.SeekByte(0)
	data, err := cursor.ReadBytes(21)
	if err != nil {
		t.Fatalf("ReadBytes after chill failed: %v", err)
	}
	if string(data) != "Hello Beautiful World" {
		t.Errorf("After Chill: %q, want %q", string(data), "Hello Beautiful World")
	}
}

func TestChillEverything(t *testing.T) {
	tmpDir := t.TempDir()
	lib, _ := Init(LibraryOptions{ColdStoragePath: tmpDir})

	g, _ := lib.Open(FileOptions{DataString: "Test data for chilling"})
	defer g.Close()

	// Chill everything
	err := g.Chill(ChillEverything)
	if err != nil {
		t.Errorf("ChillEverything returned error: %v", err)
	}

	// Verify cold storage has files
	files, _ := filepath.Glob(filepath.Join(tmpDir, g.id, "*"))
	if len(files) == 0 {
		t.Error("Expected cold storage files to be created")
	}
}

func TestChillLevels(t *testing.T) {
	tmpDir := t.TempDir()
	lib, _ := Init(LibraryOptions{ColdStoragePath: tmpDir})

	g, _ := lib.Open(FileOptions{DataString: "Initial content"})
	defer g.Close()

	// Create multiple revisions
	cursor := g.NewCursor()
	for i := 0; i < 15; i++ {
		cursor.SeekByte(0)
		cursor.InsertString("X", nil, true)
	}

	// Test ChillInactiveForks - should be gentle
	err := g.Chill(ChillInactiveForks)
	if err != nil {
		t.Errorf("ChillInactiveForks error: %v", err)
	}

	// Test ChillOldHistory - should chill older revisions
	err = g.Chill(ChillOldHistory)
	if err != nil {
		t.Errorf("ChillOldHistory error: %v", err)
	}

	// Test ChillUnusedData - more aggressive
	err = g.Chill(ChillUnusedData)
	if err != nil {
		t.Errorf("ChillUnusedData error: %v", err)
	}

	// Test ChillEverything - most aggressive
	err = g.Chill(ChillEverything)
	if err != nil {
		t.Errorf("ChillEverything error: %v", err)
	}
}

func TestChillLevelConstants(t *testing.T) {
	// Verify constants are distinct
	levels := []ChillLevel{ChillInactiveForks, ChillOldHistory, ChillUnusedData, ChillEverything}
	seen := make(map[ChillLevel]bool)
	for _, level := range levels {
		if seen[level] {
			t.Errorf("Duplicate ChillLevel value: %d", level)
		}
		seen[level] = true
	}

	// Verify ordering (increasing aggressiveness)
	if ChillInactiveForks >= ChillOldHistory {
		t.Error("ChillInactiveForks should be less than ChillOldHistory")
	}
	if ChillOldHistory >= ChillUnusedData {
		t.Error("ChillOldHistory should be less than ChillUnusedData")
	}
	if ChillUnusedData >= ChillEverything {
		t.Error("ChillUnusedData should be less than ChillEverything")
	}
}
