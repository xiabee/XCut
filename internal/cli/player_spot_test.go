package cli

import (
	"testing"
)

// TestParsePlayerSpotFlag pins the auto --player-spot grammar: four parts is
// a rect with no drawn-at moment, the fifth part is that moment, and every
// malformed spelling the flag can receive is a validation error naming the
// flag — not a silently half-parsed rect.
func TestParsePlayerSpotFlag(t *testing.T) {
	rect, at, err := parsePlayerSpotFlag("0.1,0.2,0.3,0.4")
	if err != nil || at != 0 {
		t.Fatalf("four-part flag = (%v, %.3f, %v), want a rect with at=0", rect, at, err)
	}
	if rect[0] != 0.1 || rect[3] != 0.4 {
		t.Fatalf("four-part flag rect = %v, want the components in order", rect)
	}

	rect, at, err = parsePlayerSpotFlag(" 0.1 , 0.2 , 0.3 , 0.4 , 12.5 ")
	if err != nil || at != 12.5 {
		t.Fatalf("five-part flag = (%v, %.3f, %v), want at=12.5 with whitespace trimmed", rect, at, err)
	}

	for _, bad := range []string{
		"0.1,0.2,0.3",         // too short
		"0.1,0.2,0.3,0.4,5,6", // too long
		"0.1,0.2,later,0.4",   // not a number
		"0.9,0.9,0.5,0.5",     // x+w > 1
		"-0.1,0.2,0.3,0.4",    // negative origin
		"0.1,0.2,0,0.4",       // zero width
	} {
		if _, _, err := parsePlayerSpotFlag(bad); err == nil {
			t.Fatalf("--player-spot %q was accepted", bad)
		}
	}
}
