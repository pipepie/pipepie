//go:build !windows

package doctor

import (
	"fmt"
	"syscall"
)

func checkDiskSpacePlatform(dir string) Result {
	if dir == "" || dir == "." {
		dir = "."
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return warn("Disk space", fmt.Sprintf("cannot check: %v", err))
	}
	freeGB := float64(stat.Bavail*uint64(stat.Bsize)) / (1 << 30)
	if freeGB < 1 {
		return fail("Disk space", fmt.Sprintf("%.1f GB free — need at least 1 GB", freeGB))
	}
	if freeGB < 5 {
		return warn("Disk space", fmt.Sprintf("%.1f GB free", freeGB))
	}
	return pass("Disk space", fmt.Sprintf("%.0f GB free", freeGB))
}

func checkFDLimitPlatform() Result {
	var rlimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlimit); err != nil {
		return warn("File descriptors", fmt.Sprintf("cannot check: %v", err))
	}
	if rlimit.Cur < 1024 {
		return warn("File descriptors", fmt.Sprintf("limit is %d — recommend 65536", rlimit.Cur))
	}
	return pass("File descriptors", fmt.Sprintf("limit %d", rlimit.Cur))
}
