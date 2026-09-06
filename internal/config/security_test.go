package config

import (
	"os"
	"path/filepath"
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
