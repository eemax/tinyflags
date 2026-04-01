package logging

import (
	"context"

	"github.com/eemax/tinyflags/internal/agent"
	"github.com/eemax/tinyflags/internal/core"
)

func NewHooks(logger Logger, mode core.ResolvedMode) agent.AgentHooks {
	return agent.AgentHooks{
		OnLoopStart: func(ctx context.Context, req core.RuntimeRequest) error {
			logger.Infof("mode=%s model=%s session=%s", mode.Name, mode.Model, req.SessionName)
			return nil
		},
		OnStepStart: func(ctx context.Context, step int) error {
			logger.Infof("step=%d provider request sent", step)
			return nil
		},
		OnToolCall: func(ctx context.Context, step int, call core.ToolCallRequest) error {
			logger.Infof("step=%d tool=%s request=%s", step, call.Name, string(call.Arguments))
			return nil
		},
		OnToolResult: func(ctx context.Context, step int, result core.ToolResult) error {
			if result.Command != nil {
				logger.Infof("step=%d tool=%s exit=%d command=%q", step, result.ToolName, result.Command.ExitCode, result.Command.Command)
				return nil
			}
			logger.Infof("step=%d tool=%s status=%s", step, result.ToolName, result.Status)
			return nil
		},
		OnError: func(ctx context.Context, err error) error {
			logger.Errorf("%v", err)
			return nil
		},
		OnLoopComplete: func(ctx context.Context, result *core.AgentResult) error {
			logger.Debugf("completed steps=%d tools=%v", result.Steps, result.ToolsUsed)
			return nil
		},
		OnStepComplete: func(ctx context.Context, step int, resp core.CompletionResponse) error {
			logger.Debugf("step=%d usage_in=%d usage_out=%d", step, resp.Usage.InputTokens, resp.Usage.OutputTokens)
			if resp.Refusal {
				logger.Debugf("step=%d refusal=true", step)
			}
			return nil
		},
	}
}
