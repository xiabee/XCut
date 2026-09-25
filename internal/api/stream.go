package api

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// Streaming media responses (render playback/download, asset preview,
// subtitle download) need the same treatment the upload path got: the
// server's WriteTimeout bounds a response's TOTAL write time, which a
// multi-GiB render cannot fit when the reader throttles — browser media
// elements buffer ahead and then pace their fetch, so a long playback is
// a slow reader by design. The write-idle window below converts the total
// bound into a progress bound: every delivered chunk re-arms the deadline,
// a transfer that keeps moving is never cut, and a reader that stops
// consuming still trips the window one span after the last delivered byte.

// streamIdleWindow re-arms the connection write deadline after every
// written chunk on streaming endpoints. Matches serve.go's WriteTimeout;
// a var so the deadline test can shrink it.
var streamIdleWindow = 60 * time.Second

// writeIdleWriter wraps a ResponseWriter for streaming responses. The
// server's absolute WriteTimeout (armed when the request headers are read)
// still bounds the time to first byte; from the first chunk on, this
// wrapper owns the deadline with per-write re-arming. Setting the deadline
// is best-effort: transports without deadline support (in-memory
// recorders) keep the server's fixed total-time bound.
type writeIdleWriter struct {
	http.ResponseWriter
	rc     *http.ResponseController
	window time.Duration
}

// Write re-arms the connection write deadline before delegating, so the
// window measures idle time (since the last delivered chunk), not total
// response time.
func (w *writeIdleWriter) Write(p []byte) (int, error) {
	_ = w.rc.SetWriteDeadline(time.Now().Add(w.window))
	return w.ResponseWriter.Write(p)
}

// Flush forwards to the underlying flusher if present (http.ServeContent
// itself does not flush, but a caller-provided flusher must not be lost
// behind the wrapper).
func (w *writeIdleWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real connection through
// this wrapper — the upload path hands the wrapped writer to its
// read-deadline heartbeat, which dies silently (best-effort by contract)
// if the chain stops here.
func (w *writeIdleWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// mediaContentTypes pins the type for the containers XCut actually serves.
// http.ServeContent falls back to the platform's MIME table, and that answer
// differs by host — Go's built-in table labels .webm "audio/webm" while a
// distro's /etc/mime.types says "video/webm" — and a <video> element told the
// bytes are audio refuses to play the preview. The extensions that matter here
// are therefore named here; anything else keeps the sniffed fallback.
var mediaContentTypes = map[string]string{
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".mov":  "video/quicktime",
	".mkv":  "video/x-matroska",
	".webm": "video/webm",
	".avi":  "video/x-msvideo",
	".mpg":  "video/mpeg",
	".mpeg": "video/mpeg",
	".ts":   "video/mp2t",
	".m4a":  "audio/mp4",
	".mp3":  "audio/mpeg",
	".aac":  "audio/aac",
	".wav":  "audio/wav",
	".flac": "audio/flac",
	".ogg":  "audio/ogg",
	".ass":  "text/x-ssa",
	".srt":  "application/x-subrip",
	".vtt":  "text/vtt",
	".json": "application/json",
}

// serveMediaFile streams one workspace file with range support via
// http.ServeContent under the write-idle heartbeat. All streaming routes
// go through here so the heartbeat cannot drift from the routes.
func serveMediaFile(w http.ResponseWriter, r *http.Request, name string, mod time.Time, content http.File) {
	if ct, ok := mediaContentTypes[strings.ToLower(filepath.Ext(name))]; ok {
		w.Header().Set("Content-Type", ct) // ServeContent respects a preset type
	}
	ww := &writeIdleWriter{ResponseWriter: w, rc: http.NewResponseController(w), window: streamIdleWindow}
	http.ServeContent(ww, r, name, mod, content)
}
