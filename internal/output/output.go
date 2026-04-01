package output

import (
	"encoding/json"
	"io"

	"github.com/eemax/tinyflags/internal/core"
)

type Renderer interface {
	Render(result core.AgentResult) error
}

type TextRenderer struct {
	w io.Writer
}

func NewTextRenderer(w io.Writer) *TextRenderer {
	return &TextRenderer{w: w}
}

func (r *TextRenderer) Render(result core.AgentResult) error {
	if result.ResultJSON != nil {
		b := result.ResultJSON
		if len(b) > 0 && b[len(b)-1] != '\n' {
			b = append(b, '\n')
		}
		_, err := r.w.Write(b)
		return err
	}
	s := result.Result
	if s != "" && s[len(s)-1] != '\n' {
		s += "\n"
	}
	_, err := io.WriteString(r.w, s)
	return err
}

type JSONRenderer struct {
	w          io.Writer
	resultOnly bool
}

func NewJSONRenderer(w io.Writer, resultOnly bool) *JSONRenderer {
	return &JSONRenderer{w: w, resultOnly: resultOnly}
}

func (r *JSONRenderer) Render(result core.AgentResult) error {
	if r.resultOnly {
		if result.ResultJSON != nil {
			b := result.ResultJSON
			if len(b) > 0 && b[len(b)-1] != '\n' {
				b = append(b, '\n')
			}
			_, err := r.w.Write(b)
			return err
		}
		return json.NewEncoder(r.w).Encode(result.Result)
	}

	envelope := map[string]any{
		"ok":         true,
		"result":     result.Result,
		"mode":       result.Mode,
		"model":      result.Model,
		"session":    result.Session,
		"plan":       result.Plan,
		"steps":      result.Steps,
		"tools_used": result.ToolsUsed,
		"commands":   result.Commands,
		"usage":      result.Usage,
	}
	if result.ForkedFrom != "" {
		envelope["forked_from"] = result.ForkedFrom
	}
	if result.ResultJSON != nil {
		var payload any
		if err := json.Unmarshal(result.ResultJSON, &payload); err == nil {
			envelope["result"] = payload
		}
	}
	enc := json.NewEncoder(r.w)
	enc.SetEscapeHTML(false)
	return enc.Encode(envelope)
}

func WriteErrorJSON(w io.Writer, exitCode int, errorType, message string) error {
	envelope := map[string]any{
		"ok": false,
		"error": map[string]any{
			"type":    errorType,
			"message": message,
		},
		"exit_code": exitCode,
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(envelope)
}
