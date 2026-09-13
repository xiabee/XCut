package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// neighborTools lets a packaged binary find its FFmpeg by proximity: a
// double-clicked release exe usually has no PATH set up, so when the
// config leaves the toolchain on its default (PATH) names, we also look
// right next to the executable — <exe dir>/ffmpeg.exe and
// <exe dir>/bin/ffmpeg.exe (and the ffprobe siblings). This keeps the
// "never bundle third-party binaries" rule intact: the user drops their
// own ffmpeg next to xcut and everything just works.
func neighborTools(cfg *Config) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)
	if cfg.FFmpeg.Bin == "" {
		if p, ok := neighborBin(dir, "ffmpeg"); ok {
			cfg.FFmpeg.Bin = p
		}
	}
	if cfg.FFmpeg.ProbeBin == "" {
		if p, ok := neighborBin(dir, "ffprobe"); ok {
			cfg.FFmpeg.ProbeBin = p
		}
	}
}

// neighborBin looks for <name>[.exe] in dir and in dir/bin. Empty (with
// ok=false) when nothing is there — callers keep the PATH fallback.
func neighborBin(dir, name string) (string, bool) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	for _, candidate := range []string{
		filepath.Join(dir, name+suffix),
		filepath.Join(dir, "bin", name+suffix),
	} {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, true
		}
	}
	return "", false
}
