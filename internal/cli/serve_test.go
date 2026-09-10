package cli

import (
	"net/http"
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
	// serve must refuse any non-loopback bind: authentication does not exist
	// yet, so remote exposure would be a vulnerability, not a feature.
	a := &App{Cfg: defaultTestCfg()}
	if err := cmdServe(a, []string{"--addr", "0.0.0.0:8619"}); err == nil {
		t.Fatal("remote bind accepted")
	}
	a.Cfg.Server.ListenRemote = true
	a.Cfg.Server.Listen = "0.0.0.0:8619"
	if err := cmdServe(a, nil); err == nil {
		t.Fatal("listen_remote accepted without auth")
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
