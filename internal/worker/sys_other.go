//go:build !unix

package worker

import "time"

// processCPU is not sampled on this platform; CPU saturation is then
// never reported, and the other signals still are.
func processCPU() (time.Duration, bool) { return 0, false }

// fdUsage is not sampled on this platform.
func fdUsage() (open, limit uint64, ok bool) { return 0, 0, false }
