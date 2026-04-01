package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/eemax/tinyflags/internal/core"
)

type Logger interface {
	Errorf(format string, args ...any)
	Infof(format string, args ...any)
	Debugf(format string, args ...any)
}

type ExecContext struct {
	CWD       string
	Mode      core.ResolvedMode
	RunID     int64
	Logger    Logger
	PlanOnly  bool
	Shell     string
	ShellArgs []string
}

type Tool interface {
	Name() string
	Spec() core.ToolSpec
	Execute(ctx context.Context, call core.ToolCallRequest, execCtx ExecContext) (core.ToolResult, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Register(t Tool) error {
	if r.tools == nil {
		r.tools = map[string]Tool{}
	}
	if _, exists := r.tools[t.Name()]; exists {
		return fmt.Errorf("tool %q already registered", t.Name())
	}
	r.tools[t.Name()] = t
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Available() []string {
	out := make([]string, 0, len(r.tools))
	for name := range r.tools {
		out = append(out, name)
	}
	return out
}

func (r *Registry) SpecsFor(names []string) []core.ToolSpec {
	specs := make([]core.ToolSpec, 0, len(names))
	for _, name := range names {
		if tool, ok := r.Get(name); ok {
			specs = append(specs, tool.Spec())
		}
	}
	return specs
}

func ResultMessage(result core.ToolResult) core.Message {
	payload, err := json.Marshal(result)
	if err != nil {
		payload = []byte(fmt.Sprintf(`{"tool_name":%q,"status":%q,"content":%q}`, result.ToolName, result.Status, result.Content))
	}
	return core.Message{
		Role:       "tool",
		Name:       result.ToolName,
		ToolCallID: result.ToolCallID,
		Content:    string(payload),
	}
}
