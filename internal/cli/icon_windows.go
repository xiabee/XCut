//go:build windows

package cli

import (
	"image"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

// Window icon plumbing: the WebView2 host window ships with the generic
// webview icon, so the client paints the programmatic brand mark into an
// ICO, hands it to LoadImageW and pins the result onto the window (title
// bar + taskbar) with WM_SETICON. No icon files in the repo, no resource
// compiler, nothing third-party. CreateIconFromResourceEx was tried first
// and silently rejected both PNG and BMP payloads on this Windows build —
// the file round-trip through LoadImageW is the boring, reliable path.

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	procLoadImageW   = user32.NewProc("LoadImageW")
	procSendMessageW = user32.NewProc("SendMessageW")
)

const (
	wmSetIcon      = 0x0080
	iconSmall      = 0
	iconBig        = 1
	imageIcon      = 1
	lrLoadFromFile = 0x00000010
)

// setWindowIcon best-effort applies the brand icon at the two sizes
// Windows wants (16 = title bar/small taskbar, 48 = big taskbar/Alt-Tab).
// Failure is non-fatal: an unbranded window still works.
func setWindowIcon(hwnd unsafe.Pointer) {
	if hwnd == nil {
		return
	}
	ico, err := icoBytes([]image.Image{brandIcon(16), brandIcon(32), brandIcon(48)})
	if err != nil || len(ico) == 0 {
		return
	}
	path := filepath.Join(os.TempDir(), "xcut-icon.ico")
	if err := os.WriteFile(path, ico, 0o644); err != nil {
		return
	}
	defer os.Remove(path)

	p16, _, _ := procLoadImageW.Call(0, strPtr(path), imageIcon, 16, 16, lrLoadFromFile)
	p48, _, _ := procLoadImageW.Call(0, strPtr(path), imageIcon, 48, 48, lrLoadFromFile)
	p := uintptr(hwnd)
	if p16 != 0 {
		procSendMessageW.Call(p, wmSetIcon, iconSmall, p16)
	}
	if p48 != 0 {
		procSendMessageW.Call(p, wmSetIcon, iconBig, p48)
	}
	// Deliberately NOT DestroyIcon: WM_SETICON adopts the handle, so the
	// icons must outlive the window. The client process exits with the
	// window, which reclaims them — leaking two handles per run is the
	// cheaper, correct trade.
	runtime.KeepAlive(&ico[0])
	runtime.KeepAlive(path)
}

func strPtr(s string) uintptr {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return 0
	}
	return uintptr(unsafe.Pointer(p))
}
