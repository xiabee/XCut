//go:build !windows

package cli

import "unsafe"

// Off Windows the client is a browser tab (`xcut client --browser`), and the
// browser owns window placement. The bounds file is simply not used.

func restoreWindowBounds(unsafe.Pointer, string) {}

func trackWindowBounds(unsafe.Pointer, string, <-chan struct{}) {}
