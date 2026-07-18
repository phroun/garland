//go:build linux || darwin

package garland

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// localDeviceInfo reports the device identity and free space behind a
// path via statfs. The path itself need not exist yet (a save target);
// its directory is consulted instead.
func localDeviceInfo(path string) (DeviceInfo, error) {
	p := path
	if _, err := os.Stat(p); err != nil {
		p = filepath.Dir(p)
	}

	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return DeviceInfo{FreeBytes: -1, TotalBytes: -1}, err
	}
	dev := DeviceInfo{
		// Bavail: space available to unprivileged writes (what a save
		// can actually use), not raw free blocks.
		FreeBytes:  int64(st.Bavail) * int64(st.Bsize),
		TotalBytes: int64(st.Blocks) * int64(st.Bsize),
	}
	if info, err := os.Stat(p); err == nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			dev.DeviceID = fmt.Sprintf("dev%d", stat.Dev)
		}
	}
	return dev, nil
}
