package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// writeIdleWriter.Flush exists for the reason its own comment gives: the wrapper
// must not be the thing that makes a streaming writer *look* non-flushable to
// whoever asserts the interface. Nothing in this program calls it. That was
// established, not assumed — the first version of this file asserted that
// serveMediaFile flushes the writer beneath it, and it went red, because
// net/http's ServeContent does not flush at all (grep the stdlib's fs.go for the
// call). So this belongs to the ledger's interface-shim class, not the
// untested-behaviour class; what is worth pinning is the shim's two arms, since the
// day something does flush through a streaming response this is either a transparent
// pass-through or a silent swallow.

// flushCounter records how often the writer underneath was flushed.
type flushCounter struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushCounter) Flush() {
	f.flushes++
	f.ResponseRecorder.Flush()
}

func TestWriteIdleWriterFlushIsTransparent(t *testing.T) {
	inner := &flushCounter{ResponseRecorder: httptest.NewRecorder()}
	w := &writeIdleWriter{ResponseWriter: inner, rc: http.NewResponseController(inner), window: streamIdleWindow}

	var asFlusher http.Flusher = w
	asFlusher.Flush()
	if inner.flushes != 1 {
		t.Errorf("the wrapper was handed a flusher and flushed it %d times, want 1", inner.flushes)
	}

	// The other arm: nothing underneath, no panic, nothing written. Embedding the
	// *interface* is what hides Flush — the same method set a bare
	// http.ResponseWriter argument has at a call site.
	plain := httptest.NewRecorder()
	bare := struct{ http.ResponseWriter }{plain}
	w2 := &writeIdleWriter{ResponseWriter: bare, rc: http.NewResponseController(plain), window: streamIdleWindow}
	var alsoFlusher http.Flusher = w2
	alsoFlusher.Flush()
	if plain.Body.Len() != 0 {
		t.Errorf("a flush with nothing underneath wrote %d bytes", plain.Body.Len())
	}
}
