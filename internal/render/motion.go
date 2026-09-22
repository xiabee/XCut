package render

import (
	"fmt"
	"strconv"

	"github.com/xiabee/XCut/internal/timeline"
)

// motionFilter turns a clip's framing plan (运镜) into an ffmpeg `crop` stage: a
// window sized to the canvas's aspect, magnified to fill the canvas, whose center
// slides from From to To over the clip's own duration.
//
// The split of duties is forced by the filter itself: crop evaluates w and h once
// (they define the output geometry), so the *zoom* lives there, and x and y per
// frame, so the *movement* lives there. `t` is the clip's own time because the
// stage runs after setpts on a per-clip encode — which is what keeps a plan
// attached to what the viewer sees rather than to source seconds.
//
// A plan whose center does not move emits constants, so "this clip is a still
// punch-in" is visible in the command rather than only in the numbers.
func motionFilter(m *timeline.Motion, canvasW, canvasH int, dur float64) string {
	aspect := float64(canvasW) / float64(canvasH)
	fx, fy := 0.5, 0.5
	if len(m.From) == 2 {
		fx, fy = m.From[0], m.From[1]
	}
	tx, ty := fx, fy
	if len(m.To) == 2 {
		tx, ty = m.To[0], m.To[1]
	}
	return fmt.Sprintf(
		"crop=w='min(trunc(ih*%s/2)*2,iw)':h='trunc(ih*%s/2)*2':x='%s':y='%s'",
		num(m.Zoom*aspect), num(m.Zoom),
		axis(fx, tx, "iw", "ow", dur),
		axis(fy, ty, "ih", "oh", dur))
}

// axis is one dimension of the sliding window: the center travels from a to b as
// the clip plays, and the window is clamped inside the frame at both ends, so a
// plan aimed at a corner shows frame content instead of asking crop for an
// out-of-bounds offset. The window's own size is `ow`/`oh` here — crop's x/y
// expressions have no `w`/`h`, and asking for one fails the render at configure
// time with "Undefined constant".
func axis(a, b float64, in, out string, dur float64) string {
	center := num(a)
	if a != b {
		center = fmt.Sprintf("(%s+(%s-%s)*t/%s)", num(a), num(b), num(a), num(dur))
	}
	return fmt.Sprintf("min(max(%s*%s-%s/2,0),%s-%s)", center, in, out, in, out)
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', 5, 64) }
