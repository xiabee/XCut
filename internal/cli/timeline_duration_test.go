package cli

import (
	"bytes"
	"strings"
	"testing"
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

// The CLI and the pipeline must refuse the same durations. Drift shows up as a
// different *author* of the error: the CLI quoting --duration for a value the
// pipeline would have accepted, or staying silent about one the pipeline
// refuses. Both are caught by running the real command, so nothing about
// validation is exported just to be comparable.
func TestDurationBoundRefusedByCLIWithItsOwnMessage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	var stdout, stderr bytes.Buffer
	drive := func(args ...string) int {
		stdout.Reset()
		stderr.Reset()
		return Run(args, &stdout, &stderr)
	}
	if code := drive("project", "create", "bound"); code != 0 {
		t.Fatalf("project create: %s%s", stdout.String(), stderr.String())
	}

	// Above the ceiling: the CLI itself must stop it, before any job is queued.
	if code := drive("timeline", "bound", "--duration", "14401"); code == 0 {
		t.Fatal("--duration 14401 accepted; the bound is not enforced on the way in")
	} else if !strings.Contains(stderr.String(), "--duration") {
		t.Fatalf(`14401 was refused by someone other than the CLI (the pipeline would accept it): %s`, stderr.String())
	}

	// At the ceiling: the CLI must forward it. An empty project then fails on
	// material, which is the proof the value travelled rather than being
	// rejected at the door.
	if code := drive("timeline", "bound", "--duration", "14400"); code == 0 {
		t.Fatal("expected the empty project to fail on material, not to succeed")
	} else if msg := stderr.String(); strings.Contains(msg, "--duration") {
		t.Fatalf("CLI refused a duration the pipeline supports: %s", msg)
	} else if !strings.Contains(msg, "no assets") {
		t.Fatalf("unexpected failure for an in-range duration: %s", msg)
	}
}
