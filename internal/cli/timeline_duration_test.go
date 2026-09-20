package cli

import (
	"strconv"
	"testing"

	"github.com/xiabee/XCut/internal/pipeline"
)

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

// The CLI must not promise a range the pipeline then refuses, nor refuse one the
// pipeline would have accepted — the two bounds live in different packages, so
// agreement is a contract that can drift silently. This is the behavior the
// removed "is the constant 4 hours?" test only pretended to protect.
func TestCLIDurationAgreesWithPipelineBound(t *testing.T) {
	for _, v := range []string{
		"0", "1", "5", "60", "3600", "10800", "14400", "14401", "20000", "86400",
	} {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatalf("fixture %q should parse: %v", v, err)
		}
		_, cliErr := parseDurationFlag(v)
		pipeErr := pipeline.TimelineRequest{Style: "generic_highlight", Duration: f}.Validate()
		if (cliErr == nil) != (pipeErr == nil) {
			t.Errorf("--duration %s: CLI accepted=%v but pipeline accepted=%v — the two bounds disagree",
				v, cliErr == nil, pipeErr == nil)
		}
	}
}
