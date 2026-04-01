package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eemax/tinyflags/internal/config"
	cerr "github.com/eemax/tinyflags/internal/errors"
)

func TestLoadAppliesEnvOverFileAndKeepsZeroValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
version = 1
default_mode = "text"
max_tool_retries = 0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TINYFLAGS_DEFAULT_MODE", "tool")

	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DefaultMode != "tool" {
		t.Fatalf("DefaultMode = %q, want %q", cfg.DefaultMode, "tool")
	}
	if cfg.MaxToolRetries != 0 {
		t.Fatalf("MaxToolRetries = %d, want 0", cfg.MaxToolRetries)
	}
}

func TestLoadRejectsFutureVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	exitErr, ok := err.(*cerr.ExitCodeError)
	if !ok {
		t.Fatalf("error type = %T, want *ExitCodeError", err)
	}
	if exitErr.Code != cerr.ExitRuntime {
		t.Fatalf("exit code = %d, want %d", exitErr.Code, cerr.ExitRuntime)
	}
}

func TestLoadMergesPartialModeOverridesWithDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
version = 1
[modes.commander]
system = "custom only"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	mode := cfg.Modes["commander"]
	if mode.System != "custom only" {
		t.Fatalf("System = %q", mode.System)
	}
	if len(mode.Tools) != 1 || mode.Tools[0] != "bash" {
		t.Fatalf("Tools = %#v", mode.Tools)
	}
	if !mode.PersistSession || !mode.StoreRunLog {
		t.Fatalf("mode persistence/logging flags were not preserved: %+v", mode)
	}
	if mode.Model != "ops" {
		t.Fatalf("Model = %q", mode.Model)
	}
}
