package player

import (
	"context"
	"fmt"
	"strconv"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/xcerr"
)

// PresenceScanFPS is the decode rate for a presence scan. Rallies run seconds;
// 2 fps answers "is the person in frame" without decoding everything.
const PresenceScanFPS = 2.0

// SampleWidth is the decode width for signature/presence frames.
const SampleWidth = 320

// PatchFrac is the sliding-window side (fraction of frame width) used when
// scoring a frame: the best-matching patch answers "is there a person-sized
// region anywhere that looks like the signature" — the right statistic when
// the player is small in a wide drone frame, where a whole-frame average would
// drown a real match in court pixels.
const PatchFrac = 0.2

var (
	errEmptySignature = xcerr.E(xcerr.CodeValidation, "empty player signature", nil)
	errEmptyRange     = xcerr.E(xcerr.CodeValidation, "empty presence range", nil)
)

// Sample is one presence measurement: source second + patch match 0..1.
type Sample struct {
	T float64
	V float64
}

// scanTimes enumerates the decode timestamps for [start, end) at scan fps.
func scanTimes(start, end float64) []float64 {
	step := 1.0 / PresenceScanFPS
	n := int((end-start)/step) + 1
	times := make([]float64, 0, n)
	for t := start; t < end; t += step {
		times = append(times, t)
	}
	return times
}

// measureTimes spreads n samples across the duration and appends the spot's
// own moment (deduplicated, sorted). n scales mildly with duration: long
// recordings do not need hundreds of frames for a histogram.
func measureTimes(duration, spotAt float64) []float64 {
	if duration <= 0 {
		return []float64{spotAt}
	}
	n := 6
	if duration > 1200 {
		n = 10
	}
	times := make([]float64, 0, n+1)
	for i := 0; i < n; i++ {
		t := duration * (float64(i) + 0.5) / float64(n)
		if t > duration {
			t = duration
		}
		times = append(times, t)
	}
	times = append(times, spotAt)

	seen := make(map[float64]bool, len(times))
	out := times[:0]
	for _, t := range times {
		t = float64(int64(t*1000)) / 1000
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sortF64(out)
	return out
}

func sortF64(vs []float64) {
	for i := 1; i < len(vs); i++ {
		for j := i; j > 0 && vs[j] < vs[j-1]; j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}

// rectPixels converts a normalized rect to integer pixel geometry.

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func rectPixels(rect []float64, width, height int) (x, y, w, h int, err error) {
	if len(rect) != 4 {
		return 0, 0, 0, 0, xcerr.E(xcerr.CodeValidation, "spot rect must be x,y,w,h", nil)
	}
	for _, v := range rect {
		if v < 0 || v > 1 {
			return 0, 0, 0, 0, xcerr.E(xcerr.CodeValidation, "spot rect components must be 0..1", nil)
		}
	}
	x = int(clamp01(rect[0]) * float64(width))
	y = int(clamp01(rect[1]) * float64(height))
	w = int(clamp01(rect[2]) * float64(width))
	h = int(clamp01(rect[3]) * float64(height))
	if x+w > width {
		w = width - x
	}
	if y+h > height {
		h = height - y
	}
	if w < 2 || h < 2 {
		return 0, 0, 0, 0, xcerr.E(xcerr.CodeValidation, "spot rect too small", nil)
	}
	return x, y, w, h, nil
}

// f3 formats a timestamp for an ffmpeg argument.
func f3(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }

// frameGeom scales a source geometry to SampleWidth and derives the height.
func frameGeom(srcW, srcH int) (outW, outH int) {
	outW = SampleWidth
	outH = srcH * SampleWidth / srcW
	if outH < 2 {
		outH = 2
	}
	return outW, outH
}

// decodeRange runs one ffmpeg pass over [start,end] at outW×outH and streams
// every complete RGB frame to sink. The crop stage, when non-empty, runs
// before the scale. A single input + fps filter keeps the command short.
func decodeRange(ctx context.Context, tools media.Tools, path string,
	start, end float64, crop string, outW, outH int, sink func(frame []byte) error) error {

	dur := end - start
	vf := "fps=" + strconv.FormatFloat(PresenceScanFPS, 'f', -1, 64) +
		",scale=" + strconv.Itoa(outW) + ":" + strconv.Itoa(outH) + ",format=rgb24"
	if crop != "" {
		vf = crop + "," + vf
	}

	cmd := []string{
		"-hide_banner", "-nostdin", "-v", "error",
		"-ss", f3(start),
		"-t", f3(dur),
		"-i", path,
		"-vf", vf,
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-",
	}

	frameBytes := outW * outH * 3
	var carried []byte
	return media.StreamStdout(ctx, tools.FFmpeg, func(chunk []byte) error {
		buf := append(carried, chunk...)
		full := len(buf) - len(buf)%frameBytes
		for off := 0; off+frameBytes <= full; off += frameBytes {
			if err := sink(buf[off : off+frameBytes]); err != nil {
				carried = nil
				return err
			}
		}
		carried = append([]byte{}, buf[full:]...)
		return nil
	}, cmd...)
}

// s_patchMax scores one frame: the densest window's signature-match fraction.
func s_patchMax(sig Signature, w, h int, frame []byte) float64 {
	win := int(float64(w) * PatchFrac)
	if win%2 != 0 {
		win++
	}
	if win < 4 {
		win = 4
	}
	winH := win * h / w
	if winH < 4 {
		winH = 4
	}

	w1 := w + 1
	table := make([]int, w1*(h+1))
	for y := 0; y < h; y++ {
		row := y * w * 3
		sumRow := (y + 1) * w1
		prevRow := y * w1
		run := 0
		for x := 0; x < w; x++ {
			px := row + x*3
			r, g, bl := float64(frame[px]), float64(frame[px+1]), float64(frame[px+2])
			m := 0
			if r+g+bl >= 24 && sig.Bins[binIndex(rgbToHSV(r, g, bl))] > 0 {
				m = 1
			}
			run += m
			table[sumRow+x+1] = table[prevRow+x+1] + run
		}
	}
	area := win * winH
	best := 0.0
	for y := 0; y+winH <= h; y += 4 {
		for x := 0; x+win <= w; x += 4 {
			x2, y2 := x+win, y+winH
			sum := table[y2*w1+x2] - table[y*w1+x2] - table[y2*w1+x] + table[y*w1+x]
			if f := float64(sum) / float64(area); f > best {
				best = f
			}
		}
	}
	return best
}

// ScanPresence decodes the [start,end] range full-frame at PresenceScanFPS and
// returns the presence curve: one sample per decoded frame, value = the best
// patch's signature match (0..1). The analysis cache keys this under the
// signature hash, so re-seeding the spot re-scans.
func ScanPresence(ctx context.Context, tools media.Tools, path string, probeW, probeH int, sig Signature, start, end float64) ([]Sample, error) {
	if len(sig.Bins) == 0 {
		return nil, errEmptySignature
	}
	if end <= start {
		return nil, errEmptyRange
	}
	outW, outH := frameGeom(probeW, probeH)

	var samples []Sample
	t := start
	err := decodeRange(ctx, tools, path, start, end, "", outW, outH, func(frame []byte) error {
		samples = append(samples, Sample{T: t, V: s_patchMax(sig, outW, outH, frame)})
		t += 1.0 / PresenceScanFPS
		return nil
	})
	if err != nil {
		return nil, err
	}
	return samples, nil
}

// MeasureSignature samples the spot rect across the asset (spread over the
// duration, plus the moment the user drew the spot) and builds the model.
// Lighting changes across a session are real, so frames span the whole
// recording and the histogram absorbs them all.
func MeasureSignature(ctx context.Context, tools media.Tools, path string, rect []float64, spotAt, duration float64) (Signature, int, error) {
	probe, err := media.ProbeFile(ctx, tools, path)
	if err != nil {
		return Signature{}, 0, err
	}
	sx, sy, sw, sh, err := rectPixels(rect, probe.Width, probe.Height)
	if err != nil {
		return Signature{}, 0, err
	}
	outW, outH := frameGeom(sw, sh)
	crop := fmt.Sprintf("crop=%d:%d:%d:%d", sw, sh, sx, sy)

	builder := NewBuilder()
	frames := 0
	err = decodeRange(ctx, tools, path, 0, duration, crop, outW, outH, func(frame []byte) error {
		frames++
		return builder.Add(outW, outH, frame)
	})
	if err != nil {
		return Signature{}, 0, err
	}
	sig, err := builder.Build()
	if err != nil {
		return Signature{}, frames, err
	}
	return sig, frames, nil
}
