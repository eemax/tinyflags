package websearch

import (
	"context"
	"encoding/json"

	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/tools"
)

type Stub struct{}

func NewStub() *Stub {
	return &Stub{}
}

func (s *Stub) Name() string {
	return "web_search"
}

func (s *Stub) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "web_search",
		Description: "Search the web (stub)",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
	}
}

func (s *Stub) Execute(ctx context.Context, call core.ToolCallRequest, execCtx tools.ExecContext) (core.ToolResult, error) {
	_, _ = ctx, execCtx
	return core.ToolResult{
		ToolCallID: call.ID,
		ToolName:   s.Name(),
		Status:     "unavailable",
		Content:    "web_search is not implemented in this build",
	}, nil
}
