package hooks_test

import (
	"context"
	"errors"
	"testing"

	"github.com/eemax/tinyflags/internal/agent"
	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/hooks"
)

func TestCompose_Zero(t *testing.T) {
	h := hooks.Compose()
	ctx := context.Background()

	if err := h.OnLoopStart(ctx, core.RuntimeRequest{}); err != nil {
		t.Fatalf("OnLoopStart error: %v", err)
	}
	if err := h.OnStepStart(ctx, 1); err != nil {
		t.Fatalf("OnStepStart error: %v", err)
	}
	if err := h.OnToolCall(ctx, 1, core.ToolCallRequest{}); err != nil {
		t.Fatalf("OnToolCall error: %v", err)
	}
	if err := h.OnToolResult(ctx, 1, core.ToolResult{}); err != nil {
		t.Fatalf("OnToolResult error: %v", err)
	}
	if err := h.OnStepComplete(ctx, 1, core.CompletionResponse{}); err != nil {
		t.Fatalf("OnStepComplete error: %v", err)
	}
	if err := h.OnLoopComplete(ctx, &core.AgentResult{}); err != nil {
		t.Fatalf("OnLoopComplete error: %v", err)
	}
	if err := h.OnError(ctx, errors.New("test")); err != nil {
		t.Fatalf("OnError error: %v", err)
	}
}

func TestCompose_NilCallbacks(t *testing.T) {
	h := hooks.Compose(agent.AgentHooks{})
	ctx := context.Background()

	if err := h.OnLoopStart(ctx, core.RuntimeRequest{}); err != nil {
		t.Fatalf("OnLoopStart with nil callback error: %v", err)
	}
	if err := h.OnStepStart(ctx, 1); err != nil {
		t.Fatalf("OnStepStart with nil callback error: %v", err)
	}
}

func TestCompose_ErrorPropagation(t *testing.T) {
	errFirst := errors.New("first failed")
	errSecond := errors.New("second failed")

	first := agent.AgentHooks{
		OnStepStart: func(ctx context.Context, step int) error {
			return errFirst
		},
	}
	second := agent.AgentHooks{
		OnStepStart: func(ctx context.Context, step int) error {
			return errSecond
		},
	}

	h := hooks.Compose(first, second)
	err := h.OnStepStart(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error from composed hooks")
	}
	if !errors.Is(err, errFirst) {
		t.Fatal("error should contain first hook's error")
	}
	if !errors.Is(err, errSecond) {
		t.Fatal("error should contain second hook's error (both hooks must fire)")
	}
}

func TestCompose_MultipleHooksFire(t *testing.T) {
	var calls []string

	first := agent.AgentHooks{
		OnToolCall: func(ctx context.Context, step int, call core.ToolCallRequest) error {
			calls = append(calls, "first")
			return nil
		},
	}
	second := agent.AgentHooks{
		OnToolCall: func(ctx context.Context, step int, call core.ToolCallRequest) error {
			calls = append(calls, "second")
			return nil
		},
	}

	h := hooks.Compose(first, second)
	if err := h.OnToolCall(context.Background(), 1, core.ToolCallRequest{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("calls = %v, want [first second]", calls)
	}
}
