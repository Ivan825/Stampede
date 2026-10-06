//go:build !linux && !darwin

package worker

// totalMemory is not known on this platform.
func totalMemory() uint64 { return 0 }
