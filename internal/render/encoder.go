package render

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Video encoding defaults to libx264. The machine's hardware encoders are
// probed — a real tiny encode, not a name in `-encoders` — because encoder
// presence in the build says nothing about whether a GPU and its driver are
// actually there. Selection happens once per process; the probe costs one
// short ffmpeg run. The accepted knob values live in config (EncoderNames);
// everything about how an encoder is driven lives here.

// hwEncoderCandidates lists the hardware encoders auto probes, in preference
// order for this platform. H.264 only: the reel's compatibility contract is
// MP4/H.264, so HEVC stays an explicit named choice, never an auto pick.
func hwEncoderCandidates() []string {
	if runtime.GOOS == "windows" {
		return []string{"h264_nvenc", "h264_amf", "h264_qsv"}
	}
	return []string{"h264_nvenc", "h264_vaapi", "h264_qsv", "h264_amf"}
}

// Encoder is the codec clause every video encode in one render uses. All
// normalized clips must share it: the concat is stream copy, and a reel whose
// halves were encoded by different encoders is a file no decoder guarantees.
type Encoder struct {
	Name string
	HW   bool
}

// ProbeEncoder answers "can this ffmpeg start `name` right now" by running a
// real 8-frame encode through it. A name present in the build but unusable on
// this machine (no GPU, driver too old, out of sessions) fails here, which is
// the only answer that matters.
func ProbeEncoder(ctx context.Context, ffmpegBin, name string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, errOut, err := media.Run(pctx, ffmpegBin,
		"-hide_banner", "-nostdin", "-v", "error",
		"-f", "lavfi", "-i", "color=c=black:s=256x256:r=30:d=0.4",
		"-frames:v", "8",
		"-c:v", name,
		"-f", "null", "-",
	)
	if err != nil {
		return xcerr.E(xcerr.CodeRenderFailure,
			fmt.Sprintf("encoder %s is not usable on this machine", name),
			fmt.Errorf("%v: %s", err, media.Tail(errOut, 300)))
	}
	return nil
}

// SelectEncoder resolves the render.encoder knob against this machine.
// "auto" probes the platform's hardware candidates in order and falls back to
// libx264; a named encoder that fails its probe degrades to libx264 too —
// wanting speed must never cost the render. The returned note says what was
// chosen and why when it is not what the config asked for; callers surface it
// to the user (job log, doctor) rather than swallowing it.
func SelectEncoder(ctx context.Context, ffmpegBin, cfg string) (Encoder, string, error) {
	return selectEncoderWith(ctx, ffmpegBin, cfg, ProbeEncoder)
}

func selectEncoderWith(ctx context.Context, ffmpegBin, cfg string, probe func(context.Context, string, string) error) (Encoder, string, error) {
	if cfg == "" {
		cfg = config.EncoderAuto
	}
	switch cfg {
	case config.EncoderSoftware:
		return Encoder{Name: config.EncoderSoftware}, "", nil
	case config.EncoderAuto:
		for _, name := range hwEncoderCandidates() {
			if err := probe(ctx, ffmpegBin, name); err == nil {
				return Encoder{Name: name, HW: true}, "", nil
			}
		}
		return Encoder{Name: config.EncoderSoftware},
			"no hardware encoder usable on this machine — rendering with libx264", nil
	default:
		if !config.ValidEncoderName(cfg) {
			return Encoder{}, "", xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("invalid render.encoder %q (want one of: %s)", cfg, strings.Join(config.EncoderNames, ", ")), nil)
		}
		if err := probe(ctx, ffmpegBin, cfg); err != nil {
			return Encoder{Name: config.EncoderSoftware},
				fmt.Sprintf("render.encoder=%s is not usable on this machine — rendering with libx264", cfg), nil
		}
		return Encoder{Name: cfg, HW: true}, "", nil
	}
}

// encoderVideoArgs is the video codec clause for one encode. libx264 keeps
// the shipped -preset/-crf pair; the NVENC family maps CRF onto its own -cq
// scale with a +8 offset — measured on the reference workload (1080p30,
// docs/PERFORMANCE.md): cq=crf+8 lands at x264-crf20's file size with equal
// VMAF, while passing CRF raw produced 2.3x the bytes. Every other hardware
// encoder is driven at the vendor defaults — flags invented without the
// hardware to measure them on are how renders die on machines this repo
// never saw.
func encoderVideoArgs(name string, crf int) []string {
	switch name {
	case "h264_nvenc", "hevc_nvenc":
		return []string{"-c:v", name, "-rc", "vbr", "-cq", strconv.Itoa(crf + 8), "-b:v", "0"}
	case "", config.EncoderSoftware:
		return []string{"-c:v", config.EncoderSoftware, "-preset", "veryfast", "-crf", strconv.Itoa(crf)}
	default:
		return []string{"-c:v", name}
	}
}
