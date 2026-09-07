// Package testmedia generates deterministic synthetic video fixtures with
// FFmpeg lavfi sources for integration tests. No binary fixtures are committed
// to the repository; fixtures are always generated locally (SECURITY/license
// hygiene, tiny repo).
package testmedia

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Scene is one constant-color (or pattern) segment with a sine tone.
type Scene struct {
	Seconds   float64
	Color     string  // ffmpeg color name, e.g. "red"
	Frequency float64 // sine tone Hz
}

// HasFFmpeg reports whether ffmpeg is runnable in this environment.
func HasFFmpeg() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "ffmpeg", "-version").Run() == nil
}

// DefaultFixture returns a scene list exercising scene changes, motion (via
// hue drift is not possible with color sources; color cuts suffice for scene
// detection), and audio energy variation.
func DefaultFixture() []Scene {
	return []Scene{
		{Seconds: 2, Color: "red", Frequency: 440},
		{Seconds: 2, Color: "green", Frequency: 880},
		{Seconds: 2, Color: "blue", Frequency: 220},
		{Seconds: 2, Color: "white", Frequency: 660},
	}
}

// Generate writes an H.264/AAC MP4 composed of the given scenes and returns
// its path. Deterministic content for identical inputs.
func Generate(dir, name string, scenes []Scene, width, height, fps int) (string, error) {
	if len(scenes) == 0 {
		return "", errNoScenes
	}
	var args []string
	var vparts, aparts []string
	for i, sc := range scenes {
		d := formatFloat(sc.Seconds)
		args = append(args,
			"-f", "lavfi", "-i", "color=c="+sc.Color+":s="+itoa(width)+"x"+itoa(height)+":r="+itoa(fps)+":d="+d,
			"-f", "lavfi", "-i", "sine=frequency="+formatFloat(sc.Frequency)+":duration="+d,
		)
		vparts = append(vparts, "["+itoa(i*2)+":v]")
		aparts = append(aparts, "["+itoa(i*2+1)+":a]")
	}
	for i := range scenes {
		vparts[i] += "setsar=1[v" + itoa(i) + "]"
		aparts[i] += "aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo[a" + itoa(i) + "]"
	}
	var vc, ac []string
	for i := range scenes {
		vc = append(vc, "[v"+itoa(i)+"]")
		ac = append(ac, "[a"+itoa(i)+"]")
	}
	filter := strings.Join(vparts, ";") + ";" +
		strings.Join(vc, "") + "concat=n=" + itoa(len(scenes)) + ":v=1:a=0[vout];" +
		strings.Join(aparts, ";") + ";" +
		strings.Join(ac, "") + "concat=n=" + itoa(len(scenes)) + ":v=0:a=1[aout]"

	out := filepath.Join(dir, name)
	args = append(args,
		"-filter_complex", filter,
		"-map", "[vout]", "-map", "[aout]",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "28", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "96k",
		"-movflags", "+faststart",
		"-y", out,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if outb, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(out)
		return "", errFFmpeg(outb, err)
	}
	return out, nil
}

type constErr string

func (e constErr) Error() string { return string(e) }

const errNoScenes = constErr("testmedia: no scenes given")

// GenerateAudio writes an audio-only AAC fixture from one lavfi audio
// source expression (used for analyzer tests: bursts, transients, tones).
func GenerateAudio(dir, name, lavfi string) (string, error) {
	out := filepath.Join(dir, name)
	args := []string{
		"-hide_banner", "-v", "error",
		"-f", "lavfi", "-i", lavfi,
		"-c:a", "aac", "-b:a", "96k",
		"-y", out,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if outb, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(out)
		return "", errFFmpeg(outb, err)
	}
	return out, nil
}

func errFFmpeg(out []byte, err error) error {
	snippet := string(out)
	if len(snippet) > 2000 {
		snippet = snippet[len(snippet)-2000:]
	}
	return constErr("testmedia: ffmpeg failed: " + err.Error() + ": " + snippet)
}

func itoa(n int) string { return fmtInt(n) }

func formatFloat(f float64) string {
	s := fmtFloat(f)
	// lavfi accepts decimals; trim trailing zeros for tidy args.
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if s[len(s)-1] == '.' {
		s += "0"
	}
	return s
}
