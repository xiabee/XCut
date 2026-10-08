package cli

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/player"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

// TestPlayerPhotoSeedsTheFullRow: --photo + --set measures both models from
// the still image at seed time — the row lands complete, so the analyze pass
// never re-measures it from the video (a photo rect describes the photo).
func TestPlayerPhotoSeedsTheFullRow(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = root
	if err := config.Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if err := workspace.New(root).Ensure(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(root, "xcut.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ea := &App{
		Ctx:    context.Background(),
		Stdout: io.Discard,
		Stderr: io.Discard,
		Cfg:    cfg,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	d := ea.Pipeline(db)

	p, err := db.CreateProject(context.Background(), "photo-seed")
	if err != nil {
		t.Fatal(err)
	}
	mediaPath, err := testmedia.Generate(t.TempDir(), "src.mp4", testmedia.DefaultFixture(), 320, 240, 6)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := d.ImportAsset(p, mediaPath)
	if err != nil {
		t.Fatal(err)
	}

	photo := filepath.Join(t.TempDir(), "photo.png")
	if _, _, err := media.Run(context.Background(), "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=green:s=320x240",
		"-frames:v", "1", "-y", photo); err != nil {
		t.Fatal(err)
	}

	if err := cmdPlayer(ea, []string{p.Name, "--asset", asset.ID,
		"--set", "0.3,0.3,0.2,0.2", "--photo", photo}); err != nil {
		t.Fatalf("cmdPlayer: %v", err)
	}

	seeded, err := db.GetAsset(context.Background(), asset.ID)
	if err != nil || seeded == nil {
		t.Fatalf("GetAsset: %v", err)
	}
	spot := seeded.PlayerSpot
	if spot == nil {
		t.Fatal("the photo seed never reached the asset row")
	}
	if len(spot.Bins) == 0 {
		t.Fatal("the photo seed measured no signature")
	}
	if !player.ValidBands(spot.Bands) {
		t.Fatal("the photo seed did not land a usable band model")
	}
	if spot.SampledAt == 0 {
		t.Fatal("the photo seed carries no sampled-at moment")
	}
	if spot.At != 0 {
		t.Fatal("a photo-seeded spot has no source second")
	}

	// The refusal shapes: --photo alone, and --photo with --at.
	if err := cmdPlayer(ea, []string{p.Name, "--asset", asset.ID, "--photo", photo}); err == nil ||
		!strings.Contains(xcerr.UserMessage(err), "--set") {
		t.Fatalf("photo without --set = %v, want the pair named", err)
	}
	if err := cmdPlayer(ea, []string{p.Name, "--asset", asset.ID,
		"--set", "0.3,0.3,0.2,0.2", "--photo", photo, "--at", "3"}); err == nil ||
		!strings.Contains(xcerr.UserMessage(err), "--at") {
		t.Fatalf("photo with --at = %v, want the mismatch named", err)
	}
}
