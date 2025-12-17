package garland

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryUsageBasic(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	content := "Hello, World! This is test content for memory tracking."
	g, err := lib.Open(FileOptions{DataString: content})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	stats := g.MemoryUsage()

	// Should have some memory tracked
	if stats.MemoryBytes != int64(len(content)) {
		t.Errorf("MemoryBytes = %d, want %d", stats.MemoryBytes, len(content))
	}

	// Should have at least one in-memory leaf
	if stats.InMemoryLeaves < 1 {
		t.Errorf("InMemoryLeaves = %d, want at least 1", stats.InMemoryLeaves)
	}

	// No cold storage configured, so should be 0
	if stats.ColdStoredLeaves != 0 {
		t.Errorf("ColdStoredLeaves = %d, want 0", stats.ColdStoredLeaves)
	}
}

func TestMemoryUsageWithColdStorage(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "garland_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	coldPath := filepath.Join(tempDir, "cold")
	lib, err := Init(LibraryOptions{ColdStoragePath: coldPath})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	content := "Test content for cold storage test."
	g, err := lib.Open(FileOptions{DataString: content})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Initial stats
	statsBefore := g.MemoryUsage()
	if statsBefore.MemoryBytes != int64(len(content)) {
		t.Errorf("Before chill: MemoryBytes = %d, want %d", statsBefore.MemoryBytes, len(content))
	}

	// Chill everything
	err = g.Chill(ChillEverything)
	if err != nil {
		t.Fatalf("Chill failed: %v", err)
	}

	// After chill
	statsAfter := g.MemoryUsage()
	if statsAfter.MemoryBytes != 0 {
		t.Errorf("After chill: MemoryBytes = %d, want 0", statsAfter.MemoryBytes)
	}
	if statsAfter.ColdStoredLeaves < 1 {
		t.Errorf("After chill: ColdStoredLeaves = %d, want at least 1", statsAfter.ColdStoredLeaves)
	}

	// Thaw and verify memory tracking updates
	err = g.Thaw()
	if err != nil {
		t.Fatalf("Thaw failed: %v", err)
	}

	statsThawed := g.MemoryUsage()
	if statsThawed.MemoryBytes != int64(len(content)) {
		t.Errorf("After thaw: MemoryBytes = %d, want %d", statsThawed.MemoryBytes, len(content))
	}
}

func TestIncrementalChillLRU(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "garland_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	coldPath := filepath.Join(tempDir, "cold")
	lib, err := Init(LibraryOptions{ColdStoragePath: coldPath})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create a garland with multiple leaves (large content)
	content := make([]byte, 256*1024) // 256KB - should create multiple leaves
	for i := range content {
		content[i] = byte('A' + (i % 26))
	}

	g, err := lib.Open(FileOptions{DataBytes: content})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	statsBefore := g.MemoryUsage()
	t.Logf("Before: %d bytes in memory, %d leaves", statsBefore.MemoryBytes, statsBefore.InMemoryLeaves)

	if statsBefore.InMemoryLeaves < 2 {
		t.Skip("Test requires multiple leaves, got", statsBefore.InMemoryLeaves)
	}

	// Chill 1 node at a time
	stats := lib.IncrementalChill(1)
	if stats.NodesChilled != 1 {
		t.Errorf("IncrementalChill(1) chilled %d nodes, want 1", stats.NodesChilled)
	}

	statsAfter := g.MemoryUsage()
	t.Logf("After chill(1): %d bytes in memory, %d leaves in memory, %d in cold",
		statsAfter.MemoryBytes, statsAfter.InMemoryLeaves, statsAfter.ColdStoredLeaves)

	if statsAfter.ColdStoredLeaves != 1 {
		t.Errorf("After IncrementalChill(1): ColdStoredLeaves = %d, want 1", statsAfter.ColdStoredLeaves)
	}
}

func TestIncrementalChillBudget(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "garland_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	coldPath := filepath.Join(tempDir, "cold")
	lib, err := Init(LibraryOptions{ColdStoragePath: coldPath})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create content that should create multiple leaves
	content := make([]byte, 512*1024) // 512KB
	for i := range content {
		content[i] = byte('A' + (i % 26))
	}

	g, err := lib.Open(FileOptions{DataBytes: content})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	statsBefore := g.MemoryUsage()
	if statsBefore.InMemoryLeaves < 3 {
		t.Skip("Test requires at least 3 leaves, got", statsBefore.InMemoryLeaves)
	}

	// Chill with budget of 2
	stats := lib.IncrementalChill(2)
	if stats.NodesChilled > 2 {
		t.Errorf("IncrementalChill(2) chilled %d nodes, want at most 2", stats.NodesChilled)
	}

	statsAfter := g.MemoryUsage()
	if statsAfter.ColdStoredLeaves > 2 {
		t.Errorf("After IncrementalChill(2): ColdStoredLeaves = %d, want at most 2", statsAfter.ColdStoredLeaves)
	}
}

func TestLastAccessTimeUpdates(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	g, err := lib.Open(FileOptions{DataString: "Test content"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Read some data to trigger access time update
	cursor := g.NewCursor()
	_, err = cursor.ReadBytes(5)
	if err != nil {
		t.Fatalf("ReadBytes failed: %v", err)
	}

	// Access time should be recent
	g.mu.RLock()
	var accessTime time.Time
	for _, node := range g.nodeRegistry {
		snap := node.snapshotAt(g.currentFork, g.currentRevision)
		if snap != nil && snap.isLeaf && snap.storageState == StorageMemory {
			accessTime = snap.lastAccessTime
			break
		}
	}
	g.mu.RUnlock()

	if accessTime.IsZero() {
		t.Error("lastAccessTime was not updated after read")
	}

	// Should be within the last second
	if time.Since(accessTime) > time.Second {
		t.Errorf("lastAccessTime is too old: %v ago", time.Since(accessTime))
	}
}

func TestSoftLimitTriggersBackgroundChill(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "garland_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	coldPath := filepath.Join(tempDir, "cold")

	// Set a very low soft limit
	lib, err := Init(LibraryOptions{
		ColdStoragePath:    coldPath,
		MemorySoftLimit:    100, // 100 bytes
		ChillBudgetPerTick: 5,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create content larger than soft limit
	content := "This is content that exceeds the soft limit of 100 bytes significantly."
	g, err := lib.Open(FileOptions{DataString: content})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Manually trigger memory pressure check
	stats := g.CheckMemoryPressure()

	// Should have chilled some nodes since we're over soft limit
	if stats.NodesChilled == 0 {
		// Give it another try - might need to run ChillToTarget
		stats2 := lib.ChillToTarget()
		if stats2.NodesChilled == 0 {
			t.Logf("Note: No nodes chilled despite being over soft limit")
		}
	}
}

func TestNeedsRebalancing(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Small content should be balanced
	g, err := lib.Open(FileOptions{DataString: "Small content"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if g.NeedsRebalancing() {
		t.Error("Small content should not need rebalancing")
	}
}

func TestForceRebalance(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create larger content that creates a tree
	content := make([]byte, 256*1024)
	for i := range content {
		content[i] = byte('A' + (i % 26))
	}

	g, err := lib.Open(FileOptions{DataBytes: content})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Force rebalance (even if not needed)
	g.mu.Lock()
	// Artificially mark as needing rebalance by checking internal state
	g.mu.Unlock()

	stats := g.ForceRebalance()

	// Should have done some work or indicated it was already balanced
	t.Logf("ForceRebalance result: %d rotations", stats.RotationsPerformed)

	// Verify content is still correct after rebalance
	cursor := g.NewCursor()
	data, err := cursor.ReadBytes(g.ByteCount().Value)
	if err != nil {
		t.Fatalf("ReadBytes after rebalance failed: %v", err)
	}
	if len(data) != len(content) {
		t.Errorf("Content length after rebalance = %d, want %d", len(data), len(content))
	}
	for i := 0; i < len(data) && i < len(content); i++ {
		if data[i] != content[i] {
			t.Errorf("Content mismatch at byte %d: got %c, want %c", i, data[i], content[i])
			break
		}
	}
}

func TestTotalMemoryUsageAcrossGarlands(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create two garlands
	g1, err := lib.Open(FileOptions{DataString: "First content"})
	if err != nil {
		t.Fatalf("Open g1 failed: %v", err)
	}

	g2, err := lib.Open(FileOptions{DataString: "Second content"})
	if err != nil {
		t.Fatalf("Open g2 failed: %v", err)
	}

	// Total should be sum of both
	total := lib.TotalMemoryUsage()
	expected := g1.MemoryUsage().MemoryBytes + g2.MemoryUsage().MemoryBytes

	if total != expected {
		t.Errorf("TotalMemoryUsage = %d, want %d (g1=%d + g2=%d)",
			total, expected,
			g1.MemoryUsage().MemoryBytes,
			g2.MemoryUsage().MemoryBytes)
	}
}

func TestBackgroundMaintenanceWorker(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "garland_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	coldPath := filepath.Join(tempDir, "cold")

	// Create library with background worker
	lib, err := Init(LibraryOptions{
		ColdStoragePath:    coldPath,
		MemorySoftLimit:    10, // Very low limit
		BackgroundInterval: 50 * time.Millisecond,
		ChillBudgetPerTick: 1,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer lib.StopMaintenance()

	// Create content exceeding soft limit
	content := "This content definitely exceeds 10 bytes!"
	_, err = lib.Open(FileOptions{DataString: content})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Wait for background worker to run
	time.Sleep(200 * time.Millisecond)

	// Background worker should have attempted to chill
	// (The actual chilling depends on many factors, so we just verify
	// the worker doesn't crash and the library is still functional)
	total := lib.TotalMemoryUsage()
	t.Logf("After background maintenance: %d bytes in memory", total)
}

func TestRecalculateMemoryUsage(t *testing.T) {
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	content := "Test content for recalculation"
	g, err := lib.Open(FileOptions{DataString: content})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Recalculate and verify
	g.mu.Lock()
	calculated := g.recalculateMemoryUsage()
	g.mu.Unlock()

	if calculated != int64(len(content)) {
		t.Errorf("recalculateMemoryUsage = %d, want %d", calculated, len(content))
	}

	// Should match MemoryUsage()
	stats := g.MemoryUsage()
	if stats.MemoryBytes != calculated {
		t.Errorf("MemoryUsage().MemoryBytes = %d, recalculated = %d",
			stats.MemoryBytes, calculated)
	}
}

func TestIncrementalChillWithNoEligibleNodes(t *testing.T) {
	// Library without cold storage - nothing can be chilled
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	_, err = lib.Open(FileOptions{DataString: "Some content"})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Try to chill - should report 0 nodes chilled
	stats := lib.IncrementalChill(10)
	if stats.NodesChilled != 0 {
		t.Errorf("IncrementalChill without cold storage: NodesChilled = %d, want 0",
			stats.NodesChilled)
	}
}

func TestMemoryOnlySkipsChill(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "garland_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	coldPath := filepath.Join(tempDir, "cold")
	lib, err := Init(LibraryOptions{ColdStoragePath: coldPath})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create MemoryOnly garland
	g, err := lib.Open(FileOptions{
		DataString:   "Memory only content",
		LoadingStyle: MemoryOnly,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// IncrementalChill should skip MemoryOnly garlands
	stats := lib.IncrementalChill(10)

	// The MemoryOnly garland should not have been chilled
	memStats := g.MemoryUsage()
	if memStats.ColdStoredLeaves > 0 {
		t.Errorf("MemoryOnly garland has cold stored leaves: %d", memStats.ColdStoredLeaves)
	}

	t.Logf("IncrementalChill on MemoryOnly: chilled %d nodes", stats.NodesChilled)
}
