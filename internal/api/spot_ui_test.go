package api

import (
	"strings"
	"testing"
)

// TestTheClientSpeaksTheSpotWire pins the picker's third target to the wire
// the server answers: the route literal, the fields the status line reads off
// the response, and the body shape the PUT sends. A rename at either end must
// fail here instead of blanking the status or silently clearing a spot.
func TestTheClientSpeaksTheSpotWire(t *testing.T) {
	_, js, _ := i18nAssets(t)
	for _, want := range []struct {
		literal string
		why     string
	}{
		{"/player-spot`", "the picker posts to a route no handler answers"},
		{"spot.bins", "the status cannot tell a measured spot from a bare one"},
		{"spot.rect", "the status prints nothing readable"},
		{"spot.at", "the status loses the drawn-at moment"},
		{"rect: [roiRect.x, roiRect.y, roiRect.w, roiRect.h]", "the PUT's rect must travel as the array the server validates"},
		{"at: $(\"roi-video\").currentTime || 1", "the PUT loses the frame the user drew against"},
	} {
		if !strings.Contains(js, want.literal) {
			t.Errorf("app.js does not contain %q — %s", want.literal, want.why)
		}
	}
}

// TestTheEditorSavesToTheAssetItDrewAgainst pins the save-target rule: the
// ROI editor's save names the asset it OPENED with, and switching assets
// closes the editor. The dropdown sits in the same panel as the inline
// editor, so a user can move it mid-draw — a rect drawn over one source's
// frame must never land on another asset's row.
func TestTheEditorSavesToTheAssetItDrewAgainst(t *testing.T) {
	_, js, _ := i18nAssets(t)
	for _, want := range []struct {
		literal string
		why     string
	}{
		{"roiUrl(roiAssetId)", "the save drifted to whatever the dropdown moved to while the editor sat open"},
		{"if (!roiAssetId) return;", "the save lost its opened-asset anchor"},
		{"the rect on screen was drawn against", "the asset-switch close guard was removed"},
	} {
		if !strings.Contains(js, want.literal) {
			t.Errorf("app.js does not contain %q — %s", want.literal, want.why)
		}
	}
}
