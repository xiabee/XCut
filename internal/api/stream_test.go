package api

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// streamTestServer serves one file through the production streaming helper
// (serveMediaFile — the path all streaming routes delegate to) over a real
// TCP server whose WriteTimeout is the pre-fix total-time semantics: a
// progressing-but-slow transfer must only survive through the heartbeat.
func streamTestServer(t *testing.T, file string, writeTimeout time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /file", func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(file)
		if err != nil {
			t.Errorf("open fixture: %v", err)
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			t.Errorf("stat fixture: %v", err)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		serveMediaFile(w, r, "slow.mp4", fi.ModTime(), f)
	})
	hs := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: time.Second,
		WriteTimeout:      writeTimeout,
	}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Close() })
	return ln.Addr().String()
}

// rawCountBody fetches /file over one raw connection, reading the body in
// paced chunks. It returns the number of body bytes received and the read
// error that ended the stream (io.EOF at a clean end).
func rawCountBody(t *testing.T, addr string, chunk int, nap time.Duration) (int64, error) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "GET /file HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	// Strip the response header block; body bytes possibly already buffered
	// in br are counted by the loop below.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read response header: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}
	var got int64
	buf := make([]byte, chunk)
	for {
		n, rerr := br.Read(buf)
		got += int64(n)
		if nap > 0 && n > 0 {
			time.Sleep(nap)
		}
		if rerr != nil {
			return got, rerr
		}
	}
}

// TestStreamRearmsWriteDeadline: a playback/download that takes longer than
// the server's fixed WriteTimeout must survive while the reader keeps
// consuming (browser media elements pace their fetch by design, so a long
// render is a slow reader even on loopback), and a reader that stalls must
// still be cut at the idle window. Mirrors the upload side's read-deadline
// heartbeat (M112) on the response direction.
func TestStreamRearmsWriteDeadline(t *testing.T) {
	// The idle window a test can afford; restore before anything else so a
	// fatal below cannot leak the short window into other tests. The read
	// pace keeps a 10x margin under the window: `go test` runs packages in
	// parallel, and this test coexists with heavy ones — a 5x margin was
	// observed to false-fail once when a scheduling hiccup stretched one
	// read gap past the window while render/cli tests loaded the box.
	prev := streamIdleWindow
	streamIdleWindow = 400 * time.Millisecond
	t.Cleanup(func() { streamIdleWindow = prev })

	dir := t.TempDir()
	// 1 MiB fixture: at 32 KiB per ~40 ms the full transfer needs ~1.3 s,
	// 3.2x the 400 ms total-time bound, while the margin against any single
	// read gap is 10x.
	small := filepath.Join(dir, "small.mp4")
	body := make([]byte, 1<<20)
	for i := range body {
		body[i] = byte(i)
	}
	if err := os.WriteFile(small, body, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("progressing transfer survives", func(t *testing.T) {
		addr := streamTestServer(t, small, 400*time.Millisecond)
		got, rerr := rawCountBody(t, addr, 32<<10, 40*time.Millisecond)
		if got != int64(len(body)) {
			t.Fatalf("progressing stream delivered %d/%d bytes (err %v) — write-deadline heartbeat did not hold", got, len(body), rerr)
		}
		if rerr != nil && rerr != io.EOF {
			t.Fatalf("progressing stream ended with %v after delivering all bytes", rerr)
		}
	})

	// NOTE: no plain-ServeContent counterfactual here. Its cut point
	// depends on how much of the body loopback kernel buffering swallows —
	// on this host ~450 KiB, on remote-node the whole 1 MiB (never a block,
	// never a deadline). Machine-dependent flakiness for zero coverage the
	// two subtests don't already provide: "progressing survives" fails if
	// re-arming is lost, "stalled is cut" fails if the deadline is gone.

	// Stalled reader: the body must exceed what loopback kernel buffering
	// can swallow so the server's write genuinely blocks (Windows autotune
	// caps ~16 MB, Linux defaults ~6 MB — 64 MB clears both with headroom);
	// 256 KiB consumed, then a 1.5 s stall — well past the 400 ms idle
	// window with no progress to re-arm it. The cut surfaces as EOF or a
	// transport error partway through.
	t.Run("stalled reader is cut", func(t *testing.T) {
		const total = 64 << 20
		big := filepath.Join(dir, "big.mp4")
		if err := os.WriteFile(big, make([]byte, total), 0o644); err != nil {
			t.Fatal(err)
		}
		addr := streamTestServer(t, big, 400*time.Millisecond)

		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := fmt.Fprintf(conn, "GET /file HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		br := bufio.NewReader(conn)
		for {
			line, rerr := br.ReadString('\n')
			if rerr != nil {
				t.Fatalf("read response header: %v", rerr)
			}
			if line == "\r\n" {
				break
			}
		}

		chunk := make([]byte, 64<<10)
		var got int64
		// Consume 256 KiB, then stall past the window, then drain to EOF.
		for got < 256<<10 {
			n, rerr := br.Read(chunk)
			got += int64(n)
			if rerr != nil {
				t.Fatalf("reader cut before consuming the initial 256 KiB: %v at %d", rerr, got)
			}
		}
		time.Sleep(1500 * time.Millisecond)
		for {
			n, rerr := br.Read(chunk)
			got += int64(n)
			if rerr != nil {
				break
			}
		}
		if got >= total {
			t.Fatalf("stalled reader received the full %d MiB — idle window did not fire", total>>20)
		}
		t.Logf("stalled stream cut at %d/%d bytes", got, total)
	})
}
