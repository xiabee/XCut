//go:build !windows

package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// clientSignalWriter notices the one line the client command prints when its
// server is up. Synchronising on that print is what keeps this test event-driven:
// a deadline here only reports a failure, it never decides an outcome.
//
// The channel is handed in and never reassigned — the first version cleared the
// field after firing, which the test goroutine was reading without the lock, and
// the race detector on the Linux leg found it in one run (the local box cannot
// compile this file, and the node's earlier non-race run could not see it either).
type clientSignalWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	ready chan string
	fired bool
}

func (w *clientSignalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if !w.fired && strings.Contains(w.buf.String(), "serving the same UI at") {
		w.fired = true
		// Buffered by its creator, and fired once, so this never blocks the
		// command under test.
		w.ready <- w.buf.String()
	}
	return n, err
}

func (w *clientSignalWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// TestClientCommandOnThisPlatformServesThenStops drives the command a Linux or
// macOS user actually runs for the desktop client: there is no native shell here,
// so `xcut client` is the serve pipeline plus a printed URL. Nothing had ever run
// that path — the WebView2 branch is the one the dev box exercises, and the
// fallback lived in a file no test compiled.
func TestClientCommandOnThisPlatformServesThenStops(t *testing.T) {
	a, _, _ := serveTestApp(t)
	// Port 0, so a busy machine cannot make this fail for reasons that belong to
	// whichever process got 8619 first — and the URL the command prints is read
	// back from the listener, not assumed.
	a.Cfg.Server.Listen = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(a.Ctx)
	defer cancel()
	a.Ctx = ctx
	ready := make(chan string, 1)
	out := &clientSignalWriter{ready: ready}
	a.Stdout = out
	a.Stderr = io.Discard

	done := make(chan error, 1)
	go func() { done <- cmdClient(a, nil) }()

	var line string
	select {
	case line = <-ready:
	case <-time.After(60 * time.Second):
		t.Fatalf("the client command never reported a server; output:\n%s", out.String())
	}
	// The URL is the tail of the line that says "serving the same UI at …", and
	// only that line: the buffer also holds the serve pipeline's own
	// "xcut serving http://… (loopback only)", so the first http:// in the text is
	// not the one this command printed.
	const marker = "serving the same UI at "
	at := strings.Index(line, marker)
	if at < 0 {
		t.Fatalf("the line the command printed names no URL: %q", line)
	}
	fields := strings.Fields(line[at+len(marker):])
	if len(fields) == 0 {
		t.Fatalf("the client printed %q with nothing after the marker", line)
	}
	url := fields[0]
	if !strings.HasPrefix(url, "http://") {
		t.Fatalf("the printed URL is %q", url)
	}
	// The address has to be the one that answers, not one the string claims.
	resp, err := http.Get(url + "/api/v1/health")
	if err != nil {
		t.Fatalf("the printed address %s does not serve: %v", url, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health at the printed address: %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cmdClient returned an error after the cancel: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("cmdClient never returned after its context was cancelled")
	}
	if !strings.Contains(out.String(), "server stopped") {
		t.Errorf("the client command exited without the drain's line:\n%s", out.String())
	}
	host := strings.TrimPrefix(url, "http://")
	if conn, derr := (&net.Dialer{}).DialContext(context.Background(), "tcp", host); derr == nil {
		conn.Close()
		t.Errorf("%s still accepts after the command returned", host)
	}
}
