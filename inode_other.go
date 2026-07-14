//go:build !unix

package garland

import "os"

// getInode returns 0 on platforms without a stable inode concept
// (Windows, wasm, plan9). Inode-based source-replacement detection is
// disabled there; callers already guard on a zero inode, so change
// detection falls back to size+mtime.
func getInode(info os.FileInfo) uint64 {
	return 0
}
