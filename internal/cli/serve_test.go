package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
)

func defaultTestCfg() *config.Config {
	cfg := config.Default()
	_ = config.Resolve(cfg)
	return cfg
}

func TestIsLoopbackAddr(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8619": true,
		"127.1.2.3:80":   true,
		"localhost:80":   true,
		"[::1]:8619":     true,
		"0.0.0.0:8619":   false,
		"192.168.1.5:80": false,
		"example.com:80": false,
		"[fe80::1]:80":   false,
		"no-port":        false,
		"":               false,
	}
	for addr, want := range cases {
		if got := isLoopbackAddr(addr); got != want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestServeRefusesRemoteWithoutAuth(t *testing.T) {
	// serve must refuse any non-loopback bind that is not backed by explicit
	// opt-in AND a bearer token: remote exposure without authentication would
	// be a vulnerability, not a feature (SECURITY.md, D12).
	a := &App{Cfg: defaultTestCfg()}
	if err := cmdServe(a, []string{"--addr", "0.0.0.0:8619"}); err == nil {
		t.Fatal("remote bind accepted")
	}
	a.Cfg.Server.ListenRemote = true
	a.Cfg.Server.Listen = "0.0.0.0:8619"
	if err := cmdServe(a, nil); err == nil {
		t.Fatal("listen_remote accepted without auth")
	}
	// A strong token alone is not consent to bind off-box, and a weak one is
	// not authentication.
	a.Cfg.Server.ListenRemote = false
	a.Cfg.Server.AuthToken = strings.Repeat("x", 32) // fixture, not a credential
	if _, err := serveAddr(a, []string{"--addr", "0.0.0.0:8619"}); err == nil {
		t.Fatal("remote bind accepted without listen_remote")
	}
	a.Cfg.Server.ListenRemote = true
	a.Cfg.Server.AuthToken = "short"
	if _, err := serveAddr(a, nil); err == nil {
		t.Fatal("remote bind accepted with a token below the minimum length")
	}
}

func TestServeAddrAllowsRemoteWithAuth(t *testing.T) {
	a := &App{Cfg: defaultTestCfg()}
	a.Cfg.Server.ListenRemote = true
	a.Cfg.Server.AuthToken = strings.Repeat("x", 32)
	a.Cfg.Server.Listen = "0.0.0.0:8619"
	addr, err := serveAddr(a, nil)
	if err != nil {
		t.Fatalf("configured remote bind refused: %v", err)
	}
	if addr != "0.0.0.0:8619" {
		t.Fatalf("addr %q, want the configured 0.0.0.0:8619", addr)
	}
	// Loopback keeps working with a token configured, and keeps its
	// friction-free local clients (the desktop client must not need one).
	a.Cfg.Server.Listen = "127.0.0.1:8619"
	if addr, err := serveAddr(a, nil); err != nil || addr != "127.0.0.1:8619" {
		t.Fatalf("loopback bind with auth configured: %q %v", addr, err)
	}
}

// TestNewHTTPServerTimeouts pins the full timeout set: ReadHeaderTimeout was
// the only one for a long time, which left stalled response readers (media
// downloads) pinning handler goroutines forever.
func TestNewHTTPServerTimeouts(t *testing.T) {
	s := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if s.ReadHeaderTimeout <= 0 || s.ReadTimeout <= 0 || s.WriteTimeout <= 0 || s.IdleTimeout <= 0 {
		t.Fatalf("server timeouts not fully set: header=%v read=%v write=%v idle=%v",
			s.ReadHeaderTimeout, s.ReadTimeout, s.WriteTimeout, s.IdleTimeout)
	}
}
