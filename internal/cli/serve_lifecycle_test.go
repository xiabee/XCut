package cli

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/job"
)

// startServeCore is the pipeline behind `xcut serve` and both `xcut client`
// shells: it takes over logging, sweeps what it claims to sweep, binds, and is
// drained by shutdownServe. None of that had ever been run in a test — the
// address policy and the timeout set had, which is how a defect in the
// lifetime of the log file survived every session since it was introduced.

// serveTestApp builds the App the way Run builds it for a serve command,
// against a throwaway workspace: a claim about serve.log is only worth making
// through the real config, workspace and logger shapes.
func serveTestApp(t *testing.T) (*App, *bytes.Buffer, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = root
	if err := config.Resolve(cfg); err != nil {
		t.Fatalf("config.Resolve: %v", err)
	}
	var out bytes.Buffer
	a := &App{
		Ctx:    t.Context(),
		Stdout: &out,
		Stderr: &out,
		Cfg:    cfg,
		Log:    newLogger(&out, slog.LevelInfo),
	}
	return a, &out, root
}

func requireLogged(t *testing.T, path string, want ...string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	for _, w := range want {
		if !bytes.Contains(b, []byte(w)) {
			t.Errorf("serve.log does not record %q\nfile was:\n%s", w, b)
		}
	}
}

// TestServeFileLogRecordsWhileServing is the lifetime claim: the operator is
// told (docs/OPERATIONS.md) that <workspace>/logs/serve.log is where a running
// server's failures are traceable, and api.writeErr logs a failed request for
// exactly that reason. Everything written after startServeCore returns has to
// reach the file.
func TestServeFileLogRecordsWhileServing(t *testing.T) {
	a, _, root := serveTestApp(t)
	r, err := startServeCore(a, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startServeCore: %v", err)
	}
	defer r.close()

	logPath := filepath.Join(root, "logs", "serve.log")
	// Control: this line is written inside startServeCore, before it returns.
	// If it is missing the test is reading the wrong file, and the assertions
	// below would say nothing about the logger's lifetime.
	requireLogged(t, logPath, "server started")

	addr := r.ln.Addr().String()
	resp, err := http.Get("http://" + addr + "/api/v1/projects/still-serving/timeline")
	if err != nil {
		t.Fatalf("request to the running server: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d body %s, want a 404 from the real handler", resp.StatusCode, body)
	}

	// Both halves of the trace: that a request failed, and which one.
	requireLogged(t, logPath, "request failed", "still-serving")

	shutdownServe(a, r)
	requireLogged(t, logPath, "server shutting down")
}

// TestServeStartupSweepsWhatItClaims covers the three sweeps serve does before
// it listens — the orphaned-job reconcile, the temp sweep and the upload
// staging sweep. They are destructive (they delete files and finish job rows),
// so they are asserted individually: what is reclaimed, and what must survive.
func TestServeStartupSweepsWhatItClaims(t *testing.T) {
	a, out, root := serveTestApp(t)
	ws := a.Workspace()
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}

	db, err := a.OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	proj, err := db.CreateProject(a.Ctx, "swept")
	if err != nil {
		t.Fatal(err)
	}
	// A job row left "running" by a process that is now dead, younger than
	// job.stale_running_after: only serve's unconditional sweep can reclaim it,
	// because OpenDB's age-gated sweep has already passed over it.
	id, err := a.Pipeline(db).Queue.RunInline(a.Ctx, "analyze", proj.ID,
		job.ClassCPULight, nil, func(context.Context, func(float64)) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE jobs SET status = 'running', started_at = ?, finished_at = NULL WHERE id = ?`,
		time.Now().Unix(), id); err != nil {
		t.Fatal(err)
	}

	writeAt(t, filepath.Join(ws.TempDir(), "job-scratch.tmp"), "debris from a killed job")
	imports := filepath.Join(ws.ImportsDir(), "swept")
	if err := os.MkdirAll(imports, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(imports, ".upload-1234"), "staged, never renamed")
	writeAt(t, filepath.Join(imports, "landed.mp4"), "a finished upload")
	db.Close()

	r, err := startServeCore(a, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startServeCore: %v", err)
	}
	defer r.close()

	for _, want := range []string{
		"reconciled 1 orphaned job(s)",
		"reclaimed 1 temp entries",
		"reclaimed 1 staged upload(s)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("serve startup did not report %q\nstdout was:\n%s", want, out.String())
		}
	}

	var status string
	if err := r.srv.DB.QueryRow(`SELECT status FROM jobs WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("job row after the sweep: %v", err)
	}
	if status != "failed" {
		t.Errorf("swept job status %q, want failed", status)
	}
	for _, gone := range []string{
		filepath.Join(ws.TempDir(), "job-scratch.tmp"),
		filepath.Join(imports, ".upload-1234"),
	} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s survived the startup sweep", filepath.Base(gone))
		}
	}
	if _, err := os.Stat(filepath.Join(imports, "landed.mp4")); err != nil {
		t.Errorf("a landed upload was deleted by the sweep: %v", err)
	}
	// The sweep must not have taken the log directory with it.
	requireLogged(t, filepath.Join(root, "logs", "serve.log"), "server started")
}

// TestShutdownServeStopsServing: the drain is what makes a restart safe, so it
// has to be shown on both sides — answering before, refusing afterwards.
func TestShutdownServeStopsServing(t *testing.T) {
	a, out, _ := serveTestApp(t)
	r, err := startServeCore(a, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startServeCore: %v", err)
	}
	defer r.close()
	addr := r.ln.Addr().String()

	resp, err := http.Get("http://" + addr + "/api/v1/health")
	if err != nil {
		t.Fatalf("health before shutdown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status %d, want 200 before the drain", resp.StatusCode)
	}

	if err := shutdownServe(a, r); err != nil {
		t.Fatalf("shutdownServe: %v", err)
	}
	conn, derr := net.Dial("tcp", addr)
	if derr == nil {
		conn.Close()
		t.Errorf("the port still accepts after shutdownServe")
	}
	if !strings.Contains(out.String(), "server stopped") {
		t.Errorf("shutdown said nothing about stopping\nstdout:\n%s", out.String())
	}
}

// TestServeCommandsHoldTheWriterLock pins the premise the sweeps above argue
// from: serve may delete temp/ and staging debris because the workspace writer
// lock proves every earlier writer is dead. Take the lock away and the same
// code destroys a live process's scratch.
func TestServeCommandsHoldTheWriterLock(t *testing.T) {
	for _, name := range []string{"serve", "client"} {
		if !writesWorkspace(name, nil) {
			t.Errorf("%q is not a writer command: its startup sweep deletes files another process may still own", name)
		}
	}
}

func writeAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
