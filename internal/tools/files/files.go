package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/tools"
)

type Reader struct{}
type Writer struct{}

type readInput struct {
	Path string `json:"path"`
}

type writeInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func NewReader() *Reader { return &Reader{} }
func NewWriter() *Writer { return &Writer{} }

func (r *Reader) Name() string { return "read_file" }

func (r *Reader) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "read_file",
		Description: "Read a local file",
		InputSchema: json.RawMessage(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`),
	}
}

func (r *Reader) Execute(ctx context.Context, call core.ToolCallRequest, execCtx tools.ExecContext) (core.ToolResult, error) {
	_ = ctx
	if execCtx.PlanOnly {
		return skipped(call.ID, r.Name(), "read_file skipped in plan mode"), nil
	}
	var in readInput
	if err := json.Unmarshal(call.Arguments, &in); err != nil {
		return core.ToolResult{ToolCallID: call.ID, ToolName: r.Name(), Status: "error", Content: "invalid read_file arguments"}, cerr.Wrap(cerr.ExitToolFailure, "parse read_file arguments", err)
	}
	path := resolvePath(execCtx.CWD, in.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		return core.ToolResult{
			ToolCallID: call.ID,
			ToolName:   r.Name(),
			Status:     "error",
			Content:    err.Error(),
		}, cerr.Wrap(cerr.ExitToolFailure, "read file", err)
	}
	return core.ToolResult{
		ToolCallID: call.ID,
		ToolName:   r.Name(),
		Status:     "ok",
		Content:    string(data),
		Metadata:   map[string]any{"path": path},
	}, nil
}

func (w *Writer) Name() string { return "write_file" }

func (w *Writer) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "write_file",
		Description: "Write a local file",
		InputSchema: json.RawMessage(`{"type":"object","required":["path","content"],"properties":{"path":{"type":"string"},"content":{"type":"string"}}}`),
	}
}

func (w *Writer) Execute(ctx context.Context, call core.ToolCallRequest, execCtx tools.ExecContext) (core.ToolResult, error) {
	_ = ctx
	if execCtx.PlanOnly {
		return skipped(call.ID, w.Name(), "write_file skipped in plan mode"), nil
	}
	var in writeInput
	if err := json.Unmarshal(call.Arguments, &in); err != nil {
		return core.ToolResult{ToolCallID: call.ID, ToolName: w.Name(), Status: "error", Content: "invalid write_file arguments"}, cerr.Wrap(cerr.ExitToolFailure, "parse write_file arguments", err)
	}
	path := resolvePath(execCtx.CWD, in.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return core.ToolResult{ToolCallID: call.ID, ToolName: w.Name(), Status: "error", Content: err.Error()}, cerr.Wrap(cerr.ExitToolFailure, "create parent directories", err)
	}
	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return core.ToolResult{ToolCallID: call.ID, ToolName: w.Name(), Status: "error", Content: err.Error()}, cerr.Wrap(cerr.ExitToolFailure, "write file", err)
	}
	return core.ToolResult{
		ToolCallID: call.ID,
		ToolName:   w.Name(),
		Status:     "ok",
		Content:    "file written",
		Metadata:   map[string]any{"path": path},
	}, nil
}

func resolvePath(base, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

func skipped(id, toolName, message string) core.ToolResult {
	return core.ToolResult{
		ToolCallID: id,
		ToolName:   toolName,
		Status:     "skipped_plan_mode",
		Content:    message,
	}
}
