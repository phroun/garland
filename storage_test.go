package garland

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalFileSystemOpen(t *testing.T) {
	// Create a temporary file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Hello, World!")
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	fs := &localFileSystem{}

	// Open for reading
	handle, err := fs.Open(tmpFile, OpenModeRead)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer fs.Close(handle)

	// Read content
	data, err := fs.ReadBytes(handle, 5)
	if err != nil {
		t.Fatalf("ReadBytes failed: %v", err)
	}
	if string(data) != "Hello" {
		t.Errorf("ReadBytes = %q, want %q", string(data), "Hello")
	}
}

func TestLocalFileSystemSeek(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Hello, World!")
	os.WriteFile(tmpFile, content, 0644)

	fs := &localFileSystem{}
	handle, _ := fs.Open(tmpFile, OpenModeRead)
	defer fs.Close(handle)

	// Seek to position 7
	err := fs.SeekByte(handle, 7)
	if err != nil {
		t.Fatalf("SeekByte failed: %v", err)
	}

	// Read from that position
	data, err := fs.ReadBytes(handle, 5)
	if err != nil {
		t.Fatalf("ReadBytes failed: %v", err)
	}
	if string(data) != "World" {
		t.Errorf("After seek, ReadBytes = %q, want %q", string(data), "World")
	}
}

func TestLocalFileSystemEOF(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Hi")
	os.WriteFile(tmpFile, content, 0644)

	fs := &localFileSystem{}
	handle, _ := fs.Open(tmpFile, OpenModeRead)
	defer fs.Close(handle)

	// Read all content
	_, _ = fs.ReadBytes(handle, 2)

	// Try to read more - should hit EOF
	_, err := fs.ReadBytes(handle, 1)
	if err == nil {
		t.Error("Expected EOF error")
	}

	if !fs.IsEOF(handle) {
		t.Error("IsEOF should return true after reading past end")
	}
}

func TestLocalFileSystemFileSize(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Hello, World!")
	os.WriteFile(tmpFile, content, 0644)

	fs := &localFileSystem{}
	handle, _ := fs.Open(tmpFile, OpenModeRead)
	defer fs.Close(handle)

	size, err := fs.FileSize(handle)
	if err != nil {
		t.Fatalf("FileSize failed: %v", err)
	}
	if size != 13 {
		t.Errorf("FileSize = %d, want 13", size)
	}
}

func TestLocalFileSystemWrite(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	fs := &localFileSystem{}
	handle, err := fs.Open(tmpFile, OpenModeWrite)
	if err != nil {
		t.Fatalf("Open for write failed: %v", err)
	}

	err = fs.WriteBytes(handle, []byte("Test content"))
	if err != nil {
		t.Fatalf("WriteBytes failed: %v", err)
	}

	fs.Close(handle)

	// Verify content
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "Test content" {
		t.Errorf("Written content = %q, want %q", string(data), "Test content")
	}
}

func TestLocalFileSystemTruncate(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("Hello, World!"), 0644)

	fs := &localFileSystem{}
	handle, _ := fs.Open(tmpFile, OpenModeReadWrite)

	err := fs.Truncate(handle, 5)
	if err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	fs.Close(handle)

	// Verify truncation
	data, _ := os.ReadFile(tmpFile)
	if string(data) != "Hello" {
		t.Errorf("After truncate: %q, want %q", string(data), "Hello")
	}
}

func TestLocalFileSystemInvalidHandle(t *testing.T) {
	fs := &localFileSystem{}

	// Test with invalid handle
	invalidHandle := "not a handle"

	err := fs.SeekByte(invalidHandle, 0)
	if err != ErrFileNotOpen {
		t.Errorf("SeekByte with invalid handle: expected ErrFileNotOpen, got %v", err)
	}

	_, err = fs.ReadBytes(invalidHandle, 10)
	if err != ErrFileNotOpen {
		t.Errorf("ReadBytes with invalid handle: expected ErrFileNotOpen, got %v", err)
	}

	if !fs.IsEOF(invalidHandle) {
		t.Error("IsEOF with invalid handle should return true")
	}

	err = fs.Close(invalidHandle)
	if err != ErrFileNotOpen {
		t.Errorf("Close with invalid handle: expected ErrFileNotOpen, got %v", err)
	}

	_, err = fs.FileSize(invalidHandle)
	if err != ErrFileNotOpen {
		t.Errorf("FileSize with invalid handle: expected ErrFileNotOpen, got %v", err)
	}

	err = fs.WriteBytes(invalidHandle, []byte("test"))
	if err != ErrFileNotOpen {
		t.Errorf("WriteBytes with invalid handle: expected ErrFileNotOpen, got %v", err)
	}

	err = fs.Truncate(invalidHandle, 0)
	if err != ErrFileNotOpen {
		t.Errorf("Truncate with invalid handle: expected ErrFileNotOpen, got %v", err)
	}
}

func TestLocalFileSystemBlockChecksum(t *testing.T) {
	fs := &localFileSystem{}

	_, err := fs.BlockChecksum(nil, 0, 0)
	if err != ErrNotSupported {
		t.Errorf("BlockChecksum: expected ErrNotSupported, got %v", err)
	}
}

func TestLocalFileSystemHasChanged(t *testing.T) {
	fs := &localFileSystem{}

	_, err := fs.HasChanged(nil)
	if err != ErrNotSupported {
		t.Errorf("HasChanged: expected ErrNotSupported, got %v", err)
	}
}

func TestFileColdStorage(t *testing.T) {
	tmpDir := t.TempDir()
	fs := &localFileSystem{}
	cs := newFSColdStorage(fs, tmpDir)

	// Set data
	testData := []byte("test cold storage data")
	err := cs.Set("folder1", "block1", testData)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify file was created
	expectedPath := filepath.Join(tmpDir, "folder1", "block1")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Error("Cold storage file was not created")
	}

	// Get data back
	data, err := cs.Get("folder1", "block1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(data) != string(testData) {
		t.Errorf("Get returned %q, want %q", string(data), string(testData))
	}

	// Delete
	err = cs.Delete("folder1", "block1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deleted
	if _, err := os.Stat(expectedPath); !os.IsNotExist(err) {
		t.Error("Cold storage file should be deleted")
	}
}

func TestFileColdStorageMultipleFolders(t *testing.T) {
	tmpDir := t.TempDir()
	fs := &localFileSystem{}
	cs := newFSColdStorage(fs, tmpDir)

	// Create blocks in different folders
	cs.Set("file1", "data", []byte("file1 data"))
	cs.Set("file2", "data", []byte("file2 data"))
	cs.Set("file1", "decorations", []byte("file1 decorations"))

	// Verify isolation
	data1, _ := cs.Get("file1", "data")
	data2, _ := cs.Get("file2", "data")

	if string(data1) != "file1 data" {
		t.Errorf("file1 data = %q, want %q", string(data1), "file1 data")
	}
	if string(data2) != "file2 data" {
		t.Errorf("file2 data = %q, want %q", string(data2), "file2 data")
	}

	// Verify multiple blocks in same folder
	dec, _ := cs.Get("file1", "decorations")
	if string(dec) != "file1 decorations" {
		t.Errorf("file1 decorations = %q, want %q", string(dec), "file1 decorations")
	}
}

func TestFileColdStorageGetNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	fs := &localFileSystem{}
	cs := newFSColdStorage(fs, tmpDir)

	_, err := cs.Get("nonexistent", "block")
	if err == nil {
		t.Error("Get for nonexistent block should return error")
	}
}

func TestFileColdStorageOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	fs := &localFileSystem{}
	cs := newFSColdStorage(fs, tmpDir)

	cs.Set("folder", "block", []byte("original"))
	cs.Set("folder", "block", []byte("overwritten"))

	data, _ := cs.Get("folder", "block")
	if string(data) != "overwritten" {
		t.Errorf("After overwrite: %q, want %q", string(data), "overwritten")
	}
}

func TestFileColdStorageDeleteFolder(t *testing.T) {
	tmpDir := t.TempDir()
	fs := &localFileSystem{}
	cs := newFSColdStorage(fs, tmpDir)

	// Create a block
	cs.Set("testfolder", "block1", []byte("data"))

	// Try to delete non-empty folder (should fail)
	err := cs.DeleteFolder("testfolder")
	if err == nil {
		t.Error("DeleteFolder on non-empty folder should fail")
	}

	// Delete the block first
	cs.Delete("testfolder", "block1")

	// Now delete empty folder
	err = cs.DeleteFolder("testfolder")
	if err != nil {
		t.Errorf("DeleteFolder on empty folder failed: %v", err)
	}

	// Verify folder is gone
	folderPath := filepath.Join(tmpDir, "testfolder")
	if _, err := os.Stat(folderPath); !os.IsNotExist(err) {
		t.Error("Folder should be deleted")
	}
}

func TestOpenModeConstants(t *testing.T) {
	modes := []OpenMode{OpenModeRead, OpenModeWrite, OpenModeReadWrite}
	seen := make(map[OpenMode]bool)

	for _, mode := range modes {
		if seen[mode] {
			t.Errorf("Duplicate OpenMode value: %d", mode)
		}
		seen[mode] = true
	}
}

func TestStorageStateConstants(t *testing.T) {
	states := []StorageState{StorageMemory, StorageWarm, StorageCold, StoragePlaceholder}
	seen := make(map[StorageState]bool)

	for _, state := range states {
		if seen[state] {
			t.Errorf("Duplicate StorageState value: %d", state)
		}
		seen[state] = true
	}

	// Verify zero value is StorageMemory
	var s StorageState
	if s != StorageMemory {
		t.Errorf("Zero value of StorageState = %d, want StorageMemory (%d)", s, StorageMemory)
	}
}

func TestOptimizedRegionHandle(t *testing.T) {
	handle := OptimizedRegionHandle{
		startByte: 100,
		endByte:   200,
		region:    nil, // no actual region for this test
	}

	if handle.StartByte() != 100 {
		t.Errorf("StartByte() = %d, want 100", handle.StartByte())
	}
	if handle.EndByte() != 200 {
		t.Errorf("EndByte() = %d, want 200", handle.EndByte())
	}
	if handle.Region() != nil {
		t.Error("Region() should be nil")
	}
}

func TestLoaderStruct(t *testing.T) {
	// Just verify the struct can be created
	loader := &Loader{
		garland:     nil,
		source:      nil,
		sourceType:  0,
		bytesLoaded: 100,
		runesLoaded: 80,
		linesLoaded: 5,
		eofReached:  false,
	}

	if loader.bytesLoaded != 100 {
		t.Errorf("bytesLoaded = %d, want 100", loader.bytesLoaded)
	}
	if loader.runesLoaded != 80 {
		t.Errorf("runesLoaded = %d, want 80", loader.runesLoaded)
	}
	if loader.linesLoaded != 5 {
		t.Errorf("linesLoaded = %d, want 5", loader.linesLoaded)
	}
	if loader.eofReached {
		t.Error("eofReached should be false")
	}
}
