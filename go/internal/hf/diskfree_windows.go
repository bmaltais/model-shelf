//go:build windows

package hf

import (
	"syscall"
	"unsafe"
)

func diskFreeOS(dir string) uint64 {
	var free, total, avail uint64
	ptr, _ := syscall.UTF16PtrFromString(dir)
	getDiskFreeSpaceEx := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&free)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&avail)),
	)
	return free
}
