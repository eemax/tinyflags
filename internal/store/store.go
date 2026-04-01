package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/eemax/tinyflags/internal/agent"
	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
)

type RunLogger interface {
	StartRun(run core.RunRecord) (int64, error)
	FinishRun(id int64, result core.RunRecord) error
	LogToolCall(call core.ToolCallRecord) (int64, error)
	UpdateToolCall(call core.ToolCallRecord) error
	LogShellCommand(cmd core.ShellCommandRecord) error
}

const schemaVersion = 2

func OpenDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, cerr.Wrap(cerr.ExitSessionFailure, "create database directory", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, cerr.Wrap(cerr.ExitSessionFailure, "open database", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		return nil, cerr.Wrap(cerr.ExitSessionFailure, "enable foreign keys", err)
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version;`).Scan(&version); err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "read database schema version", err)
	}
	if version > schemaVersion {
		return cerr.New(cerr.ExitSessionFailure, fmt.Sprintf("database schema version %d is newer than this build supports", version))
	}
	if version == schemaVersion {
		return nil
	}

	if version == 0 {
		return createLatestSchema(db)
	}
	if version == 1 {
		stmts := []string{
			`ALTER TABLE runs ADD COLUMN response_model TEXT;`,
			`ALTER TABLE runs ADD COLUMN provider_response_id TEXT;`,
			`ALTER TABLE runs ADD COLUMN finish_reason TEXT;`,
			`ALTER TABLE runs ADD COLUMN native_finish_reason TEXT;`,
			`ALTER TABLE runs ADD COLUMN provider_metadata_json TEXT;`,
		}
		for _, stmt := range stmts {
			if _, err := db.Exec(stmt); err != nil {
				return cerr.Wrap(cerr.ExitSessionFailure, "migrate database", err)
			}
		}
		if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d;`, schemaVersion)); err != nil {
			return cerr.Wrap(cerr.ExitSessionFailure, "set database schema version", err)
		}
		return nil
	}
	return nil
}

func createLatestSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS runs (
			id INTEGER PRIMARY KEY,
			session_id INTEGER REFERENCES sessions(id) ON DELETE SET NULL,
			mode_name TEXT NOT NULL,
			model_name TEXT NOT NULL,
			response_model TEXT,
			provider_response_id TEXT,
			finish_reason TEXT,
			native_finish_reason TEXT,
			provider_metadata_json TEXT,
			prompt TEXT NOT NULL,
			stdin_text TEXT,
			system_text TEXT,
			skill_name TEXT,
			cwd TEXT,
			format TEXT NOT NULL,
			plan_only INTEGER NOT NULL,
			status TEXT NOT NULL,
			exit_code INTEGER NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			duration_ms INTEGER,
			input_tokens INTEGER,
			output_tokens INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY,
			session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			run_id INTEGER REFERENCES runs(id),
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			tool_name TEXT,
			name TEXT,
			tool_call_id TEXT,
			tool_calls_json TEXT,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS tool_calls (
			id INTEGER PRIMARY KEY,
			run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			step_index INTEGER NOT NULL,
			tool_name TEXT NOT NULL,
			request_json TEXT NOT NULL,
			response_json TEXT,
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			duration_ms INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS shell_commands (
			id INTEGER PRIMARY KEY,
			tool_call_id INTEGER NOT NULL REFERENCES tool_calls(id) ON DELETE CASCADE,
			command TEXT NOT NULL,
			cwd TEXT,
			exit_code INTEGER,
			stdout_text TEXT,
			stderr_text TEXT,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			duration_ms INTEGER
		);`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return cerr.Wrap(cerr.ExitSessionFailure, "migrate database", err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d;`, schemaVersion)); err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "set database schema version", err)
	}
	return nil
}

type SQLiteRunLogger struct {
	db *sql.DB
}

func NewSQLiteRunLogger(db *sql.DB) *SQLiteRunLogger {
	return &SQLiteRunLogger{db: db}
}

func (l *SQLiteRunLogger) StartRun(run core.RunRecord) (int64, error) {
	var sessionID any
	if run.SessionID != nil {
		sessionID = *run.SessionID
	}
	result, err := l.db.Exec(
		`INSERT INTO runs (session_id, mode_name, model_name, response_model, provider_response_id, finish_reason, native_finish_reason, provider_metadata_json, prompt, stdin_text, system_text, skill_name, cwd, format, plan_only, status, exit_code, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, run.ModeName, run.ModelName, nullableString(run.ResponseModel), nullableString(run.ProviderResponseID), nullableString(run.FinishReason), nullableString(run.NativeFinishReason), nullableString(run.ProviderMetadataJSON), run.Prompt, run.StdinText, run.SystemText, run.SkillName, run.CWD, run.Format, boolToInt(run.PlanOnly), run.Status, run.ExitCode, run.StartedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, cerr.Wrap(cerr.ExitSessionFailure, "start run", err)
	}
	return result.LastInsertId()
}

func (l *SQLiteRunLogger) FinishRun(id int64, result core.RunRecord) error {
	var finishedAt any
	if result.FinishedAt != nil {
		finishedAt = result.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := l.db.Exec(
		`UPDATE runs SET response_model=?, provider_response_id=?, finish_reason=?, native_finish_reason=?, provider_metadata_json=?, status=?, exit_code=?, finished_at=?, duration_ms=?, input_tokens=?, output_tokens=? WHERE id=?`,
		nullableString(result.ResponseModel), nullableString(result.ProviderResponseID), nullableString(result.FinishReason), nullableString(result.NativeFinishReason), nullableString(result.ProviderMetadataJSON), result.Status, result.ExitCode, finishedAt, result.DurationMS, result.InputTokens, result.OutputTokens, id,
	)
	if err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "finish run", err)
	}
	return nil
}

func (l *SQLiteRunLogger) LogToolCall(call core.ToolCallRecord) (int64, error) {
	result, err := l.db.Exec(
		`INSERT INTO tool_calls (run_id, step_index, tool_name, request_json, response_json, status, started_at, finished_at, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		call.RunID, call.StepIndex, call.ToolName, call.RequestJSON, call.ResponseJSON, call.Status, call.StartedAt.UTC().Format(time.RFC3339Nano), nullableTime(call.FinishedAt), call.DurationMS,
	)
	if err != nil {
		return 0, cerr.Wrap(cerr.ExitSessionFailure, "log tool call", err)
	}
	return result.LastInsertId()
}

func (l *SQLiteRunLogger) UpdateToolCall(call core.ToolCallRecord) error {
	_, err := l.db.Exec(
		`UPDATE tool_calls SET response_json=?, status=?, finished_at=?, duration_ms=? WHERE id=?`,
		call.ResponseJSON, call.Status, nullableTime(call.FinishedAt), call.DurationMS, call.ID,
	)
	if err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "update tool call", err)
	}
	return nil
}

func (l *SQLiteRunLogger) LogShellCommand(cmd core.ShellCommandRecord) error {
	_, err := l.db.Exec(
		`INSERT INTO shell_commands (tool_call_id, command, cwd, exit_code, stdout_text, stderr_text, started_at, finished_at, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cmd.ToolCallID, cmd.Command, cmd.CWD, cmd.ExitCode, cmd.StdoutText, cmd.StderrText, cmd.StartedAt.UTC().Format(time.RFC3339Nano), nullableTime(cmd.FinishedAt), cmd.DurationMS,
	)
	if err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "log shell command", err)
	}
	return nil
}

type HookConfig struct {
	Logger          RunLogger
	Run             core.RunRecord
	SessionID       *int64
	CaptureCommands bool
	CaptureStdout   bool
	CaptureStderr   bool
	Now             func() time.Time
}

type hookState struct {
	mu             sync.Mutex
	runID          int64
	startedAt      time.Time
	toolCallIDs    map[string]int64
	toolCallTimes  map[string]time.Time
	usage          core.Usage
	providerSteps  []core.ProviderMetadata
	latestProvider core.ProviderMetadata
}

type RunTracker struct {
	cfg   HookConfig
	state *hookState
}

func NewRunTracker(cfg HookConfig) *RunTracker {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &RunTracker{
		cfg: cfg,
		state: &hookState{
			toolCallIDs:   map[string]int64{},
			toolCallTimes: map[string]time.Time{},
		},
	}
}

func (t *RunTracker) Hooks() agent.AgentHooks {
	return agent.AgentHooks{
		OnLoopStart: func(ctx context.Context, req core.RuntimeRequest) error {
			_ = ctx
			now := t.cfg.Now().UTC()
			run := t.cfg.Run
			run.SessionID = t.cfg.SessionID
			run.Prompt = req.Prompt
			run.StdinText = req.StdinText
			run.CWD = req.CWD
			run.SkillName = req.SkillName
			run.PlanOnly = req.PlanOnly
			run.StartedAt = now
			run.Status = "running"
			id, err := t.cfg.Logger.StartRun(run)
			if err != nil {
				return err
			}
			t.state.mu.Lock()
			t.state.startedAt = now
			t.state.runID = id
			t.state.usage = core.Usage{}
			t.state.mu.Unlock()
			return nil
		},
		OnStepComplete: func(ctx context.Context, step int, resp core.CompletionResponse) error {
			_, _ = ctx, step
			t.state.mu.Lock()
			t.state.usage.InputTokens += resp.Usage.InputTokens
			t.state.usage.OutputTokens += resp.Usage.OutputTokens
			recordProviderMetadataLocked(t.state, resp.ProviderMetadata)
			t.state.mu.Unlock()
			return nil
		},
		OnToolCall: func(ctx context.Context, step int, call core.ToolCallRequest) error {
			_ = ctx
			t.state.mu.Lock()
			defer t.state.mu.Unlock()
			now := t.cfg.Now().UTC()
			payload, _ := json.Marshal(call)
			id, err := t.cfg.Logger.LogToolCall(core.ToolCallRecord{
				RunID:       t.state.runID,
				StepIndex:   step,
				ToolName:    call.Name,
				RequestJSON: string(payload),
				Status:      "running",
				StartedAt:   now,
			})
			if err != nil {
				return err
			}
			t.state.toolCallIDs[call.ID] = id
			t.state.toolCallTimes[call.ID] = now
			return nil
		},
		OnToolResult: func(ctx context.Context, step int, result core.ToolResult) error {
			_ = ctx
			t.state.mu.Lock()
			defer t.state.mu.Unlock()
			now := t.cfg.Now().UTC()
			payload, _ := json.Marshal(result)
			callID := t.state.toolCallIDs[result.ToolCallID]
			startedAt := t.state.toolCallTimes[result.ToolCallID]
			if startedAt.IsZero() {
				startedAt = now
			}
			if err := t.cfg.Logger.UpdateToolCall(core.ToolCallRecord{
				ID:           callID,
				RunID:        t.state.runID,
				StepIndex:    step,
				ToolName:     result.ToolName,
				ResponseJSON: string(payload),
				Status:       result.Status,
				FinishedAt:   &now,
				DurationMS:   now.Sub(startedAt).Milliseconds(),
			}); err != nil {
				return err
			}
			if result.Command != nil {
				cmd := core.ShellCommandRecord{
					ToolCallID: callID,
					Command:    capturedString(t.cfg.CaptureCommands, result.Command.Command),
					CWD:        result.Command.CWD,
					ExitCode:   result.Command.ExitCode,
					StdoutText: capturedString(t.cfg.CaptureStdout, result.Command.Stdout),
					StderrText: capturedString(t.cfg.CaptureStderr, result.Command.Stderr),
					StartedAt:  startedAt,
					FinishedAt: &now,
					DurationMS: now.Sub(startedAt).Milliseconds(),
				}
				if err := t.cfg.Logger.LogShellCommand(cmd); err != nil {
					return err
				}
			}
			return nil
		},
		OnLoopComplete: func(ctx context.Context, result *core.AgentResult) error {
			_ = ctx
			if result != nil {
				result.RunID = t.RunID()
			}
			return nil
		},
		OnError: func(ctx context.Context, err error) error {
			_ = ctx
			metadata, ok := core.ProviderMetadataFromError(err)
			if !ok {
				return nil
			}
			t.state.mu.Lock()
			recordProviderMetadataLocked(t.state, metadata)
			t.state.mu.Unlock()
			return nil
		},
	}
}

func (t *RunTracker) RunID() int64 {
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	return t.state.runID
}

func (t *RunTracker) FinalizeSuccess(result core.AgentResult) error {
	return t.finish("success", result.ExitCode, result.Usage)
}

func (t *RunTracker) FinalizeError(err error) error {
	exitCode := cerr.ExitRuntime
	var typed *cerr.ExitCodeError
	if errors.As(err, &typed) {
		exitCode = typed.Code
	}
	return t.finish("error", exitCode, core.Usage{})
}

func (t *RunTracker) finish(status string, exitCode int, usage core.Usage) error {
	t.state.mu.Lock()
	runID := t.state.runID
	startedAt := t.state.startedAt
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		usage = t.state.usage
	}
	latestProvider := t.state.latestProvider
	providerSteps := append([]core.ProviderMetadata(nil), t.state.providerSteps...)
	t.state.mu.Unlock()
	if runID == 0 {
		return nil
	}
	now := t.cfg.Now().UTC()
	metadataJSON := marshalProviderMetadata(providerSteps)
	return t.cfg.Logger.FinishRun(runID, core.RunRecord{
		ResponseModel:        latestProvider.ResponseModel,
		ProviderResponseID:   latestProvider.ResponseID,
		FinishReason:         latestProvider.FinishReason,
		NativeFinishReason:   latestProvider.NativeFinishReason,
		ProviderMetadataJSON: metadataJSON,
		Status:               status,
		ExitCode:             exitCode,
		FinishedAt:           &now,
		DurationMS:           now.Sub(startedAt).Milliseconds(),
		InputTokens:          usage.InputTokens,
		OutputTokens:         usage.OutputTokens,
	})
}

func NewHooks(cfg HookConfig) agent.AgentHooks {
	return NewRunTracker(cfg).Hooks()
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func capturedString(enabled bool, value string) string {
	if !enabled {
		return ""
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func recordProviderMetadataLocked(state *hookState, metadata core.ProviderMetadata) {
	if providerMetadataEmpty(metadata) {
		return
	}
	state.latestProvider = metadata
	state.providerSteps = append(state.providerSteps, metadata)
}

func providerMetadataEmpty(metadata core.ProviderMetadata) bool {
	return metadata.ResponseID == "" &&
		metadata.ResponseModel == "" &&
		metadata.FinishReason == "" &&
		metadata.NativeFinishReason == "" &&
		metadata.SystemFingerprint == "" &&
		metadata.Refusal == "" &&
		metadata.Error == nil &&
		len(metadata.Extra) == 0
}

func marshalProviderMetadata(steps []core.ProviderMetadata) string {
	if len(steps) == 0 {
		return ""
	}
	payload, err := json.Marshal(map[string]any{"steps": steps})
	if err != nil {
		return ""
	}
	return string(payload)
}

func DebugRuns(ctx context.Context, db *sql.DB) ([]core.RunRecord, error) {
	_ = ctx
	rows, err := db.Query(`SELECT id, mode_name, model_name, response_model, provider_response_id, finish_reason, native_finish_reason, provider_metadata_json, prompt, format, status, exit_code, started_at FROM runs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.RunRecord{}
	for rows.Next() {
		var rec core.RunRecord
		var started string
		var responseModel sql.NullString
		var providerResponseID sql.NullString
		var finishReason sql.NullString
		var nativeFinishReason sql.NullString
		var providerMetadataJSON sql.NullString
		if err := rows.Scan(&rec.ID, &rec.ModeName, &rec.ModelName, &responseModel, &providerResponseID, &finishReason, &nativeFinishReason, &providerMetadataJSON, &rec.Prompt, &rec.Format, &rec.Status, &rec.ExitCode, &started); err != nil {
			return nil, err
		}
		rec.ResponseModel = responseModel.String
		rec.ProviderResponseID = providerResponseID.String
		rec.FinishReason = finishReason.String
		rec.NativeFinishReason = nativeFinishReason.String
		rec.ProviderMetadataJSON = providerMetadataJSON.String
		rec.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		out = append(out, rec)
	}
	return out, rows.Err()
}
