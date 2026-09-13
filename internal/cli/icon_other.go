//go:build !windows

package cli

import "unsafe"

// setWindowIcon has nothing to do off Windows: the icon goes into the
// desktop environment's own theme machinery, which is out of scope for
// the non-Windows fallback (serve + browser).
func setWindowIcon(unsafe.Pointer) {}
