package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/subs"
	"github.com/xiabee/XCut/internal/worker"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("subtitles", "speech-to-text subtitles via the AI sidecar (SRT or styled ASS)",
		usageSyntax("xcut subtitles <media-file> [--ass] [--out path] [--lang code] [--model name]"), cmdSubtitles)
}

// cmdSubtitles transcribes a media file through the configured AI sidecar
// and writes subtitle files. The core never ships or downloads models: the
// sidecar owns Whisper (or any other speech recognizer) and honestly
// reports what it can do — a missing model is a capability gap with a hint,
// not a broken pipeline.
func cmdSubtitles(a *App, args []string) error {
	if len(args) == 0 {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut subtitles <media-file> [--ass] [--out path] [--lang code] [--model name]", nil)
	}
	mediaPath, rest := args[0], args[1:]
	if strings.HasPrefix(mediaPath, "-") {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut subtitles <media-file> [--ass] [--out path] [--lang code] [--model name]", nil)
	}
	abs, err := filepath.Abs(mediaPath)
	if err != nil {
		return xcerr.E(xcerr.CodeValidation, "cannot resolve path: "+mediaPath, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return xcerr.E(xcerr.CodeNotFound, "file does not exist", err)
	}

	karaoke, outPath, lang, model := false, "", "", ""
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--ass":
			karaoke = true
		case "--out":
			i++
			if i >= len(rest) {
				return xcerr.E(xcerr.CodeValidation, "--out needs a path", nil)
			}
			outPath = rest[i]
		case "--lang":
			i++
			if i >= len(rest) {
				return xcerr.E(xcerr.CodeValidation, "--lang needs a language code", nil)
			}
			lang = rest[i]
		case "--model":
			i++
			if i >= len(rest) {
				return xcerr.E(xcerr.CodeValidation, "--model needs a model name", nil)
			}
			model = rest[i]
		default:
			return xcerr.E(xcerr.CodeValidation, "unknown flag: "+rest[i], nil)
		}
	}

	// Resolve the effective output path first so the source-overwrite guard
	// fires before any sidecar work: subtitles are derived data, the media
	// is the user's original — same rule as render (never clobber the input).
	if outPath == "" {
		ext := ".srt"
		if karaoke {
			ext = ".ass"
		}
		outPath = strings.TrimSuffix(abs, filepath.Ext(abs)) + ext
	}
	if pipeline.SameFileOrPath(outPath, abs) {
		return xcerr.E(xcerr.CodeValidation,
			"subtitle output would overwrite the source media — pick a different --out path", nil)
	}

	bin := worker.ResolveAIBin(a.Cfg.Workers.AIBin)
	if bin == "" {
		return xcerr.E(xcerr.CodeNotFound,
			"no AI sidecar available — subtitles need a transcript sidecar (install scripts/xcut-ai-sidecar.py on PATH as xcut-ai, or set workers.ai_bin)", nil)
	}

	caps, err := worker.Capabilities(a.Ctx, bin)
	if err != nil {
		return err
	}
	if !sidecarOffers(caps, "transcript") {
		return xcerr.E(xcerr.CodeNotFound,
			"the AI sidecar does not offer speech transcription — install a Whisper backend it can use (openai-whisper, faster-whisper or whisper-cli) and check `xcut doctor`", nil)
	}
	if m := transcriptModel(caps); m != nil && !m.Available {
		return xcerr.E(xcerr.CodeNotFound,
			"transcription model is advertised but not installed ("+m.Detail+") — install it, or pick another with --model", nil)
	}

	params := map[string]any{"analyzer": "transcript"}
	if lang != "" {
		params["language"] = lang
	}
	if model != "" {
		params["model"] = model
	}
	raw, err := worker.AIAnalyze(a.Ctx, bin, "analyze", abs, params, 10*time.Minute)
	if err != nil {
		return err
	}
	t, err := subs.Parse(raw)
	if err != nil {
		return err
	}

	f, err := os.Create(outPath)
	if err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot create subtitle file", err)
	}
	defer f.Close()
	// --ass asks for the styled file. Only the karaoke fill needs to know where
	// each syllable fell; the frame, the wrap and the dwell do not, so a sidecar
	// that times lines gets captions rather than an error.
	kind := "srt"
	write := func() error { return subs.WriteSRT(t, f) }
	if karaoke {
		if t.HasWordTimings() {
			kind = "karaoke ass"
			write = func() error { return subs.WriteKaraokeASS(t, subs.KaraokeStyle{}, f) }
		} else {
			kind = "caption ass"
			write = func() error { return subs.WriteCaptionASS(t, subs.KaraokeStyle{}, f) }
		}
	}
	if err := write(); err != nil {
		return err
	}

	fmt.Fprintf(a.Stdout, "wrote %s subtitles (%d segments, language %q): %s\n",
		kind, len(t.Segments), t.Language, outPath)
	return nil
}

// sidecarOffers reports whether the capabilities payload advertises a
// transcript model entry (a sidecar without the analyze op fails later in
// AIAnalyze with its own error).
func sidecarOffers(c *worker.AICapabilities, analyzer string) bool {
	for _, m := range c.Models {
		if strings.HasPrefix(m.Name, analyzer) {
			return true
		}
	}
	return false
}

func transcriptModel(c *worker.AICapabilities) *worker.AIModel {
	for i := range c.Models {
		if strings.HasPrefix(c.Models[i].Name, "transcript") {
			return &c.Models[i]
		}
	}
	return nil
}
