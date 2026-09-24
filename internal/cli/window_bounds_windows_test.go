//go:build windows

package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"
)

// The win32 half of window placement gets a real (but never visible) window:
// a message-class window created without WS_VISIBLE never appears on any
// desktop, yet GetWindowRect/MoveWindow behave on it exactly as on the
// client's host window — which makes the restore plumbing assertable at
// night without flashing a frame on anyone's screen.

var (
	procGetModuleHandleW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
)

type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     syscall.Handle
	HIcon         syscall.Handle
	HCursor       syscall.Handle
	HbrBackground syscall.Handle
	LpszMenuName  uintptr
	LpszClassName uintptr
	HIconSm       syscall.Handle
}

func defWindowProc(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), w, l)
	return r
}

// newHiddenTestWindow creates an invisible window with its own window class.
// DestroyWindow is registered on t.Cleanup.
func newHiddenTestWindow(t *testing.T) uintptr {
	t.Helper()
	instance, _, _ := procGetModuleHandleW.Call(0)
	className, err := syscall.UTF16PtrFromString("xcut-bounds-test-" + filepath.Base(os.Args[0]))
	if err != nil {
		t.Fatal(err)
	}
	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   syscall.NewCallback(func(hwnd uintptr, msg uint32, w, l uintptr) uintptr { return defWindowProc(hwnd, msg, w, l) }),
		HInstance:     syscall.Handle(instance),
		LpszClassName: uintptr(unsafe.Pointer(className)),
	}
	if _, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); callErr != nil && callErr != syscall.Errno(0) {
		// Class already registered from an earlier test in this process: fine.
		_ = callErr
	}
	title, err := syscall.UTF16PtrFromString("xcut bounds test")
	if err != nil {
		t.Fatal(err)
	}
	// Style 0: created hidden. A window nobody ever shows cannot flash.
	// SyscallN (not LazyProc.Call) so the handle converts to Pointer inside
	// the vet-unsafeptr whitelist — the handle's one and only conversion.
	hwndRaw, _, _ := syscall.SyscallN(procCreateWindowExW.Addr(), 0,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 800, 600, 0, 0, instance, 0)
	if hwndRaw == 0 {
		t.Fatal("CreateWindowExW failed")
	}
	t.Cleanup(func() { procDestroyWindow.Call(hwndRaw) })
	return hwndRaw
}

func TestRestoreWindowBoundsMovesARealWindow(t *testing.T) {
	hwnd := newHiddenTestWindow(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "client-window.json")

	// A small placement any display can satisfy (640x480 well inside every
	// work area the fleet runs): restore must move the real window onto it.
	want := windowBounds{X: 20, Y: 20, W: 640, H: 480}
	if err := saveWindowBounds(path, want); err != nil {
		t.Fatal(err)
	}
	restoreWindowBounds(hwnd, path)
	got, ok := getWindowBounds(hwnd)
	if !ok {
		t.Fatal("could not read back the window placement")
	}
	if got != want {
		t.Fatalf("after restore the window sits at %+v, want %+v", got, want)
	}

	// The off-screen arm: a placement from a monitor that no longer exists
	// must land inside today's work area, not stay stranded.
	stranded := windowBounds{X: 99999, Y: 99999, W: 640, H: 480}
	if err := saveWindowBounds(path, stranded); err != nil {
		t.Fatal(err)
	}
	restoreWindowBounds(hwnd, path)
	got, ok = getWindowBounds(hwnd)
	if !ok {
		t.Fatal("could not read back the window placement")
	}
	area := workArea()
	if got.X < area.Left || got.Y < area.Top || got.X+got.W > area.Right || got.Y+got.H > area.Bottom {
		t.Fatalf("stranded placement was not clamped into the work area %+v: got %+v", area, got)
	}

	// The tracking save path: getWindowBounds must observe a real move.
	if err := saveWindowBounds(path, windowBounds{X: 30, Y: 40, W: 640, H: 480}); err != nil {
		t.Fatal(err)
	}
	restoreWindowBounds(hwnd, path)
	_ = os.Remove(path)
	if b, ok := getWindowBounds(hwnd); !ok || b != (windowBounds{X: 30, Y: 40, W: 640, H: 480}) {
		t.Fatalf("getWindowBounds read %+v (ok=%v), want the placement just set", b, ok)
	}
}
