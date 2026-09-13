//go:build windows

package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("client", "open the desktop editing client (native window over the local server)",
		usageSyntax("xcut client [--addr host:port] [--browser]"), cmdClient)
}

// webviewVersion reports the installed WebView2 runtime version, or "" when
// the runtime is unavailable (used by the client command and doctor).
func webviewVersion() string {
	v, err := webviewloader.GetInstalledVersion()
	if err != nil {
		return ""
	}
	return v
}

// cmdClient opens the desktop editing client: the serve pipeline runs
// in-process (same loopback bind, same workspace lock, same crash
// reconciliation), and a native WebView2 window hosts the embedded UI.
// Closing the window shuts the server down with the same drain as serve.
// `--browser` skips the shell and behaves like `xcut serve` after opening
// the system browser — the only mode on non-Windows builds.
func cmdClient(a *App, args []string) error {
	browser := false
	rest := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--browser" {
			browser = true
		} else {
			rest = append(rest, arg)
		}
	}

	if browser || runtime.GOOS != "windows" {
		addr, err := serveAddr(a, rest)
		if err != nil {
			return err
		}
		r, err := startServeCore(a, addr)
		if err != nil {
			return err
		}
		defer r.close()
		url := "http://" + r.ln.Addr().String()
		fmt.Fprintf(a.Stdout, "opening the client in your browser: %s\n", url)
		openBrowser(url)
		select {
		case err := <-r.errCh:
			return xcerr.E(xcerr.CodeInternal, "server error", err)
		case <-a.Ctx.Done():
			return shutdownServe(a, r)
		}
	}

	if webviewVersion() == "" {
		return xcerr.E(xcerr.CodeNotFound,
			"the WebView2 runtime is not installed — install \"Evergreen WebView2 Runtime\" from Microsoft, or use `xcut client --browser` / `xcut serve`", nil)
	}

	addr, err := serveAddr(a, rest)
	if err != nil {
		return err
	}
	r, err := startServeCore(a, addr)
	if err != nil {
		return err
	}
	defer r.close()
	url := "http://" + r.ln.Addr().String()

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "xcut — local-first auto editing",
			Width:  1500,
			Height: 940,
			Center: true,
		},
	})
	if w == nil {
		return xcerr.E(xcerr.CodeInternal,
			"cannot create the WebView2 window — use `xcut client --browser` or `xcut serve`", nil)
	}
	defer w.Destroy()
	w.SetTitle("xcut — local-first auto editing")
	w.SetSize(1500, 940, webview2.HintNone)
	setWindowIcon(w.Window())
	w.Navigate(url)

	// External exits (Ctrl+C, a fatal serve error) terminate the UI loop;
	// window close is the normal path and falls through to the drain.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-r.errCh:
		case <-a.Ctx.Done():
		case <-sigCh:
		}
		w.Dispatch(func() { w.Terminate() })
	}()

	fmt.Fprintf(a.Stdout, "xcut client ready at %s (close the window to stop)\n", url)
	w.Run()

	fmt.Fprintln(a.Stdout, "window closed — shutting down")
	return shutdownServe(a, r)
}

// openBrowser hands the URL to the OS default browser via rundll32
// (argv-style exec, no shell). Failure is non-fatal: the URL is on stdout.
func openBrowser(url string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "xcut: could not open the browser (%v) — open %s manually\n", err, url)
	}
}

// clientShellInfo describes the native shell availability for doctor.
func clientShellInfo() (string, string) {
	if v := webviewVersion(); v != "" {
		return v, "available — `xcut client` opens the desktop window"
	}
	return "", "WebView2 runtime not installed (use `xcut client --browser` or `xcut serve`, or install Evergreen WebView2 Runtime)"
}
