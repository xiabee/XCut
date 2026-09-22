package render

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

func TestMusicGainParsing(t *testing.T) {
	def, mus := DefaultMusicGain, timeline.MetaMusicGain
	for _, tc := range []struct {
		name string
		key  string
		def  float64
		md   map[string]string
		want float64
		err  string
	}{
		{"absent falls back to the default", mus, def, map[string]string{}, def, ""},
		{"empty falls back too", mus, def, map[string]string{mus: ""}, def, ""},
		{"recorded level is used", mus, def, map[string]string{mus: "0.4200"}, 0.42, ""},
		{"zero refused", mus, def, map[string]string{mus: "0.0000"}, 0, "music_gain"},
		{"past one refused", timeline.MetaSourceGain, DefaultSourceGain,
			map[string]string{timeline.MetaSourceGain: "1.4"}, 0, "source_gain"},
		{"not a number refused", timeline.MetaSourceGain, DefaultSourceGain,
			map[string]string{timeline.MetaSourceGain: "loud"}, 0, "not a number"},
	} {
		// The key is named by the case, not guessed from its title: guessing gave
		// two cases the wrong file to parse, and they "passed" on a default.
		got, err := musicGain(tc.md, tc.key, tc.def)
		if tc.err == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			if got != tc.want {
				t.Errorf("%s: %g, want %g", tc.name, got, tc.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted, want a refusal naming %q", tc.name, tc.err)
			continue
		}
		if !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s: refusal did not name %q: %v", tc.name, tc.err, err)
		}
		if xcerr.CodeOf(err) != xcerr.CodeValidation {
			t.Errorf("%s: code %s, want validation", tc.name, xcerr.CodeOf(err))
		}
	}
}

// TestMixCommandCarriesBothLevels reads the arguments the product assembled for
// the mix, from the child itself: looping the bed, scaling each side to its
// recorded level, copying the video so a bed costs no second encode.
func TestMixCommandCarriesBothLevels(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	bed := filepath.Join(dir, "bed.m4a")
	partial := filepath.Join(dir, "reel.mp4.partial")
	for _, p := range []string{bed, partial} {
		if err := os.WriteFile(p, []byte("stand-in bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	argv := filepath.Join(dir, "argv.log")
	t.Setenv("XCUT_FAKE_FFMPEG", "1")
	t.Setenv("XCUT_FAKE_FFMPEG_ARGV", argv)
	tools := media.Tools{FFmpeg: os.Args[0], FFprobe: "ffprobe", Threads: 2}

	err := mixMusic(context.Background(), Options{Tools: tools, TempDir: dir}, partial, bed, 0.9, 0.35, 7.5)
	raw, rerr := os.ReadFile(argv)
	if rerr != nil {
		t.Fatalf("the child never reported its arguments: %v", rerr)
	}
	got := string(raw)
	for _, want := range []string{
		"-stream_loop -1", "amix=inputs=2", "volume=0.9000", "volume=0.3500",
		"-c:v copy", "-t 7.500",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the mix command lost %q, got:\n%s", want, got)
		}
	}
	// The mix is its own file until it wins: the reel is never truncated in place.
	if !strings.Contains(got, ".music.mp4") {
		t.Fatalf("the mix was not written beside the reel, so a failure could damage it:\n%s", got)
	}
	if err == nil {
		t.Fatal("the stand-in FFmpeg must fail the mix")
	}
	if _, serr := os.Stat(partial + ".music.mp4"); !os.IsNotExist(serr) {
		t.Fatal("a failed mix must not leave its intermediate file behind")
	}
}

// TestMissingBedNamesTheProblem is the promise the document makes: a reel that
// says it has music does not render silently without it.
func TestMissingBedNamesTheProblem(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	partial := filepath.Join(dir, "reel.mp4.partial")
	if err := os.WriteFile(partial, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := mixMusic(context.Background(), Options{Tools: media.Tools{FFmpeg: "ffmpeg"}},
		partial, filepath.Join(dir, "gone.m4a"), 0.9, 0.35, 5)
	if err == nil {
		t.Fatal("a missing bed must be an error")
	}
	if xcerr.CodeOf(err) != xcerr.CodeNotFound {
		t.Fatalf("code %s, want not_found", xcerr.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "music file is missing") {
		t.Fatalf("message does not say what to do about it: %v", err)
	}
}
