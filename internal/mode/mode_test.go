package mode_test

import (
	"testing"
	"time"

	"github.com/eemax/tinyflags/internal/config"
	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/mode"
)

func TestResolveExpandsAliasesAndAppliesOverrides(t *testing.T) {
	cfg := config.DefaultConfig()
	req := core.RuntimeRequest{
		ModeName:       "commander",
		ModelOverride:  "fast",
		Format:         "json",
		MaxSteps:       2,
		MaxToolRetries: 0,
		Timeout:        10 * time.Second,
	}

	resolved, err := mode.Resolve(cfg, req)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.Model != "openai/gpt-4o-mini" {
		t.Fatalf("Model = %q", resolved.Model)
	}
	if resolved.Format != "json" {
		t.Fatalf("Format = %q", resolved.Format)
	}
	if resolved.MaxSteps != 2 {
		t.Fatalf("MaxSteps = %d", resolved.MaxSteps)
	}
	if resolved.MaxToolRetries != 0 {
		t.Fatalf("MaxToolRetries = %d", resolved.MaxToolRetries)
	}
	if resolved.Timeout != 10*time.Second {
		t.Fatalf("Timeout = %s", resolved.Timeout)
	}
}

func TestResolveRejectsUnsupportedFormat(t *testing.T) {
	cfg := config.DefaultConfig()
	modeCfg := cfg.Modes["text"]
	modeCfg.Format = "yaml"
	cfg.Modes["text"] = modeCfg

	_, err := mode.Resolve(cfg, core.RuntimeRequest{ModeName: "text"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	exitErr, ok := err.(*cerr.ExitCodeError)
	if !ok || exitErr.Code != cerr.ExitRuntime {
		t.Fatalf("error = %#v", err)
	}
}
