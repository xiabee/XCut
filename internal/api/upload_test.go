package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/workspace"
)

// seedUploadProject creates a project and returns its id.
func seedUploadProject(t *testing.T, s *Server, name string) string {
	t.Helper()
	rec, out := do(t, s, "POST", "/api/v1/projects", `{"name":"`+name+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %v", rec.Code, out)
	}
	return out["project"].(map[string]any)["id"].(string)
}

func uploadReq(t *testing.T, s *Server, pid, filename string, body string) (rec *httptest.ResponseRecorder, out map[string]any) {
	t.Helper()
	path := "/api/v1/projects/" + pid + "/assets/upload"
	if filename != "" {
		path += "?filename=" + url.QueryEscape(filename)
	}
	return do(t, s, "POST", path, body)
}

// TestAssetUploadImportsCopy: a real small video uploaded as content lands
// under imports/<project>/, probes cleanly, and produces an asset row.
func TestAssetUploadImportsCopy(t *testing.T) {
	s := testServer(t)
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	pid := seedUploadProject(t, s, "upload-ok")

	root := t.TempDir()
	src, err := testmedia.Generate(root, "clip.mp4", testmedia.DefaultFixture(), 160, 120, 6)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	rec, out := uploadReq(t, s, pid, "match cam 1.mp4", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %v", rec.Code, out)
	}
	asset := out["asset"].(map[string]any)
	if asset["filename"] != "match cam 1.mp4" {
		t.Fatalf("asset filename = %v", asset["filename"])
	}

	// The landed copy lives in the workspace imports dir, one folder per project.
	landed := filepath.Join(s.Pipe.WS.ImportsDir(), pid, "match cam 1.mp4")
	if _, err := os.Stat(landed); err != nil {
		t.Fatalf("uploaded copy missing at %s: %v", landed, err)
	}

	// A second upload of the same name must not overwrite: it lands as a new
	// file and becomes a new asset (distinct path → distinct asset id).
	rec2, out2 := uploadReq(t, s, pid, "match cam 1.mp4", string(body))
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second upload: %d %v", rec2.Code, out2)
	}
	asset2 := out2["asset"].(map[string]any)
	if asset2["path"] == asset["path"] {
		t.Fatal("second upload overwrote the first landed copy")
	}
}

// TestAssetUploadRefusesJunk: path-traversal names are reduced to a bare
// name inside imports/, empty bodies are refused, and non-media content is
// cleaned up instead of littering the imports dir.
func TestAssetUploadRefusesJunk(t *testing.T) {
	s := testServer(t)
	pid := seedUploadProject(t, s, "upload-junk")

	// Traversal attempt: the name is reduced; the landed file (had the
	// content been media) could only live inside imports/.
	rec, out := uploadReq(t, s, pid, `..\..\evil.mp4`, "x")
	if rec.Code == http.StatusCreated {
		t.Fatalf("junk content must not import, got %d", rec.Code)
	}
	landed := filepath.Join(s.Pipe.WS.ImportsDir(), pid)
	entries, err := os.ReadDir(landed)
	if err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), "..") {
				t.Fatalf("traversal name landed: %s", e.Name())
			}
		}
	}

	// Empty body.
	rec, out = uploadReq(t, s, pid, "empty.mp4", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty upload: %d %v", rec.Code, out)
	}

	// Missing filename.
	rec, out = uploadReq(t, s, pid, "", "x")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing filename: %d %v", rec.Code, out)
	}

	// Non-media content: the copy is probed, refused, and cleaned up.
	rec, out = uploadReq(t, s, pid, "notes.txt", "definitely not video")
	if rec.Code == http.StatusCreated {
		t.Fatalf("text content must not import, got %d", rec.Code)
	}
	if entries, err := os.ReadDir(landed); err == nil && len(entries) != 0 {
		t.Fatalf("failed probe left litter in imports/: %d entries", len(entries))
	}
}

// TestProjectDeleteRemovesImports: deleting the project takes its
// uploaded-copy directory with it — the artifacts must not accumulate on
// disk forever.
func TestProjectDeleteRemovesImports(t *testing.T) {
	s := testServer(t)
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	pid := seedUploadProject(t, s, "delete-imports")

	root := t.TempDir()
	src, err := testmedia.Generate(root, "clip.mp4", testmedia.DefaultFixture(), 160, 120, 6)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if rec, out := uploadReq(t, s, pid, "clip.mp4", string(body)); rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %v", rec.Code, out)
	}
	imports := filepath.Join(s.Pipe.WS.ImportsDir(), pid)
	if entries, err := os.ReadDir(imports); err != nil || len(entries) == 0 {
		t.Fatalf("uploaded copy missing pre-delete: %v", err)
	}

	rec, out := do(t, s, "DELETE", "/api/v1/projects/"+pid, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %v", rec.Code, out)
	}
	if _, err := os.Stat(imports); !os.IsNotExist(err) {
		t.Fatalf("imports dir must die with the project, err=%v", err)
	}
}

// trickleBody emits total bytes, pausing delay every napBytes: a slow
// producer that still makes progress (the disk-paced case — the server
// accepts bytes only as fast as it can write them). Pacing counts BYTES,
// not Read calls: the transport chooses its own buffer sizes. It
// fabricates its bytes (no real content), which is what the stalled /
// progressing cases want — the probe refuses them either way.
type trickleBody struct {
	total    int
	napBytes int
	delay    time.Duration
	sent     int
	sinceNap int
}

func (b *trickleBody) Read(p []byte) (int, error) {
	if b.sent >= b.total {
		return 0, io.EOF
	}
	if b.sinceNap >= b.napBytes {
		time.Sleep(b.delay)
		b.sinceNap = 0
	}
	n := len(p)
	if n > b.total-b.sent {
		n = b.total - b.sent
	}
	for j := range p[:n] {
		p[j] = 0xAA
	}
	b.sent += n
	b.sinceNap += n
	return n, nil
}

// TestUploadRearmsReadDeadline: a transfer that takes longer than the
// server's fixed ReadTimeout must survive as long as the client keeps
// making progress, and a stalled client must still be cut at the idle
// window. Uses a real TCP server (deadlines only exist on real
// connections) with deliberately short timeouts so the test stays fast.
func TestUploadRearmsReadDeadline(t *testing.T) {
	s := testServer(t)
	pid := seedUploadProject(t, s, "upload-slow")
	path := "/api/v1/projects/" + pid + "/assets/upload?filename=slow.mp4"

	// The idle window a test can afford; restore before anything else so a
	// fatal below cannot leak the short window into other tests.
	prev := uploadIdleWindow
	uploadIdleWindow = 250 * time.Millisecond
	t.Cleanup(func() { uploadIdleWindow = prev })

	// Real TCP server with a 250 ms total-time ReadTimeout — the pre-m112
	// semantics under which the progressing transfer below must have died.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       250 * time.Millisecond,
	}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Close() })

	// uploadMsg returns the endpoint's error message: the probe refusal and
	// the transfer failure both answer 500 with different messages, so the
	// message (not the status) tells the stages apart.
	uploadMsg := func(body *trickleBody) (string, error) {
		req, err := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String()+path, body)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		var out struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &out)
		return out.Message, nil
	}

	// Control: the same junk content through the in-memory recorder gives
	// the endpoint's message AFTER reading the whole body (the probe
	// refusal) — ffmpeg presence only changes which message that is.
	_, ctrlOut := do(t, s, "POST", path, "definitely not video")
	wantMsg, _ := ctrlOut["message"].(string)

	// Progressing transfer: ~0.6 s total, 2.4x the total-time bound, but
	// every 100 ms gap sits inside one idle window. Reaching the full-body
	// status proves the whole body was read: the deadline heartbeat held.
	slow := &trickleBody{total: 384 << 10, napBytes: 64 << 10, delay: 100 * time.Millisecond}
	start := time.Now()
	msg, err := uploadMsg(slow)
	if err != nil {
		t.Fatalf("progressing upload died: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Fatalf("progressing upload finished too fast (%s) — pacing broken", elapsed)
	}
	if msg != wantMsg {
		t.Fatalf("progressing upload answered %q, want the probe-refusal %q — body not fully read", msg, wantMsg)
	}

	// Stalled transfer: one 64 KiB burst, then 800 ms of silence — well
	// past the 250 ms idle window with no progress to re-arm it. The cut
	// surfaces as a transport error or a transfer-failure 500 — what it
	// must never be is the full-body-read status.
	stall := &trickleBody{total: 384 << 10, napBytes: 64 << 10, delay: 800 * time.Millisecond}
	msg, err = uploadMsg(stall)
	if err != nil {
		t.Logf("stalled upload cut with transport error: %v", err)
	} else if msg == wantMsg {
		t.Fatalf("stalled upload reached the probe stage — idle window did not fire")
	} else {
		t.Logf("stalled upload cut with message %q", msg)
	}
}

// pacingFor returns the per-nap delay that spreads nap-sized chunks over
// total of transfer time: a fixture of any size gets the same flight time,
// so the bound below is exceeded by construction, not by fixture luck.
func pacingFor(bodyLen, napBytes int, total time.Duration) time.Duration {
	if bodyLen <= 0 {
		return total
	}
	d := time.Duration(float64(total) * float64(napBytes) / float64(bodyLen))
	if d < time.Millisecond {
		d = time.Millisecond
	}
	return d
}

// pacedBody streams real content (probe-passing media) at a paced rate:
// at most napBytes per Read, one delay nap between them. The per-Read cap
// keeps the byte accounting exact whatever buffer sizes the transport
// picks, so the flight time below is by construction, not by fixture luck.
type pacedBody struct {
	data     []byte
	napBytes int
	delay    time.Duration
	sent     int
	sinceNap int
}

func (b *pacedBody) Read(p []byte) (int, error) {
	if b.sent >= len(b.data) {
		return 0, io.EOF
	}
	if b.sinceNap >= b.napBytes {
		time.Sleep(b.delay)
		b.sinceNap = 0
	}
	n := len(p)
	if n > b.napBytes {
		n = b.napBytes
	}
	if n > len(b.data)-b.sent {
		n = len(b.data) - b.sent
	}
	copy(p, b.data[b.sent:b.sent+n])
	b.sent += n
	b.sinceNap += n
	return n, nil
}

// TestUploadResponseSurvivesServerWriteTimeout: the upload's 201 is written
// only after the whole body copy and the import probe — both far past the
// server's absolute WriteTimeout, which net/http arms once when the request
// headers are read (the read-deadline heartbeat does not touch it). Without
// the write-idle re-arm the transfer lands the file and creates the asset
// row while the response dies on the expired deadline: the browser answers
// "network error" and the user's retry lands a duplicate import. Real TCP
// server with a 400 ms WriteTimeout against a paced ~1 s transfer, and real
// media so the probe passes and the response is the 201 a browser waits for.
func TestUploadResponseSurvivesServerWriteTimeout(t *testing.T) {
	s := testServer(t)
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	pid := seedUploadProject(t, s, "upload-write")

	root := t.TempDir()
	src, err := testmedia.Generate(root, "clip.mp4", testmedia.DefaultFixture(), 160, 120, 6)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	const writeTO = 400 * time.Millisecond
	hs := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: time.Second,
		WriteTimeout:      writeTO,
	}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Close() })

	// Paced so the transfer spends ~1.2 s in flight whatever the generator
	// produced — past the 400 ms bound by a 3x margin, so the deadline
	// armed at header-read is already expired by the time the handler's
	// only response write happens.
	slow := &pacedBody{data: body, napBytes: 4 << 10, delay: pacingFor(len(body), 4<<10, 1200*time.Millisecond)}
	path := "http://" + ln.Addr().String() + "/api/v1/projects/" + pid + "/assets/upload?filename=slow.mp4"
	req, err := http.NewRequest(http.MethodPost, path, slow)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload response lost after a %s transfer (server WriteTimeout %s): %v",
			time.Since(start).Round(time.Millisecond), writeTO, err)
	}
	defer resp.Body.Close()
	if elapsed := time.Since(start); elapsed < writeTO {
		t.Fatalf("transfer finished in %s, below the %s bound — pacing broken, the case proves nothing",
			elapsed.Round(time.Millisecond), writeTO)
	}
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		t.Fatalf("upload: %d %s", resp.StatusCode, raw)
	}
	var out struct {
		Asset map[string]any `json:"asset"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("201 body unreadable: %v", err)
	}
	if out.Asset["id"] == nil || out.Asset["id"] == "" {
		t.Fatalf("201 carried no asset id: %v", out.Asset)
	}
}

// TestRequestFailuresLeaveServerSideTraces: the HTTP response carries only
// the user-safe message, so the technical cause must land in the server log
// — otherwise a failed request is undiagnosable from serve.log.
func TestRequestFailuresLeaveServerSideTraces(t *testing.T) {
	var buf safeBuffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	root := t.TempDir()
	db, err := storage.Open(root + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := config.Default()
	cfg.Workspace = root
	if err := config.Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	ws := workspace.New(root)
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		DB:   db,
		Pipe: pipeline.NewDeps(context.Background(), db, ws, cfg, logger),
		Log:  logger,
	}
	s.Pipe.TimelineWriteLock = &s.TimelineMu

	// A failing request (invalid JSON): the response stays user-safe...
	rec, out := do(t, s, "POST", "/api/v1/projects", `{broken`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid body: %d %v", rec.Code, out)
	}
	if msg, _ := out["message"].(string); strings.Contains(msg, "{broken") {
		t.Fatalf("response leaked raw body: %q", msg)
	}
	// ...and the log carries code, method, path and the technical cause.
	trace := buf.String()
	for _, want := range []string{"request failed", "code=validation", "method=POST", "path=/api/v1/projects", "invalid character"} {
		if !strings.Contains(trace, want) {
			t.Fatalf("log trace missing %q in:\n%s", want, trace)
		}
	}
}

// safeBuffer is a bytes.Buffer safe for the slog handler's single-threaded
// use in this test (kept named for clarity).
type safeBuffer struct{ bytes.Buffer }

func (b *safeBuffer) Write(p []byte) (int, error) { return b.Buffer.Write(p) }

// TestConcurrentSameNameUploadsNeverOverwrite: N concurrent uploads with
// the same filename must all land as distinct files (never-overwrite is a
// contract, not a best effort). The probe in each request is the slow part
// and runs concurrently; without landing serialization the fast
// pick-slot+rename sections then collide — this test pins the mutex.
func TestConcurrentSameNameUploadsNeverOverwrite(t *testing.T) {
	s := testServer(t)
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	pid := seedUploadProject(t, s, "upload-race")

	root := t.TempDir()
	src, err := testmedia.Generate(root, "clip.mp4", testmedia.DefaultFixture(), 160, 120, 6)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	const n = 8
	type result struct {
		code int
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST",
				"/api/v1/projects/"+pid+"/assets/upload?filename=race.mp4",
				bytes.NewReader(body))
			req.RemoteAddr = "127.0.0.1:52000" // local client; the gate is auth_test.go's job
			s.Handler().ServeHTTP(rec, req)
			results[i] = result{code: rec.Code}
		}(i)
	}
	close(start)
	wg.Wait()

	for i, r := range results {
		if r.code != http.StatusCreated {
			t.Fatalf("upload %d answered %d, want 201", i, r.code)
		}
	}

	entries, err := os.ReadDir(filepath.Join(s.Pipe.WS.Root, "imports", pid))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != n {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("imports/ holds %d files (%v), want %d — an upload overwrote another",
			len(entries), names, n)
	}

	assets, err := s.DB.ListAssets(context.Background(), pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != n {
		t.Fatalf("%d asset rows, want %d", len(assets), n)
	}
}
