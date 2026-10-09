package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/testmedia"
)

// photoBytes renders a real one-frame photo with the same lavfi sources the
// video fixtures use — the endpoint decodes through ffmpeg like any user
// image — and hands back its content for a raw-body POST.
func photoBytes(t *testing.T, color string) []byte {
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
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// postPhoto sends the raw-body request the endpoint serves (the string-body
// do() helper cannot carry image bytes).
func postPhoto(t *testing.T, s *Server, path string, body []byte) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:52000"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// TestSpotPhotoSeedsTheFullRow: content up, rect on the photo, both models
// measured server-side, the row lands complete (At=0 — a photo has no source
// second) and the staged copy leaves no temp debris behind.
func TestSpotPhotoSeedsTheFullRow(t *testing.T) {
	s := testServer(t)
	pid, aid := seedAsset(t, s, "spot-photo", "cam1.mp4")
	photo := photoBytes(t, "green")

	base := "/api/v1/projects/" + pid + "/assets/" + aid + "/player-spot/photo?filename=me.png&rect=0.3,0.3,0.2,0.2"
	rec, out := postPhoto(t, s, base, photo)
	if rec.Code != 200 {
		t.Fatalf("photo seed: %d %v", rec.Code, out)
	}
	spot, ok := out["spot"].(map[string]any)
	if !ok {
		t.Fatalf("no spot in response: %v", out)
	}
	bins, _ := spot["bins"].([]any)
	if len(bins) == 0 {
		t.Fatal("the photo seed measured no signature")
	}
	bands, _ := spot["bands"].([]any)
	if len(bands) != 3 {
		t.Fatalf("the photo seed carries %d band entries, want the 3-band marker", len(bands))
	}
	for i, b := range bands {
		arr, _ := b.([]any)
		if len(arr) != len(bins) {
			t.Fatalf("band %d carries %d bins, want %d", i, len(arr), len(bins))
		}
	}
	if at, _ := spot["at"].(float64); at != 0 {
		t.Fatalf("a photo-seeded spot carries at=%v — a photo has no source second", at)
	}
	if sampled, _ := spot["sampled_at"].(float64); sampled == 0 {
		t.Fatal("the photo seed carries no sampled-at moment")
	}

	// The stored row is the response's row: analyze reads this and never
	// re-measures from the video.
	rec, out = do(t, s, "GET", "/api/v1/projects/"+pid+"/assets/"+aid+"/player-spot", "")
	if rec.Code != 200 {
		t.Fatalf("get after photo seed: %d %v", rec.Code, out)
	}
	stored, _ := out["spot"].(map[string]any)
	if stored == nil {
		t.Fatal("the photo seed never reached the asset row")
	}
	sb, _ := stored["bins"].([]any)
	if len(sb) != len(bins) {
		t.Fatalf("stored bins %d != response bins %d", len(sb), len(bins))
	}

	// Request-scoped staging: the workspace temp dir holds nothing after the
	// request, success or not.
	if used := s.Pipe.WS.TempUsage(); used != 0 {
		t.Fatalf("temp dir holds %d bytes after the photo seed — the staged copy leaked", used)
	}
}

// TestSpotPhotoStarvingRectStillSeeds: a rect too thin to yield three bands
// seeds the single histogram with the same present-but-empty band marker the
// video and CLI paths write — degraded, not failed.
func TestSpotPhotoStarvingRectStillSeeds(t *testing.T) {
	s := testServer(t)
	pid, aid := seedAsset(t, s, "spot-photo-thin", "cam2.mp4")
	photo := photoBytes(t, "red")

	base := "/api/v1/projects/" + pid + "/assets/" + aid + "/player-spot/photo?filename=me.png&rect=0.05,0.49,0.9,0.02"
	rec, out := postPhoto(t, s, base, photo)
	if rec.Code != 200 {
		t.Fatalf("starving photo seed: %d %v", rec.Code, out)
	}
	spot, _ := out["spot"].(map[string]any)
	bins, _ := spot["bins"].([]any)
	if len(bins) == 0 {
		t.Fatal("the starving rect measured no single histogram")
	}
	bands, _ := spot["bands"].([]any)
	if len(bands) != 3 {
		t.Fatalf("band marker = %v, want three present-but-empty entries", bands)
	}
	for i, b := range bands {
		if arr, _ := b.([]any); len(arr) != 0 {
			t.Fatalf("band %d is not empty (%d bins) — a starved spot must not pretend to a band model", i, len(arr))
		}
	}
}

// TestSpotPhotoRefusals: the malformed shapes are refused before any
// measurement — bad/missing rect, missing filename, empty and oversized
// bodies, a non-image, and a cross-project asset id.
func TestSpotPhotoRefusals(t *testing.T) {
	s := testServer(t)
	pid, aid := seedAsset(t, s, "spot-photo-refuse", "cam3.mp4")
	_, otherAid := seedAsset(t, s, "spot-photo-refuse-other", "cam4.mp4")
	photo := photoBytes(t, "blue")
	base := "/api/v1/projects/" + pid + "/assets/" + aid + "/player-spot/photo"

	rec, out := postPhoto(t, s, base+"?filename=me.png", photo)
	if rec.Code != 400 {
		t.Fatalf("missing rect must be 400, got %d (%v)", rec.Code, out)
	}
	rec, _ = postPhoto(t, s, base+"?filename=me.png&rect=0.9,0.9,0.5,0.5", photo)
	if rec.Code != 400 {
		t.Fatalf("out-of-frame rect must be 400, got %d", rec.Code)
	}
	rec, _ = postPhoto(t, s, base+"?rect=0.3,0.3,0.2,0.2", photo)
	if rec.Code != 400 {
		t.Fatalf("missing filename must be 400, got %d", rec.Code)
	}
	rec, _ = postPhoto(t, s, base+"?filename=me.png&rect=0.3,0.3,0.2,0.2", nil)
	if rec.Code != 400 {
		t.Fatalf("empty body must be 400, got %d", rec.Code)
	}
	rec, _ = postPhoto(t, s, base+"?filename=me.png&rect=0.3,0.3,0.2,0.2",
		make([]byte, maxSpotPhotoBytes+1))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("oversized body must be refused at the 64 MiB bound, got %d", rec.Code)
	}
	rec, out = postPhoto(t, s, base+"?filename=me.png&rect=0.3,0.3,0.2,0.2", []byte("this is not an image"))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a non-image must be unsupported media, got %d (%v)", rec.Code, out)
	}
	rec, _ = postPhoto(t, s, "/api/v1/projects/"+pid+"/assets/"+otherAid+"/player-spot/photo?filename=me.png&rect=0.3,0.3,0.2,0.2", photo)
	if rec.Code != 404 {
		t.Fatalf("cross-project asset id must be 404, got %d", rec.Code)
	}

	// None of the refusals touched the stored spot.
	rec, out = do(t, s, "GET", "/api/v1/projects/"+pid+"/assets/"+aid+"/player-spot", "")
	if spot, _ := out["spot"].(map[string]any); rec.Code != 200 || spot != nil {
		t.Fatalf("a refused seed left a spot behind: %d %v", rec.Code, out)
	}
	if used := s.Pipe.WS.TempUsage(); used != 0 {
		t.Fatalf("temp dir holds %d bytes after refusals — staging leaked", used)
	}
}
