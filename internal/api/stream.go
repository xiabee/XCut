package api

import (
	"net/http"
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

// serveMediaFile streams one workspace file with range support via
// http.ServeContent under the write-idle heartbeat. All streaming routes
// go through here so the heartbeat cannot drift from the routes.
func serveMediaFile(w http.ResponseWriter, r *http.Request, name string, mod time.Time, content http.File) {
	ww := &writeIdleWriter{ResponseWriter: w, rc: http.NewResponseController(w), window: streamIdleWindow}
	http.ServeContent(ww, r, name, mod, content)
}
