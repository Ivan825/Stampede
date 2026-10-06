//go:build unix

package worker

import (
	"os"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

// processCPU returns user plus system CPU time consumed by this process.
func processCPU() (time.Duration, bool) {
	var ru unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), true
}

// fdUsage returns the open file descriptors and the soft limit. Linux
// lists them under /proc/self/fd; the BSDs and macOS under /dev/fd.
func fdUsage() (open, limit uint64, ok bool) {
	var rl unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &rl); err != nil {
		return 0, 0, false
	}
	dir := "/dev/fd"
	if runtime.GOOS == "linux" {
		dir = "/proc/self/fd"
	}
	f, err := os.Open(dir)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return 0, 0, false
	}
	// The directory handle itself is one of the entries.
	// Rlimit.Cur is int64 on the BSDs.
	return uint64(max(len(names)-1, 0)), uint64(rl.Cur), true //nolint:unconvert // width differs by GOOS
}
