//go:build !windows

package cli

// Off Windows the client is a browser tab (`xcut client --browser`), and the
// browser owns window placement. The bounds file is simply not used.

func restoreWindowBounds(uintptr, string) {}

func trackWindowBounds(uintptr, string, <-chan struct{}) {}
