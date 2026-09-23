package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/version"
)

// `xcut config show` is the output an operator pastes into a bug report, and
// `xcut init` is the command that decides what lands in a 0644 config.json. Both
// carry a secret-handling claim in docs/SECURITY.md, and neither claim had ever
// been checked by running the command that makes it — the redaction call site and
// the token-blanking line were both at 0 % coverage.
//
// Everything here goes through Run, with --config and --workspace pinned to a
// throwaway directory: the machine running the test may own a real ~/.xcut, and a
// secret-handling assertion that quietly reads it proves nothing about either.

// fixtureToken is a bearer token in shape and nothing else. It is built at
// runtime so no scanner (or person) meets a credential in this file.
func fixtureToken() string { return strings.Repeat("qt", 16) } // 32 chars, MinAuthTokenLen x2

func writeConfig(t *testing.T, dir string, body map[string]any) string {
	t.Helper()
	b, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// shownToken reads back the field the way a consumer would: the encoder escapes
// "<set>" as \u003cset\u003e, so the marker is only ever visible once parsed — a
// substring check on the raw bytes would fail on a mask that works.
func shownToken(t *testing.T, out string) string {
	t.Helper()
	var shown map[string]any
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("the output is no longer parseable config: %v\n%s", err, out)
	}
	server, _ := shown["server"].(map[string]any)
	if server == nil {
		t.Fatalf("the shown config lost its server section:\n%s", out)
	}
	got, _ := server["auth_token"].(string)
	return got
}

func TestConfigShowMasksTheTokenItWasGiven(t *testing.T) {
	ws := t.TempDir()
	tok := fixtureToken()
	cfgPath := writeConfig(t, ws, map[string]any{
		"workspace": ws,
		"server":    map[string]any{"listen": "127.0.0.1:8619", "auth_token": tok},
	})

	var stdout, stderr strings.Builder
	if code := Run([]string{"--config", cfgPath, "--workspace", ws, "config", "show"}, &stdout, &stderr); code != 0 {
		t.Fatalf("config show: exit %d\n%s", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, tok) {
		t.Errorf("config show printed the bearer token it was handed\n%s", out)
	}
	if got := shownToken(t, out); got != "<set>" {
		t.Errorf("auth_token was shown as %q; an operator has to be able to tell a masked token from an absent one", got)
	}
	// The output must still be the config: masking one field is not omitting them.
	var shown map[string]any
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("the masked output is no longer parseable config: %v", err)
	}
	if got := shown["server"].(map[string]any)["listen"]; got != "127.0.0.1:8619" {
		t.Errorf("server.listen came back as %v; the rest of the config must survive the redaction", got)
	}
}

// The environment is the other way a token arrives, and the one an operator is
// most likely to use on a server box — the file on disk then says nothing.
func TestConfigShowMasksAnEnvToken(t *testing.T) {
	ws := t.TempDir()
	tok := fixtureToken()
	cfgPath := writeConfig(t, ws, map[string]any{"workspace": ws})
	t.Setenv("XCUT_AUTH_TOKEN", tok)

	var stdout, stderr strings.Builder
	if code := Run([]string{"--config", cfgPath, "--workspace", ws, "config", "show"}, &stdout, &stderr); code != 0 {
		t.Fatalf("config show: exit %d\n%s", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, tok) {
		t.Errorf("the XCUT_AUTH_TOKEN value reached the output\n%s", out)
	}
	if got := shownToken(t, out); got != "<set>" {
		t.Errorf("an env-supplied token was reported as %q, want the mask", got)
	}
}

// TestInitDoesNotBakeAnEnvTokenIntoConfigFile: `xcut init` writes a starting
// config.json at 0644. Blanking the token is one line, and it is the line that
// keeps a token that was only ever in the environment from becoming a file every
// local user can read.
func TestInitDoesNotBakeAnEnvTokenIntoConfigFile(t *testing.T) {
	ws := t.TempDir()
	// A bootstrap config in its own directory: without one, init would read this
	// machine's real ~/.xcut, and the assertion would be about the operator's
	// settings rather than about what init writes.
	bootstrap := writeConfig(t, t.TempDir(), map[string]any{"workspace": ws})
	tok := fixtureToken()
	t.Setenv("XCUT_AUTH_TOKEN", tok)

	var stdout, stderr strings.Builder
	if code := Run([]string{"--config", bootstrap, "--workspace", ws, "init"}, &stdout, &stderr); code != 0 {
		t.Fatalf("init: exit %d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	written, err := os.ReadFile(filepath.Join(ws, "config.json"))
	if err != nil {
		t.Fatalf("init wrote no config file: %v", err)
	}
	if strings.Contains(string(written), tok) {
		t.Errorf("init baked the environment's bearer token into a 0644 config.json:\n%s", written)
	}
	var doc map[string]any
	if err := json.Unmarshal(written, &doc); err != nil {
		t.Fatalf("the written config is not parseable: %v\n%s", err, written)
	}
	// The field ships with omitempty, so "not written" and "written empty" are the
	// same fact here: no token in the file. Anything else is a leak.
	if server, ok := doc["server"].(map[string]any); ok {
		if s, isString := server["auth_token"].(string); isString && s != "" {
			t.Errorf("auth_token in the written config is %q, want it absent or empty", s)
		}
	}
}

// `xcut config path` is how an operator finds which of the two layers they are
// editing; `xcut config` with anything else is the usage line, not a panic —
// the argument check reads args[0] before looking at the length.
func TestConfigPathAndArgumentRefusals(t *testing.T) {
	ws := t.TempDir()
	cfgPath := writeConfig(t, ws, map[string]any{"workspace": ws})

	var stdout, stderr strings.Builder
	if code := Run([]string{"--config", cfgPath, "--workspace", ws, "config", "path"}, &stdout, &stderr); code != 0 {
		t.Fatalf("config path: exit %d\n%s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != cfgPath {
		t.Errorf("config path printed %q, want the file that was passed in (%s)", got, cfgPath)
	}

	for _, args := range [][]string{
		{"config"},
		{"config", "show", "extra"},
		{"config", "sideways"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(append([]string{"--config", cfgPath, "--workspace", ws}, args...), &stdout, &stderr); code != exitUsage && code != exitFailure {
			t.Fatalf("%v exited %d; a refusal has to be a nonzero one", args, code)
		}
		if !strings.Contains(stderr.String(), "xcut config show|path") {
			t.Errorf("%v did not print the usage line: %q", args, stderr.String())
		}
		if stdout.String() != "" {
			t.Errorf("%v wrote a config to stdout while refusing: %q", args, stdout.String())
		}
	}
}

// The version line is what a bug report starts with, and what the release
// artifacts stamp through -ldflags. Its shape is the contract.
func TestVersionLineSaysWhatTheBinaryKnows(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := Run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version: exit %d\n%s", code, stderr.String())
	}
	line := strings.TrimSpace(stdout.String())
	if line != version.String() {
		t.Errorf("xcut version printed %q, want the package's own banner %q", line, version.String())
	}
	for _, want := range []string{"xcut ", version.Version, runtime.GOOS + "/" + runtime.GOARCH} {
		if !strings.Contains(line, want) {
			t.Errorf("the version line lost %q: %s", want, line)
		}
	}
}

// `xcut usage` is the answer to every question of the form "how do I call it",
// printed by two paths (an unknown command, and -h). It is built from the command
// registry, so the page has to carry every registered command — the docs test
// checks docs/USAGE.md against that registry; this one checks the binary's own
// page, which no test had rendered.
func TestUsagePageListsEveryRegisteredCommand(t *testing.T) {
	if len(commands) < 10 {
		t.Fatalf("the registry holds %d commands; init() did not run, so this check is empty", len(commands))
	}
	var stdout, stderr strings.Builder
	if code := Run([]string{"definitely-not-a-command"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("an unknown command exited %d, want %d", code, exitUsage)
	}
	page := stderr.String()
	if !strings.Contains(page, "Usage: xcut") {
		t.Fatalf("the unknown-command page carries no usage line:\n%s", page)
	}
	var missing []string
	for _, c := range commands {
		if !strings.Contains(page, c.name) || !strings.Contains(page, c.summary) {
			missing = append(missing, c.name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the usage page omits registered commands: %v", missing)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"-h"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("-h exited %d, want the usage page and 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage: xcut") {
		t.Errorf("-h printed no usage page:\n%s", stdout.String())
	}
}
