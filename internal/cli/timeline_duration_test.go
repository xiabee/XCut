package cli

import "testing"

// --duration is accepted in exactly the range the pipeline accepts, and empty
// means "the style decides" — the two failure modes are a CLI that refuses a
// value the engine supports, and one that accepts a value it will reject.
func TestParseDurationFlag(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float64
		ok   bool
	}{
		{"", 0, true},
		{"   ", 0, true},
		{"45", 45, true},
		{"45.5", 45.5, true},
		{"1", 1, true},
		{"14400", 14400, true},
		{"0.5", 0, false},
		{"0", 0, true}, // same meaning as omitting it, as in the API
		{"-10", 0, false},
		{"14401", 0, false},
		{"banana", 0, false},
		{"1e400", 0, false}, // +Inf through ParseFloat
	} {
		got, err := parseDurationFlag(tc.in)
		if tc.ok && err != nil {
			t.Errorf("--duration %q: got %v, want %v", tc.in, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("--duration %q: accepted, want rejection", tc.in)
		}
		if tc.ok && got != tc.want {
			t.Errorf("--duration %q: got %v, want %v", tc.in, got, tc.want)
		}
	}
}
