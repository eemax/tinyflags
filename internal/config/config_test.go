package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eemax/tinyflags/internal/config"
	cerr "github.com/eemax/tinyflags/internal/errors"
)

func TestLoadAppliesEnvOverFileAndKeepsZeroValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
version = 1
default_mode = "text"
default_model = "openai/gpt-4.1"
max_tool_retries = 0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TINYFLAGS_DEFAULT_MODE", "tool")
	t.Setenv("TINYFLAGS_DEFAULT_MODEL", "openai/gpt-4o-mini")

	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DefaultMode != "tool" {
		t.Fatalf("DefaultMode = %q, want %q", cfg.DefaultMode, "tool")
	}
	if cfg.DefaultModel != "openai/gpt-4o-mini" {
		t.Fatalf("DefaultModel = %q, want %q", cfg.DefaultModel, "openai/gpt-4o-mini")
	}
	if cfg.MaxToolRetries != 0 {
		t.Fatalf("MaxToolRetries = %d, want 0", cfg.MaxToolRetries)
	}
}

func TestLoadRejectsFutureVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
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
	if mode.Model != "" {
		t.Fatalf("Model = %q", mode.Model)
	}
}

func TestDefaultConfigOmitsBuiltInModelDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.DefaultModel != "" {
		t.Fatalf("DefaultModel = %q", cfg.DefaultModel)
	}
	if len(cfg.Models) != 0 {
		t.Fatalf("Models = %#v", cfg.Models)
	}
	for name, mode := range cfg.Modes {
		if mode.Model != "" {
			t.Fatalf("mode %q model = %q", name, mode.Model)
		}
	}
}

func TestLoadFromAnchorFindsRepoConfigWhenHomeMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "repo")
	nested := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "config.toml")
	if err := os.WriteFile(path, []byte("version = 1\ndefault_mode = \"text\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, loadedPath, err := config.LoadFromAnchor("", nested)
	if err != nil {
		t.Fatalf("LoadFromAnchor returned error: %v", err)
	}
	if cfg.DefaultMode != "text" {
		t.Fatalf("DefaultMode = %q", cfg.DefaultMode)
	}
	if loadedPath != path {
		t.Fatalf("path = %q, want %q", loadedPath, path)
	}
}

func TestLoadFromAnchorMergesRepoAndHomeModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".tinyflags"), 0o755); err != nil {
		t.Fatal(err)
	}
	homeModels := filepath.Join(home, ".tinyflags", "models.toml")
	if err := os.WriteFile(homeModels, []byte(`
[models.fast]
id = "openai/gpt-4.1"

[models.home]
id = "anthropic/claude-opus-4.5"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := filepath.Join(t.TempDir(), "repo")
	nested := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	repoModels := filepath.Join(repo, "models.toml")
	if err := os.WriteFile(repoModels, []byte(`
[models.fast]
id = "openai/gpt-4o-mini"

[models.repo]
id = "google/gemini-2.5-pro"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := config.LoadFromAnchor("", nested)
	if err != nil {
		t.Fatalf("LoadFromAnchor returned error: %v", err)
	}
	if cfg.Models["fast"] != "openai/gpt-4.1" {
		t.Fatalf("fast = %q", cfg.Models["fast"])
	}
	if cfg.Models["home"] != "anthropic/claude-opus-4.5" {
		t.Fatalf("home = %q", cfg.Models["home"])
	}
	if cfg.Models["repo"] != "google/gemini-2.5-pro" {
		t.Fatalf("repo = %q", cfg.Models["repo"])
	}
}

func TestLoadFromAnchorUsesLoadedConfigPathForModelDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".tinyflags"), 0o755); err != nil {
		t.Fatal(err)
	}
	homeConfig := filepath.Join(home, ".tinyflags", "config.toml")
	if err := os.WriteFile(homeConfig, []byte("version = 1\ndefault_mode = \"text\"\ndefault_model = \"fast\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	homeModels := filepath.Join(home, ".tinyflags", "models.toml")
	if err := os.WriteFile(homeModels, []byte(`
[models.fast]
id = "openai/gpt-4.1"

[models.home]
id = "anthropic/claude-opus-4.5"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := filepath.Join(t.TempDir(), "repo")
	nested := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "models.toml"), []byte(`
[models.fast]
id = "openai/gpt-4o-mini"

[models.repo]
id = "google/gemini-2.5-pro"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, loadedPath, err := config.LoadFromAnchor("", nested)
	if err != nil {
		t.Fatalf("LoadFromAnchor returned error: %v", err)
	}
	if loadedPath != homeConfig {
		t.Fatalf("loadedPath = %q, want %q", loadedPath, homeConfig)
	}
	if cfg.Models["fast"] != "openai/gpt-4.1" {
		t.Fatalf("fast = %q", cfg.Models["fast"])
	}
	if cfg.Models["home"] != "anthropic/claude-opus-4.5" {
		t.Fatalf("home = %q", cfg.Models["home"])
	}
	if _, ok := cfg.Models["repo"]; ok {
		t.Fatalf("repo alias leaked into loaded config models: %#v", cfg.Models)
	}
}

func TestLoadFromAnchorUsesExplicitConfigPathForModelDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(repo, "config.toml")
	if err := os.WriteFile(configPath, []byte("version = 1\ndefault_mode = \"text\"\ndefault_model = \"repofast\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "models.toml"), []byte("[models.repofast]\nid = \"openai/gpt-4o-mini\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, loadedPath, err := config.LoadFromAnchor(configPath, outside)
	if err != nil {
		t.Fatalf("LoadFromAnchor returned error: %v", err)
	}
	if loadedPath != configPath {
		t.Fatalf("path = %q, want %q", loadedPath, configPath)
	}
	if cfg.DefaultModel != "repofast" {
		t.Fatalf("DefaultModel = %q", cfg.DefaultModel)
	}
	if cfg.Models["repofast"] != "openai/gpt-4o-mini" {
		t.Fatalf("repofast = %q", cfg.Models["repofast"])
	}
}

func TestExpandPathHandlesTildeForms(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"empty", "", "", false},
		{"tilde only", "~", home, false},
		{"tilde slash", "~/foo", filepath.Join(home, "foo"), false},
		{"absolute", "/usr/bin", "/usr/bin", false},
		{"relative", "rel/path", "rel/path", false},
		{"tilde username passthrough", "~other/foo", "~other/foo", false},
		{"spaces only", "   ", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.ExpandPath(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExpandPath(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoadFromAnchorRejectsModelEntriesWithoutID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "models.toml"), []byte("[models.fast]\nname = \"missing id\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := config.LoadFromAnchor("", repo)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() == "" {
		t.Fatal("expected non-empty error")
	}
}
