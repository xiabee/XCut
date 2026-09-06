package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/xiabee/XCut/internal/xcerr"
)

// fileLogger writes slog records to <ws>/logs/<name>.log with size-based
// rotation (config log.max_size_mb / log.max_files). Serve mode uses it so
// a long-running instance keeps logs bounded (CONFIG PROMISE → implementation).
type fileLogger struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	f        *os.File
	written  int64
}

func newFileLogger(dir, name string, maxMB, keep int) (*fileLogger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, xcerr.E(xcerr.CodeInternal, "cannot create logs directory", err)
	}
	if maxMB <= 0 {
		maxMB = 50
	}
	if keep <= 0 {
		keep = 3
	}
	fl := &fileLogger{
		path:     filepath.Join(dir, name+".log"),
		maxBytes: int64(maxMB) << 20,
		keep:     keep,
	}
	if err := fl.open(); err != nil {
		return nil, err
	}
	return fl, nil
}

func (l *fileLogger) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot open log file", err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return xcerr.E(xcerr.CodeInternal, "cannot stat log file", err)
	}
	l.f = f
	l.written = fi.Size()
	return nil
}

// Write implements io.Writer for slog handlers; rotates when the file grows
// past maxBytes.
func (l *fileLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return 0, os.ErrClosed
	}
	if l.written+int64(len(p)) > l.maxBytes {
		l.rotateLocked()
	}
	n, err := l.f.Write(p)
	l.written += int64(n)
	return n, err
}

func (l *fileLogger) rotateLocked() {
	l.f.Close()
	l.f = nil
	// Shift existing rotations: keep-N deletes the oldest.
	for i := l.keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
	}
	_ = os.Rename(l.path, l.path+".1")
	_ = os.Remove(fmt.Sprintf("%s.%d", l.path, l.keep+1))
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return // logging is best-effort; nothing sane to do without a file
	}
	l.f = f
	l.written = 0
}

// Close flushes and closes the underlying file.
func (l *fileLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Sync()
	l.f.Close()
	l.f = nil
	return err
}

// newServeLogger builds the serve-mode logger: file (rotated) + stderr.
func newServeLogger(a *App, wsDir string) (*slog.Logger, func()) {
	fl, err := newFileLogger(filepath.Join(wsDir, "logs"), "serve",
		a.Cfg.Log.MaxSizeMB, a.Cfg.Log.MaxFiles)
	level := slog.LevelInfo
	switch a.Cfg.Log.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	if err != nil {
		a.Log.Warn("file logging unavailable; stderr only", "err", err)
		return a.Log, func() {}
	}
	combined := io.MultiWriter(os.Stderr, fl)
	return slog.New(slog.NewTextHandler(combined, &slog.HandlerOptions{Level: level})), func() { _ = fl.Close() }
}
