package cli

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
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
	"github.com/eemax/tinyflags/internal/provider/openrouter"
	"github.com/eemax/tinyflags/internal/schema"
	"github.com/eemax/tinyflags/internal/session"
	"github.com/eemax/tinyflags/internal/skill"
	"github.com/eemax/tinyflags/internal/store"
	"github.com/eemax/tinyflags/internal/tools"
)

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

func (a *App) newRunCommand(globals *globalOptions) *cobra.Command {
	opts := runOptions{maxSteps: -1, maxToolRetries: -1}
	cmd := &cobra.Command{
		Use:           "run [flags] \"prompt\"",
		Short:         "Run a prompt explicitly",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.configPath = globals.configPath
			opts.format = core.FirstNonEmpty(opts.format, globals.format)
			opts.resultOnly = opts.resultOnly || globals.resultOnly
			opts.verbose = opts.verbose || globals.verbose
			opts.debug = opts.debug || globals.debug
			return a.executeRun(cmd.Context(), cmd, args, opts)
		},
	}
	a.bindRunFlags(cmd.Flags(), &opts)
	return cmd
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

	formatHint := core.FirstNonEmpty(opts.format, "text")
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
	resolvedCWD, err := a.resolveCWD(request.CWD)
	if err != nil {
		return withFormat(err, formatHint)
	}
	request.CWD = resolvedCWD

	cfg, _, err := config.LoadFromAnchor(opts.configPath, resolvedCWD)
	if err != nil {
		return withFormat(err, formatHint)
	}
	if opts.format == "" {
		formatHint = cfg.DefaultFormat
	}

	resolvedMode, err := mode.Resolve(cfg, request)
	if err != nil {
		return withFormat(err, formatHint)
	}
	if request.Format == "" {
		request.Format = resolvedMode.Format
	}
	formatHint = request.Format
	timeoutCtx, cancelTimeout := withInvocationTimeout(ctx, resolvedMode.Timeout)
	defer cancelTimeout()
	runCtx, stopSignal := signal.NotifyContext(timeoutCtx, os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	if err := invocationTimeoutError(runCtx); err != nil {
		return withFormat(err, formatHint)
	}
	request.StdinText, err = readStdinIfPresent()
	if err != nil {
		var exitErr *cerr.ExitCodeError
		if errors.As(err, &exitErr) {
			return withFormat(err, formatHint)
		}
		return withFormat(cerr.Wrap(cerr.ExitRuntime, "read stdin", err), formatHint)
	}
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

	needsDB := request.SessionName != "" || request.ForkSessionName != "" ||
		resolvedMode.StoreRunLog || resolvedMode.PersistSession
	var db *sql.DB
	if needsDB {
		db, err = store.OpenDB(cfg.DBPath)
		if err != nil {
			return withFormat(err, formatHint)
		}
		defer db.Close()
	}

	var sessionStore *session.SQLiteStore
	if db != nil {
		sessionStore = session.NewSQLiteStore(db)
	}
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
	if resolvedMode.StoreRunLog && db != nil {
		runRecord := core.RunRecord{
			SessionID:  sessionID(currentSession),
			ModeName:   resolvedMode.Name,
			ModelName:  resolvedMode.Model,
			SystemText: core.JoinNonEmpty("\n\n", resolvedMode.SystemPrompt, skillText, request.SystemInline),
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

	if currentSession != nil && !request.NoSessionSave && resolvedMode.PersistSession && sessionStore != nil {
		persistedMessages := redactPersistedMessages(runOutput.NewMessages, resolvedMode)
		if err := sessionStore.AppendMessages(currentSession.ID, nullableRunID(runOutput.Result.RunID), persistedMessages); err != nil {
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

const maxStdinBytes = 10 << 20 // 10 MB

func readStdinIfPresent() (string, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxStdinBytes {
		return "", cerr.New(cerr.ExitRuntime, "stdin exceeds 10 MB limit")
	}
	return string(data), nil
}

func redactPersistedMessages(messages []core.Message, mode core.ResolvedMode) []core.Message {
	capture := core.ToolResultCapture{
		CaptureCommands: mode.CaptureCommands,
		CaptureStdout:   mode.CaptureStdout,
		CaptureStderr:   mode.CaptureStderr,
	}
	out := make([]core.Message, len(messages))
	for i, msg := range messages {
		out[i] = capture.RedactMessage(msg)
	}
	return out
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
