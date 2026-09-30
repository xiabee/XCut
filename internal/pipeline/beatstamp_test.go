package pipeline

import (
	"testing"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/timeline"
)

// halfSecond is a 120 BPM lattice the fixtures can share.
func halfSecondGrid() analysis.BeatGrid {
	var beats []float64
	for t := 0.5; t <= 10.0; t += 0.5 {
		beats = append(beats, t)
	}
	return analysis.BeatGrid{Period: 0.5, Phase: 0.5, BPM: 120, Beats: beats}
}

func otherGrid() analysis.BeatGrid {
	var beats []float64
	for t := 0.3; t <= 9.0; t += 0.3 {
		beats = append(beats, t)
	}
	return analysis.BeatGrid{Period: 0.3, Phase: 0.3, BPM: 200, Beats: beats}
}

func docWithClips(assetIDs ...string) *timeline.Timeline {
	var clips []timeline.Clip
	for i, id := range assetIDs {
		clips = append(clips, timeline.Clip{
			ID: "c" + id, AssetID: id,
			SourceStart: 0, SourceEnd: 1, TimelineStart: float64(i), Speed: 1,
		})
	}
	return &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks:  []timeline.Track{{ID: "v1", Kind: "video", Clips: clips}},
	}
}

func TestStampBeatGridStampsTheBedThatWon(t *testing.T) {
	tl := docWithClips("a", "b")
	bed := &musicBed{bpm: 120.04, phase: 0.25, beats: []float64{0.25, 0.75}}
	grids := []assetBeatGrid{{assetID: "a", grid: halfSecondGrid()}}
	stampBeatGrid(tl, bed, grids)
	// The bed outranked whatever the assets carried (bedWins handed its beats to
	// every item), so the document must state the bed's grid, not the asset's.
	if got := tl.Metadata[timeline.MetaBeatBPM]; got != "120.0400" {
		t.Errorf("beat_bpm = %q, want the bed's 120.0400", got)
	}
	if got := tl.Metadata[timeline.MetaBeatPhase]; got != "0.2500" {
		t.Errorf("beat_phase = %q, want the bed's 0.2500", got)
	}
}

func TestStampBeatGridStampsTheOneAssetGrid(t *testing.T) {
	tl := docWithClips("a")
	stampBeatGrid(tl, nil, []assetBeatGrid{{assetID: "a", grid: halfSecondGrid()}})
	if got := tl.Metadata[timeline.MetaBeatBPM]; got != "120.0000" {
		t.Errorf("beat_bpm = %q, want 120.0000", got)
	}
	if got := tl.Metadata[timeline.MetaBeatPhase]; got != "0.5000" {
		t.Errorf("beat_phase = %q, want 0.5000", got)
	}
}

func TestStampBeatGridRefusesTwoDifferentGrids(t *testing.T) {
	// Two assets, two different lattices, both contributed clips: a document-level
	// grid over both would be fiction, and the ruler would draw one asset's pulse
	// over the other's material. Nothing is the honest answer.
	tl := docWithClips("a", "b")
	grids := []assetBeatGrid{
		{assetID: "a", grid: halfSecondGrid()},
		{assetID: "b", grid: otherGrid()},
	}
	stampBeatGrid(tl, nil, grids)
	if _, has := tl.Metadata[timeline.MetaBeatBPM]; has {
		t.Errorf("two distinct grids produced a beat_bpm (%q); the document must state neither", tl.Metadata[timeline.MetaBeatBPM])
	}
	if _, has := tl.Metadata[timeline.MetaBeatPhase]; has {
		t.Errorf("two distinct grids produced a beat_phase; the document must state neither")
	}
}

func TestStampBeatGridCollapsesTheSameLatticeFittedTwice(t *testing.T) {
	// The same audio analyzed for two assets yields the same floats: one distinct
	// grid, so the document does state it.
	tl := docWithClips("a", "b")
	grids := []assetBeatGrid{
		{assetID: "a", grid: halfSecondGrid()},
		{assetID: "b", grid: halfSecondGrid()},
	}
	stampBeatGrid(tl, nil, grids)
	if got := tl.Metadata[timeline.MetaBeatBPM]; got != "120.0000" {
		t.Errorf("beat_bpm = %q, want one shared lattice stamped", got)
	}
}

func TestStampBeatGridWithoutAGridStampsNothing(t *testing.T) {
	tl := docWithClips("a")
	stampBeatGrid(tl, nil, nil)
	if len(tl.Metadata) != 0 {
		t.Errorf("no grid anywhere produced metadata %v; the document must stay silent", tl.Metadata)
	}
	// A bed whose audio had no believable grid: the music plays, nothing snapped,
	// so there is no grid to state either.
	stampBeatGrid(tl, &musicBed{bpm: 0}, nil)
	if len(tl.Metadata) != 0 {
		t.Errorf("a griddless bed produced metadata %v", tl.Metadata)
	}
}

func TestGridsServedByKeepsOnlyContributingAssets(t *testing.T) {
	// B's asset contributed no clip to the document (its events all lost the
	// selection), so B's grid did not shape the reel and must not claim it —
	// leaving only A's grid, which stampBeatGrid may then state.
	tl := docWithClips("a")
	grids := []assetBeatGrid{
		{assetID: "a", grid: halfSecondGrid()},
		{assetID: "b", grid: otherGrid()},
	}
	served := gridsServedBy(tl, grids)
	if len(served) != 1 || served[0].assetID != "a" {
		t.Fatalf("served = %+v, want only asset a's grid", served)
	}
	stampBeatGrid(tl, nil, served)
	if got := tl.Metadata[timeline.MetaBeatBPM]; got != "120.0000" {
		t.Errorf("beat_bpm = %q, want a's grid stamped once b's unserved grid is out", got)
	}
}

func TestOneGridDistinguishesLattices(t *testing.T) {
	same := []assetBeatGrid{
		{assetID: "a", grid: halfSecondGrid()},
		{assetID: "b", grid: halfSecondGrid()},
	}
	if got := len(oneGrid(same)); got != 1 {
		t.Errorf("identical lattices collapsed to %d; want 1", got)
	}
	diff := []assetBeatGrid{
		{assetID: "a", grid: halfSecondGrid()},
		{assetID: "b", grid: otherGrid()},
	}
	if got := len(oneGrid(diff)); got != 2 {
		t.Errorf("distinct lattices collapsed to %d; want 2 (the refusal depends on it)", got)
	}
}
