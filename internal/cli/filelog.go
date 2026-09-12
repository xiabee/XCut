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
	warned   bool
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
	// A reader pinning the live log without the delete-share bit blocks the
	// rename (Windows). Truncating in place anyway would silently destroy
	// the rotation history — keep appending to the held file instead and
	// retry on the next rotation.
	if err := os.Rename(l.path, l.path+".1"); err != nil {
		if !l.warned {
			l.warned = true
			fmt.Fprintf(os.Stderr, "xcut: log rotation postponed — the active log is held open by another reader: %v\n", err)
		}
		f, reopenErr := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if reopenErr != nil {
			return // keep the closed-file dead-end warn path behavior
		}
		l.f = f
		// Reset the counter so the capped writer does not re-attempt the
		// (still failing) rename on every subsequent write — the file may
		// grow to ~2x maxBytes before the next try, which is fine.
		l.written = 0
		return
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", l.path, l.keep+1))
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		// Logging is best-effort, but never silently: a failed reopen used
		// to dead-end the logger (every later write fails with no trace).
		if !l.warned {
			l.warned = true
			fmt.Fprintf(os.Stderr, "xcut: log rotation failed — file logging is disabled for the rest of this session: %v\n", err)
		}
		return
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

// serveLogLevel resolves the serve logger's level: the -v/-q flags win over
// config (documented precedence: flags are the most specific layer) — the
// serve logger must honor them like the base logger already does, or
// `xcut serve -v` silently stays at info.
func serveLogLevel(a *App) slog.Level {
	switch {
	case a.Verbose:
		return slog.LevelDebug
	case a.Quiet:
		return slog.LevelWarn
	}
	switch a.Cfg.Log.Level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// newServeLogger builds the serve-mode logger: file (rotated) + stderr.
func newServeLogger(a *App, wsDir string) (*slog.Logger, func()) {
	fl, err := newFileLogger(filepath.Join(wsDir, "logs"), "serve",
		a.Cfg.Log.MaxSizeMB, a.Cfg.Log.MaxFiles)
	level := serveLogLevel(a)
	if err != nil {
		a.Log.Warn("file logging unavailable; stderr only", "err", err)
		return a.Log, func() {}
	}
	combined := io.MultiWriter(os.Stderr, fl)
	return slog.New(slog.NewTextHandler(combined, &slog.HandlerOptions{Level: level})), func() { _ = fl.Close() }
}
