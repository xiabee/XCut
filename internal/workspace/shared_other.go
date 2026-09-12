//go:build !windows

package workspace

import "os"

// OpenReadable is os.Open on platforms where open handles never block a
// replacement rename (POSIX unlink semantics).
func OpenReadable(path string) (*os.File, error) { return os.Open(path) }

// posixRemove is unused off Windows (RetryableReplace returns the rename
// error before reaching it).
func posixRemove(path string) error { return os.Remove(path) }
