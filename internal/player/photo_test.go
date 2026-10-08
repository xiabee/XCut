package player

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/xcerr"
)

// photoFixture writes a real PNG via the same lavfi sources the video
// fixtures use — the photo path decodes through ffmpeg like any user image.
func photoFixture(t *testing.T, color string) string {
	t.Helper()
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.png")
	if _, _, err := media.Run(context.Background(), "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c="+color+":s=320x240",
		"-frames:v", "1", "-y", path); err != nil {
		t.Fatalf("photo fixture: %v", err)
	}
	return path
}

// TestMeasureSignatureFromImage builds both models from one decoded frame:
// the rect is on the photo, the histograms are the same geometry the video
// measure produces, and a band-starving rect degrades to the single
// histogram exactly like the video path.
func TestMeasureSignatureFromImage(t *testing.T) {
	path := photoFixture(t, "green")
	tools := media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}

	sig, mr, err := MeasureSignatureFromImage(context.Background(), tools, path,
		[]float64{0.3, 0.3, 0.2, 0.2})
	if err != nil {
		t.Fatal(err)
	}
	sum := 0.0
	for _, b := range sig.Bins {
		sum += b
	}
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("photo histogram sums to %.4f, want an L1-normalized 1.0", sum)
	}
	if mr == nil {
		t.Fatal("the photo's square spot yielded no band model")
	}
	for band := 0; band < RegionCount; band++ {
		if len(mr.Bands[band]) != HueBins*SatBins*ValBins {
			t.Fatalf("band %d carries %d bins", band, len(mr.Bands[band]))
		}
	}

	// Band starvation degrades, not fails: a 4-row sliver still measures
	// the single histogram and reports no band model.
	_, mr, err = MeasureSignatureFromImage(context.Background(), tools, path,
		[]float64{0.05, 0.49, 0.9, 0.02})
	if err != nil {
		t.Fatal(err)
	}
	if mr != nil {
		t.Fatal("a band-starving photo rect produced a band model")
	}

	// A non-image is a clean unsupported-media refusal, not a corrupt model.
	if _, _, err := MeasureSignatureFromImage(context.Background(), tools,
		filepath.Join(t.TempDir(), "not-image.png"), []float64{0.3, 0.3, 0.2, 0.2}); err == nil {
		t.Fatal("a non-image measured instead of refusing")
	} else if !xcerr.IsCode(err, xcerr.CodeUnsupportedMedia) && !xcerr.IsCode(err, xcerr.CodeNotFound) {
		t.Fatalf("non-image error = %v, want unsupported_media/not_found", err)
	}
}
