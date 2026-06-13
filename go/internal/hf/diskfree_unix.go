//go:build !windows

package hf

import "golang.org/x/sys/unix"

func diskFreeOS(dir string) uint64 {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return 0
	}
	return stat.Bavail * uint64(stat.Bsize)
}
