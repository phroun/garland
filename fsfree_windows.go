//go:build windows

package garland

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// localDeviceInfo reports the volume identity and free space behind a
// path via GetDiskFreeSpaceExW. The path itself need not exist yet (a
// save target); its directory is consulted instead.
func localDeviceInfo(path string) (DeviceInfo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return DeviceInfo{FreeBytes: -1, TotalBytes: -1}, err
	}
	dir := filepath.Dir(abs)

	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return DeviceInfo{FreeBytes: -1, TotalBytes: -1}, err
	}
	var freeToCaller, total, totalFree uint64
	r1, _, callErr := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r1 == 0 {
		return DeviceInfo{FreeBytes: -1, TotalBytes: -1}, callErr
	}
	return DeviceInfo{
		DeviceID:   filepath.VolumeName(abs),
		FreeBytes:  int64(freeToCaller),
		TotalBytes: int64(total),
	}, nil
}
