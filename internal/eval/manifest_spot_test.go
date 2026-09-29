package eval

import "testing"

// TestManifestPlayerSpotValidation pins the schema half: a valid spot parses,
// every malformed spelling is refused at --check time (before an import and a
// full analysis pass), and the field stays unknown-field-guarded like the
// rest of the manifest.
func TestManifestPlayerSpotValidation(t *testing.T) {
	ok := `{"version":1,"cases":[{"name":"c","media":"m.mp4","expected":[{"start":1,"end":2}],` +
		`"player_spot":{"x":0.4,"y":0.7,"w":0.15,"h":0.1,"at":12.5}}]}`
	if _, err := ParseManifest([]byte(ok)); err != nil {
		t.Fatalf("a valid player_spot was refused: %v", err)
	}

	bad := []string{
		`{"version":1,"cases":[{"name":"c","media":"m.mp4","expected":[{"start":1,"end":2}],` +
			`"player_spot":{"x":0.9,"y":0.7,"w":0.15,"h":0.1}}]}`, // x+w > 1
		`{"version":1,"cases":[{"name":"c","media":"m.mp4","expected":[{"start":1,"end":2}],` +
			`"player_spot":{"x":0.4,"y":0.7,"w":0,"h":0.1}}]}`, // zero width
		`{"version":1,"cases":[{"name":"c","media":"m.mp4","expected":[{"start":1,"end":2}],` +
			`"player_spot":{"x":0.4,"y":0.7,"w":0.15,"h":0.1,"at":-1}}]}`, // negative drawn-at
		`{"version":1,"cases":[{"name":"c","media":"m.mp4","expected":[{"start":1,"end":2}],` +
			`"player_spot":{"x":0.4,"y":0.7,"w":0.15,"h":0.1,"at":12.5,"side":"left"}}]}`, // unknown field
	}
	for _, body := range bad {
		if _, err := ParseManifest([]byte(body)); err == nil {
			t.Fatalf("player_spot manifest accepted: %.120s", body)
		}
	}
}
