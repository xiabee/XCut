package cli

import (
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
