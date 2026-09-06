package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/xiabee/XCut/internal/api"
	"github.com/xiabee/XCut/internal/version"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("serve", "run the local HTTP API (serve [--addr host:port])", cmdServe)
}

// cmdServe runs the localhost API. Remote listening is refused outright:
// authentication does not exist yet, so LAN exposure is a vulnerability, not
// a feature (DECISIONS D8).
func cmdServe(a *App, args []string) error {
	addr := ""
	pos, err := parseCommandArgs(args, map[string]*string{"addr": &addr})
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut serve [--addr host:port]", nil)
	}
	if addr == "" {
		addr = a.Cfg.Server.Listen
	}
	if a.Cfg.Server.ListenRemote || !isLoopbackAddr(addr) {
		return xcerr.E(xcerr.CodeValidation,
			"remote listening is not available yet (no authentication); "+
				"set server.listen to a 127.0.0.1 address", nil)
	}

	db, err := a.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()

	srv := &api.Server{DB: db, Pipe: a.Pipeline(db)}
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No write/idle timeouts yet: long requests (future render triggers)
		// will need explicit budgets; revisit with the job-running endpoints.
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot bind "+addr, err)
	}
	fmt.Fprintf(a.Stdout, "xcut serving http://%s (loopback only, ctrl+c to stop)\n", addr)
	a.Log.Info("server started", "addr", addr, "version", version.Version)

	// Graceful shutdown on signal.
	errCh := make(chan error, 1)
	go func() {
		if serr := httpServer.Serve(ln); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			errCh <- serr
		}
	}()

	select {
	case err := <-errCh:
		return xcerr.E(xcerr.CodeInternal, "server error", err)
	case <-a.Ctx.Done():
		a.Log.Info("server shutting down")
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shCtx)
		srv.Shutdown() // wait for in-flight async jobs
		fmt.Fprintln(a.Stdout, "server stopped")
		return nil
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
