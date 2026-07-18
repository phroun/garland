package garland

import (
	"io"
	"os"
	"path/filepath"
	"time"
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

// FileMetadata describes a file as observed at one moment: the
// information Garland tracks to detect external modification of a
// source file between opening it and saving over it. A virtualized
// filesystem supplies these through FileSystemInterface.Stat, or the
// application volunteers them via Garland.ReportSourceMetadata when it
// learns fresher facts than Garland could observe itself.
type FileMetadata struct {
	// Exists is false when the path currently names no file. The other
	// fields are meaningless in that case.
	Exists bool

	// Size is the file's length in bytes.
	Size int64

	// ModTime is the file's last-modification time.
	ModTime time.Time

	// Identity names the underlying storage object, independent of the
	// path (on local unix filesystems "dev<dev>:ino<inode>"). Two
	// different non-empty identities for the same path mean the path
	// was re-bound to a different object - the classic write-temp-and-
	// rename "file replaced" case. Empty means unknown; identity
	// comparison is then skipped and detection falls back to
	// size + mtime.
	Identity string
}

// DeviceInfo describes the storage device/volume behind a path, for
// free-space warnings ("this save may not fit") and for recognizing
// when two paths live on different media (e.g. saving to removable
// media vs. the working drive).
type DeviceInfo struct {
	// DeviceID identifies the device/volume holding the path (on local
	// unix filesystems "dev<dev>", on Windows the volume name). Two
	// equal non-empty IDs mean the same device. Empty means unknown.
	DeviceID string

	// FreeBytes is the space available to new writes, -1 if unknown.
	FreeBytes int64

	// TotalBytes is the device's total capacity, -1 if unknown.
	TotalBytes int64
}

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

	// Rename atomically replaces newpath with oldpath. Cold storage
	// relies on this so a block being re-written (chill) can never be
	// torn-read by a concurrent Get (unlocked save phase, thaw).
	Rename(oldpath, newpath string) error

	// Stat reports a path's current metadata for external-modification
	// detection. A missing file is NOT an error: report it as
	// FileMetadata{Exists: false} with a nil error; errors are reserved
	// for real failures. Implementations without metadata may return
	// ErrNotSupported - Garland then tracks only what the application
	// volunteers through ReportSourceMetadata.
	Stat(name string) (FileMetadata, error)

	// DeviceInfo reports the storage device behind a path (identity and
	// free space), for save-time free-space warnings. May return
	// ErrNotSupported.
	DeviceInfo(name string) (DeviceInfo, error)
}

// localFileHandle wraps an os.File for the local file system.
type localFileHandle struct {
	file *os.File
	eof  bool
}

// localFileSystem implements FileSystemInterface for local files.
type localFileSystem struct{}

// NewLocalFileSystem returns a FileSystemInterface backed by the real
// operating-system filesystem - the same implementation Garland uses
// by default. Hosts that want to pass an explicit filesystem to
// SaveAs, or wrap/partially delegate to local disk when building a
// custom FileSystemInterface, can obtain one here instead of
// reimplementing the whole interface. (SaveAs also accepts a nil
// filesystem, which resolves to this automatically.)
func NewLocalFileSystem() FileSystemInterface {
	return &localFileSystem{}
}

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

func (fs *localFileSystem) Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
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

func (fs *localFileSystem) Stat(name string) (FileMetadata, error) {
	info, err := os.Stat(name)
	if os.IsNotExist(err) {
		return FileMetadata{}, nil
	}
	if err != nil {
		return FileMetadata{}, err
	}
	return FileMetadata{
		Exists:   true,
		Size:     info.Size(),
		ModTime:  info.ModTime(),
		Identity: localFileIdentity(info),
	}, nil
}

func (fs *localFileSystem) DeviceInfo(name string) (DeviceInfo, error) {
	return localDeviceInfo(name)
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
	// Write-then-rename: a concurrent Get (the lock-free save phase, a
	// thaw on another goroutine) must never see a half-written block.
	// Same-block Sets are serialized by the garland lock, so the .tmp
	// name cannot collide with itself.
	path := filepath.Join(dir, block)
	tmp := path + ".tmp"
	if err := cs.fs.WriteFile(tmp, data); err != nil {
		return err
	}
	return cs.fs.Rename(tmp, path)
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

	// pendingTail holds an incomplete UTF-8 sequence (at most 3 bytes)
	// cut from the end of the last chunk, so a rune split across two
	// channel sends never lands split across two leaves (which would
	// corrupt rune/line counts). Flushed verbatim at end of stream, so
	// binary (non-UTF-8) content is passed through byte-for-byte.
	// Touched only by the loader goroutine.
	pendingTail []byte

	// Control
	stopChan chan struct{}
}
