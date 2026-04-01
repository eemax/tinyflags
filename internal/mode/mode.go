package mode

import (
	"fmt"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
)

func Resolve(cfg core.Config, req core.RuntimeRequest) (core.ResolvedMode, error) {
	name := req.ModeName
	if name == "" {
		name = cfg.DefaultMode
	}
	modeCfg, ok := cfg.Modes[name]
	if !ok {
		return core.ResolvedMode{}, cerr.New(cerr.ExitCLIUsage, fmt.Sprintf("mode %q not found", name))
	}

	modelName, err := resolveModel(cfg, firstNonEmpty(req.ModelOverride, modeCfg.Model, cfg.DefaultModel))
	if err != nil {
		return core.ResolvedMode{}, err
	}

	format := firstNonEmpty(req.Format, modeCfg.Format, cfg.DefaultFormat)
	if !supportedFormat(format) {
		return core.ResolvedMode{}, cerr.New(cerr.ExitRuntime, fmt.Sprintf("unsupported format %q in resolved mode (expected text or json)", format))
	}
	maxSteps := cfg.MaxSteps
	if modeCfg.MaxSteps > 0 {
		maxSteps = modeCfg.MaxSteps
	}
	if req.MaxSteps >= 0 {
		maxSteps = req.MaxSteps
	}

	maxToolRetries := cfg.MaxToolRetries
	if modeCfg.MaxToolRetries != 0 || modeCfg.Tools != nil {
		maxToolRetries = modeCfg.MaxToolRetries
	}
	if req.MaxToolRetries >= 0 {
		maxToolRetries = req.MaxToolRetries
	}

	timeout := cfg.Timeout
	if modeCfg.Timeout > 0 {
		timeout = modeCfg.Timeout
	}
	if req.Timeout > 0 {
		timeout = req.Timeout
	}

	return core.ResolvedMode{
		Name:            name,
		Description:     modeCfg.Description,
		Model:           modelName,
		Format:          format,
		SystemPrompt:    modeCfg.System,
		Tools:           append([]string(nil), modeCfg.Tools...),
		PersistSession:  modeCfg.PersistSession,
		StoreRunLog:     modeCfg.StoreRunLog,
		CaptureCommands: modeCfg.CaptureCommands,
		CaptureStdout:   modeCfg.CaptureStdout,
		CaptureStderr:   modeCfg.CaptureStderr,
		MaxSteps:        maxSteps,
		MaxToolRetries:  maxToolRetries,
		Timeout:         timeout,
	}, nil
}

func resolveModel(cfg core.Config, name string) (string, error) {
	if name == "" {
		return "", cerr.New(cerr.ExitCLIUsage, "model is required")
	}
	if resolved, ok := cfg.Models[name]; ok {
		return resolved, nil
	}
	return name, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func supportedFormat(format string) bool {
	return format == "text" || format == "json"
}
