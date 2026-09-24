//go:build windows

package cli

import (
	"time"
	"unsafe"
)

// The win32 half of window placement: GetWindowRect while the client runs,
// SystemParametersInfoW for the primary monitor's work area (the taskbar
// excluded — a restored window must not hide behind it). Best-effort
// throughout: a window that forgets its position is an annoyance, one that
// fails to open is a bug.

var (
	procGetWindowRect        = user32.NewProc("GetWindowRect")
	procMoveWindow           = user32.NewProc("MoveWindow")
	procSystemParametersInfo = user32.NewProc("SystemParametersInfoW")
)

const spiGetWorkarea = 0x0048

type rectWindows struct {
	Left, Top, Right, Bottom int32
}

// getWindowBounds reads the current placement of the host window.
func getWindowBounds(hwnd uintptr) (windowBounds, bool) {
	if hwnd == 0 {
		return windowBounds{}, false
	}
	var r rectWindows
	p := hwnd
	if _, _, _ = procGetWindowRect.Call(p, uintptr(unsafe.Pointer(&r))); r.Right <= r.Left || r.Bottom <= r.Top {
		return windowBounds{}, false
	}
	return windowBounds{X: int(r.Left), Y: int(r.Top), W: int(r.Right - r.Left), H: int(r.Bottom - r.Top)}, true
}

// workArea returns the primary monitor's usable desktop.
func workArea() rect {
	var r rectWindows
	if _, _, _ = procSystemParametersInfo.Call(spiGetWorkarea, 0, uintptr(unsafe.Pointer(&r)), 0); r.Right > r.Left && r.Bottom > r.Top {
		return rect{Left: int(r.Left), Top: int(r.Top), Right: int(r.Right), Bottom: int(r.Bottom)}
	}
	// The API cannot answer here (a headless service session reports no work
	// area). A conservative default keeps the clamp working — "unknown" must
	// not disable it, because a stranded window is the bug it exists to
	// prevent; the saved width/height clamps still fit the window to it.
	return rect{Left: 0, Top: 0, Right: 1280, Bottom: 1024}
}

// restoreWindowBounds moves the host window onto its saved placement,
// clamped to today's work area. Best-effort.
func restoreWindowBounds(hwnd uintptr, path string) {
	if hwnd == 0 {
		return
	}
	b := clampBounds(loadWindowBounds(path), workArea())
	procMoveWindow.Call(uintptr(hwnd), uintptr(b.X), uintptr(b.Y), uintptr(b.W), uintptr(b.H), 1)
}

// trackWindowBounds keeps the saved placement current while the client
// window lives. A 2 s ticker is cheaper and far simpler than subclassing the
// window procedure for WM_MOVE/WM_SIZE — the file is 40 bytes — and the save
// is atomic, so a kill shot mid-write cannot tear it.
func trackWindowBounds(hwnd uintptr, path string, stop <-chan struct{}) {
	save := func() {
		if b, ok := getWindowBounds(hwnd); ok {
			_ = saveWindowBounds(path, b)
		}
	}
	save()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				save()
				return
			case <-t.C:
				save()
			}
		}
	}()
}
