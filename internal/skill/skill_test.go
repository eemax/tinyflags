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
