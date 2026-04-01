package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
)

const (
	envAPIKey              = "TINYFLAGS_API_KEY"
	envBaseURL             = "TINYFLAGS_BASE_URL"
	envDefaultMode         = "TINYFLAGS_DEFAULT_MODE"
	envDefaultModel        = "TINYFLAGS_DEFAULT_MODEL"
	envDefaultFormat       = "TINYFLAGS_DEFAULT_FORMAT"
	envDBPath              = "TINYFLAGS_DB_PATH"
	envSkillsDir           = "TINYFLAGS_SKILLS_DIR"
	envShell               = "TINYFLAGS_SHELL"
	envTimeout             = "TINYFLAGS_TIMEOUT"
	envMaxSteps            = "TINYFLAGS_MAX_STEPS"
	envMaxToolRetries      = "TINYFLAGS_MAX_TOOL_RETRIES"
	envLogLevel            = "TINYFLAGS_LOG_LEVEL"
	envPlanModeInstruction = "TINYFLAGS_PLAN_MODE_INSTRUCTION"
)

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tinyflags", "config.toml"), nil
}

func DefaultConfig() core.Config {
	return core.Config{
		Version:        1,
		BaseURL:        "https://openrouter.ai/api/v1",
		DefaultMode:    "commander",
		DefaultModel:   "openai/gpt-4o-mini",
		DefaultFormat:  "text",
		DBPath:         "~/.tinyflags/tinyflags.db",
		SkillsDir:      "~/.tinyflags/skills",
		Shell:          "/bin/bash",
		ShellArgs:      []string{"-lc"},
		Timeout:        2 * time.Minute,
		MaxSteps:       12,
		MaxToolRetries: 3,
		LogLevel:       "error",
		Models: map[string]string{
			"fast":  "openai/gpt-4o-mini",
			"smart": "anthropic/claude-opus-4-5",
			"ops":   "openai/gpt-4.1",
		},
		Modes: map[string]core.ModeConfig{
			"text": {
				Description:     "Plain text interaction",
				Model:           "fast",
				Format:          "text",
				Tools:           []string{},
				PersistSession:  true,
				StoreRunLog:     true,
				CaptureCommands: false,
				CaptureStdout:   false,
				CaptureStderr:   false,
				MaxSteps:        4,
				MaxToolRetries:  0,
			},
			"tool": {
				Description:     "Bounded tool-enabled mode",
				Model:           "smart",
				Format:          "text",
				Tools:           []string{"read_file", "write_file"},
				PersistSession:  true,
				StoreRunLog:     true,
				CaptureCommands: true,
				CaptureStdout:   true,
				CaptureStderr:   true,
				MaxSteps:        8,
				MaxToolRetries:  3,
			},
			"commander": {
				Description:     "Full shell execution mode",
				Model:           "ops",
				Format:          "text",
				Tools:           []string{"bash"},
				PersistSession:  true,
				StoreRunLog:     true,
				CaptureCommands: true,
				CaptureStdout:   true,
				CaptureStderr:   true,
				MaxSteps:        12,
				MaxToolRetries:  3,
			},
		},
		Skills: map[string]string{
			"commit": "You write concise, conventional commit messages.",
		},
	}
}

func Load(configPath string) (core.Config, string, error) {
	cfg := cloneConfig(DefaultConfig())
	path := configPath
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return core.Config{}, "", cerr.Wrap(cerr.ExitRuntime, "resolve default config path", err)
		}
	}
	path, err := ExpandPath(path)
	if err != nil {
		return core.Config{}, "", cerr.Wrap(cerr.ExitRuntime, "expand config path", err)
	}

	if _, err := os.Stat(path); err == nil {
		fileCfg := core.Config{}
		meta, err := toml.DecodeFile(path, &fileCfg)
		if err != nil {
			return core.Config{}, "", cerr.Wrap(cerr.ExitRuntime, "parse config", err)
		}
		if fileCfg.Version == 0 {
			fileCfg.Version = 1
		}
		if fileCfg.Version != 1 {
			return core.Config{}, "", cerr.New(cerr.ExitRuntime, fmt.Sprintf("config version %d is incompatible with this build, please migrate", fileCfg.Version))
		}
		cfg = mergeConfig(cfg, fileCfg, meta)
	} else if !os.IsNotExist(err) {
		return core.Config{}, "", cerr.Wrap(cerr.ExitRuntime, "read config", err)
	}

	if err := applyEnv(&cfg, os.LookupEnv); err != nil {
		return core.Config{}, "", err
	}

	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version != 1 {
		return core.Config{}, "", cerr.New(cerr.ExitRuntime, fmt.Sprintf("config version %d is incompatible with this build, please migrate", cfg.Version))
	}

	if cfg.DBPath, err = ExpandPath(cfg.DBPath); err != nil {
		return core.Config{}, "", cerr.Wrap(cerr.ExitRuntime, "expand db path", err)
	}
	if cfg.SkillsDir, err = ExpandPath(cfg.SkillsDir); err != nil {
		return core.Config{}, "", cerr.Wrap(cerr.ExitRuntime, "expand skills dir", err)
	}

	return cfg, path, nil
}

func ExpandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}

func applyEnv(cfg *core.Config, lookup func(string) (string, bool)) error {
	if v, ok := lookup(envAPIKey); ok {
		cfg.APIKey = v
	}
	if v, ok := lookup(envBaseURL); ok {
		cfg.BaseURL = v
	}
	if v, ok := lookup(envDefaultMode); ok {
		cfg.DefaultMode = v
	}
	if v, ok := lookup(envDefaultModel); ok {
		cfg.DefaultModel = v
	}
	if v, ok := lookup(envDefaultFormat); ok {
		cfg.DefaultFormat = v
	}
	if v, ok := lookup(envDBPath); ok {
		cfg.DBPath = v
	}
	if v, ok := lookup(envSkillsDir); ok {
		cfg.SkillsDir = v
	}
	if v, ok := lookup(envShell); ok {
		cfg.Shell = v
	}
	if v, ok := lookup(envLogLevel); ok {
		cfg.LogLevel = v
	}
	if v, ok := lookup(envPlanModeInstruction); ok {
		cfg.PlanModeInstruction = v
	}
	if v, ok := lookup(envTimeout); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cerr.Wrap(cerr.ExitRuntime, "parse TINYFLAGS_TIMEOUT", err)
		}
		cfg.Timeout = d
	}
	if v, ok := lookup(envMaxSteps); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cerr.Wrap(cerr.ExitRuntime, "parse TINYFLAGS_MAX_STEPS", err)
		}
		cfg.MaxSteps = n
	}
	if v, ok := lookup(envMaxToolRetries); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cerr.Wrap(cerr.ExitRuntime, "parse TINYFLAGS_MAX_TOOL_RETRIES", err)
		}
		cfg.MaxToolRetries = n
	}
	return nil
}

func mergeConfig(base, override core.Config, meta toml.MetaData) core.Config {
	out := cloneConfig(base)
	if override.Version != 0 {
		out.Version = override.Version
	}
	if override.APIKey != "" {
		out.APIKey = override.APIKey
	}
	if override.BaseURL != "" {
		out.BaseURL = override.BaseURL
	}
	if override.DefaultMode != "" {
		out.DefaultMode = override.DefaultMode
	}
	if override.DefaultModel != "" {
		out.DefaultModel = override.DefaultModel
	}
	if override.DefaultFormat != "" {
		out.DefaultFormat = override.DefaultFormat
	}
	if override.DBPath != "" {
		out.DBPath = override.DBPath
	}
	if override.SkillsDir != "" {
		out.SkillsDir = override.SkillsDir
	}
	if override.Shell != "" {
		out.Shell = override.Shell
	}
	if len(override.ShellArgs) > 0 {
		out.ShellArgs = append([]string(nil), override.ShellArgs...)
	}
	if override.Timeout > 0 {
		out.Timeout = override.Timeout
	}
	if meta.IsDefined("max_steps") {
		out.MaxSteps = override.MaxSteps
	}
	if meta.IsDefined("max_tool_retries") {
		out.MaxToolRetries = override.MaxToolRetries
	}
	if override.LogLevel != "" {
		out.LogLevel = override.LogLevel
	}
	if override.PlanModeInstruction != "" {
		out.PlanModeInstruction = override.PlanModeInstruction
	}
	for key, value := range override.Models {
		out.Models[key] = value
	}
	for key, value := range override.Modes {
		baseMode, ok := out.Modes[key]
		if !ok {
			baseMode = core.ModeConfig{}
		}
		out.Modes[key] = mergeModeConfig(baseMode, value, meta, key)
	}
	for key, value := range override.Skills {
		out.Skills[key] = value
	}
	return out
}

func cloneConfig(in core.Config) core.Config {
	out := in
	out.Models = cloneStringMap(in.Models)
	out.Skills = cloneStringMap(in.Skills)
	out.ShellArgs = append([]string(nil), in.ShellArgs...)
	out.Modes = make(map[string]core.ModeConfig, len(in.Modes))
	for name, mode := range in.Modes {
		mode.Tools = append([]string(nil), mode.Tools...)
		out.Modes[name] = mode
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mergeModeConfig(base, override core.ModeConfig, meta toml.MetaData, name string) core.ModeConfig {
	out := base
	if meta.IsDefined("modes", name, "description") {
		out.Description = override.Description
	}
	if meta.IsDefined("modes", name, "model") {
		out.Model = override.Model
	}
	if meta.IsDefined("modes", name, "format") {
		out.Format = override.Format
	}
	if meta.IsDefined("modes", name, "system") {
		out.System = override.System
	}
	if meta.IsDefined("modes", name, "tools") {
		out.Tools = append([]string(nil), override.Tools...)
	}
	if meta.IsDefined("modes", name, "persist_session") {
		out.PersistSession = override.PersistSession
	}
	if meta.IsDefined("modes", name, "store_run_log") {
		out.StoreRunLog = override.StoreRunLog
	}
	if meta.IsDefined("modes", name, "capture_commands") {
		out.CaptureCommands = override.CaptureCommands
	}
	if meta.IsDefined("modes", name, "capture_stdout") {
		out.CaptureStdout = override.CaptureStdout
	}
	if meta.IsDefined("modes", name, "capture_stderr") {
		out.CaptureStderr = override.CaptureStderr
	}
	if meta.IsDefined("modes", name, "max_steps") {
		out.MaxSteps = override.MaxSteps
	}
	if meta.IsDefined("modes", name, "max_tool_retries") {
		out.MaxToolRetries = override.MaxToolRetries
	}
	if meta.IsDefined("modes", name, "timeout") {
		out.Timeout = override.Timeout
	}
	return out
}
