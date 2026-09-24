package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/subs"
	"github.com/xiabee/XCut/internal/testmedia"
)

// The re-lay arm is justified in three ledgers by one sentence: it is cheaper than
// re-transcribing. This file measures the half of that claim this repository can
// measure — the re-lay itself, end to end through the product path (read the envelope,
// validate it, lay it out for the reel's canvas, write the file atomically). The other
// half is a Whisper round trip on the user's machine with a backend nobody here owns, so
// the row it feeds says what is measured and names what is not.

func stagedTranscript(b *testing.B, segments, wordsPer int) (Deps, *storage.Project, int64) {
	b.Helper()
	if !testmedia.HasFFmpeg() {
		b.Skip("ffmpeg not available")
	}
	media, err := testmedia.GenerateRally(b.TempDir(), "hall.mp4", 320, 240, 25, 6,
		[]testmedia.RallySpec{{Start: 0, End: 6, HitEvery: 1.2}})
	if err != nil {
		b.Fatal(err)
	}
	d, p := bareDeps(b)
	if _, err := d.ImportAsset(p, media); err != nil {
		b.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		b.Fatal(err)
	}
	// The canvas a re-lay reads is the timeline document's, so staging without this
	// step would time a layout against the writer's shipped default instead.
	if _, err := d.BuildTimeline(p, TimelineRequest{Style: DefaultExportStyle, Duration: 4}); err != nil {
		b.Fatal(err)
	}
	tr := &subs.Transcript{Language: "zh", Segments: make([]subs.Segment, 0, segments)}
	t := 0.5
	for i := 0; i < segments; i++ {
		var text strings.Builder
		for w := 0; w < wordsPer; w++ {
			fmt.Fprintf(&text, "word%05d ", i*10+w)
		}
		tr.Segments = append(tr.Segments, subs.Segment{Start: t, End: t + 2.4, Text: text.String()})
		t += 2.5
	}
	// Bound to the asset the project really has: an unbound payload is not a
	// re-layable one, and the benchmark would then time a refusal.
	asset, aerr := d.subtitleAsset(p, "")
	if aerr != nil {
		b.Fatal(aerr)
	}
	raw, err := json.Marshal(transcriptRecord{AssetID: asset.ID, Transcript: tr})
	if err != nil {
		b.Fatal(err)
	}
	tp, err := d.transcriptPath(p.ID)
	if err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tp), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(tp, raw, 0o644); err != nil {
		b.Fatal(err)
	}
	return d, p, int64(len(raw))
}

func benchmarkReStyle(b *testing.B, segments, wordsPer int) {
	d, p, bytesStored := stagedTranscript(b, segments, wordsPer)
	if !d.HasStoredTranscript(p.ID) {
		b.Fatal("the staged transcript is not re-layable, so nothing would be timed")
	}
	var worst time.Duration
	var assBytes int64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		if err := d.RestyleSubtitles(p.ID); err != nil {
			b.Fatal(err)
		}
		if el := time.Since(start); el > worst {
			worst = el
		}
		if ass, serr := d.SubtitlesPath(p.ID, "ass"); serr == nil {
			if fi, ferr := os.Stat(ass); ferr == nil {
				assBytes = fi.Size()
			}
		}
	}
	b.ReportMetric(float64(bytesStored)/1024, "KiB_in")
	b.ReportMetric(float64(assBytes)/1024, "KiB_out")
	b.ReportMetric(float64(worst.Microseconds()), "µs_worst")
	// The bound the arm claims to live under: a re-lay stands in for a transcription
	// only while it stays far below one, so the number is asserted, not just printed.
	if worst > 2*time.Second {
		b.Errorf("a %d-cue re-lay took %s, past the 2 s this arm is meant to stay under", segments, worst)
	}
}

func BenchmarkReStyleCaptions(b *testing.B) {
	b.Run("cues=200", func(b *testing.B) { benchmarkReStyle(b, 200, 4) })
	b.Run("cues=3000", func(b *testing.B) { benchmarkReStyle(b, 3000, 4) })
}
