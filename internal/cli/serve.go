package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/xiabee/XCut/internal/api"
	"github.com/xiabee/XCut/internal/version"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("serve", "run the local HTTP API", usageSyntax("xcut serve [--addr host:port]"), cmdServe)
}

// runningServe is a started server: the listener is live and the pipeline
// is swept. Callers own the shutdown sequence (shutdownServe) and close.
type runningServe struct {
	srv        *api.Server
	httpServer *http.Server
	ln         net.Listener
	// errCh receives a fatal serve error (anything but http.ErrServerClosed).
	errCh chan error
	close func() // releases the DB handle
}

// serveAddr validates and resolves the listen address for serve/client.
func serveAddr(a *App, args []string) (string, error) {
	addr := ""
	pos, err := parseCommandArgs(args, map[string]*string{"addr": &addr})
	if err != nil {
		return "", err
	}
	if len(pos) != 0 {
		return "", xcerr.E(xcerr.CodeValidation, "usage: xcut serve [--addr host:port]", nil)
	}
	if addr == "" {
		addr = a.Cfg.Server.Listen
	}
	if a.Cfg.Server.ListenRemote || !isLoopbackAddr(addr) {
		return "", xcerr.E(xcerr.CodeValidation,
			"remote listening is not available yet (no authentication); "+
				"set server.listen to a 127.0.0.1 address", nil)
	}
	return addr, nil
}

// startServeCore is the serve pipeline shared by `xcut serve` and the
// `xcut client` shell: open the DB, take over logging, sweep orphans and
// temp debris, bind loopback and start serving. The caller owns shutdown:
// shutdownServe for the drain, then r.close for the DB handle.
func startServeCore(a *App, addr string) (*runningServe, error) {
	db, err := a.OpenDB()
	if err != nil {
		return nil, err
	}

	// Serve mode logs to stderr AND a rotated file in the workspace so a
	// long-running instance stays diagnosable without unbounded log growth.
	slogSvc, closeLog := newServeLogger(a, a.Cfg.Workspace)
	defer closeLog()
	a.Log = slogSvc

	srv := &api.Server{DB: db, Pipe: a.Pipeline(db)}
	// Serve owns every timeline document write in this process: PUTs,
	// backup restores and regeneration writes must share one mutex or a
	// PUT racing a regeneration could collide revisions with it.
	srv.Pipe.TimelineWriteLock = &srv.TimelineMu

	// The workspace writer lock is held for serve's lifetime, so every
	// queued/running row visible at startup was left by a dead process.
	// Sweep them all now: OpenDB's age-gated sweep alone would leave a fresh
	// orphan "running" until stale_running_after passes, wedging its project
	// behind duplicate-job 409s and the delete guard with no way to clear it.
	if n, err := srv.Pipe.Queue.ReconcileOrphans(a.Ctx, 0); err != nil {
		a.Log.Warn("startup job sweep failed", "err", err)
	} else if n > 0 {
		fmt.Fprintf(a.Stdout, "reconciled %d orphaned job(s) left by a previous run\n", n)
	}

	// Same safety argument as the job sweep: with the writer lock held, all
	// previous writers are provably dead, so every temp/ entry is crash or
	// cancellation debris. Sweep it before listening — otherwise debris sits
	// against the temp budget and the only remover (xcut cleanup) is
	// lock-refused while serve runs.
	if removed, bytes, err := a.Workspace().CleanupTemp(false); err != nil {
		a.Log.Warn("startup temp sweep failed", "err", err)
	} else if len(removed) > 0 {
		a.Log.Info("swept temp debris at startup", "entries", len(removed), "bytes", bytes)
		fmt.Fprintf(a.Stdout, "reclaimed %d temp entries (%d bytes)\n", len(removed), bytes)
	}

	// Crash debris from interrupted uploads: a .upload-* staging file in
	// imports/ was never renamed into place, so nothing references it. The
	// writer lock proves the uploading serve is dead.
	if staged, err := sweepUploadStaging(a.Workspace()); err != nil {
		a.Log.Warn("startup upload sweep failed", "err", err)
	} else if staged > 0 {
		fmt.Fprintf(a.Stdout, "reclaimed %d staged upload(s)\n", staged)
	}

	httpServer := newHTTPServer(addr, srv.Handler())

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		db.Close()
		return nil, xcerr.E(xcerr.CodeInternal, "cannot bind "+addr, err)
	}
	fmt.Fprintf(a.Stdout, "xcut serving http://%s (loopback only)\n", ln.Addr().String())
	a.Log.Info("server started", "addr", ln.Addr().String(), "version", version.Version)

	errCh := make(chan error, 1)
	go func() {
		if serr := httpServer.Serve(ln); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			errCh <- serr
		}
	}()

	return &runningServe{
		srv:        srv,
		httpServer: httpServer,
		ln:         ln,
		errCh:      errCh,
		close:      func() { db.Close() },
	}, nil
}

// shutdownServe drains in-flight work gracefully: HTTP connections first
// (bounded), then the job queue (bounded), with a loud note when a wedged
// job is left for the startup sweep to reconcile.
func shutdownServe(a *App, r *runningServe) error {
	a.Log.Info("server shutting down")
	fmt.Fprintln(a.Stdout, "shutting down — in-flight jobs drain before exit")
	shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.httpServer.Shutdown(shCtx)
	// Job contexts derive from the app context, so they were cancelled by
	// the signal and are winding down; give the cleanup a generous bound.
	// A job wedged outside its cancellation must not own the shutdown — the
	// startup sweep reconciles whatever it leaves.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()
	if err := r.srv.Shutdown(drainCtx); err != nil {
		a.Log.Warn("job drain timed out; startup sweep will reconcile", "err", err)
		fmt.Fprintln(a.Stdout, "warning: some jobs did not finish winding down (startup sweep will reconcile them)")
	}
	fmt.Fprintln(a.Stdout, "server stopped")
	return nil
}

// cmdServe runs the localhost API. Remote listening is refused outright:
// authentication does not exist yet, so LAN exposure is a vulnerability, not
// a feature (DECISIONS D8).
func cmdServe(a *App, args []string) error {
	addr, err := serveAddr(a, args)
	if err != nil {
		return err
	}

	r, err := startServeCore(a, addr)
	if err != nil {
		return err
	}
	defer r.close()

	// Graceful shutdown on signal.
	select {
	case err := <-r.errCh:
		return xcerr.E(xcerr.CodeInternal, "server error", err)
	case <-a.Ctx.Done():
		// Graceful drain waits for in-flight jobs; a wedged drain must not
		// own the terminal — the second Ctrl+C is a hard exit.
		forceCh := make(chan os.Signal, 1)
		signal.Notify(forceCh, os.Interrupt)
		defer signal.Stop(forceCh)
		go func() {
			<-forceCh
			fmt.Fprintln(a.Stdout, "forced exit (second signal)")
			os.Exit(130)
		}()
		fmt.Fprintln(a.Stdout, "shutting down — press ctrl+c again to force quit")
		return shutdownServe(a, r)
	}
}

// newHTTPServer builds the loopback API server with its full timeout set.
// WriteTimeout bounds wedged response writers (a stalled reader of a media
// response would otherwise pin the handler goroutine forever); it is generous
// because loopback transfers complete quickly even for large MP4s.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Hostnames: only "localhost" is acceptable; anything else is remote.
		return host == "localhost"
	}
	return ip.IsLoopback()
}
