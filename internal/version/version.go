// Package version exposes build information stamped in at link time.
package version

import (
	"fmt"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
)

// Set with -ldflags "-X github.com/Ivan825/Stampede/internal/version.Version=...".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a one-line human readable version description.
func init() {
	// `go install …@v1.2.3` sets no ldflags; the module version is in the
	// build info instead.
	if Version != "dev" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		Version = strings.TrimPrefix(bi.Main.Version, "v")
	}
}

// pseudoRe matches Go pseudo-versions (an untagged commit), such as
// 0.0.0-20261007001005-a6f637a608ac.
var pseudoRe = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)

// Released reports whether this build is a tagged release, which has
// matching images on ghcr.io.
func Released() bool {
	v := strings.TrimPrefix(Version, "v")
	return v != "" && v[0] >= '0' && v[0] <= '9' && !pseudoRe.MatchString(v) && !strings.Contains(v, "SNAPSHOT")
}

func String() string {
	return fmt.Sprintf("stampede %s (commit %s, built %s, %s/%s, %s)",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
