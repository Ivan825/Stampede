package worker

import "golang.org/x/sys/unix"

// totalMemory returns physical memory in bytes (0 when unknown).
func totalMemory() uint64 {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0
	}
	// Totalram and Unit are 32-bit on some architectures.
	return uint64(si.Totalram) * uint64(si.Unit) //nolint:unconvert // width differs by GOARCH
}
