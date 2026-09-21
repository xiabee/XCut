package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

// MaxScoreMarks bounds what one asset may carry. A mark is a float in a TEXT
// column, and the detector that produces them is aimed by a threshold the user
// picks: an unreasonably low one on long footage must not turn the project row
// into an unbounded blob (rule 4 — nothing unbounded). 4096 is far past the
// 43 marks a real 8-minute match produced.
const MaxScoreMarks = 4096

// ScoreMarks are the source-time seconds where a burned-in scoreboard changed
// — one per finished point — together with the normalized crop they were read
// from and when. Stored as JSON in the assets.score_marks column (” = unset)
// because they are the user's annotation of *this* source, not a derived cache
// entry that may be evicted between runs.
type ScoreMarks struct {
	// Crop is [x, y, w, h] in 0..1 fractions of the frame — the region the
	// scoreboard digits occupy. Kept so a later scan can be compared, and so
	// `xcut boundaries list` can say where the marks came from.
	Crop  []float64 `json:"crop"`
	Times []float64 `json:"times"`
	At    int64     `json:"at"`
}

// Valid reports whether the marks are storable: a sane crop and a bounded,
// finite, strictly increasing set of times. An empty set is valid — "scanned
// this region and the score never changed" is a result worth keeping, and it
// reads differently from never scanned. Callers get a validation error naming
// the offending part rather than a blob the timeline must defend itself
// against.
func (m *ScoreMarks) Valid() bool {
	if m == nil || !ValidCropRect(m.Crop) || len(m.Times) > MaxScoreMarks {
		return false
	}
	prev := math.Inf(-1)
	for _, t := range m.Times {
		if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 || !(t > prev) {
			return false
		}
		prev = t
	}
	return true
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
	// ScoreMarks is nil unless the user pointed the scoreboard detector at
	// this source; it rides the asset because the crop belongs to the camera,
	// not to the style.
	ScoreMarks *ScoreMarks `json:"score_marks,omitempty"`
	// ScoreCrop is the region the user pointed the scoreboard detector at
	// ([x, y, w, h], normalized). It is intent; ScoreMarks is measurement. The
	// analyze stage scans when marks are missing or were measured against a
	// different rect, so moving the crop re-derives the marks by itself.
	ScoreCrop []float64 `json:"score_crop,omitempty"`
}

const assetCols = `id, project_id, path, filename, fingerprint, duration_s, width, height,
fps, video_codec, audio_codec, has_audio, bitrate, size_bytes, probe_json, created_at, motion_roi,
score_marks, score_crop`

func scanAsset(row interface{ Scan(...any) error }) (*Asset, error) {
	var a Asset
	var hasAudio int
	var roiJSON, marksJSON, cropJSON string
	err := row.Scan(&a.ID, &a.ProjectID, &a.Path, &a.Filename, &a.Fingerprint,
		&a.DurationSec, &a.Width, &a.Height, &a.FPS,
		&a.VideoCodec, &a.AudioCodec, &hasAudio, &a.Bitrate, &a.SizeBytes,
		&a.ProbeJSON, &a.CreatedAt, &roiJSON, &marksJSON, &cropJSON)
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
	if marksJSON != "" {
		marks := &ScoreMarks{}
		if err := json.Unmarshal([]byte(marksJSON), marks); err != nil {
			return nil, xcerr.E(xcerr.CodeStorageFailure,
				"asset "+a.ID+" has a corrupt score_marks", err)
		}
		a.ScoreMarks = marks
	}
	if cropJSON != "" {
		var crop []float64
		if err := json.Unmarshal([]byte(cropJSON), &crop); err != nil {
			return nil, xcerr.E(xcerr.CodeStorageFailure,
				"asset "+a.ID+" has a corrupt score_crop", err)
		}
		a.ScoreCrop = crop
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

// ValidCropRect reports whether a normalized [x, y, w, h] region lies inside
// the frame — the same rule as a motion ROI, expressed over a slice because
// that is the shape the sidecar protocol and the web UI both carry.
func ValidCropRect(crop []float64) bool {
	if len(crop) != 4 {
		return false
	}
	return (&MotionROI{X: crop[0], Y: crop[1], W: crop[2], H: crop[3]}).Valid()
}

// SetAssetScoreMarks stores (marks != nil) or clears (marks == nil) the
// scoreboard marks read off one asset. Reports NotFound when the asset id is
// unknown.
func (d *DB) SetAssetScoreMarks(ctx context.Context, assetID string, marks *ScoreMarks) error {
	if marks != nil && !marks.Valid() {
		return xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("score marks need a normalized crop x,y,w,h with x+w,y+h<=1, at most %d finite increasing times",
				MaxScoreMarks), nil)
	}
	value, cropValue := "", ""
	if marks != nil {
		b, err := json.Marshal(marks)
		if err != nil {
			return xcerr.E(xcerr.CodeStorageFailure, "cannot serialize score marks", err)
		}
		value = string(b)
		// The region travels with its measurement: a row holding marks without
		// the matching score_crop is a row the timeline must ignore, and writing
		// the pair apart is how that state gets created by accident (the eval
		// harness did exactly that, and silently stopped using its own scan).
		cb, err := json.Marshal(marks.Crop)
		if err != nil {
			return xcerr.E(xcerr.CodeStorageFailure, "cannot serialize score crop", err)
		}
		cropValue = string(cb)
	}
	res, err := d.ExecContext(ctx, `
UPDATE assets SET
	score_marks = ?,
	score_crop = CASE WHEN ? = '' THEN score_crop ELSE ? END
WHERE id = ?`, value, cropValue, cropValue, assetID)
	if err != nil {
		return xcerr.E(xcerr.CodeStorageFailure, "cannot save asset score marks", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return xcerr.E(xcerr.CodeNotFound, "asset not found", nil)
	}
	return nil
}

// SameCrop compares two normalized regions by value (nil means "unset"). The
// staleness rule — marks measured against a different rect must not steer
// clips — is needed by the analyze stage and the CLI listing alike.
func SameCrop(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SetAssetScoreCrop records (or clears, with nil) the region the scoreboard
// occupies on one asset. Moving the region drops the marks measured against the
// old one: stale boundaries would end clips at points that belong to a different
// part of the frame, which is worse than no boundaries at all.
func (d *DB) SetAssetScoreCrop(ctx context.Context, assetID string, crop []float64) error {
	if crop != nil && !ValidCropRect(crop) {
		return xcerr.E(xcerr.CodeValidation,
			"score crop must be x,y,w,h with 0<=x,y, 0<w,h and x+w,y+h<=1 (normalized to the frame)", nil)
	}
	value := ""
	if crop != nil {
		b, err := json.Marshal(crop)
		if err != nil {
			return xcerr.E(xcerr.CodeStorageFailure, "cannot serialize score crop", err)
		}
		value = string(b)
	}
	// Re-writing the same region is a no-op for the measurement (a UI that
	// saves twice must not wipe a good scan); any *change*, including clearing,
	// drops the marks measured against the old rect.
	res, err := d.ExecContext(ctx, `
UPDATE assets SET score_crop = ?,
	score_marks = CASE WHEN score_crop = ? THEN score_marks ELSE '' END
WHERE id = ?`, value, value, assetID)
	if err != nil {
		return xcerr.E(xcerr.CodeStorageFailure, "cannot save asset score crop", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return xcerr.E(xcerr.CodeNotFound, "asset not found", nil)
	}
	return nil
}
