package media

import (
	"strings"
	"testing"
)

// TestMemoryMaxAnswerReadsWhatSystemdSays covers the decision that decides whether an
// operator sees OK or WARN. The values are the ones measured in the field: a byte count
// from a host that attaches the scope, "infinity" from the Kylin hybrid hierarchy whose
// systemd-run exits 0 while the child allocates freely, and the empty string from a unit
// the manager has never heard of.
func TestMemoryMaxAnswerReadsWhatSystemdSays(t *testing.T) {
	cases := []struct {
		name        string
		value       string
		wantMB      int64
		wantApplied bool
		wantIn      string
	}{
		{"the number we asked for", "134217728", 128, true, "134217728"},
		{"default cap in bytes", "1610612736", 1536, true, "1610612736"},
		{"infinity is not a cap", "infinity", 128, false, "not attached"},
		{"a unit nobody knows", "", 128, false, "answers nothing"},
		{"one byte off is off", "134217727", 128, false, "not the 128 MB"},
		{"a clamped limit is named", "67108864", 128, false, "67108864"},
		{"manager words are quoted back", "no such property", 128, false, "no such property"},
		{"surrounding whitespace is not a different answer", "  134217728\n", 128, true, "134217728"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applied, known, detail := memoryMaxAnswer(tc.value, tc.wantMB)
			if applied != tc.wantApplied {
				t.Errorf("MemoryMax=%q with a %d MB cap: applied=%v, want %v (%s)",
					tc.value, tc.wantMB, applied, tc.wantApplied, detail)
			}
			if !known {
				t.Errorf("the manager answered %q, so this must not be reported as unmeasurable", tc.value)
			}
			if detail == "" {
				t.Error("every answer needs the words that go to an operator")
			}
			if !strings.Contains(detail, tc.wantIn) {
				t.Errorf("detail = %q, want it to name %q", detail, tc.wantIn)
			}
		})
	}
}

// TestMemoryMaxAnswerNeverCallsSilenceApplied is the one-line version of the failure this
// whole mechanism exists to avoid: on a host whose manager attaches nothing, the honest
// answer is "not applied". Asserting it for both empty-shaped answers keeps a future
// simplification ("if the call succeeded, the cap holds") from going green.
func TestMemoryMaxAnswerNeverCallsSilenceApplied(t *testing.T) {
	for _, v := range []string{"", "infinity", "0", "-1"} {
		if applied, _, _ := memoryMaxAnswer(v, 1536); applied {
			t.Errorf("MemoryMax=%q was read as applied", v)
		}
	}
}
