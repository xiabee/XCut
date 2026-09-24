package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The client remembers its window across runs. The maths (clamping a saved
// placement onto today's work area) is the part that can strand a window
// off-screen or behind the taskbar, so it is pinned on every platform even
// though only Windows draws the window.

func TestClampBoundsKeepsTheWindowReachable(t *testing.T) {
	area := rect{Left: 0, Top: 0, Right: 1920, Bottom: 1040} // 1080p minus taskbar

	// A normal saved placement passes through unchanged.
	b := clampBounds(windowBounds{X: 100, Y: 80, W: 1500, H: 940}, area)
	if b != (windowBounds{X: 100, Y: 80, W: 1500, H: 940}) {
		t.Fatalf("an on-screen placement was moved: %+v", b)
	}

	// A monitor that was unplugged: the saved X sits right of today's screen.
	b = clampBounds(windowBounds{X: 3000, Y: 80, W: 1500, H: 940}, area)
	if b.X+b.W > area.Right || b.X < area.Left {
		t.Fatalf("window restored off the right edge: %+v", b)
	}

	// The title bar must stay reachable: at most the bottom strip off-screen,
	// never the top.
	b = clampBounds(windowBounds{X: 10, Y: -500, W: 1500, H: 940}, area)
	if b.Y < area.Top {
		t.Fatalf("window restored above the work area: %+v", b)
	}
	b = clampBounds(windowBounds{X: 10, Y: 2000, W: 1500, H: 940}, area)
	if b.Y+b.H > area.Bottom || b.Y < area.Top {
		t.Fatalf("window restored below the work area: %+v", b)
	}
	// Half off the bottom edge (bottom at 1440 on a 1040 area) — the case a
	// threshold widened by a constant would survive.
	b = clampBounds(windowBounds{X: 10, Y: 500, W: 1500, H: 940}, area)
	if b.Y+b.H > area.Bottom {
		t.Fatalf("half-off-screen placement not pulled back: %+v", b)
	}

	// A placement saved on a bigger monitor: shrink into today's area rather
	// than overflow it.
	b = clampBounds(windowBounds{X: 0, Y: 0, W: 3000, H: 1500}, area)
	if b.W > area.Right-area.Left || b.H > area.Bottom-area.Top {
		t.Fatalf("window larger than the work area: %+v", b)
	}
}

func TestWindowBoundsRoundTripAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client-window.json")

	// Nothing saved yet: the shell's own first-run defaults.
	if b := loadWindowBounds(path); b != defaultWindowBounds() {
		t.Fatalf("fresh load = %+v, want the defaults %+v", b, defaultWindowBounds())
	}

	want := windowBounds{X: 12, Y: 34, W: 1500, H: 940}
	if err := saveWindowBounds(path, want); err != nil {
		t.Fatal(err)
	}
	if b := loadWindowBounds(path); b != want {
		t.Fatalf("round trip = %+v, want %+v", b, want)
	}

	// A torn file (killed mid-write is exactly what the atomic rename exists
	// for, but a hand-truncated file must also not break the client).
	if err := os.WriteFile(path, []byte(`{"x":10,"y":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := loadWindowBounds(path); b != defaultWindowBounds() {
		t.Fatalf("torn file = %+v, want the defaults", b)
	}

	// A file with no usable size is ignored too.
	if err := os.WriteFile(path, []byte(`{"x":10,"y":10,"w":0,"h":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := loadWindowBounds(path); b != defaultWindowBounds() {
		t.Fatalf("empty placement = %+v, want the defaults", b)
	}

	// The save is atomic: a temp file must not survive beside the real one.
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("the atomic save left its temp file behind")
	}
	raw, err := os.ReadFile(path)
	if err != nil || !json.Valid(raw) {
		t.Fatalf("the saved file is not valid JSON: %v", err)
	}
}
