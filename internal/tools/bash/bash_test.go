package bash_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/tools"
	bashtool "github.com/eemax/tinyflags/internal/tools/bash"
)

func TestExecuteReturnsPlannedResultInPlanMode(t *testing.T) {
	tool := bashtool.New()
	result, err := tool.Execute(context.Background(), core.ToolCallRequest{
		ID:        "1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"echo hello"}`),
	}, tools.ExecContext{
		CWD:       t.TempDir(),
		PlanOnly:  true,
		Shell:     "/bin/sh",
		ShellArgs: []string{"-c"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Status != "planned" {
		t.Fatalf("status = %q", result.Status)
	}
	if result.Command == nil || result.Command.Executed {
		t.Fatalf("command metadata = %+v", result.Command)
	}
}

func TestExecuteRunsCommandInResolvedCWD(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}

	tool := bashtool.New()
	result, err := tool.Execute(context.Background(), core.ToolCallRequest{
		ID:        "1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"pwd","cwd":"sub"}`),
	}, tools.ExecContext{
		CWD:       dir,
		Shell:     "/bin/sh",
		ShellArgs: []string{"-c"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Command == nil {
		t.Fatal("expected command metadata")
	}
	if got := strings.TrimSpace(result.Content); got != subdir {
		t.Fatalf("pwd output = %q, want %q", got, subdir)
	}
}

func TestExecuteSeparatesStdoutAndStderr(t *testing.T) {
	tool := bashtool.New()
	result, err := tool.Execute(context.Background(), core.ToolCallRequest{
		ID:        "1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"printf out; printf err >&2"}`),
	}, tools.ExecContext{
		CWD:       t.TempDir(),
		Shell:     "/bin/sh",
		ShellArgs: []string{"-c"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Command == nil {
		t.Fatal("expected command metadata")
	}
	if result.Command.Stdout != "out" {
		t.Fatalf("stdout = %q", result.Command.Stdout)
	}
	if result.Command.Stderr != "err" {
		t.Fatalf("stderr = %q", result.Command.Stderr)
	}
	if result.Content != "outerr" {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestExecuteReturnsShellFailureOnNonZeroExit(t *testing.T) {
	tool := bashtool.New()
	_, err := tool.Execute(context.Background(), core.ToolCallRequest{
		ID:        "1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"exit 7"}`),
	}, tools.ExecContext{
		CWD:       t.TempDir(),
		Shell:     "/bin/sh",
		ShellArgs: []string{"-c"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	exitErr, ok := err.(*cerr.ExitCodeError)
	if !ok || exitErr.Code != cerr.ExitShellFailure {
		t.Fatalf("error = %#v", err)
	}
}

func TestExecuteReturnsTimeoutError(t *testing.T) {
	tool := bashtool.New()
	_, err := tool.Execute(context.Background(), core.ToolCallRequest{
		ID:        "1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"sleep 2","timeout_seconds":1}`),
	}, tools.ExecContext{
		CWD:       t.TempDir(),
		Shell:     "/bin/sh",
		ShellArgs: []string{"-c"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	exitErr, ok := err.(*cerr.ExitCodeError)
	if !ok || exitErr.Code != cerr.ExitTimeout {
		t.Fatalf("error = %#v", err)
	}
}
