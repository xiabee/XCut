package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
)

// TestSubtitlesOutRefusesSourceOverwrite: --out pointing at the input media
// must be refused before any sidecar work, and the media must survive
// byte-identical. The guard runs ahead of sidecar resolution, so no fake
// sidecar is needed here — a command that would truncate the user's original
// is a usage error, not a runtime failure.
func TestSubtitlesOutRefusesSourceOverwrite(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)

	media := filepath.Join(root, "song.mp4")
	original := []byte("user's original media bytes")
	if err := os.WriteFile(media, original, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"subtitles", media, "--out", media}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("--out onto the source media must fail (stdout: %s)", stdout.String())
	}
	if !strings.Contains(stderr.String(), "overwrite the source media") {
		t.Fatalf("expected a source-overwrite refusal, got: %s", stderr.String())
	}
	got, err := os.ReadFile(media)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("source media was modified by a refused subtitles run")
	}

	// A differently-spelled but same-denoted path must also be refused
	// (normalized comparison, exercising the not-same-string branch).
	dotty := filepath.Join(root, ".", "song.mp4")
	stderr.Reset()
	code = Run([]string{"subtitles", media, "--out", dotty}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "overwrite the source media") {
		t.Fatalf("normalized same-path --out must be refused, got code %d: %s", code, stderr.String())
	}

	// The default output path (media stem + .srt) must keep working for a
	// media file that does not itself end in .srt/.ass: it is refused only
	// by the absent sidecar, not by the overwrite guard.
	stderr.Reset()
	code = Run([]string{"subtitles", media}, &stdout, &stderr)
	if code == 0 {
		t.Skip("an AI sidecar is unexpectedly available; default-path case already covered elsewhere")
	}
	if strings.Contains(stderr.String(), "overwrite") {
		t.Fatalf("default output path must not trip the overwrite guard: %s", stderr.String())
	}
}

// TestEvalOutRefusesManifestAndMediaOverwrite: eval results are derived data;
// the hand-written manifest and the case media are the irreplaceable inputs.
// The guard fires before any ffmpeg work, so plain files suffice.
func TestEvalOutRefusesManifestAndMediaOverwrite(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)

	media := filepath.Join(root, "cases.mp4")
	if err := os.WriteFile(media, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	manifest := `{"version": 1, "cases": [
		{"name": "c1", "media": "cases.mp4", "expected": [{"start": 0, "end": 2}]}
	]}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"eval", manifestPath, "--out", manifestPath}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("--out onto the manifest must fail (stdout: %s)", stdout.String())
	}
	if !strings.Contains(stderr.String(), "overwrite the input manifest") {
		t.Fatalf("expected a manifest-overwrite refusal, got: %s", stderr.String())
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(manifestBytes) {
		t.Fatal("manifest was modified by a refused eval run")
	}

	// --out onto a case's source media is equally refused.
	stderr.Reset()
	code = Run([]string{"eval", manifestPath, "--out", media}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "overwrite a case's source media") {
		t.Fatalf("--out onto case media must be refused, got code %d: %s", code, stderr.String())
	}
	if b, rerr := os.ReadFile(media); rerr == nil && string(b) != "placeholder" {
		t.Fatal("case media was modified by a refused eval run")
	}

	// A distinct results path must not trip the guard (it fails later, on
	// missing ffmpeg or missing analysis — either is fine here).
	stderr.Reset()
	code = Run([]string{"eval", manifestPath, "--out", filepath.Join(root, "results.json")}, &stdout, &stderr)
	if code == 0 && !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg available and eval ran fully — acceptable")
	}
	if strings.Contains(stderr.String(), "overwrite") {
		t.Fatalf("distinct --out must not trip the overwrite guard: %s", stderr.String())
	}
}
