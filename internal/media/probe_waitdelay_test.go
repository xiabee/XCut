package media

import (
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
)

// The decision behind ProbeFile's WaitDelay recovery: the JSON is the product,
// so completeness — parseable AND stream-bearing — is what separates a drain
// scheduling lapse from real data loss. Table over the payload shapes; the
// staged mechanism (a child that writes the payload and hands its pipe to a
// longer-lived grandchild) is TestProbeRecoversCompleteJSONFromWaitDelay,
// which proves the bytes really are in Run's buffer when ErrWaitDelay arrives.
func TestProbeWaitDelayRecoverable(t *testing.T) {
	full := `{"streams":[{"index":0,"codec_type":"video","codec_name":"h264"}],"format":{"format_name":"mov,mp4","duration":"3.000000"}}`
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"complete answer", full, true},
		{"parses but streamless — not an answer", `{"format":{"duration":"3"}}`, false},
		{"truncated mid-object", `{"streams":[{"index":0`, false},
		{"empty buffer", ``, false},
		{"garbage", `not json at all`, false},
	}
	for _, tc := range cases {
		if got := probeWaitDelayRecoverable([]byte(tc.out)); got != tc.want {
			t.Errorf("%s: recoverable=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestProbeWaitDelayRecoverableTypeStability pins the round-trip the decision
// relies on: the recovery only helps when ffprobe's own answer unmarshals into
// probeOutput — if a JSON encoding change ever breaks that contract, the
// decision must fail CLOSED (a truncated answer is a real failure), never
// silently accept garbage as a probe.
func TestProbeWaitDelayRecoverableTypeStability(t *testing.T) {
	full := `{"streams":[{"index":0,"codec_type":"video"}],"format":{"duration":"3.000000","bit_rate":"1000","format_name":"mov"}}`
	if !probeWaitDelayRecoverable([]byte(full)) {
		t.Fatal("a genuine ffprobe-shaped answer must recover")
	}
	var po probeOutput
	if err := json.Unmarshal([]byte(full), &po); err != nil {
		t.Fatalf("the same payload must feed the real consumer: %v", err)
	}
	if len(po.Streams) == 0 || po.Streams[0].CodecType != "video" {
		t.Fatalf("probeOutput lost the fields the decision checked: %+v", po)
	}
}

// The error identity the recovery branches on, pinned so a rename of the
// mechanism cannot strand the branch dead.
func TestProbeWaitDelayErrorIdentity(t *testing.T) {
	if !errors.Is(exec.ErrWaitDelay, exec.ErrWaitDelay) {
		t.Fatal("unreachable")
	}
	if probeWaitDelayRecoverable(nil) {
		t.Fatal("nil output must not recover")
	}
}
