package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/provider"
	"github.com/eemax/tinyflags/internal/schema"
	"github.com/eemax/tinyflags/internal/tools"
)

type AgentHooks struct {
	OnLoopStart    func(ctx context.Context, req core.RuntimeRequest) error
	OnStepStart    func(ctx context.Context, step int) error
	OnToolCall     func(ctx context.Context, step int, call core.ToolCallRequest) error
	OnToolResult   func(ctx context.Context, step int, result core.ToolResult) error
	OnStepComplete func(ctx context.Context, step int, resp core.CompletionResponse) error
	OnLoopComplete func(ctx context.Context, result *core.AgentResult) error
	OnError        func(ctx context.Context, err error) error
}

type Runner struct {
	Provider provider.Provider
	Tools    *tools.Registry
	Hooks    AgentHooks
}

type RunInput struct {
	Request         core.RuntimeRequest
	Mode            core.ResolvedMode
	SessionMessages []core.Message
	SkillText       string
	PlanInstruction string
	SchemaBytes     []byte
	ExecContext     tools.ExecContext
}

type RunOutput struct {
	Result      core.AgentResult
	SystemText  string
	NewMessages []core.Message
	AllMessages []core.Message
	Usage       core.Usage
	Steps       int
	Commands    []core.CommandSummary
	ToolsUsed   []string
}

func (r *Runner) Run(ctx context.Context, input RunInput) (RunOutput, error) {
	if r.Provider == nil {
		return RunOutput{}, cerr.New(cerr.ExitRuntime, "provider is required")
	}
	if r.Tools == nil {
		return RunOutput{}, cerr.New(cerr.ExitRuntime, "tool registry is required")
	}

	if r.Hooks.OnLoopStart != nil {
		if err := r.Hooks.OnLoopStart(ctx, input.Request); err != nil {
			return RunOutput{}, r.joinWithHookError(ctx, err)
		}
	}

	systemText := core.JoinNonEmpty("\n\n", input.Mode.SystemPrompt, input.SkillText, input.Request.SystemInline)
	messages := make([]core.Message, 0, len(input.SessionMessages)+4)
	if systemText != "" {
		messages = append(messages, core.Message{Role: "system", Content: systemText})
	}
	if input.Request.PlanOnly {
		messages = append(messages, core.Message{Role: "system", Content: input.PlanInstruction})
	}
	if len(input.SchemaBytes) > 0 {
		messages = append(messages, core.Message{Role: "system", Content: schemaInstruction(input.SchemaBytes)})
	}
	messages = append(messages, messagesFrom(input.SessionMessages)...)

	newMessages := make([]core.Message, 0, 4)
	if strings.TrimSpace(input.Request.Prompt) != "" {
		userMsg := core.Message{Role: "user", Content: input.Request.Prompt}
		messages = append(messages, userMsg)
		newMessages = append(newMessages, userMsg)
	}
	if input.Request.StdinText != "" {
		stdinMsg := core.Message{Role: "user", Content: input.Request.StdinText}
		messages = append(messages, stdinMsg)
		newMessages = append(newMessages, stdinMsg)
	}

	consecutiveFailures := 0
	usage := core.Usage{}
	var toolsUsed []string
	var commands []core.CommandSummary

	for step := 1; ; step++ {
		if err := contextTimeoutError(ctx); err != nil {
			return RunOutput{}, r.joinWithHookError(ctx, err)
		}
		if r.Hooks.OnStepStart != nil {
			if err := r.Hooks.OnStepStart(ctx, step); err != nil {
				return RunOutput{}, r.joinWithHookError(ctx, err)
			}
		}

		resp, err := r.Provider.Complete(ctx, core.CompletionRequest{
			Model:      input.Mode.Model,
			Messages:   messages,
			Tools:      r.Tools.SpecsFor(input.Mode.Tools),
			MaxSteps:   input.Mode.MaxSteps,
			Timeout:    input.Mode.Timeout,
			JSONSchema: input.SchemaBytes,
			PlanOnly:   input.Request.PlanOnly,
		})
		if err != nil {
			return RunOutput{}, r.joinWithHookError(ctx, normalizeContextError(ctx, err))
		}
		if err := contextTimeoutError(ctx); err != nil {
			return RunOutput{}, r.joinWithHookError(ctx, err)
		}
		usage.InputTokens += resp.Usage.InputTokens
		usage.OutputTokens += resp.Usage.OutputTokens

		if r.Hooks.OnStepComplete != nil {
			if err := r.Hooks.OnStepComplete(ctx, step, resp); err != nil {
				return RunOutput{}, r.joinWithHookError(ctx, err)
			}
		}
		if resp.Refusal {
			err := cerr.New(cerr.ExitRefusal, "provider refused the request")
			return RunOutput{}, r.joinWithHookError(ctx, err)
		}

		assistantMsg := resp.AssistantMessage
		if len(resp.ToolCalls) > 0 {
			assistantMsg.ToolCalls = append([]core.ToolCallRequest(nil), resp.ToolCalls...)
			messages = append(messages, assistantMsg)
			newMessages = append(newMessages, assistantMsg)

			for _, call := range resp.ToolCalls {
				if !toolAllowed(input.Mode.Tools, call.Name) {
					err := cerr.New(cerr.ExitToolFailure, fmt.Sprintf("tool %q is not allowed in mode %q", call.Name, input.Mode.Name))
					return RunOutput{}, r.joinWithHookError(ctx, err)
				}
				tool, ok := r.Tools.Get(call.Name)
				if !ok {
					err := cerr.New(cerr.ExitToolFailure, fmt.Sprintf("tool %q is not registered", call.Name))
					return RunOutput{}, r.joinWithHookError(ctx, err)
				}
				if r.Hooks.OnToolCall != nil {
					if err := r.Hooks.OnToolCall(ctx, step, call); err != nil {
						return RunOutput{}, r.joinWithHookError(ctx, err)
					}
				}
				result, execErr := executeToolCall(ctx, tool, call, input.ExecContext)
				execErr = normalizeContextError(ctx, execErr)
				if result.ToolCallID == "" {
					result.ToolCallID = call.ID
				}
				if result.ToolName == "" {
					result.ToolName = call.Name
				}
				if r.Hooks.OnToolResult != nil {
					if err := r.Hooks.OnToolResult(ctx, step, result); err != nil {
						return RunOutput{}, r.joinWithHookError(ctx, err)
					}
				}
				messages = append(messages, tools.ResultMessage(result))
				newMessages = append(newMessages, tools.ResultMessage(result))
				toolsUsed = appendUnique(toolsUsed, call.Name)
				if result.Command != nil && input.Mode.CaptureCommands {
					commands = append(commands, core.CommandSummary{
						Command:  result.Command.Command,
						CWD:      result.Command.CWD,
						ExitCode: result.Command.ExitCode,
					})
				}

				if execErr == nil {
					consecutiveFailures = 0
					if err := contextTimeoutError(ctx); err != nil {
						return RunOutput{}, r.joinWithHookError(ctx, err)
					}
					continue
				}

				if exitErr, ok := execErr.(*cerr.ExitCodeError); ok && exitErr.Code == cerr.ExitTimeout {
					return RunOutput{}, r.joinWithHookError(ctx, exitErr)
				}
				consecutiveFailures++
				if input.Request.FailOnToolError {
					return RunOutput{}, r.joinWithHookError(ctx, execErr)
				}
				if budgetExceeded(consecutiveFailures, input.Mode.MaxToolRetries) {
					if exitErr, ok := execErr.(*cerr.ExitCodeError); ok {
						return RunOutput{}, r.joinWithHookError(ctx, exitErr)
					}
					err := cerr.Wrap(cerr.ExitToolFailure, "tool retry budget exhausted", execErr)
					return RunOutput{}, r.joinWithHookError(ctx, err)
				}
			}

			if step >= input.Mode.MaxSteps {
				err := cerr.New(cerr.ExitRuntime, "max steps reached")
				return RunOutput{}, r.joinWithHookError(ctx, err)
			}
			continue
		}

		if assistantMsg.Content != "" || assistantMsg.Role != "" {
			if assistantMsg.Role == "" {
				assistantMsg.Role = "assistant"
			}
			messages = append(messages, assistantMsg)
			newMessages = append(newMessages, assistantMsg)
		}

		result := core.AgentResult{
			Ok:         true,
			Result:     assistantMsg.Content,
			Mode:       input.Mode.Name,
			Model:      input.Mode.Model,
			Session:    input.Request.SessionName,
			ForkedFrom: forkedFrom(input.Request),
			Plan:       input.Request.PlanOnly,
			Steps:      step,
			ToolsUsed:  toolsUsed,
			Commands:   commands,
			Usage:      usage,
		}
		if r.Hooks.OnLoopComplete != nil {
			if err := r.Hooks.OnLoopComplete(ctx, &result); err != nil {
				return RunOutput{}, err
			}
		}
		return RunOutput{
			Result:      result,
			SystemText:  systemText,
			NewMessages: newMessages,
			AllMessages: messages,
			Usage:       usage,
			Steps:       step,
			Commands:    commands,
			ToolsUsed:   toolsUsed,
		}, nil
	}
}

func contextTimeoutError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return cerr.New(cerr.ExitTimeout, "invocation timed out")
	}
	return nil
}

func normalizeContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if timeoutErr := contextTimeoutError(ctx); timeoutErr != nil {
		return timeoutErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return cerr.New(cerr.ExitTimeout, "invocation timed out")
	}
	return err
}

func schemaInstruction(schemaBytes []byte) string {
	_ = schemaBytes
	return "Return valid JSON only. Do not wrap it in markdown or add explanatory text."
}

func toolAllowed(allowed []string, name string) bool {
	for _, item := range allowed {
		if item == name {
			return true
		}
	}
	return false
}

func messagesFrom(in []core.Message) []core.Message {
	out := make([]core.Message, 0, len(in))
	for _, msg := range in {
		out = append(out, msg)
	}
	return out
}

func appendUnique(values []string, item string) []string {
	for _, value := range values {
		if value == item {
			return values
		}
	}
	return append(values, item)
}

func budgetExceeded(failures, budget int) bool {
	if budget < 0 {
		return false
	}
	return failures >= budget
}

func forkedFrom(req core.RuntimeRequest) string {
	return req.ForkedFrom
}

func executeToolCall(ctx context.Context, tool tools.Tool, call core.ToolCallRequest, execCtx tools.ExecContext) (core.ToolResult, error) {
	spec := tool.Spec()
	if len(spec.InputSchema) == 0 {
		return tool.Execute(ctx, call, execCtx)
	}
	if _, err := schema.ValidateJSON(ctx, spec.InputSchema, call.Arguments); err != nil {
		execErr := cerr.Wrap(cerr.ExitToolFailure, fmt.Sprintf("validate %s arguments", call.Name), err)
		return core.ToolResult{
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Status:     "error",
			Content:    execErr.Error(),
		}, execErr
	}
	return tool.Execute(ctx, call, execCtx)
}

func (r *Runner) joinWithHookError(ctx context.Context, err error) error {
	if r.Hooks.OnError == nil {
		return err
	}
	if hookErr := r.Hooks.OnError(ctx, err); hookErr != nil {
		return errors.Join(err, hookErr)
	}
	return err
}

func MarshalMessage(msg core.Message) string {
	data, _ := json.Marshal(msg)
	return string(data)
}
