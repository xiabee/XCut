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
type clientSignalWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	notify chan string
}

func (w *clientSignalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if w.notify != nil && strings.Contains(w.buf.String(), "serving the same UI at") {
		line := w.buf.String()
		w.notify <- line
		w.notify = nil
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
	ctx, cancel := context.WithCancel(a.Ctx)
	defer cancel()
	a.Ctx = ctx
	out := &clientSignalWriter{notify: make(chan string, 1)}
	a.Stdout = out
	a.Stderr = io.Discard

	done := make(chan error, 1)
	go func() { done <- cmdClient(a, nil) }()

	var line string
	select {
	case line = <-out.notify:
	case <-time.After(60 * time.Second):
		t.Fatalf("the client command never reported a server; output:\n%s", out.String())
	}
	url := strings.TrimSpace(line[strings.Index(line, "http://"):])
	if url == "" || !strings.HasPrefix(url, "http://") {
		t.Fatalf("no URL in the line the command printed: %q", line)
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
