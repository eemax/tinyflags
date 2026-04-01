package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/eemax/tinyflags/internal/agent"
	"github.com/eemax/tinyflags/internal/config"
	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/hooks"
	"github.com/eemax/tinyflags/internal/logging"
	"github.com/eemax/tinyflags/internal/mode"
	"github.com/eemax/tinyflags/internal/output"
	"github.com/eemax/tinyflags/internal/provider"
	"github.com/eemax/tinyflags/internal/provider/openrouter"
	"github.com/eemax/tinyflags/internal/schema"
	"github.com/eemax/tinyflags/internal/session"
	"github.com/eemax/tinyflags/internal/skill"
	"github.com/eemax/tinyflags/internal/store"
	"github.com/eemax/tinyflags/internal/tools"
	bashtool "github.com/eemax/tinyflags/internal/tools/bash"
	filetools "github.com/eemax/tinyflags/internal/tools/files"
	"github.com/eemax/tinyflags/internal/tools/websearch"
	"github.com/eemax/tinyflags/internal/version"
)

type App struct {
	Stdout           io.Writer
	Stderr           io.Writer
	HTTPClient       *http.Client
	ProviderRegistry *provider.Registry
	ToolRegistry     *tools.Registry
	Now              func() time.Time
}

type runOptions struct {
	mode            string
	session         string
	forkSession     string
	system          string
	skill           string
	model           string
	format          string
	outputSchema    string
	resultOnly      bool
	plan            bool
	timeout         time.Duration
	maxSteps        int
	maxToolRetries  int
	failOnToolError bool
	cwd             string
	configPath      string
	noSessionSave   bool
	verbose         bool
	debug           bool
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
			rootRun.format = firstNonEmpty(rootRun.format, globals.format)
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

func (a *App) newRunCommand(globals *globalOptions) *cobra.Command {
	opts := runOptions{maxSteps: -1, maxToolRetries: -1}
	cmd := &cobra.Command{
		Use:           "run [flags] \"prompt\"",
		Short:         "Run a prompt explicitly",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.configPath = globals.configPath
			opts.format = firstNonEmpty(opts.format, globals.format)
			opts.resultOnly = opts.resultOnly || globals.resultOnly
			opts.verbose = opts.verbose || globals.verbose
			opts.debug = opts.debug || globals.debug
			return a.executeRun(cmd.Context(), cmd, args, opts)
		},
	}
	a.bindRunFlags(cmd.Flags(), &opts)
	return cmd
}

func (a *App) bindGlobalFlags(flags *pflag.FlagSet, opts *globalOptions) {
	flags.StringVar(&opts.configPath, "config", "", "Specify config file path")
	flags.StringVar(&opts.format, "format", "", "Output renderer: text or json")
	flags.BoolVar(&opts.resultOnly, "result-only", false, "Suppress JSON envelope metadata")
	flags.BoolVar(&opts.verbose, "verbose", false, "Write operational summaries to stderr")
	flags.BoolVar(&opts.debug, "debug", false, "Write deeper request/response diagnostics to stderr")
}

func (a *App) bindRunFlags(flags *pflag.FlagSet, opts *runOptions) {
	flags.StringVar(&opts.mode, "mode", "", "Select named mode")
	flags.StringVar(&opts.session, "session", "", "Select persistent conversation state")
	flags.StringVar(&opts.forkSession, "fork-session", "", "Fork source session into a new named session and run against the fork")
	flags.StringVar(&opts.system, "system", "", "Inject inline system prompt")
	flags.StringVar(&opts.skill, "skill", "", "Load named reusable instruction text")
	flags.StringVar(&opts.model, "model", "", "Override mode/model default")
	flags.StringVar(&opts.outputSchema, "output-schema", "", "Path to JSON schema file for native structured output requests and final validation")
	flags.BoolVar(&opts.plan, "plan", false, "Disable side-effecting tool execution; request a plan instead")
	flags.DurationVar(&opts.timeout, "timeout", 0, "Hard cap for the invocation")
	flags.IntVar(&opts.maxSteps, "max-steps", -1, "Cap model/tool loop iterations")
	flags.IntVar(&opts.maxToolRetries, "max-tool-retries", -1, "Cap consecutive tool failures absorbed before termination")
	flags.BoolVar(&opts.failOnToolError, "fail-on-tool-error", false, "Terminate immediately on first tool failure")
	flags.StringVar(&opts.cwd, "cwd", "", "Set working directory for bash/file tools")
	flags.BoolVar(&opts.noSessionSave, "no-session-save", false, "Load session but do not persist new turn")
}

func (a *App) executeRun(ctx context.Context, cmd *cobra.Command, args []string, opts runOptions) error {
	_ = cmd
	if len(args) != 1 {
		return withFormat(cerr.New(cerr.ExitCLIUsage, "run requires exactly one prompt argument"), opts.format)
	}
	if opts.forkSession != "" && opts.session == "" {
		return withFormat(cerr.New(cerr.ExitCLIUsage, "--fork-session requires --session"), opts.format)
	}

	cfg, _, err := config.Load(opts.configPath)
	if err != nil {
		return withFormat(err, opts.format)
	}
	formatHint := firstNonEmpty(opts.format, cfg.DefaultFormat)
	request := core.RuntimeRequest{
		Prompt:          args[0],
		ModeName:        opts.mode,
		SessionName:     opts.session,
		ForkSessionName: opts.forkSession,
		SystemInline:    opts.system,
		SkillName:       opts.skill,
		ModelOverride:   opts.model,
		Format:          opts.format,
		OutputSchema:    opts.outputSchema,
		ResultOnly:      opts.resultOnly,
		PlanOnly:        opts.plan,
		Timeout:         opts.timeout,
		MaxSteps:        opts.maxSteps,
		MaxToolRetries:  opts.maxToolRetries,
		FailOnToolError: opts.failOnToolError,
		CWD:             opts.cwd,
		NoSessionSave:   opts.noSessionSave,
		Verbose:         opts.verbose,
		Debug:           opts.debug,
		ConfigPath:      opts.configPath,
	}

	resolvedMode, err := mode.Resolve(cfg, request)
	if err != nil {
		return withFormat(err, formatHint)
	}
	if request.Format == "" {
		request.Format = resolvedMode.Format
	}
	formatHint = request.Format
	runCtx, cancel := withInvocationTimeout(ctx, resolvedMode.Timeout)
	defer cancel()
	if err := invocationTimeoutError(runCtx); err != nil {
		return withFormat(err, formatHint)
	}
	request.StdinText, err = readStdinIfPresent()
	if err != nil {
		return withFormat(cerr.Wrap(cerr.ExitRuntime, "read stdin", err), formatHint)
	}
	if err := invocationTimeoutError(runCtx); err != nil {
		return withFormat(err, formatHint)
	}
	resolvedCWD, err := a.resolveCWD(request.CWD)
	if err != nil {
		return withFormat(err, formatHint)
	}
	request.CWD = resolvedCWD
	if err := invocationTimeoutError(runCtx); err != nil {
		return withFormat(err, formatHint)
	}

	skillText := ""
	if request.SkillName != "" {
		skillText, _, err = skill.Load(request.SkillName, request.CWD, cfg)
		if err != nil {
			return withFormat(err, formatHint)
		}
	}
	if err := invocationTimeoutError(runCtx); err != nil {
		return withFormat(err, formatHint)
	}
	schemaBytes, err := schema.Load(request.OutputSchema)
	if err != nil {
		return withFormat(err, formatHint)
	}
	if err := invocationTimeoutError(runCtx); err != nil {
		return withFormat(err, formatHint)
	}

	logger := a.newLogger(cfg, request)
	db, err := store.OpenDB(cfg.DBPath)
	if err != nil {
		return withFormat(err, formatHint)
	}
	defer db.Close()

	sessionStore := session.NewSQLiteStore(db)
	var currentSession *core.Session
	sessionMessages := []core.Message{}
	if request.ForkSessionName != "" {
		forked, err := sessionStore.Fork(request.SessionName, request.ForkSessionName)
		if err != nil {
			return withFormat(err, formatHint)
		}
		request.ForkedFrom = request.SessionName
		request.SessionName = forked.Name
		currentSession = &forked
	} else if request.SessionName != "" {
		loaded, err := sessionStore.LoadOrCreate(request.SessionName)
		if err != nil {
			return withFormat(err, formatHint)
		}
		currentSession = &loaded
	}
	if currentSession != nil {
		sessionMessages, err = sessionStore.GetMessages(currentSession.ID)
		if err != nil {
			return withFormat(err, formatHint)
		}
	}
	if err := invocationTimeoutError(runCtx); err != nil {
		return withFormat(err, formatHint)
	}

	runtimeProvider := a.providerForConfig(cfg)
	hookSets := []agent.AgentHooks{logging.NewHooks(logger, resolvedMode)}
	var runTracker *store.RunTracker
	if resolvedMode.StoreRunLog {
		runRecord := core.RunRecord{
			SessionID:  sessionID(currentSession),
			ModeName:   resolvedMode.Name,
			ModelName:  resolvedMode.Model,
			SystemText: joinSystemText(resolvedMode.SystemPrompt, skillText, request.SystemInline),
			Format:     request.Format,
		}
		runTracker = store.NewRunTracker(store.HookConfig{
			Logger:          store.NewSQLiteRunLogger(db),
			Run:             runRecord,
			SessionID:       sessionID(currentSession),
			CaptureCommands: resolvedMode.CaptureCommands,
			CaptureStdout:   resolvedMode.CaptureStdout,
			CaptureStderr:   resolvedMode.CaptureStderr,
			Now:             a.Now,
		})
		hookSets = append(hookSets, runTracker.Hooks())
	}
	runner := &agent.Runner{
		Provider: runtimeProvider,
		Tools:    a.ToolRegistry,
		Hooks:    hooks.Compose(hookSets...),
	}
	finishError := func(err error) error {
		err = normalizeInvocationError(runCtx, err)
		if runTracker != nil {
			if finishErr := runTracker.FinalizeError(err); finishErr != nil {
				err = errors.Join(err, finishErr)
			}
		}
		return withFormat(err, formatHint)
	}
	if runtimeOpenRouter, ok := runtimeProvider.(*openrouter.Client); ok {
		if err := validateRunModel(runCtx, runtimeOpenRouter, resolvedMode, len(schemaBytes) > 0); err != nil {
			return finishError(err)
		}
	}
	planInstruction := cfg.PlanModeInstruction
	if strings.TrimSpace(planInstruction) == "" {
		planInstruction = "You are operating in PLAN ONLY mode.\nDo not execute any tools or shell commands.\nInstead, describe exactly what commands or actions you would take, in order,\nand why. Format your response as a numbered action plan."
	}
	runOutput, err := runner.Run(runCtx, agent.RunInput{
		Request:         request,
		Mode:            resolvedMode,
		SessionMessages: sessionMessages,
		SkillText:       skillText,
		PlanInstruction: planInstruction,
		SchemaBytes:     schemaBytes,
		ExecContext: tools.ExecContext{
			CWD:       request.CWD,
			Mode:      resolvedMode,
			Logger:    logger,
			PlanOnly:  request.PlanOnly,
			Shell:     cfg.Shell,
			ShellArgs: cfg.ShellArgs,
		},
	})
	if err != nil {
		return finishError(err)
	}
	if runTracker != nil && runOutput.Result.RunID == 0 {
		runOutput.Result.RunID = runTracker.RunID()
	}
	if err := invocationTimeoutError(runCtx); err != nil {
		return finishError(err)
	}

	if len(schemaBytes) > 0 {
		validated, err := schema.Validate(runCtx, schemaBytes, runOutput.Result.Result)
		if err != nil {
			return finishError(err)
		}
		runOutput.Result.ResultJSON = validated
	}
	runOutput.Result.ExitCode = cerr.ExitSuccess
	if err := invocationTimeoutError(runCtx); err != nil {
		return finishError(err)
	}

	if currentSession != nil && !request.NoSessionSave && resolvedMode.PersistSession {
		if err := sessionStore.AppendMessages(currentSession.ID, nullableRunID(runOutput.Result.RunID), runOutput.NewMessages); err != nil {
			return finishError(err)
		}
	}
	if err := invocationTimeoutError(runCtx); err != nil {
		return finishError(err)
	}
	if runTracker != nil {
		if err := runTracker.FinalizeSuccess(runOutput.Result); err != nil {
			return finishError(err)
		}
	}

	renderer := a.newRenderer(request.Format, request.ResultOnly)
	if err := renderer.Render(runOutput.Result); err != nil {
		return finishError(err)
	}
	return nil
}

func (a *App) newSessionCommand(globals *globalOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "session", Short: "Session management"}
	cmd.AddCommand(&cobra.Command{
		Use:           "list",
		Short:         "List sessions",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			items, err := admin.List()
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, items, func() string {
				lines := make([]string, 0, len(items))
				for _, item := range items {
					lines = append(lines, item.Name)
				}
				return strings.Join(lines, "\n")
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "show <name>",
		Args:          cobra.ExactArgs(1),
		Short:         "Show a session",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			sessionValue, messages, err := admin.Show(args[0])
			if err != nil {
				return err
			}
			payload := core.SessionExport{Session: sessionValue, Messages: messages}
			return a.renderValue(globals.format, payload, func() string {
				lines := []string{sessionValue.Name}
				for _, msg := range messages {
					lines = append(lines, fmt.Sprintf("%s: %s", msg.Role, msg.Content))
				}
				return strings.Join(lines, "\n")
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "delete <name>",
		Args:          cobra.ExactArgs(1),
		Short:         "Delete a session",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			if err := admin.Delete(args[0]); err != nil {
				return err
			}
			return a.renderValue(globals.format, map[string]any{"deleted": args[0]}, func() string { return args[0] })
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "clear <name>",
		Args:          cobra.ExactArgs(1),
		Short:         "Clear a session",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			if err := admin.Clear(args[0]); err != nil {
				return err
			}
			return a.renderValue(globals.format, map[string]any{"cleared": args[0]}, func() string { return args[0] })
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "export <name>",
		Args:          cobra.ExactArgs(1),
		Short:         "Export a session",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			exported, err := admin.Export(args[0])
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, exported, func() string {
				data, _ := json.MarshalIndent(exported, "", "  ")
				return string(data)
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "fork <source> <destination>",
		Args:          cobra.ExactArgs(2),
		Short:         "Fork a session without running a prompt",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			forked, err := admin.Fork(args[0], args[1])
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, forked, func() string { return forked.Name })
		},
	})
	return cmd
}

func (a *App) newModeCommand(globals *globalOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "mode", Short: "Mode inspection"}
	cmd.AddCommand(&cobra.Command{
		Use:           "list",
		Short:         "List modes",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load(globals.configPath)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(cfg.Modes))
			for name := range cfg.Modes {
				names = append(names, name)
			}
			sort.Strings(names)
			return a.renderValue(globals.format, names, func() string { return strings.Join(names, "\n") })
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "show <name>",
		Args:          cobra.ExactArgs(1),
		Short:         "Show a resolved mode",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load(globals.configPath)
			if err != nil {
				return err
			}
			resolved, err := mode.Resolve(cfg, core.RuntimeRequest{ModeName: args[0], MaxSteps: -1, MaxToolRetries: -1})
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, resolved, func() string {
				data, _ := json.MarshalIndent(resolved, "", "  ")
				return string(data)
			})
		},
	})
	return cmd
}

func (a *App) newSkillCommand(globals *globalOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "skill", Short: "Skill inspection"}
	cmd.AddCommand(&cobra.Command{
		Use:           "list",
		Short:         "List skills",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load(globals.configPath)
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			items, err := skill.List(cwd, cfg)
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, items, func() string {
				lines := make([]string, 0, len(items))
				for _, item := range items {
					lines = append(lines, item.Name)
				}
				return strings.Join(lines, "\n")
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "show <name>",
		Args:          cobra.ExactArgs(1),
		Short:         "Show a skill",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load(globals.configPath)
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			content, info, err := skill.Load(args[0], cwd, cfg)
			if err != nil {
				return err
			}
			payload := map[string]any{"name": info.Name, "source": info.Source, "path": info.Path, "content": content}
			return a.renderValue(globals.format, payload, func() string { return content })
		},
	})
	return cmd
}

func (a *App) newConfigCommand(globals *globalOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Config inspection"}
	cmd.AddCommand(&cobra.Command{
		Use:           "show",
		Short:         "Show resolved config",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load(globals.configPath)
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, cfg, func() string {
				data, _ := json.MarshalIndent(cfg, "", "  ")
				return string(data)
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "path",
		Short:         "Show config path",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, path, err := config.Load(globals.configPath)
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, map[string]any{"path": path}, func() string { return path })
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "validate",
		Short:         "Validate config",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := config.Load(globals.configPath)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			report, err := validateConfigModels(ctx, cfg, a.HTTPClient)
			if err != nil {
				return err
			}
			if report.HasFailures() {
				return cerr.New(cerr.ExitRuntime, strings.Join(report.FailureMessages(), "; "))
			}
			payload := map[string]any{"ok": true, "path": path, "default_mode": cfg.DefaultMode}
			if len(report.Checks) > 0 {
				payload["openrouter_validation"] = report.Checks
			}
			if len(report.Warnings) > 0 {
				payload["warnings"] = report.Warnings
			}
			return a.renderValue(globals.format, payload, func() string {
				if len(report.Warnings) == 0 {
					return "ok"
				}
				lines := []string{"ok"}
				for _, warning := range report.Warnings {
					lines = append(lines, "warning: "+warning)
				}
				return strings.Join(lines, "\n")
			})
		},
	})
	return cmd
}

func (a *App) newDoctorCommand(globals *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:           "doctor",
		Short:         "Environment checks",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := config.Load(globals.configPath)
			if err != nil {
				return err
			}
			checks := map[string]any{
				"config_path":  path,
				"config_parse": map[string]any{"ok": true},
				"api_key":      map[string]any{"ok": cfg.APIKey != ""},
			}
			db, dbErr := store.OpenDB(cfg.DBPath)
			if dbErr != nil {
				checks["db"] = map[string]any{"ok": false, "error": dbErr.Error()}
			} else {
				defer db.Close()
				checks["db"] = map[string]any{"ok": true}
			}
			if info, err := os.Stat(cfg.SkillsDir); err != nil {
				checks["skills_dir"] = map[string]any{"ok": false, "error": err.Error()}
			} else {
				checks["skills_dir"] = map[string]any{"ok": info.IsDir()}
			}
			if _, err := os.Stat(cfg.Shell); err != nil {
				checks["shell"] = map[string]any{"ok": false, "error": err.Error()}
			} else {
				checks["shell"] = map[string]any{"ok": true}
			}
			if cfg.APIKey == "" {
				checks["openrouter"] = map[string]any{"ok": false, "error": "missing API key"}
			} else {
				ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
				defer cancel()
				if err := openrouter.CheckConnectivity(ctx, cfg.BaseURL, cfg.APIKey, a.HTTPClient); err != nil {
					checks["openrouter"] = map[string]any{"ok": false, "error": err.Error()}
				} else {
					checks["openrouter"] = map[string]any{"ok": true}
				}
			}
			validateCtx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			report, err := validateConfigModels(validateCtx, cfg, a.HTTPClient)
			if err != nil {
				checks["openrouter_model_validation"] = map[string]any{"ok": false, "error": err.Error()}
			} else {
				item := map[string]any{"ok": !report.HasFailures()}
				if len(report.Checks) > 0 {
					item["checks"] = report.Checks
				}
				if len(report.Warnings) > 0 {
					item["warnings"] = report.Warnings
				}
				checks["openrouter_model_validation"] = item
			}
			return a.renderValue(globals.format, checks, func() string {
				lines := []string{}
				keys := make([]string, 0, len(checks))
				for name := range checks {
					keys = append(keys, name)
				}
				sort.Strings(keys)
				for _, name := range keys {
					raw := checks[name]
					if name == "config_path" {
						lines = append(lines, fmt.Sprintf("%s: %v", name, raw))
						continue
					}
					item := raw.(map[string]any)
					status := "ok"
					if ok, _ := item["ok"].(bool); !ok {
						status = "fail"
					}
					line := fmt.Sprintf("%s: %s", name, status)
					if err, ok := item["error"].(string); ok && err != "" {
						line += " (" + err + ")"
					} else if warnings, ok := item["warnings"].([]string); ok && len(warnings) > 0 {
						line += " (warning: " + strings.Join(warnings, "; ") + ")"
					} else if warnings, ok := item["warnings"].([]any); ok && len(warnings) > 0 {
						values := make([]string, 0, len(warnings))
						for _, warning := range warnings {
							if text, ok := warning.(string); ok && text != "" {
								values = append(values, text)
							}
						}
						if len(values) > 0 {
							line += " (warning: " + strings.Join(values, "; ") + ")"
						}
					}
					lines = append(lines, line)
				}
				return strings.Join(lines, "\n")
			})
		},
	}
}

func (a *App) newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "version",
		Short:         "Print version/build info",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			payload := map[string]string{"version": version.Version, "commit": version.Commit, "date": version.Date}
			format := currentFormat(cmd)
			if err := validateCLIFormat(format); err != nil {
				return err
			}
			if format == "json" {
				enc := json.NewEncoder(a.Stdout)
				enc.SetEscapeHTML(false)
				return enc.Encode(payload)
			}
			_, err := fmt.Fprintf(a.Stdout, "%s %s %s\n", version.Version, version.Commit, version.Date)
			return err
		},
	}
}

func (a *App) sessionAdmin(configPath string) (*sql.DB, session.AdminStore, error) {
	cfg, _, err := config.Load(configPath)
	if err != nil {
		return nil, nil, err
	}
	db, err := store.OpenDB(cfg.DBPath)
	if err != nil {
		return nil, nil, err
	}
	return db, session.NewSQLiteStore(db), nil
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
		cwd, err := os.Getwd()
		if err != nil {
			return "", cerr.Wrap(cerr.ExitRuntime, "resolve current directory", err)
		}
		return cwd, nil
	}
	if filepath.IsAbs(value) {
		return value, nil
	}
	cwd, err := os.Getwd()
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

func readStdinIfPresent() (string, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func sessionID(item *core.Session) *int64 {
	if item == nil {
		return nil
	}
	id := item.ID
	return &id
}

func nullableRunID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	value := id
	return &value
}

func joinSystemText(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, "\n\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
	if format == "" || supportedFormat(format) {
		return nil
	}
	return cerr.New(cerr.ExitCLIUsage, fmt.Sprintf("unsupported format %q (expected text or json)", format))
}

func supportedFormat(format string) bool {
	return format == "text" || format == "json"
}

func withInvocationTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func invocationTimeoutError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return cerr.New(cerr.ExitTimeout, "invocation timed out")
	}
	return nil
}

func normalizeInvocationError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if timeoutErr := invocationTimeoutError(ctx); timeoutErr != nil {
		return timeoutErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return cerr.New(cerr.ExitTimeout, "invocation timed out")
	}
	return err
}
