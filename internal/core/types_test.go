package core_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/eemax/tinyflags/internal/core"
)

func TestProviderError_Error(t *testing.T) {
	err := &core.ProviderError{Err: errors.New("boom")}
	if err.Error() != "boom" {
		t.Fatalf("Error() = %q", err.Error())
	}

	nilErr := (*core.ProviderError)(nil)
	if nilErr.Error() != "" {
		t.Fatalf("nil Error() = %q", nilErr.Error())
	}

	noInner := &core.ProviderError{}
	if noInner.Error() != "" {
		t.Fatalf("no inner Error() = %q", noInner.Error())
	}
}

func TestProviderError_Unwrap(t *testing.T) {
	inner := errors.New("inner")
	err := &core.ProviderError{Err: inner}
	if err.Unwrap() != inner {
		t.Fatal("Unwrap did not return inner")
	}

	nilErr := (*core.ProviderError)(nil)
	if nilErr.Unwrap() != nil {
		t.Fatal("nil Unwrap should return nil")
	}
}

func TestProviderMetadataFromError(t *testing.T) {
	meta := core.ProviderMetadata{ResponseID: "gen-123"}
	err := &core.ProviderError{
		Err:      errors.New("test"),
		Metadata: meta,
	}

	got, ok := core.ProviderMetadataFromError(err)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.ResponseID != "gen-123" {
		t.Fatalf("ResponseID = %q", got.ResponseID)
	}

	_, ok = core.ProviderMetadataFromError(errors.New("plain"))
	if ok {
		t.Fatal("expected ok=false for non-provider error")
	}
}

func TestToolResultCaptureRedactToolResult(t *testing.T) {
	capture := core.ToolResultCapture{
		CaptureCommands: false,
		CaptureStdout:   false,
		CaptureStderr:   true,
	}
	result := core.ToolResult{
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
	}

	got := capture.RedactToolResult(result)
	if got.Command == nil {
		t.Fatal("expected command result")
	}
	if got.Command.Command != "" {
		t.Fatalf("command = %q, want empty", got.Command.Command)
	}
	if got.Command.Stdout != "" {
		t.Fatalf("stdout = %q, want empty", got.Command.Stdout)
	}
	if got.Command.Stderr != "err" {
		t.Fatalf("stderr = %q, want err", got.Command.Stderr)
	}
	if got.Content != "err" {
		t.Fatalf("content = %q, want err", got.Content)
	}
}

func TestToolResultCaptureRedactMessage(t *testing.T) {
	capture := core.ToolResultCapture{}
	original := core.ToolResult{
		ToolCallID: "call-1",
		ToolName:   "bash",
		Status:     "ok",
		Content:    "secret-outsecret-err",
		Command: &core.CommandResult{
			Command:  "echo secret",
			CWD:      "/repo",
			Stdout:   "secret-out",
			Stderr:   "secret-err",
			ExitCode: 0,
			Executed: true,
		},
	}
	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	msg := capture.RedactMessage(core.Message{
		Role:       "tool",
		Name:       "bash",
		ToolCallID: "call-1",
		Content:    string(payload),
	})

	var got core.ToolResult
	if err := json.Unmarshal([]byte(msg.Content), &got); err != nil {
		t.Fatalf("invalid redacted content: %v", err)
	}
	if got.Content != "" {
		t.Fatalf("content = %q, want empty", got.Content)
	}
	if got.Command == nil {
		t.Fatal("expected command result")
	}
	if got.Command.Command != "" || got.Command.Stdout != "" || got.Command.Stderr != "" {
		t.Fatalf("unexpected command payload: %+v", got.Command)
	}
}
