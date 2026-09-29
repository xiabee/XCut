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
