package media

import (
	"encoding/json"
	"strings"
	"testing"
)

// kylinProbeOutput is the shape captured from a Kylin V10 aarch64 box, where
// the Hisilicon OMX decoder plugin writes its own log to fd 1 while ffprobe is
// writing JSON to that same fd.
//
// The interleaving is mid-line, not merely line-to-line: a log fragment can
// land inside a string value ("codec_long_name": "H.264 12:09:31.974 …"), so
// any filter loose enough to recover a parseable document is also loose enough
// to let log text into the metadata and report it as fact. That is why this
// build is refused with a remedy instead of repaired — and
// TestFilteringWouldNotBeSafe keeps that reason from being lost.
func kylinProbeOutput() []byte {
	return []byte("{\n" +
		"12:09:31.971  3917874 3917874 [LOG_INFO] ComponentCore: VIDEO:[OMX_GetHandle]:[109] name OMX.hisi.video.decoder.avc\n" +
		"12:09:31.974  3917874 3917874 [LOG_INFO] OMXParms: VIDEO:[PrintPortDefinition]:[31] width 640, height 360, framerate 30\n" +
		"    \"programs\": [],\n" +
		"    \"streams\": [\n" +
		"        {\n" +
		"            \"index\": 0,\n" +
		"            \"codec_name\": \"h264\",\n" +
		"            \"codec_type\": \"video\",\n" +
		"            \"codec_long_name\": \"H.264 12:09:31.974  3917874 3917874 [LOG_INFO] OMXParms: VIDEO:[SetParameter]\",\n" +
		"            \"width\": 640,\n" +
		"            \"height\": 360\n" +
		"12:09:31.989  3917874 3917874 [LOG_ERR] : inotify_add_watch\n" +
		"        }\n" +
		"    ]\n}\n")
}

func TestProbeParseFailureNamesTheHostileBuild(t *testing.T) {
	msg := probeParseFailure(kylinProbeOutput())
	if !strings.Contains(msg, "ffprobe build") {
		t.Fatalf("vendor contamination diagnosed as a generic parse error: %q", msg)
	}
	// The message has to carry the way out, not just the blame.
	for _, want := range []string{"XCUT_FFPROBE", "doctor"} {
		if !strings.Contains(msg, want) {
			t.Errorf("guidance missing %q: %q", want, msg)
		}
	}
	// An environment failure must never be reported as an unsupported file:
	// that sends users to re-encode media that is fine.
	if strings.Contains(msg, "not a supported media file") {
		t.Errorf("environment failure blamed on the media file: %q", msg)
	}
}

func TestProbeParseFailureStaysGenericOtherwise(t *testing.T) {
	if got := probeParseFailure([]byte("not json at all")); got != "cannot parse probe output" {
		t.Errorf("generic case reported %q", got)
	}
	if got := probeParseFailure(nil); got != "cannot parse probe output" {
		t.Errorf("empty buffer reported %q", got)
	}
}

// TestFilteringWouldNotBeSafe documents the rejected repair: dropping the lines
// that do not look like JSON yields a *valid* document here, but a valid
// document with a corrupted value. A repair that can silently misreport media
// metadata is worse than a loud refusal, so ProbeFile keeps refusing.
func TestFilteringWouldNotBeSafe(t *testing.T) {
	dirty := kylinProbeOutput()
	if json.Valid(dirty) {
		t.Fatal("fixture stopped resembling the captured contamination")
	}
	var kept []string
	for _, line := range strings.Split(string(dirty), "\n") {
		t2 := strings.TrimLeft(line, " \t")
		if t2 == "" {
			continue
		}
		switch t2[0] {
		case '{', '}', '[', ']', '"':
			kept = append(kept, line)
		}
	}
	cleaned := []byte(strings.Join(kept, "\n"))
	if !json.Valid(cleaned) {
		t.Skip("this fixture is now unfilterable too; the refusal path covers it")
	}
	var doc struct {
		Streams []struct {
			CodecLongName string `json:"codec_long_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(cleaned, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Streams) != 1 {
		t.Fatalf("streams lost: %d", len(doc.Streams))
	}
	if !strings.Contains(doc.Streams[0].CodecLongName, "LOG_INFO") {
		t.Error("the filter no longer smuggles log text into metadata; re-check whether a repair is safe after all")
	}
}
