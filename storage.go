package garland

import (
	"io"
	"os"
	"path/filepath"
)

// OpenMode specifies how a file should be opened.
type OpenMode int

const (
	// OpenModeRead opens the file for reading only.
	OpenModeRead OpenMode = iota

	// OpenModeWrite opens the file for writing only.
	OpenModeWrite

	// OpenModeReadWrite opens the file for reading and writing.
	OpenModeReadWrite
)

// FileHandle represents an open file.
type FileHandle interface{}

// FileSystemInterface abstracts file operations for custom protocols.
// The library provides a default implementation for local files.
type FileSystemInterface interface {
	// Required methods
	Open(name string, mode OpenMode) (FileHandle, error)
	SeekByte(handle FileHandle, pos int64) error
	ReadBytes(handle FileHandle, length int) ([]byte, error)
	IsEOF(handle FileHandle) bool
	Close(handle FileHandle) error

	// Optional methods (may return ErrNotSupported)
	HasChanged(handle FileHandle) (bool, error)
	FileSize(handle FileHandle) (int64, error)
	BlockChecksum(handle FileHandle, start, length int64) ([]byte, error)
	WriteBytes(handle FileHandle, data []byte) error
	Truncate(handle FileHandle, size int64) error

	// Convenience methods for file operations
	WriteFile(name string, data []byte) error
	ReadFile(name string) ([]byte, error)

	// Directory operations
	MkdirAll(path string) error
	Remove(name string) error
	Rmdir(path string) error // Only removes empty directories
}

// localFileHandle wraps an os.File for the local file system.
type localFileHandle struct {
	file *os.File
	eof  bool
}

// localFileSystem implements FileSystemInterface for local files.
type localFileSystem struct{}

func (fs *localFileSystem) Open(name string, mode OpenMode) (FileHandle, error) {
	var flag int
	switch mode {
	case OpenModeRead:
		flag = os.O_RDONLY
	case OpenModeWrite:
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	case OpenModeReadWrite:
		flag = os.O_RDWR | os.O_CREATE
	}

	f, err := os.OpenFile(name, flag, 0644)
	if err != nil {
		return nil, err
	}
	return &localFileHandle{file: f}, nil
}

func (fs *localFileSystem) SeekByte(handle FileHandle, pos int64) error {
	h, ok := handle.(*localFileHandle)
	if !ok {
		return ErrFileNotOpen
	}
	_, err := h.file.Seek(pos, io.SeekStart)
	h.eof = false
	return err
}

func (fs *localFileSystem) ReadBytes(handle FileHandle, length int) ([]byte, error) {
	h, ok := handle.(*localFileHandle)
	if !ok {
		return nil, ErrFileNotOpen
	}
	data := make([]byte, length)
	n, err := h.file.Read(data)
	if err == io.EOF {
		h.eof = true
		if n == 0 {
			return nil, err
		}
		return data[:n], nil
	}
	if err != nil {
		return nil, err
	}
	return data[:n], nil
}

func (fs *localFileSystem) IsEOF(handle FileHandle) bool {
	h, ok := handle.(*localFileHandle)
	if !ok {
		return true
	}
	return h.eof
}

func (fs *localFileSystem) Close(handle FileHandle) error {
	h, ok := handle.(*localFileHandle)
	if !ok {
		return ErrFileNotOpen
	}
	return h.file.Close()
}

func (fs *localFileSystem) HasChanged(handle FileHandle) (bool, error) {
	// TODO: Implement by checking mtime or size
	return false, ErrNotSupported
}

func (fs *localFileSystem) FileSize(handle FileHandle) (int64, error) {
	h, ok := handle.(*localFileHandle)
	if !ok {
		return 0, ErrFileNotOpen
	}
	info, err := h.file.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (fs *localFileSystem) BlockChecksum(handle FileHandle, start, length int64) ([]byte, error) {
	return nil, ErrNotSupported
}

func (fs *localFileSystem) WriteBytes(handle FileHandle, data []byte) error {
	h, ok := handle.(*localFileHandle)
	if !ok {
		return ErrFileNotOpen
	}
	_, err := h.file.Write(data)
	return err
}

func (fs *localFileSystem) Truncate(handle FileHandle, size int64) error {
	h, ok := handle.(*localFileHandle)
	if !ok {
		return ErrFileNotOpen
	}
	return h.file.Truncate(size)
}

func (fs *localFileSystem) WriteFile(name string, data []byte) error {
	return os.WriteFile(name, data, 0644)
}

func (fs *localFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (fs *localFileSystem) MkdirAll(path string) error {
	return os.MkdirAll(path, 0755)
}

func (fs *localFileSystem) Remove(name string) error {
	return os.Remove(name)
}

func (fs *localFileSystem) Rmdir(path string) error {
	// os.Remove only removes empty directories when given a directory path
	return os.Remove(path)
}

// fsColdStorage implements ColdStorageInterface using a FileSystemInterface.
// This allows cold storage to work with any filesystem implementation.
type fsColdStorage struct {
	fs       FileSystemInterface
	basePath string
}

// newFSColdStorage creates a ColdStorageInterface backed by a FileSystemInterface.
func newFSColdStorage(fs FileSystemInterface, basePath string) *fsColdStorage {
	return &fsColdStorage{fs: fs, basePath: basePath}
}

func (cs *fsColdStorage) Set(folder, block string, data []byte) error {
	dir := filepath.Join(cs.basePath, folder)
	if err := cs.fs.MkdirAll(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, block)
	return cs.fs.WriteFile(path, data)
}

func (cs *fsColdStorage) Get(folder, block string) ([]byte, error) {
	path := filepath.Join(cs.basePath, folder, block)
	return cs.fs.ReadFile(path)
}

func (cs *fsColdStorage) Delete(folder, block string) error {
	path := filepath.Join(cs.basePath, folder, block)
	return cs.fs.Remove(path)
}

// DeleteFolder removes an empty folder from cold storage.
func (cs *fsColdStorage) DeleteFolder(folder string) error {
	path := filepath.Join(cs.basePath, folder)
	return cs.fs.Rmdir(path)
}

// Loader handles background loading of data from various sources.
type Loader struct {
	garland *Garland

	// Source
	source     io.Reader
	sourceType int // 0 = reader, 1 = channel

	// Progress
	bytesLoaded int64
	runesLoaded int64
	linesLoaded int64
	eofReached  bool

	// Channel source
	dataChan chan []byte

	// Control
	stopChan chan struct{}
}

// OptimizedRegionHandle provides access to an active optimized region.
type OptimizedRegionHandle struct {
	startByte int64
	endByte   int64
	region    OptimizedRegion
}

// StartByte returns the starting byte position of the region.
func (h *OptimizedRegionHandle) StartByte() int64 {
	return h.startByte
}

// EndByte returns the ending byte position of the region (exclusive).
func (h *OptimizedRegionHandle) EndByte() int64 {
	return h.endByte
}

// Region returns the underlying OptimizedRegion implementation.
func (h *OptimizedRegionHandle) Region() OptimizedRegion {
	return h.region
}

// OptimizedRegion is implemented by high-performance editing zones.
type OptimizedRegion interface {
	// Counts
	ByteCount() int64
	RuneCount() int64
	LineCount() int64

	// Operations (offset is relative to region start)
	InsertBytes(offset int64, data []byte, decorations []RelativeDecoration, insertBefore bool) error
	DeleteBytes(offset, length int64) ([]RelativeDecoration, error)
	ReadBytes(offset, length int64) ([]byte, error)

	// Versioning
	CommitSnapshot() (RevisionID, error)
	RevertTo(revision RevisionID) error

	// Dissolution back to tree structure
	Dissolve() (data []byte, decorations []Decoration, err error)
}
