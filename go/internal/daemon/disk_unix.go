//go:build !windows

package daemon

import "syscall"

func diskUsagePlatform(path string) (totalGB, freeGB float64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	return float64(total) / (1024 * 1024 * 1024), float64(free) / (1024 * 1024 * 1024)
}
