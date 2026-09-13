//go:build !windows

package cli

import (
	"fmt"

	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("client", "open the desktop editing client (native window over the local server)",
		usageSyntax("xcut client [--addr host:port]"), cmdClient)
}

// cmdClient on non-Windows builds: the WebView2 shell is Windows-only, so
// the command falls back to serve + system browser (the embedded UI is the
// same). A native GTK/WebKit shell is a possible future addition.
func cmdClient(a *App, args []string) error {
	addr, err := serveAddr(a, args)
	if err != nil {
		return err
	}
	r, err := startServeCore(a, addr)
	if err != nil {
		return err
	}
	defer r.close()
	url := "http://" + r.ln.Addr().String()
	fmt.Fprintf(a.Stdout, "the native client shell is Windows-only; serving the same UI at %s — open it in your browser\n", url)
	select {
	case err := <-r.errCh:
		return xcerr.E(xcerr.CodeInternal, "server error", err)
	case <-a.Ctx.Done():
		return shutdownServe(a, r)
	}
}

// clientShellInfo describes the native shell availability for doctor
// (non-Windows builds have no WebView2 shell; the browser serves the UI).
func clientShellInfo() (string, string) {
	return "", "native shell is Windows-only — the same UI is served to browsers by `xcut serve` / `xcut client`"
}
