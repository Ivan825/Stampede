//go:build !linux

package health

// portUsage is only measured on Linux, where the kernel lists sockets in
// /proc; elsewhere the signal is skipped.
func portUsage() (used, total uint64, ok bool) { return 0, 0, false }

// ifaceCounters is only read on Linux; elsewhere network throughput is
// not checked.
func ifaceCounters() map[string]ifaceCount { return nil }
