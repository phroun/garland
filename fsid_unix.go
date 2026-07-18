//go:build unix

package garland

import (
	"fmt"
	"os"
	"syscall"
)

// localFileIdentity names the storage object behind a stat result on
// unix-like systems ("dev<dev>:ino<inode>"), used to detect the source
// file being replaced (rename/swap in place of an edit). Returns ""
// when the underlying Sys() value is not a *syscall.Stat_t; identity
// comparison is then skipped and detection falls back to size + mtime.
func localFileIdentity(info os.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("dev%d:ino%d", stat.Dev, stat.Ino)
	}
	return ""
}
