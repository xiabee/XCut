package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// MotionROI is a normalized region of interest (0..1) attached to ONE
// asset — the per-source court region. It overrides the preset's
// motion_roi for this source during timeline generation. Stored as JSON
// in the assets.motion_roi column (” = unset).
type MotionROI struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Valid mirrors the style.Validate motion_roi rule.
func (r *MotionROI) Valid() bool {
	if r == nil {
		return false
	}
	for _, v := range []float64{r.X, r.Y, r.W, r.H} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return r.X >= 0 && r.Y >= 0 && r.W > 0 && r.H > 0 && r.X+r.W <= 1 && r.Y+r.H <= 1
}

// Asset is an imported media file within a project.
type Asset struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"project_id"`
	Path        string  `json:"path"`
	Filename    string  `json:"filename"`
	Fingerprint string  `json:"fingerprint"`
	DurationSec float64 `json:"duration_s"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	FPS         float64 `json:"fps"`
	VideoCodec  string  `json:"video_codec"`
	AudioCodec  string  `json:"audio_codec,omitempty"`
	HasAudio    bool    `json:"has_audio"`
	Bitrate     int64   `json:"bitrate"`
	SizeBytes   int64   `json:"size_bytes"`
	// ProbeJSON is stored for diagnostics and never served: it is the whole
	// ffprobe document, which on a real 10-minute recording measured 5.4 KB of
	// a 5.9 KB project response (91 %) that no client of the API reads — and
	// the web UI polls this endpoint. It stays out of JSON by contract, so the
	// storage round-trip keeps working while the wire does not carry it.
	ProbeJSON string     `json:"-"`
	CreatedAt int64      `json:"created_at"`
	MotionROI *MotionROI `json:"motion_roi,omitempty"`
}

const assetCols = `id, project_id, path, filename, fingerprint, duration_s, width, height,
fps, video_codec, audio_codec, has_audio, bitrate, size_bytes, probe_json, created_at, motion_roi`

func scanAsset(row interface{ Scan(...any) error }) (*Asset, error) {
	var a Asset
	var hasAudio int
	var roiJSON string
	err := row.Scan(&a.ID, &a.ProjectID, &a.Path, &a.Filename, &a.Fingerprint,
		&a.DurationSec, &a.Width, &a.Height, &a.FPS,
		&a.VideoCodec, &a.AudioCodec, &hasAudio, &a.Bitrate, &a.SizeBytes,
		&a.ProbeJSON, &a.CreatedAt, &roiJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.HasAudio = hasAudio != 0
	if roiJSON != "" {
		roi := &MotionROI{}
		if err := json.Unmarshal([]byte(roiJSON), roi); err != nil {
			return nil, xcerr.E(xcerr.CodeStorageFailure,
				"asset "+a.ID+" has a corrupt motion_roi", err)
		}
		a.MotionROI = roi
	}
	return &a, nil
}

// UpsertAsset inserts or updates (by project+path) an asset row. The asset
// ID is stable across re-imports: timeline clips reference assets by ID, so
// re-importing the same file must refresh the probe data without re-keying
// the row (a new ID would brick every stored timeline that clips it). On
// conflict the stored ID and created_at win; a.ID is updated to match.
func (d *DB) UpsertAsset(ctx context.Context, a *Asset) error {
	if a.ID == "" {
		a.ID = NewID("asst")
	}
	if a.CreatedAt == 0 {
		a.CreatedAt = time.Now().Unix()
	}
	hasAudio := 0
	if a.HasAudio {
		hasAudio = 1
	}
	_, err := d.ExecContext(ctx, `
INSERT INTO assets (id, project_id, path, filename, fingerprint, duration_s, width, height,
	fps, video_codec, audio_codec, has_audio, bitrate, size_bytes, probe_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(project_id, path) DO UPDATE SET
	filename=excluded.filename,
	fingerprint=excluded.fingerprint,
	duration_s=excluded.duration_s,
	width=excluded.width,
	height=excluded.height,
	fps=excluded.fps,
	video_codec=excluded.video_codec,
	audio_codec=excluded.audio_codec,
	has_audio=excluded.has_audio,
	bitrate=excluded.bitrate,
	size_bytes=excluded.size_bytes,
	probe_json=excluded.probe_json`,
		a.ID, a.ProjectID, a.Path, a.Filename, a.Fingerprint, a.DurationSec,
		a.Width, a.Height, a.FPS, a.VideoCodec, a.AudioCodec, hasAudio,
		a.Bitrate, a.SizeBytes, a.ProbeJSON, a.CreatedAt)
	if err != nil {
		return xcerr.E(xcerr.CodeStorageFailure, "cannot save asset", err)
	}
	// Reconcile the caller's copy with the stored identity: the freshly
	// generated ID only sticks when the row was actually new.
	stored, err := scanAsset(d.QueryRowContext(ctx,
		`SELECT `+assetCols+` FROM assets WHERE project_id = ? AND path = ?`,
		a.ProjectID, a.Path))
	if err != nil {
		return xcerr.E(xcerr.CodeStorageFailure, "cannot read back asset", err)
	}
	if stored != nil {
		a.ID = stored.ID
		a.CreatedAt = stored.CreatedAt
	}
	return nil
}

// GetAsset by id; nil when missing.
func (d *DB) GetAsset(ctx context.Context, id string) (*Asset, error) {
	a, err := scanAsset(d.QueryRowContext(ctx,
		`SELECT `+assetCols+` FROM assets WHERE id = ?`, id))
	if err != nil {
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot read asset", err)
	}
	return a, nil
}

// ListAssets of a project, ordered by import time.
func (d *DB) ListAssets(ctx context.Context, projectID string) ([]Asset, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT `+assetCols+` FROM assets WHERE project_id = ? ORDER BY created_at, id`, projectID)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot list assets", err)
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot read asset row", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// SetAssetROI stores (roi != nil) or clears (roi == nil) the asset's
// per-source motion ROI. Reports NotFound when the asset id is unknown.
func (d *DB) SetAssetROI(ctx context.Context, assetID string, roi *MotionROI) error {
	if roi != nil && !roi.Valid() {
		return xcerr.E(xcerr.CodeValidation,
			"roi must satisfy 0<=x,y and 0<w,h and x+w,y+h<=1 (normalized to the frame)", nil)
	}
	value := ""
	if roi != nil {
		b, err := json.Marshal(roi)
		if err != nil {
			return xcerr.E(xcerr.CodeStorageFailure, "cannot serialize motion roi", err)
		}
		value = string(b)
	}
	res, err := d.ExecContext(ctx, `UPDATE assets SET motion_roi = ? WHERE id = ?`, value, assetID)
	if err != nil {
		return xcerr.E(xcerr.CodeStorageFailure, "cannot save asset roi", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return xcerr.E(xcerr.CodeNotFound, "asset not found", nil)
	}
	return nil
}
