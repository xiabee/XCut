// Package timeline defines XCut's Timeline intermediate representation — the
// stable, versioned, serializable contract between style/selection (what to
// make) and the renderer (how to make it). Analyzers never talk to FFmpeg
// directly; everything materializes as a validated Timeline first (D7).
package timeline

// Version is the current Timeline schema version. Bump on breaking changes;
// the renderer refuses unknown versions.
const Version = 1

// Canvas is the output video geometry.
type Canvas struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	FPS    float64 `json:"fps"`
}

// Transition describes the join to the NEXT clip.
type Transition struct {
	Type     string  `json:"type"` // "cut" | "fade" (v1)
	Duration float64 `json:"duration"`
}

// Clip is one source-media excerpt placed on the timeline.
type Clip struct {
	ID            string            `json:"id"`
	AssetID       string            `json:"asset_id"`
	SourcePath    string            `json:"source_path,omitempty"` // filled at generation; renderer input
	SourceStart   float64           `json:"source_start"`
	SourceEnd     float64           `json:"source_end"`
	TimelineStart float64           `json:"timeline_start"`
	Speed         float64           `json:"speed"`  // 1 = normal; >0
	Volume        float64           `json:"volume"` // 0..1
	Transition    *Transition       `json:"transition,omitempty"`
	Effects       []string          `json:"effects,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Duration is the clip's playback duration on the timeline, accounting for
// speed. Never negative for a valid clip.
func (c Clip) Duration() float64 {
	if c.Speed <= 0 {
		return 0
	}
	return (c.SourceEnd - c.SourceStart) / c.Speed
}

// Track is an ordered list of non-overlapping clips.
type Track struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // "video" | "audio"
	Clips []Clip `json:"clips"`
}

// Timeline is the root document.
type Timeline struct {
	Version   int     `json:"version"`
	ProjectID string  `json:"project_id,omitempty"`
	Canvas    Canvas  `json:"canvas"`
	Tracks    []Track `json:"tracks"`
	// Revision is a server-managed document counter (bumped on every saved
	// write, absent until the first save). Writers must send the revision
	// they read; a mismatch means the document changed underneath them and
	// the save is refused — without this, two editors silently destroy each
	// other's clips. Not part of the schema version above.
	Revision int64             `json:"revision,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Duration returns the total timeline length (end of the last clip).
func (t *Timeline) Duration() float64 {
	end := 0.0
	for _, tr := range t.Tracks {
		for _, c := range tr.Clips {
			if e := c.TimelineStart + c.Duration(); e > end {
				end = e
			}
		}
	}
	return end
}
