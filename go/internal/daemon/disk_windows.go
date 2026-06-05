//go:build windows

package daemon

import (
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

func diskUsagePlatform(path string) (totalGB, freeGB float64) {

	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	pathPtr, _ := syscall.UTF16PtrFromString(path)

	ret, _, _ := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if ret == 0 {
		return 0, 0
	}
	return float64(totalBytes) / (1024 * 1024 * 1024), float64(freeBytesAvailable) / (1024 * 1024 * 1024)
}
