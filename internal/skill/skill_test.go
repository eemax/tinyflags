package skill_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eemax/tinyflags/internal/config"
	"github.com/eemax/tinyflags/internal/skill"
)

func TestLoadPrefersProjectLocalThenGlobalThenInline(t *testing.T) {
	projectDir := t.TempDir()
	globalDir := t.TempDir()
	projectSkillDir := filepath.Join(projectDir, ".tinyflags", "skills")
	if err := os.MkdirAll(projectSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectSkillDir, "review.md"), []byte("project"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "review.md"), []byte("global"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.SkillsDir = globalDir
	cfg.Skills["review"] = "inline"

	content, info, err := skill.Load("review", projectDir, cfg)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if content != "project" {
		t.Fatalf("content = %q, want %q", content, "project")
	}
	if info.Source != "project-local" {
		t.Fatalf("source = %q", info.Source)
	}
}

func TestLoadReturnsErrorForEmptyName(t *testing.T) {
	cfg := config.DefaultConfig()
	_, _, err := skill.Load("", t.TempDir(), cfg)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestLoadReturnsErrorForMissingSkill(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.SkillsDir = t.TempDir()
	_, _, err := skill.Load("nonexistent", t.TempDir(), cfg)
	if err == nil {
		t.Fatal("expected error for missing skill")
	}
}

func TestListReturnsAllSources(t *testing.T) {
	projectDir := t.TempDir()
	globalDir := t.TempDir()
	projectSkillDir := filepath.Join(projectDir, ".tinyflags", "skills")
	if err := os.MkdirAll(projectSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectSkillDir, "local.md"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "global.md"), []byte("global"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.SkillsDir = globalDir
	cfg.Skills["inline"] = "inline content"

	items, err := skill.List(projectDir, cfg)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	names := map[string]bool{}
	for _, item := range items {
		names[item.Name] = true
	}
	for _, want := range []string{"local", "global", "inline", "commit"} {
		if !names[want] {
			t.Fatalf("missing skill %q in list: %v", want, items)
		}
	}
}
