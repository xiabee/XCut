package config

import (
	"fmt"
	"runtime"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Resource profile knob: "auto" sizes the concurrency knobs from the machine
// (default), "manual" keeps whatever the config layers said. The boundary the
// auto sizer respects is politeness: at most ~half of the machine's logical
// CPUs and ~half of its RAM ever belong to ffmpeg, so the desktop in front of
// the tool keeps its headroom.

const (
	ProfileAuto   = "auto"
	ProfileManual = "manual"
)

// MachineSpec is what the auto sizer knows about the host.
type MachineSpec struct {
	LogicalCPU int
	// TotalMemoryBytes is 0 when the platform cannot report it; the sizer
	// then skips the RAM guard and sizes from CPUs alone.
	TotalMemoryBytes uint64
}

// ResourceRecommendation is the auto-sized posture for one machine.
type ResourceRecommendation struct {
	MaxFFmpegProcesses int
	FFmpegThreads      int
	MaxAnalysisWorkers int
	MaxRenderWorkers   int
}

// Describe renders one human line (doctor, config show notes).
func (r ResourceRecommendation) Describe(spec MachineSpec) string {
	return fmt.Sprintf("profile=auto: %d ffmpeg children × %d threads (of %d logical CPUs%s), analysis workers %d",
		r.MaxFFmpegProcesses, r.FFmpegThreads, spec.LogicalCPU, ramNote(spec), r.MaxAnalysisWorkers)
}

func ramNote(spec MachineSpec) string {
	if spec.TotalMemoryBytes == 0 {
		return ", RAM unknown"
	}
	return fmt.Sprintf(", %.0f GB RAM", float64(spec.TotalMemoryBytes)/(1<<30))
}

// RecommendResources sizes the concurrency knobs. Politeness budget: ffmpeg
// may use at most half of the logical CPUs (children × threads ≤ logical/2)
// and at most half of RAM (children × 1.5 GB ≤ RAM/2). Everything below the
// budget scales up, everything above scales down, and nothing drops below
// 2 children × 2 threads so "fast" never collapses to "serial".
func RecommendResources(spec MachineSpec) ResourceRecommendation {
	logical := spec.LogicalCPU
	if logical < 1 {
		logical = 1
	}
	children := clampInt(logical/4, 2, 4)
	threads := clampInt((logical/2)/children, 2, 8)

	if spec.TotalMemoryBytes > 0 {
		ramGB := float64(spec.TotalMemoryBytes) / (1 << 30)
		for children > 1 && float64(children)*1.5 > ramGB/2 {
			children--
		}
		// Threads re-balance after the RAM guard shrank the children count.
		threads = clampInt((logical/2)/children, 2, 8)
	}

	return ResourceRecommendation{
		MaxFFmpegProcesses: children,
		FFmpegThreads:      threads,
		MaxAnalysisWorkers: children,
		MaxRenderWorkers:   1, // one render already parallelizes across clips
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// DetectMachine reports the specs the auto sizer works from.
func DetectMachine() MachineSpec {
	return MachineSpec{
		LogicalCPU:       runtime.NumCPU(),
		TotalMemoryBytes: totalMemoryBytes(),
	}
}

// applyAutoProfile overwrites the concurrency knobs with machine-sized values.
// Called from Resolve only when the profile knob says auto.
func applyAutoProfile(cfg *Config) {
	rec := RecommendResources(DetectMachine())
	cfg.Resource.MaxFFmpegProcesses = rec.MaxFFmpegProcesses
	cfg.Resource.FFmpegThreads = rec.FFmpegThreads
	cfg.Resource.MaxAnalysisWorkers = rec.MaxAnalysisWorkers
	cfg.Resource.MaxRenderWorkers = rec.MaxRenderWorkers
}

// validateProfile checks the profile knob itself.
func validateProfile(profile string) error {
	switch profile {
	case "", ProfileAuto, ProfileManual:
		return nil
	default:
		return xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("invalid resource.profile %q (want auto|manual)", profile), nil)
	}
}
