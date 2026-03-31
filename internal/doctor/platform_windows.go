//go:build windows

package doctor

func checkDiskSpacePlatform(dir string) Result {
	return warn("Disk space", "check not available on Windows")
}

func checkFDLimitPlatform() Result {
	return warn("File descriptors", "check not available on Windows")
}
