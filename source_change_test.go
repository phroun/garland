package garland

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSourceChangeDetection(t *testing.T) {
	// Create a temporary file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	initialContent := []byte("Hello, World!")
	if err := os.WriteFile(tmpFile, initialContent, 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Open the file
	lib, err := Init(LibraryOptions{})
	if err != nil {
		t.Fatalf("Failed to init library: %v", err)
	}

	g, err := lib.Open(FileOptions{FilePath: tmpFile})
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer g.Close()

	// Check initial state
	info, err := g.CheckSourceMetadata()
	if err != nil {
		t.Fatalf("CheckSourceMetadata failed: %v", err)
	}
	if info.Type != SourceUnchanged {
		t.Errorf("Expected SourceUnchanged, got %v", info.Type)
	}

	// Modify the file (append)
	f, err := os.OpenFile(tmpFile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to open file for append: %v", err)
	}
	f.WriteString(" Extra content!")
	f.Close()

	// Check for append
	info, err = g.CheckSourceMetadata()
	if err != nil {
		t.Fatalf("CheckSourceMetadata failed: %v", err)
	}
	if info.Type != SourceAppended {
		t.Errorf("Expected SourceAppended, got %v", info.Type)
	}
	if info.AppendedBytes != 15 { // " Extra content!" = 15 bytes
		t.Errorf("Expected 15 appended bytes, got %d", info.AppendedBytes)
	}
}

func TestSourceChangeDetectionTruncate(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	initialContent := []byte("Hello, World! This is a longer content.")
	if err := os.WriteFile(tmpFile, initialContent, 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{FilePath: tmpFile})
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer g.Close()

	// Truncate the file
	if err := os.WriteFile(tmpFile, []byte("Short"), 0644); err != nil {
		t.Fatalf("Failed to truncate file: %v", err)
	}

	info, err := g.CheckSourceMetadata()
	if err != nil {
		t.Fatalf("CheckSourceMetadata failed: %v", err)
	}
	if info.Type != SourceTruncated {
		t.Errorf("Expected SourceTruncated, got %v", info.Type)
	}
}

func TestSourceChangeDetectionDeleted(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(tmpFile, []byte("Hello"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{FilePath: tmpFile})
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer g.Close()

	// Delete the file
	os.Remove(tmpFile)

	info, err := g.CheckSourceMetadata()
	if err != nil {
		t.Fatalf("CheckSourceMetadata failed: %v", err)
	}
	if info.Type != SourceDeleted {
		t.Errorf("Expected SourceDeleted, got %v", info.Type)
	}
}

func TestSourceChangeCounter(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(tmpFile, []byte("Hello"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{FilePath: tmpFile})
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer g.Close()

	// Initial counter should be 0
	if g.sourceState.changeCounter != 0 {
		t.Errorf("Initial changeCounter should be 0, got %d", g.sourceState.changeCounter)
	}

	// Append to file
	f, _ := os.OpenFile(tmpFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString("!")
	f.Close()

	// Check - should increment counter
	g.CheckSourceMetadata()
	if g.sourceState.changeCounter != 1 {
		t.Errorf("changeCounter should be 1 after first change, got %d", g.sourceState.changeCounter)
	}

	// Append again
	f, _ = os.OpenFile(tmpFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString("!")
	f.Close()

	g.CheckSourceMetadata()
	if g.sourceState.changeCounter != 2 {
		t.Errorf("changeCounter should be 2 after second change, got %d", g.sourceState.changeCounter)
	}
}

func TestWarmTrustLevels(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(tmpFile, []byte("Hello, World!"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{FilePath: tmpFile})
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer g.Close()

	// Initial trust level should be Full (no changes)
	trust := g.getWarmTrustLevel(1) // Arbitrary nodeID
	if trust != WarmTrustFull {
		t.Errorf("Expected WarmTrustFull, got %v", trust)
	}

	// After a change detection, trust should be Stale
	f, _ := os.OpenFile(tmpFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString("!")
	f.Close()

	g.CheckSourceMetadata()

	trust = g.getWarmTrustLevel(1)
	if trust != WarmTrustStale {
		t.Errorf("Expected WarmTrustStale, got %v", trust)
	}

	// After verification, trust should be Verified
	g.updateWarmVerification(1)
	trust = g.getWarmTrustLevel(1)
	if trust != WarmTrustVerified {
		t.Errorf("Expected WarmTrustVerified, got %v", trust)
	}
}

func TestAppendPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(tmpFile, []byte("Hello"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{FilePath: tmpFile})
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer g.Close()

	// Default policy should be Ask
	if g.sourceState.appendPolicy != AppendPolicyAsk {
		t.Errorf("Default appendPolicy should be AppendPolicyAsk")
	}

	// Set to continuous
	g.SetAppendPolicy(AppendPolicyContinuous)
	if g.sourceState.appendPolicy != AppendPolicyContinuous {
		t.Errorf("appendPolicy should be AppendPolicyContinuous")
	}
}

func TestSourceWatch(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(tmpFile, []byte("Hello"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{FilePath: tmpFile})
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer g.Close()

	// Test that watch can be enabled and disabled without panic
	g.EnableSourceWatch(100 * time.Millisecond)

	if !g.sourceState.watchEnabled {
		t.Error("Watch should be enabled")
	}

	g.DisableSourceWatch()

	if g.sourceState.watchEnabled {
		t.Error("Watch should be disabled")
	}
}

func TestSourceChangeTypeString(t *testing.T) {
	tests := []struct {
		t    SourceChangeType
		want string
	}{
		{SourceUnchanged, "unchanged"},
		{SourceAppended, "appended"},
		{SourceModified, "modified"},
		{SourceTruncated, "truncated"},
		{SourceReplaced, "replaced"},
		{SourceDeleted, "deleted"},
	}

	for _, tt := range tests {
		if got := tt.t.String(); got != tt.want {
			t.Errorf("%d.String() = %q, want %q", tt.t, got, tt.want)
		}
	}
}

func TestAcknowledgeSourceChange(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(tmpFile, []byte("Hello"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	lib, _ := Init(LibraryOptions{})
	g, err := lib.Open(FileOptions{FilePath: tmpFile})
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer g.Close()

	// Create a change
	f, _ := os.OpenFile(tmpFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString("!")
	f.Close()

	g.CheckSourceMetadata()
	if g.sourceState.changeCounter == 0 {
		t.Error("changeCounter should be non-zero after change")
	}

	// Acknowledge without reload (keep our version)
	g.AcknowledgeSourceChange(false)

	// Counter should be reset, trust restored
	if g.sourceState.changeCounter != 0 {
		t.Errorf("changeCounter should be 0 after acknowledge, got %d", g.sourceState.changeCounter)
	}

	trust := g.getWarmTrustLevel(1)
	if trust != WarmTrustFull {
		t.Errorf("Trust should be WarmTrustFull after acknowledge, got %v", trust)
	}
}
