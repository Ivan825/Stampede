package worker

import "golang.org/x/sys/unix"

// totalMemory returns physical memory in bytes (0 when unknown).
func totalMemory() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}
