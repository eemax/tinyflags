package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/eemax/tinyflags/internal/config"
	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/logging"
	"github.com/eemax/tinyflags/internal/output"
	"github.com/eemax/tinyflags/internal/provider"
	"github.com/eemax/tinyflags/internal/provider/openrouter"
	"github.com/eemax/tinyflags/internal/tools"
	bashtool "github.com/eemax/tinyflags/internal/tools/bash"
	filetools "github.com/eemax/tinyflags/internal/tools/files"
	"github.com/eemax/tinyflags/internal/tools/websearch"
)

type App struct {
	Stdout           io.Writer
	Stderr           io.Writer
	HTTPClient       *http.Client
	ProviderRegistry *provider.Registry
	ToolRegistry     *tools.Registry
	Now              func() time.Time
}

type globalOptions struct {
	configPath string
	format     string
	resultOnly bool
	verbose    bool
	debug      bool
}

type formatHintError struct {
	format string
	err    error
}

func (e *formatHintError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *formatHintError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func NewApp(stdout, stderr io.Writer) *App {
	app := &App{
		Stdout:     stdout,
		Stderr:     stderr,
		HTTPClient: http.DefaultClient,
		Now:        time.Now,
	}
	app.ProviderRegistry = provider.NewRegistry()
	app.ToolRegistry = defaultToolRegistry()
	return app
}

func (a *App) Execute(args []string) int {
	cmd := a.NewRootCommand()
	cmd.SetOut(a.Stdout)
	cmd.SetErr(a.Stderr)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		format := currentFormat(cmd)
		if format == "" {
			format = hintedFormat(err)
		}
		code := cerr.ExitRuntime
		var exitErr *cerr.ExitCodeError
		if errors.As(err, &exitErr) {
			code = exitErr.Code
			if format == "json" {
				_ = output.WriteErrorJSON(a.Stdout, code, errorTypeForExitCode(code), exitErr.Error())
			}
			fmt.Fprintln(a.Stderr, exitErr.Error())
			return code
		}
		if format == "json" {
			_ = output.WriteErrorJSON(a.Stdout, code, errorTypeForExitCode(code), err.Error())
		}
		fmt.Fprintln(a.Stderr, err.Error())
		return code
	}
	return cerr.ExitSuccess
}

func (a *App) NewRootCommand() *cobra.Command {
	var globals globalOptions
	rootRun := runOptions{maxSteps: -1, maxToolRetries: -1}
	rootCmd := &cobra.Command{
		Use:           "tinyflags [flags] \"prompt\"",
		Short:         "Headless, scriptable AI agent harness for the command line",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			_, _ = cmd, args
			return validateCLIFormat(globals.format)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rootRun.configPath = globals.configPath
			rootRun.format = core.FirstNonEmpty(rootRun.format, globals.format)
			rootRun.resultOnly = rootRun.resultOnly || globals.resultOnly
			rootRun.verbose = rootRun.verbose || globals.verbose
			rootRun.debug = rootRun.debug || globals.debug
			return a.executeRun(cmd.Context(), cmd, args, rootRun)
		},
	}
	a.bindGlobalFlags(rootCmd.PersistentFlags(), &globals)
	a.bindRunFlags(rootCmd.Flags(), &rootRun)
	rootCmd.AddCommand(a.newRunCommand(&globals))
	rootCmd.AddCommand(a.newSessionCommand(&globals))
	rootCmd.AddCommand(a.newModeCommand(&globals))
	rootCmd.AddCommand(a.newSkillCommand(&globals))
	rootCmd.AddCommand(a.newConfigCommand(&globals))
	rootCmd.AddCommand(a.newDoctorCommand(&globals))
	rootCmd.AddCommand(a.newVersionCommand())
	return rootCmd
}

func (a *App) bindGlobalFlags(flags *pflag.FlagSet, opts *globalOptions) {
	flags.StringVar(&opts.configPath, "config", "", "Specify config file path")
	flags.StringVar(&opts.format, "format", "", "Output renderer: text or json")
	flags.BoolVar(&opts.resultOnly, "result-only", false, "Suppress JSON envelope metadata")
	flags.BoolVar(&opts.verbose, "verbose", false, "Write operational summaries to stderr")
	flags.BoolVar(&opts.debug, "debug", false, "Write deeper request/response diagnostics to stderr")
}

func (a *App) loadCommandConfig(configPath string) (core.Config, string, error) {
	anchor, err := a.resolveCWD("")
	if err != nil {
		return core.Config{}, "", err
	}
	return config.LoadFromAnchor(configPath, anchor)
}

func (a *App) newRenderer(format string, resultOnly bool) output.Renderer {
	if format == "json" {
		return output.NewJSONRenderer(a.Stdout, resultOnly)
	}
	return output.NewTextRenderer(a.Stdout)
}

func (a *App) newLogger(cfg core.Config, request core.RuntimeRequest) logging.Logger {
	level := cfg.LogLevel
	if request.Debug {
		level = "debug"
	} else if request.Verbose {
		level = "info"
	}
	return logging.New(a.Stderr, level)
}

func (a *App) resolveCWD(value string) (string, error) {
	if value == "" {
		cwd, err := filepath.Abs(".")
		if err != nil {
			return "", cerr.Wrap(cerr.ExitRuntime, "resolve current directory", err)
		}
		return cwd, nil
	}
	if filepath.IsAbs(value) {
		return value, nil
	}
	cwd, err := filepath.Abs(".")
	if err != nil {
		return "", cerr.Wrap(cerr.ExitRuntime, "resolve current directory", err)
	}
	return filepath.Join(cwd, value), nil
}

func (a *App) renderValue(format string, value any, text func() string) error {
	if err := validateCLIFormat(format); err != nil {
		return err
	}
	if format == "json" {
		enc := json.NewEncoder(a.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(value)
	}
	_, err := io.WriteString(a.Stdout, text())
	return err
}

func defaultToolRegistry() *tools.Registry {
	registry := tools.NewRegistry()
	_ = registry.Register(bashtool.New())
	_ = registry.Register(filetools.NewReader())
	_ = registry.Register(filetools.NewWriter())
	_ = registry.Register(websearch.NewStub())
	return registry
}

func (a *App) providerForConfig(cfg core.Config) provider.Provider {
	if a.ProviderRegistry != nil {
		if p, ok := a.ProviderRegistry.Get("openrouter"); ok {
			return p
		}
	}
	return openrouter.New(cfg.BaseURL, cfg.APIKey, a.HTTPClient)
}

func redactKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "[redacted]"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

func errorTypeForExitCode(code int) string {
	switch code {
	case cerr.ExitTimeout:
		return "timeout"
	case cerr.ExitRefusal:
		return "provider_refusal"
	case cerr.ExitSchemaValidation:
		return "schema_validation_failure"
	case cerr.ExitToolFailure:
		return "tool_failure"
	case cerr.ExitShellFailure:
		return "shell_failure"
	case cerr.ExitSessionFailure:
		return "session_failure"
	case cerr.ExitCLIUsage:
		return "invalid_cli_usage"
	default:
		return "runtime_error"
	}
}

func currentFormat(cmd *cobra.Command) string {
	for current := cmd; current != nil; current = current.Parent() {
		if flag := current.Flags().Lookup("format"); flag != nil && flag.Value.String() != "" {
			return flag.Value.String()
		}
		if flag := current.PersistentFlags().Lookup("format"); flag != nil && flag.Value.String() != "" {
			return flag.Value.String()
		}
	}
	return ""
}

func withFormat(err error, format string) error {
	if err == nil || format == "" {
		return err
	}
	return &formatHintError{format: format, err: err}
}

func hintedFormat(err error) string {
	var hinted *formatHintError
	if errors.As(err, &hinted) {
		return hinted.format
	}
	return ""
}

func validateCLIFormat(format string) error {
	if format == "" || core.SupportedFormat(format) {
		return nil
	}
	return cerr.New(cerr.ExitCLIUsage, fmt.Sprintf("unsupported format %q (expected text or json)", format))
}
