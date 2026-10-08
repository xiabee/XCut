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

// frameGeom scales a source geometry to the sampling resolution: the LONGER
// side lands on SampleWidth. Landscape keeps the width rule it always had;
// portrait media and tall spot crops cap their height instead — the width
// rule sent their byte volume (frame × fps × duration) past the streaming
// budget on shapes that are perfectly scannable once the longer side is the
// capped one.
func frameGeom(srcW, srcH int) (outW, outH int) {
	if srcH > srcW {
		outH = SampleWidth
		outW = srcW * SampleWidth / srcH
	} else {
		outW = SampleWidth
		outH = srcH * SampleWidth / srcW
	}
	if outW < 2 {
		outW = 2
	}
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

// s_patchMaxMR is the multi-region patch-max: the same sliding statistic, but
// each candidate window is scored as a hypothesized person box — the three
// bands are cut from the WINDOW at the fractions the model was trained on,
// which only means anatomy when the window has the spot's own shape. So the
// patch keeps the spot's aspect (sampled height-per-width, measured from the
// spot rect by the caller), clamped into the frame so a portrait spot on
// landscape video still scans — the clamp degrades the band correspondence
// for that degenerate seeding, and is the honest behavior: every window
// still scores, none is dropped silently.
func s_patchMaxMR(mr MultiRegionSignature, spotHPerW float64, w, h int, frame []byte) float64 {
	win := int(float64(w) * PatchFrac)
	if win%2 != 0 {
		win++
	}
	if win < 4 {
		win = 4
	}
	winH := int(float64(win) * spotHPerW)
	if winH%2 != 0 {
		winH++
	}
	if winH > h {
		winH = h
	}
	if winH < 4 {
		winH = 4
	}

	best := 0.0
	for y := 0; y+winH <= h; y += 4 {
		for x := 0; x+win <= w; x += 4 {
			if f := scoreWindow(&mr, frame, w*3, x, y, win, winH); f > best {
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

// ScanPresenceMR is the multi-region scan: the decode is the same, the per-
// frame statistic scores every candidate window as a hypothesized person box
// against the band model. spotHPerW is the spot rect's sampled height-per-
// width — the aspect the patch keeps so its bands mean the anatomy the
// histograms were built from (clamped into the frame for degenerate seeds;
// see s_patchMaxMR).
func ScanPresenceMR(ctx context.Context, tools media.Tools, path string, probeW, probeH int, mr MultiRegionSignature, spotHPerW float64, start, end float64) ([]Sample, error) {
	for band := 0; band < RegionCount; band++ {
		if len(mr.Bands[band]) == 0 {
			return nil, errEmptySignature
		}
	}
	if end <= start {
		return nil, errEmptyRange
	}
	if spotHPerW <= 0 {
		return nil, xcerr.E(xcerr.CodeValidation, "spot aspect must be positive", nil)
	}
	outW, outH := frameGeom(probeW, probeH)

	var samples []Sample
	t := start
	err := decodeRange(ctx, tools, path, start, end, "", outW, outH, func(frame []byte) error {
		samples = append(samples, Sample{T: t, V: s_patchMaxMR(mr, spotHPerW, outW, outH, frame)})
		t += 1.0 / PresenceScanFPS
		return nil
	})
	if err != nil {
		return nil, err
	}
	return samples, nil
}

// MeasureSignature samples the spot rect across the asset (spread over the
// duration, plus the moment the user drew the spot) and builds both models
// from the one decode pass: the single histogram (Phase 1) and the per-band
// model (Phase 2). The band model is nil when the spot's sampled shape
// cannot yield three usable bands — a wide-short crop starves the head band
// — and that spot then runs on the single histogram alone.
func MeasureSignature(ctx context.Context, tools media.Tools, path string, rect []float64, spotAt, duration float64) (Signature, *MultiRegionSignature, int, error) {
	probe, err := media.ProbeFile(ctx, tools, path)
	if err != nil {
		return Signature{}, nil, 0, err
	}
	sx, sy, sw, sh, err := rectPixels(rect, probe.Width, probe.Height)
	if err != nil {
		return Signature{}, nil, 0, err
	}
	outW, outH := frameGeom(sw, sh)
	crop := fmt.Sprintf("crop=%d:%d:%d:%d", sw, sh, sx, sy)

	builder := NewBuilder()
	mrBuilder := NewMultiRegionBuilder()
	frames := 0
	err = decodeRange(ctx, tools, path, 0, duration, crop, outW, outH, func(frame []byte) error {
		frames++
		if err := builder.Add(outW, outH, frame); err != nil {
			return err
		}
		return mrBuilder.AddFeed(outW, outH, frame)
	})
	if err != nil {
		return Signature{}, nil, 0, err
	}
	sig, err := builder.Build()
	if err != nil {
		return Signature{}, nil, frames, err
	}
	var mr *MultiRegionSignature
	if m, merr := mrBuilder.Build(); merr == nil {
		mr = &m
	}
	return sig, mr, frames, nil
}

// MeasureSignatureFromImage builds both models from a still image — the
// photo-reference seeding (PERSON_FILTER_ROADMAP Phase 3's CLI half): the
// rect is normalized to the PHOTO, resolution-independent like every spot
// rect, and one decoded frame feeds the same builders the video measure
// uses. A spot whose sampled shape cannot yield three bands comes back with
// a nil band model, exactly like the video measure.
func MeasureSignatureFromImage(ctx context.Context, tools media.Tools, path string, rect []float64) (Signature, *MultiRegionSignature, error) {
	probe, err := media.ProbeFile(ctx, tools, path)
	if err != nil {
		return Signature{}, nil, err
	}
	sx, sy, sw, sh, err := rectPixels(rect, probe.Width, probe.Height)
	if err != nil {
		return Signature{}, nil, err
	}
	outW, outH := frameGeom(sw, sh)
	crop := fmt.Sprintf("crop=%d:%d:%d:%d", sw, sh, sx, sy)

	vf := "scale=" + strconv.Itoa(outW) + ":" + strconv.Itoa(outH) + ",format=rgb24"
	cmd := []string{
		"-hide_banner", "-nostdin", "-v", "error",
		"-i", path,
		"-vf", crop + "," + vf,
		"-frames:v", "1",
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-",
	}
	frameBytes := outW * outH * 3
	var frame []byte
	err = media.StreamStdout(ctx, tools.FFmpeg, func(chunk []byte) error {
		frame = append(frame, chunk...)
		if len(frame) > frameBytes {
			return xcerr.E(xcerr.CodeUnsupportedMedia, "the image decoded to more than one frame", nil)
		}
		return nil
	}, cmd...)
	if err != nil {
		return Signature{}, nil, err
	}
	if len(frame) != frameBytes {
		return Signature{}, nil, xcerr.E(xcerr.CodeUnsupportedMedia,
			fmt.Sprintf("the image decoded to %d bytes, want %d (%dx%d RGB)", len(frame), frameBytes, outW, outH), nil)
	}
	builder := NewBuilder()
	if err := builder.Add(outW, outH, frame); err != nil {
		return Signature{}, nil, err
	}
	sig, err := builder.Build()
	if err != nil {
		return Signature{}, nil, err
	}
	mrBuilder := NewMultiRegionBuilder()
	if err := mrBuilder.AddFeed(outW, outH, frame); err != nil {
		return Signature{}, nil, err
	}
	var mr *MultiRegionSignature
	if m, merr := mrBuilder.Build(); merr == nil {
		mr = &m
	}
	return sig, mr, nil
}
