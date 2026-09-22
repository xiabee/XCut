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
	ID            string      `json:"id"`
	AssetID       string      `json:"asset_id"`
	SourcePath    string      `json:"source_path,omitempty"` // filled at generation; renderer input
	SourceStart   float64     `json:"source_start"`
	SourceEnd     float64     `json:"source_end"`
	TimelineStart float64     `json:"timeline_start"`
	Speed         float64     `json:"speed"`  // 1 = normal; >0
	Volume        float64     `json:"volume"` // 0..1
	Transition    *Transition `json:"transition,omitempty"`
	// Motion is the clip's framing plan: show a window of the source rather than
	// the whole frame, and slide that window while the clip plays (运镜). nil =
	// the whole frame, which is what every timeline written before this field
	// existed carries, so an old document renders exactly as it did.
	Motion   *Motion           `json:"motion,omitempty"`
	Effects  []string          `json:"effects,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Metadata keys that cross a package boundary: the timeline build writes them and
// the renderer reads them, so the names are the contract and neither side may
// spell them out locally.
const (
	// MetaMusic is the path of the music bed laid under this reel. Its presence
	// commits the render to mixing it in: a bed that has since moved is refused,
	// not silently dropped.
	MetaMusic = "music"
	// MetaMusicGain and MetaSourceGain are the two levels of the mix.
	MetaMusicGain  = "music_gain"
	MetaSourceGain = "source_gain"
	// MetaMusicBPM records the tempo the cuts were snapped to, when the bed had a
	// grid worth believing — a reader should not have to re-analyze the audio to
	// learn what the edit thought.
	MetaMusicBPM = "music_bpm"
)

// Motion describes the framed window as a zoom factor and the normalized center
// it holds at the start and the end of the clip. Both centers are optional (a
// missing one means the middle of the frame), and equal centers give a still
// punch-in; different ones are a drift the renderer interpolates over the
// clip's own time.
//
// Zoom is a fraction of the source's height, so 0.5 shows half the frame's
// height magnified to fill the canvas. The window always takes the canvas's
// aspect ratio, which is why a 9:16 canvas over a 16:9 source reframes instead
// of letterboxing — and why a zoom that cannot fit horizontally is clamped by
// the renderer rather than refused.
type Motion struct {
	Zoom float64   `json:"zoom"`
	From []float64 `json:"from,omitempty"` // [x,y], 0..1
	To   []float64 `json:"to,omitempty"`   // [x,y], 0..1
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
