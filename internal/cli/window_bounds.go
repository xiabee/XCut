package cli

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/xiabee/XCut/internal/xcerr"
)

// The client remembers its window the way a desktop application does: bounds
// are saved while it runs and restored at the next start, clamped to the
// work area so a monitor that has since been unplugged cannot leave the
// window stranded off-screen. The geometry maths here is platform-neutral
// and tested everywhere; the win32 halves live in the build-tagged files.

// windowBounds is the persisted placement of the client window.
type windowBounds struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// defaultWindowBounds matches the shell's own first-run size.
func defaultWindowBounds() windowBounds {
	return windowBounds{W: 1500, H: 940}
}

// rect is a monitor work area (the desktop minus the taskbar).
type rect struct {
	Left, Top, Right, Bottom int
}

// clampBounds pulls a saved placement back onto the work area: the window
// must fit the desktop on both axes and sit fully inside it — the size
// clamps make the title bar reachable by construction (a window is never
// shorter than 150px, so even a bottom-clamped placement keeps that much of
// itself on screen).
func clampBounds(b windowBounds, area rect) windowBounds {
	if area.Right <= area.Left || area.Bottom <= area.Top {
		return b
	}
	if b.W > area.Right-area.Left {
		b.W = area.Right - area.Left
	}
	if b.H > area.Bottom-area.Top {
		b.H = area.Bottom - area.Top
	}
	if b.W < 200 {
		b.W = 200
	}
	if b.H < 150 {
		b.H = 150
	}
	if b.X < area.Left {
		b.X = area.Left
	}
	if b.Y < area.Top {
		b.Y = area.Top
	}
	if b.X+b.W > area.Right {
		b.X = area.Right - b.W
	}
	if b.Y+b.H > area.Bottom {
		b.Y = area.Bottom - b.H
	}
	return b
}

// loadWindowBounds reads the saved placement, falling back to the defaults
// when nothing (or nothing usable) is stored. A torn or stale file is the
// same as no file — the client must never refuse to open over it.
func loadWindowBounds(path string) windowBounds {
	b := defaultWindowBounds()
	raw, err := os.ReadFile(path)
	if err != nil {
		return b
	}
	var saved windowBounds
	if err := json.Unmarshal(raw, &saved); err != nil {
		return b
	}
	if saved.W > 0 && saved.H > 0 {
		b = saved
	}
	return b
}

// saveWindowBounds persists the placement atomically; best-effort, since a
// read-only workspace costs the user their window position, not their data.
func saveWindowBounds(path string, b windowBounds) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot encode window bounds", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot create workspace directory", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot write window bounds", err)
	}
	return os.Rename(tmp, path)
}
