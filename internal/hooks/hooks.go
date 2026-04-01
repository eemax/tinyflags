package hooks

import (
	"context"
	"errors"

	"github.com/eemax/tinyflags/internal/agent"
	"github.com/eemax/tinyflags/internal/core"
)

func Compose(hookSets ...agent.AgentHooks) agent.AgentHooks {
	return agent.AgentHooks{
		OnLoopStart: func(ctx context.Context, req core.RuntimeRequest) error {
			var errs []error
			for _, hook := range hookSets {
				if hook.OnLoopStart != nil {
					if err := hook.OnLoopStart(ctx, req); err != nil {
						errs = append(errs, err)
					}
				}
			}
			return errors.Join(errs...)
		},
		OnStepStart: func(ctx context.Context, step int) error {
			var errs []error
			for _, hook := range hookSets {
				if hook.OnStepStart != nil {
					if err := hook.OnStepStart(ctx, step); err != nil {
						errs = append(errs, err)
					}
				}
			}
			return errors.Join(errs...)
		},
		OnToolCall: func(ctx context.Context, step int, call core.ToolCallRequest) error {
			var errs []error
			for _, hook := range hookSets {
				if hook.OnToolCall != nil {
					if err := hook.OnToolCall(ctx, step, call); err != nil {
						errs = append(errs, err)
					}
				}
			}
			return errors.Join(errs...)
		},
		OnToolResult: func(ctx context.Context, step int, result core.ToolResult) error {
			var errs []error
			for _, hook := range hookSets {
				if hook.OnToolResult != nil {
					if err := hook.OnToolResult(ctx, step, result); err != nil {
						errs = append(errs, err)
					}
				}
			}
			return errors.Join(errs...)
		},
		OnStepComplete: func(ctx context.Context, step int, resp core.CompletionResponse) error {
			var errs []error
			for _, hook := range hookSets {
				if hook.OnStepComplete != nil {
					if err := hook.OnStepComplete(ctx, step, resp); err != nil {
						errs = append(errs, err)
					}
				}
			}
			return errors.Join(errs...)
		},
		OnLoopComplete: func(ctx context.Context, result *core.AgentResult) error {
			var errs []error
			for _, hook := range hookSets {
				if hook.OnLoopComplete != nil {
					if err := hook.OnLoopComplete(ctx, result); err != nil {
						errs = append(errs, err)
					}
				}
			}
			return errors.Join(errs...)
		},
		OnError: func(ctx context.Context, err error) error {
			var errs []error
			for _, hook := range hookSets {
				if hook.OnError != nil {
					if hookErr := hook.OnError(ctx, err); hookErr != nil {
						errs = append(errs, hookErr)
					}
				}
			}
			return errors.Join(errs...)
		},
	}
}
