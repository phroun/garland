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

// fileColdStorage implements ColdStorageInterface using the local file system.
type fileColdStorage struct {
	basePath string
}

func newFileColdStorage(basePath string) *fileColdStorage {
	return &fileColdStorage{basePath: basePath}
}

func (cs *fileColdStorage) Set(folder, block string, data []byte) error {
	dir := filepath.Join(cs.basePath, folder)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, block)
	return os.WriteFile(path, data, 0644)
}

func (cs *fileColdStorage) Get(folder, block string) ([]byte, error) {
	path := filepath.Join(cs.basePath, folder, block)
	return os.ReadFile(path)
}

func (cs *fileColdStorage) Delete(folder, block string) error {
	path := filepath.Join(cs.basePath, folder, block)
	return os.Remove(path)
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
