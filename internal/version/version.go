// Package version exposes build information stamped in at link time.
package version

import (
	"fmt"
	"runtime"
)

// Set with -ldflags "-X github.com/Ivan825/Stampede/internal/version.Version=...".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a one-line human readable version description.
func String() string {
	return fmt.Sprintf("stampede %s (commit %s, built %s, %s/%s, %s)",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
