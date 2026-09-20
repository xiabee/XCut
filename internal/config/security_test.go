package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOversizedConfigRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	big := make([]byte, maxConfigBytes+1)
	if err := os.WriteFile(p, big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("oversized config accepted")
	}
}

func TestLoadJunkConfigRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"workspace": [1,2,3], "resource": {"max_concurrent_jobs": {"a":1}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("type-confused config accepted")
	}
}

// A remote bind is only reachable by an operator who both opts in and supplies
// something to authenticate the peers that opt-in then accepts. Every hole in
// that pair has to stay closed (D12).
//
// The token fixtures are built at runtime: a high-entropy literal assigned to a
// token field is exactly what the repo's secret scanner is there to refuse.
func TestRemoteBindRequiresAuthToken(t *testing.T) {
	long := strings.Repeat("x", 32)
	spaced := strings.Repeat("x", 16) + " " + strings.Repeat("y", 15)
	cases := []struct {
		name     string
		remote   bool
		token    string
		listen   string
		wantFail bool
		wantAddr string
	}{
		{"remote without token", true, "", "0.0.0.0:8619", true, ""},
		{"remote with an empty-but-padded token", true, "   ", "0.0.0.0:8619", true, ""},
		{"remote with a short token", true, "guessable", "0.0.0.0:8619", true, ""},
		{"remote with a token containing whitespace", true, spaced, "0.0.0.0:8619", true, ""},
		{"remote with a valid token", true, long, "0.0.0.0:8619", false, "0.0.0.0:8619"},
		{"loopback keeps its no-token default", false, "", "127.0.0.1:8619", false, "127.0.0.1:8619"},
		// The token is not the opt-in: a configured token must never be read as
		// consent to listen off-box.
		{"token alone does not lift the loopback pin", false, long, "0.0.0.0:8619", false, "127.0.0.1:8619"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Default()
			cfg.Server.ListenRemote = c.remote
			cfg.Server.AuthToken = c.token
			cfg.Server.Listen = c.listen
			err := Resolve(cfg)
			if c.wantFail {
				if err == nil {
					t.Fatalf("accepted remote bind with token %q", c.token)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if cfg.Server.Listen != c.wantAddr {
				t.Fatalf("listen = %q, want %q", cfg.Server.Listen, c.wantAddr)
			}
		})
	}
}

func TestAuthTokenFromEnv(t *testing.T) {
	long := strings.Repeat("x", 32)
	t.Setenv("XCUT_AUTH_TOKEN", "  "+long+"  ")
	cfg := Default()
	Env(cfg)
	cfg.Server.ListenRemote = true
	if err := Resolve(cfg); err != nil {
		t.Fatalf("env token refused: %v", err)
	}
	if cfg.Server.AuthToken != long {
		t.Fatalf("token not trimmed: %q", cfg.Server.AuthToken)
	}
}

// `xcut config show` is the paste-into-a-bug-report output, so the resolved
// token must never appear in it.
func TestRedactedMasksAuthToken(t *testing.T) {
	long := strings.Repeat("x", 32)
	cfg := Default()
	cfg.Server.AuthToken = long
	b, err := json.MarshalIndent(cfg.Redacted(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), long) {
		t.Fatalf("redacted config leaked the token: %s", b)
	}
	if cfg.Server.AuthToken == "" {
		t.Fatal("Redacted mutated the source config")
	}
	// An unset token stays absent, so a default config prints unchanged.
	plain, _ := json.Marshal(Default().Redacted())
	if strings.Contains(string(plain), "auth_token") {
		t.Fatalf("empty token still serialized: %s", plain)
	}
}
