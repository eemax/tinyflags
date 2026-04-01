package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eemax/tinyflags/internal/agent"
	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/tools"
	filetools "github.com/eemax/tinyflags/internal/tools/files"
)

type fakeProvider struct {
	requests  []core.CompletionRequest
	responses []core.CompletionResponse
}

func (p *fakeProvider) Complete(ctx context.Context, req core.CompletionRequest) (core.CompletionResponse, error) {
	_ = ctx
	p.requests = append(p.requests, req)
	if len(p.responses) == 0 {
		return core.CompletionResponse{}, nil
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

type fakeTool struct {
	spec      core.ToolSpec
	result    core.ToolResult
	err       error
	execCount int
}

func (t *fakeTool) Name() string { return t.result.ToolName }
func (t *fakeTool) Spec() core.ToolSpec {
	if t.spec.Name != "" {
		return t.spec
	}
	return core.ToolSpec{Name: t.result.ToolName}
}
func (t *fakeTool) Execute(ctx context.Context, call core.ToolCallRequest, execCtx tools.ExecContext) (core.ToolResult, error) {
	_, _, _ = ctx, call, execCtx
	t.execCount++
	return t.result, t.err
}

func TestRunnerBuildsPromptStackInOrder(t *testing.T) {
	provider := &fakeProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	}
	registry := tools.NewRegistry()
	runner := &agent.Runner{Provider: provider, Tools: registry}

	_, err := runner.Run(context.Background(), agent.RunInput{
		Request: core.RuntimeRequest{
			Prompt:    "prompt",
			StdinText: "stdin",
			PlanOnly:  true,
		},
		Mode: core.ResolvedMode{
			Name:           "text",
			Model:          "model",
			Tools:          []string{},
			MaxSteps:       4,
			MaxToolRetries: 0,
			SystemPrompt:   "mode",
		},
		SessionMessages: []core.Message{{Role: "assistant", Content: "session"}},
		SkillText:       "skill",
		PlanInstruction: "plan",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(provider.requests))
	}
	got := provider.requests[0].Messages
	if len(got) != 5 {
		t.Fatalf("message count = %d, want 5", len(got))
	}
	if got[0].Content != "mode\n\nskill" {
		t.Fatalf("system content = %q", got[0].Content)
	}
	if got[1].Content != "plan" {
		t.Fatalf("plan content = %q", got[1].Content)
	}
	if got[2].Content != "session" || got[3].Content != "prompt" || got[4].Content != "stdin" {
		t.Fatalf("unexpected message order: %+v", got)
	}
}

func TestRunnerReturnsHardErrorForDisallowedTool(t *testing.T) {
	provider := &fakeProvider{
		responses: []core.CompletionResponse{{
			AssistantMessage: core.Message{Role: "assistant"},
			ToolCalls:        []core.ToolCallRequest{{ID: "1", Name: "bash", Arguments: json.RawMessage(`{"command":"true"}`)}},
		}},
	}
	runner := &agent.Runner{Provider: provider, Tools: tools.NewRegistry()}
	_, err := runner.Run(context.Background(), agent.RunInput{
		Request: core.RuntimeRequest{Prompt: "prompt"},
		Mode: core.ResolvedMode{
			Name:           "text",
			Model:          "model",
			Tools:          []string{},
			MaxSteps:       4,
			MaxToolRetries: 0,
		},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	exitErr, ok := err.(*cerr.ExitCodeError)
	if !ok || exitErr.Code != cerr.ExitToolFailure {
		t.Fatalf("error = %#v", err)
	}
}

func TestRunnerSkipsSideEffectsInPlanMode(t *testing.T) {
	provider := &fakeProvider{
		responses: []core.CompletionResponse{
			{
				AssistantMessage: core.Message{Role: "assistant"},
				ToolCalls: []core.ToolCallRequest{{
					ID:        "1",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"notes.txt"}`),
				}},
			},
			{AssistantMessage: core.Message{Role: "assistant", Content: "done"}},
		},
	}
	registry := tools.NewRegistry()
	if err := registry.Register(filetools.NewReader()); err != nil {
		t.Fatal(err)
	}
	runner := &agent.Runner{Provider: provider, Tools: registry}

	output, err := runner.Run(context.Background(), agent.RunInput{
		Request: core.RuntimeRequest{Prompt: "prompt", PlanOnly: true},
		Mode: core.ResolvedMode{
			Name:           "tool",
			Model:          "model",
			Tools:          []string{"read_file"},
			MaxSteps:       4,
			MaxToolRetries: 1,
		},
		PlanInstruction: "plan only",
		ExecContext:     tools.ExecContext{CWD: t.TempDir(), PlanOnly: true},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if output.Result.Result != "done" {
		t.Fatalf("result = %q", output.Result.Result)
	}
	if output.Steps != 2 {
		t.Fatalf("steps = %d, want 2", output.Steps)
	}
	found := false
	for _, msg := range output.NewMessages {
		if msg.Role == "tool" && msg.Name == "read_file" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected tool result message in persisted messages")
	}
}

func TestRunnerFeedsToolErrorsBackWhenBudgetAllows(t *testing.T) {
	provider := &fakeProvider{
		responses: []core.CompletionResponse{
			{
				AssistantMessage: core.Message{Role: "assistant"},
				ToolCalls: []core.ToolCallRequest{{
					ID:        "1",
					Name:      "write_file",
					Arguments: json.RawMessage(`{"path":"x","content":"y"}`),
				}},
			},
			{AssistantMessage: core.Message{Role: "assistant", Content: "wrapped up"}},
		},
	}
	registry := tools.NewRegistry()
	if err := registry.Register(&fakeTool{
		result: core.ToolResult{ToolName: "write_file", Status: "error", Content: "boom"},
		err:    cerr.New(cerr.ExitToolFailure, "boom"),
	}); err != nil {
		t.Fatal(err)
	}

	runner := &agent.Runner{Provider: provider, Tools: registry}
	output, err := runner.Run(context.Background(), agent.RunInput{
		Request: core.RuntimeRequest{Prompt: "prompt"},
		Mode: core.ResolvedMode{
			Name:           "tool",
			Model:          "model",
			Tools:          []string{"write_file"},
			MaxSteps:       4,
			MaxToolRetries: 2,
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if output.Result.Result != "wrapped up" {
		t.Fatalf("result = %q", output.Result.Result)
	}
}

func TestRunnerRejectsInvalidToolArgumentsBeforeExecution(t *testing.T) {
	provider := &fakeProvider{
		responses: []core.CompletionResponse{
			{
				AssistantMessage: core.Message{Role: "assistant"},
				ToolCalls: []core.ToolCallRequest{{
					ID:        "1",
					Name:      "write_file",
					Arguments: json.RawMessage(`{"path":7}`),
				}},
			},
			{AssistantMessage: core.Message{Role: "assistant", Content: "wrapped up"}},
		},
	}
	registry := tools.NewRegistry()
	tool := &fakeTool{
		spec: core.ToolSpec{
			Name:        "write_file",
			InputSchema: json.RawMessage(`{"type":"object","required":["path","content"],"properties":{"path":{"type":"string"},"content":{"type":"string"}}}`),
		},
		result: core.ToolResult{ToolName: "write_file", Status: "ok", Content: "should not run"},
	}
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}

	runner := &agent.Runner{Provider: provider, Tools: registry}
	output, err := runner.Run(context.Background(), agent.RunInput{
		Request: core.RuntimeRequest{Prompt: "prompt"},
		Mode: core.ResolvedMode{
			Name:           "tool",
			Model:          "model",
			Tools:          []string{"write_file"},
			MaxSteps:       4,
			MaxToolRetries: 2,
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if output.Result.Result != "wrapped up" {
		t.Fatalf("result = %q", output.Result.Result)
	}
	if tool.execCount != 0 {
		t.Fatalf("tool executed %d times", tool.execCount)
	}
	found := false
	for _, msg := range output.NewMessages {
		if msg.Role == "tool" && msg.Name == "write_file" && strings.Contains(msg.Content, "validate write_file arguments") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected validation error tool message, got %+v", output.NewMessages)
	}
}

func TestRunnerInvalidToolArgumentsHonorFailFast(t *testing.T) {
	provider := &fakeProvider{
		responses: []core.CompletionResponse{
			{
				AssistantMessage: core.Message{Role: "assistant"},
				ToolCalls: []core.ToolCallRequest{{
					ID:        "1",
					Name:      "write_file",
					Arguments: json.RawMessage(`{"path":7}`),
				}},
			},
		},
	}
	registry := tools.NewRegistry()
	tool := &fakeTool{
		spec: core.ToolSpec{
			Name:        "write_file",
			InputSchema: json.RawMessage(`{"type":"object","required":["path","content"],"properties":{"path":{"type":"string"},"content":{"type":"string"}}}`),
		},
		result: core.ToolResult{ToolName: "write_file", Status: "ok", Content: "should not run"},
	}
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}

	runner := &agent.Runner{Provider: provider, Tools: registry}
	_, err := runner.Run(context.Background(), agent.RunInput{
		Request: core.RuntimeRequest{Prompt: "prompt", FailOnToolError: true},
		Mode: core.ResolvedMode{
			Name:           "tool",
			Model:          "model",
			Tools:          []string{"write_file"},
			MaxSteps:       4,
			MaxToolRetries: 2,
		},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	exitErr, ok := err.(*cerr.ExitCodeError)
	if !ok || exitErr.Code != cerr.ExitToolFailure {
		t.Fatalf("error = %#v", err)
	}
	if tool.execCount != 0 {
		t.Fatalf("tool executed %d times", tool.execCount)
	}
}

func TestRunnerOmitsCommandSummariesWhenCaptureDisabled(t *testing.T) {
	provider := &fakeProvider{
		responses: []core.CompletionResponse{
			{
				AssistantMessage: core.Message{Role: "assistant"},
				ToolCalls: []core.ToolCallRequest{{
					ID:        "1",
					Name:      "bash",
					Arguments: json.RawMessage(`{"command":"echo hi"}`),
				}},
			},
			{AssistantMessage: core.Message{Role: "assistant", Content: "done"}},
		},
	}
	registry := tools.NewRegistry()
	if err := registry.Register(&fakeTool{
		result: core.ToolResult{
			ToolCallID: "1",
			ToolName:   "bash",
			Status:     "ok",
			Content:    "hi\n",
			Command: &core.CommandResult{
				Command:  "echo hi",
				CWD:      "/repo",
				ExitCode: 0,
				Executed: true,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	runner := &agent.Runner{Provider: provider, Tools: registry}
	output, err := runner.Run(context.Background(), agent.RunInput{
		Request: core.RuntimeRequest{Prompt: "prompt"},
		Mode: core.ResolvedMode{
			Name:            "commander",
			Model:           "model",
			Tools:           []string{"bash"},
			MaxSteps:        4,
			MaxToolRetries:  1,
			CaptureCommands: false,
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(output.Commands) != 0 {
		t.Fatalf("commands = %+v, want none", output.Commands)
	}
}

func TestRunnerReturnsHookFailure(t *testing.T) {
	provider := &fakeProvider{
		responses: []core.CompletionResponse{{AssistantMessage: core.Message{Role: "assistant", Content: "done"}}},
	}
	runner := &agent.Runner{
		Provider: provider,
		Tools:    tools.NewRegistry(),
		Hooks: agent.AgentHooks{
			OnLoopComplete: func(ctx context.Context, result *core.AgentResult) error {
				_, _ = ctx, result
				return cerr.New(cerr.ExitSessionFailure, "finish run failed")
			},
		},
	}

	_, err := runner.Run(context.Background(), agent.RunInput{
		Request: core.RuntimeRequest{Prompt: "prompt"},
		Mode: core.ResolvedMode{
			Name:           "text",
			Model:          "model",
			Tools:          []string{},
			MaxSteps:       4,
			MaxToolRetries: 0,
		},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	exitErr, ok := err.(*cerr.ExitCodeError)
	if !ok || exitErr.Code != cerr.ExitSessionFailure {
		t.Fatalf("error = %#v", err)
	}
}
