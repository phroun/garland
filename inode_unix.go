//go:build unix

package garland

import (
	"os"
	"syscall"
)

// getInode extracts the inode number from file info on unix-like
// systems, used to detect the source file being replaced (rename/swap
// in place of an edit). Returns 0 when the underlying Sys() value is
// not a *syscall.Stat_t.
func getInode(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Ino
	}
	return 0
}
