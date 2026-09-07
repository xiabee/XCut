// Package testmedia generates deterministic synthetic video fixtures with
// FFmpeg lavfi sources for integration tests. No binary fixtures are committed
// to the repository; fixtures are always generated locally (SECURITY/license
// hygiene, tiny repo).
package testmedia

import (
	"context"
	"fmt"
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

// GenerateMotionCorner builds a silent fixture whose only motion is a
// testsrc2 patch in the top-left quadrant over a still black canvas — the
// synthetic shape for ROI motion isolation tests.
func GenerateMotionCorner(dir, name string, width, height, fps, seconds int) (string, error) {
	out := filepath.Join(dir, name)
	d := formatFloat(float64(seconds))
	filter := "[0:v][1:v]overlay=0:0[v]"
	args := []string{
		"-hide_banner", "-v", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=black:s=%dx%d:r=%d:d=%s", width, height, fps, d),
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=s=%dx%d:r=%d:d=%s", width/2, height/2, fps, d),
		"-filter_complex", filter,
		"-map", "[v]",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "28", "-pix_fmt", "yuv420p",
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
	if !strings.Contains(s, ".") {
		return s // integers ("10") must never lose their trailing zero
	}
	// lavfi accepts decimals; trim trailing zeros for tidy args.
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if s[len(s)-1] == '.' {
		s += "0"
	}
	return s
}

// RallySpec is one synthetic rally window: active video + periodic hit
// bursts, embedded in an otherwise still and silent timeline.
type RallySpec struct {
	Start, End float64 // seconds on the output timeline
	HitEvery   float64 // seconds between hit transients inside the rally
}

// GenerateRally builds a badminton-like synthetic fixture: testsrc2 motion
// and hit-burst audio during rallies, stillness and silence between them.
// Ground truth (the rally windows) is known by construction — the eval
// harness scores the pipeline against it.
func GenerateRally(dir, name string, width, height, fps int, duration float64, rallies []RallySpec) (string, error) {
	if len(rallies) == 0 || duration <= 0 {
		return "", errNoScenes
	}
	isRally := func(t float64) bool {
		for _, r := range rallies {
			if t >= r.Start-1e-9 && t < r.End-1e-9 {
				return true
			}
		}
		return false
	}

	var args []string
	var vlabels, alabels []string
	idx := 0
	segments := 0
	for t := 0.0; t < duration-1e-9; {
		// Find the next boundary: nearest rally edge after t.
		next := duration
		for _, r := range rallies {
			if r.Start > t+1e-9 && r.Start < next {
				next = r.Start
			}
			if r.End > t+1e-9 && r.End < next {
				next = r.End
			}
		}
		if next > duration {
			next = duration
		}
		len := formatFloat(next - t)
		hitEvery := 0.7
		for _, r := range rallies {
			if t >= r.Start-1e-9 && t < r.End-1e-9 && r.HitEvery > 0 {
				hitEvery = r.HitEvery
			}
		}
		if isRally(t) {
			args = append(args, "-f", "lavfi", "-i",
				"testsrc2=s="+itoa(width)+"x"+itoa(height)+":r="+itoa(fps)+":d="+len)
			args = append(args, "-f", "lavfi", "-i",
				"sine=frequency=1000:duration="+len+
					",volume=volume='if(lt(mod(t\\,"+formatFloat(hitEvery)+")\\,0.05)\\,1\\,0.003)':eval=frame")
		} else {
			args = append(args, "-f", "lavfi", "-i",
				"color=c=black:s="+itoa(width)+"x"+itoa(height)+":r="+itoa(fps)+":d="+len)
			args = append(args, "-f", "lavfi", "-i",
				"aevalsrc=0:d="+len+":s=44100")
		}
		vlabels = append(vlabels, "["+itoa(idx)+":v]setsar=1[v"+itoa(segments)+"]")
		alabels = append(alabels, "["+itoa(idx+1)+":a]aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo[a"+itoa(segments)+"]")
		idx += 2
		segments++
		t = next
	}
	var vrefs, arefs []string
	for i := 0; i < segments; i++ {
		vrefs = append(vrefs, "[v"+itoa(i)+"]")
		arefs = append(arefs, "[a"+itoa(i)+"]")
	}
	filter := strings.Join(vlabels, ";") + ";" + strings.Join(alabels, ";") + ";" +
		strings.Join(vrefs, "") + "concat=n=" + itoa(segments) + ":v=1:a=0[vout];" +
		strings.Join(arefs, "") + "concat=n=" + itoa(segments) + ":v=0:a=1[aout]"

	out := filepath.Join(dir, name)
	args = append(args,
		"-filter_complex", filter,
		"-map", "[vout]", "-map", "[aout]",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "28", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "96k",
		"-y", out,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if outb, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(out)
		return "", errFFmpeg(outb, err)
	}
	return out, nil
}
