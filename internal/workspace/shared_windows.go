//go:build windows

package workspace

import (
	"os"

	"golang.org/x/sys/windows"
)

// OpenReadable opens path for reading with full Windows sharing modes, so a
// concurrent publish (POSIX delete + rename, see RetryableReplace) can
// replace the file while a download or playback still holds it open. Plain
// os.Open shares only READ|WRITE, which pins the name against replacement
// for as long as the handle is open — a re-render publishing over a render
// a client is watching would fail.
func OpenReadable(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// posixRemove frees a file's name even when a delete-sharing reader holds it
// open (Windows 10+ POSIX semantics: the holder keeps reading the old data
// until EOF; the name becomes reusable immediately). Best-effort by design —
// callers fall back to the original rename error when this fails.
func posixRemove(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.DeleteFile(p)
}
