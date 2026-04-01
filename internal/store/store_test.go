package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/store"
)

func TestOpenDBBootstrapsSchema(t *testing.T) {
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "tinyflags.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, table := range []string{"sessions", "messages", "runs", "tool_calls", "shell_commands"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("expected table %q: %v", table, err)
		}
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version;`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("user_version = %d, want 1", version)
	}
}

func TestHooksPersistRunToolAndShellRecords(t *testing.T) {
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "tinyflags.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tracker := store.NewRunTracker(store.HookConfig{
		Logger: store.NewSQLiteRunLogger(db),
		Run: core.RunRecord{
			ModeName:  "commander",
			ModelName: "openai/gpt-4.1",
			Format:    "text",
		},
		CaptureCommands: true,
		CaptureStdout:   true,
		CaptureStderr:   true,
	})
	hooks := tracker.Hooks()

	if err := hooks.OnLoopStart(context.Background(), core.RuntimeRequest{Prompt: "hello", CWD: "/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := hooks.OnToolCall(context.Background(), 1, core.ToolCallRequest{ID: "call-1", Name: "bash"}); err != nil {
		t.Fatal(err)
	}
	if err := hooks.OnToolResult(context.Background(), 1, core.ToolResult{
		ToolCallID: "call-1",
		ToolName:   "bash",
		Status:     "ok",
		Content:    "done",
		Command: &core.CommandResult{
			Command:  "echo hi",
			CWD:      "/repo",
			ExitCode: 0,
			Stdout:   "hi\n",
			Executed: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := hooks.OnLoopComplete(context.Background(), &core.AgentResult{
		ExitCode: 0,
		Usage:    core.Usage{InputTokens: 12, OutputTokens: 8},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.FinalizeSuccess(core.AgentResult{
		ExitCode: 0,
		Usage:    core.Usage{InputTokens: 12, OutputTokens: 8},
	}); err != nil {
		t.Fatal(err)
	}

	assertCount(t, db, "runs", 1)
	assertCount(t, db, "tool_calls", 1)
	assertCount(t, db, "shell_commands", 1)

	var status string
	var exitCode int
	if err := db.QueryRow(`SELECT status, exit_code FROM runs LIMIT 1`).Scan(&status, &exitCode); err != nil {
		t.Fatal(err)
	}
	if status != "success" || exitCode != 0 {
		t.Fatalf("run status/exit = %q/%d", status, exitCode)
	}
}

func TestHooksPersistErrorExitCode(t *testing.T) {
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "tinyflags.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tracker := store.NewRunTracker(store.HookConfig{
		Logger: store.NewSQLiteRunLogger(db),
		Run: core.RunRecord{
			ModeName:  "commander",
			ModelName: "openai/gpt-4.1",
			Format:    "text",
		},
	})
	hooks := tracker.Hooks()

	if err := hooks.OnLoopStart(context.Background(), core.RuntimeRequest{Prompt: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.FinalizeError(cerr.New(cerr.ExitShellFailure, "boom")); err != nil {
		t.Fatal(err)
	}

	var status string
	var exitCode int
	if err := db.QueryRow(`SELECT status, exit_code FROM runs LIMIT 1`).Scan(&status, &exitCode); err != nil {
		t.Fatal(err)
	}
	if status != "error" || exitCode != cerr.ExitShellFailure {
		t.Fatalf("run status/exit = %q/%d", status, exitCode)
	}
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}

func TestDebugRunsReturnsStoredRuns(t *testing.T) {
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "tinyflags.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	logger := store.NewSQLiteRunLogger(db)
	now := time.Now().UTC()
	if _, err := logger.StartRun(core.RunRecord{
		ModeName:  "text",
		ModelName: "openai/gpt-4o-mini",
		Prompt:    "hello",
		Format:    "text",
		Status:    "running",
		ExitCode:  0,
		StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	runs, err := store.DebugRuns(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Prompt != "hello" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestHooksRespectCaptureFlags(t *testing.T) {
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "tinyflags.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tracker := store.NewRunTracker(store.HookConfig{
		Logger: store.NewSQLiteRunLogger(db),
		Run: core.RunRecord{
			ModeName:  "commander",
			ModelName: "openai/gpt-4.1",
			Format:    "text",
		},
		CaptureCommands: false,
		CaptureStdout:   false,
		CaptureStderr:   true,
	})
	hooks := tracker.Hooks()

	if err := hooks.OnLoopStart(context.Background(), core.RuntimeRequest{Prompt: "hello", CWD: "/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := hooks.OnToolCall(context.Background(), 1, core.ToolCallRequest{ID: "call-1", Name: "bash"}); err != nil {
		t.Fatal(err)
	}
	if err := hooks.OnToolResult(context.Background(), 1, core.ToolResult{
		ToolCallID: "call-1",
		ToolName:   "bash",
		Status:     "shell_error",
		Content:    "outerr",
		Command: &core.CommandResult{
			Command:  "printf out; printf err >&2",
			CWD:      "/repo",
			ExitCode: 7,
			Stdout:   "out",
			Stderr:   "err",
			Executed: true,
		},
	}); err != nil {
		t.Fatal(err)
	}

	var command, stdout, stderr string
	if err := db.QueryRow(`SELECT command, stdout_text, stderr_text FROM shell_commands LIMIT 1`).Scan(&command, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if command != "" {
		t.Fatalf("command = %q, want empty", command)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "err" {
		t.Fatalf("stderr = %q", stderr)
	}
}
