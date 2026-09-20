package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Probe is the distilled result of ffprobing a media file.
type Probe struct {
	Path        string          `json:"path"`
	DurationSec float64         `json:"duration_s"`
	Width       int             `json:"width"`
	Height      int             `json:"height"`
	FPS         float64         `json:"fps"`
	VideoCodec  string          `json:"video_codec"`
	AudioCodec  string          `json:"audio_codec,omitempty"`
	HasAudio    bool            `json:"has_audio"`
	Bitrate     int64           `json:"bitrate"`
	SizeBytes   int64           `json:"size_bytes"`
	FormatName  string          `json:"format_name"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}

// probeOutput mirrors the subset of ffprobe JSON we consume.
type probeOutput struct {
	Streams []struct {
		Index        int    `json:"index"`
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		RFrameRate   string `json:"r_frame_rate"`
		SampleRate   string `json:"sample_rate"`
		Channels     int    `json:"channels"`
		Duration     string `json:"duration"`
		BitRate      string `json:"bit_rate"`
		Disposition  struct {
			Default int `json:"default"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		BitRate    string `json:"bit_rate"`
		Size       string `json:"size"`
	} `json:"format"`
}

// ProbeFile runs ffprobe against path and returns distilled metadata.
// The file must exist (NotFound otherwise). A file ffprobe cannot parse is
// reported as UnsupportedMedia — user media is untrusted input (SECURITY.md).
func ProbeFile(ctx context.Context, tools Tools, path string) (*Probe, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, xcerr.E(xcerr.CodeNotFound, "file does not exist", err)
		}
		return nil, xcerr.E(xcerr.CodeValidation, "cannot access file", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Note: "--" is not portable across ffprobe builds; path is passed as the
	// final argument and is never interpreted as a shell string (no shell).
	out, errOut, err := Run(ctx, tools.FFprobe,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, xcerr.E(xcerr.CodeFFmpegFailure, "probe timed out or was cancelled", ctx.Err())
		}
		// A missing/unrunnable ffprobe is an environment problem, not a
		// property of the file — mislabeling it as unsupported media sends
		// users chasing the wrong file.
		if errors.Is(err, exec.ErrNotFound) {
			return nil, xcerr.E(xcerr.CodeFFmpegFailure,
				"ffprobe is not runnable — install it or set XCUT_FFPROBE (see xcut doctor)", err)
		}
		// Keep the tail of ffprobe's own output: exit status alone gives the
		// user nothing to act on ("Invalid data" vs missing decoder etc.).
		tail := string(errOut)
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		return nil, xcerr.E(xcerr.CodeUnsupportedMedia,
			"file is not a supported media file", fmt.Errorf("%v: %s", err, tail))
	}

	var po probeOutput
	if err := json.Unmarshal(out, &po); err != nil {
		return nil, xcerr.E(xcerr.CodeFFmpegFailure, probeParseFailure(out), err)
	}

	p := &Probe{Path: path}
	fi, statErr := os.Stat(path)
	if statErr == nil {
		p.SizeBytes = fi.Size()
	}

	p.FormatName = po.Format.FormatName
	if d, ok := atof(po.Format.Duration); ok {
		p.DurationSec = d
	}
	if b, ok := atoi(po.Format.BitRate); ok {
		p.Bitrate = b
	}

	var fpsStr string
	for _, s := range po.Streams {
		switch s.CodecType {
		case "video":
			if p.VideoCodec == "" || s.Disposition.Default == 1 {
				p.VideoCodec = s.CodecName
				p.Width = s.Width
				p.Height = s.Height
				fpsStr = pickFPS(s.AvgFrameRate, s.RFrameRate, fpsStr)
			}
		case "audio":
			if p.AudioCodec == "" || s.Disposition.Default == 1 {
				p.AudioCodec = s.CodecName
				p.HasAudio = true
			}
		}
	}
	if p.VideoCodec == "" {
		return nil, xcerr.E(xcerr.CodeUnsupportedMedia, "no video stream found", nil)
	}
	if f, ok := atof(fpsStr); ok {
		p.FPS = f
	}
	// Stream duration can be more precise than format duration for some
	// containers; prefer a positive stream duration when format lacks one.
	if p.DurationSec == 0 {
		for _, s := range po.Streams {
			if s.CodecType == "video" {
				if d, ok := atof(s.Duration); ok {
					p.DurationSec = d
					break
				}
			}
		}
	}
	p.Raw = json.RawMessage(out)
	return p, nil
}

// vendorLogMarkers identify a decoder plugin writing its own log to stdout.
// On a Kylin V10 aarch64 box the Hisilicon OMX layer prints one line per
// component call — `12:09:31.971  3917874 3917874 [LOG_INFO] ComponentCore:
// VIDEO:[OMX_GetHandle]...` — interleaving with ffprobe's JSON *mid-line*, so
// the document cannot be cleaned without risking a value that silently absorbs
// log text. That build is therefore refused with a remedy, not repaired.
var vendorLogMarkers = []string{"[LOG_INFO]", "[LOG_ERR]", "[LOG_WARN]", "OMX_"}

func probeParseFailure(out []byte) string {
	for _, m := range vendorLogMarkers {
		if strings.Contains(string(out), m) {
			return "this ffprobe build writes decoder-plugin logs to stdout and corrupts its own JSON output — " +
				"install a stock FFmpeg or point XCUT_FFPROBE at one (xcut doctor shows which binary is used)"
		}
	}
	return "cannot parse probe output"
}

func pickFPS(avg, r, cur string) string {
	if v, ok := atof(avg); ok && v > 0 {
		return avg
	}
	if v, ok := atof(r); ok && v > 0 {
		return r
	}
	return cur
}

// atof parses a decimal or rational ("30000/1001") string.
func atof(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0, false
	}
	if num, den, ok := strings.Cut(s, "/"); ok {
		n, err1 := strconv.ParseFloat(num, 64)
		d, err2 := strconv.ParseFloat(den, 64)
		if err1 != nil || err2 != nil || d == 0 {
			return 0, false
		}
		return n / d, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func atoi(s string) (int64, bool) {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// HumanDuration renders seconds as a compact human string (logs/UI).
func HumanDuration(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	total := time.Duration(sec * float64(time.Second))
	h := int(total.Hours())
	m := int(total.Minutes()) % 60
	s := int(total.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	default:
		return fmt.Sprintf("%d:%02d", m, s)
	}
}
