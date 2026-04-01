package bash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/tools"
)

type Tool struct{}

type input struct {
	Command        string `json:"command"`
	CWD            string `json:"cwd"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func New() *Tool {
	return &Tool{}
}

func (t *Tool) Name() string {
	return "bash"
}

func (t *Tool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "bash",
		Description: "Execute a shell command through the configured shell",
		InputSchema: json.RawMessage(`{"type":"object","required":["command"],"properties":{"command":{"type":"string"},"cwd":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1}}}`),
	}
}

func (t *Tool) Execute(ctx context.Context, call core.ToolCallRequest, execCtx tools.ExecContext) (core.ToolResult, error) {
	var in input
	if err := json.Unmarshal(call.Arguments, &in); err != nil {
		return core.ToolResult{
			ToolCallID: call.ID,
			ToolName:   t.Name(),
			Status:     "error",
			Content:    "invalid bash arguments",
		}, cerr.Wrap(cerr.ExitToolFailure, "parse bash tool arguments", err)
	}
	cwd := execCtx.CWD
	if in.CWD != "" {
		if filepath.IsAbs(in.CWD) {
			cwd = in.CWD
		} else {
			cwd = filepath.Join(execCtx.CWD, in.CWD)
		}
	}

	if execCtx.PlanOnly {
		return core.ToolResult{
			ToolCallID: call.ID,
			ToolName:   t.Name(),
			Status:     "planned",
			Content:    fmt.Sprintf("planned command: %s", in.Command),
			Command: &core.CommandResult{
				Command:  in.Command,
				CWD:      cwd,
				ExitCode: 0,
				Planned:  true,
				Executed: false,
			},
		}, nil
	}

	cmdCtx := ctx
	cancel := func() {}
	if in.TimeoutSeconds > 0 {
		timeout := time.Duration(in.TimeoutSeconds) * time.Second
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	args := append(append([]string(nil), execCtx.ShellArgs...), in.Command)
	cmd := exec.CommandContext(cmdCtx, execCtx.Shell, args...)
	cmd.Dir = cwd
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()
	if cmdCtx.Err() == context.DeadlineExceeded || ctx.Err() == context.DeadlineExceeded {
		return core.ToolResult{
			ToolCallID: call.ID,
			ToolName:   t.Name(),
			Status:     "timeout",
			Content:    "command timed out",
			Command: &core.CommandResult{
				Command:  in.Command,
				CWD:      cwd,
				Stdout:   stdout,
				Stderr:   stderr,
				ExitCode: 0,
				Executed: true,
				TimedOut: true,
			},
		}, cerr.New(cerr.ExitTimeout, "command timed out")
	}

	result := core.ToolResult{
		ToolCallID: call.ID,
		ToolName:   t.Name(),
		Status:     "ok",
		Content:    combinedOutput(stdout, stderr),
		Command: &core.CommandResult{
			Command:  in.Command,
			CWD:      cwd,
			Stdout:   stdout,
			Stderr:   stderr,
			Executed: true,
		},
	}
	if err == nil {
		return result, nil
	}

	exitCode := 1
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}
	result.Status = "shell_error"
	result.Content = combinedOutput(stdout, stderr)
	result.Command.ExitCode = exitCode
	return result, cerr.Wrap(cerr.ExitShellFailure, fmt.Sprintf("command exited with status %d", exitCode), err)
}

func combinedOutput(stdout, stderr string) string {
	if stdout == "" {
		return stderr
	}
	return stdout + stderr
}
