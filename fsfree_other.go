//go:build !linux && !darwin && !windows

package garland

// localDeviceInfo has no implementation on this platform; device
// identity and free-space reporting are unavailable.
func localDeviceInfo(path string) (DeviceInfo, error) {
	return DeviceInfo{FreeBytes: -1, TotalBytes: -1}, ErrNotSupported
}
