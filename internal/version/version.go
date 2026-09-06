// Package version carries build identity for the xcut binary.
package version

import "runtime"

// Values overridable at build time via -ldflags.
var (
	Version   = "0.1.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// String returns the one-line version banner.
func String() string {
	return "xcut " + Version + " (" + Commit + ", " + BuildDate + ", " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH + ")"
}
