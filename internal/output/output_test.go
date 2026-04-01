package output_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/output"
)

func TestTextRendererWritesPlainText(t *testing.T) {
	var buf bytes.Buffer
	renderer := output.NewTextRenderer(&buf)

	if err := renderer.Render(core.AgentResult{Result: "hello"}); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if got := buf.String(); got != "hello" {
		t.Fatalf("output = %q, want %q", got, "hello")
	}
}

func TestTextRendererPreservesExistingTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	renderer := output.NewTextRenderer(&buf)

	if err := renderer.Render(core.AgentResult{Result: "hello\n"}); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if got := buf.String(); got != "hello\n" {
		t.Fatalf("output = %q, want %q", got, "hello\n")
	}
}

func TestTextRendererPrefersStructuredJSON(t *testing.T) {
	var buf bytes.Buffer
	renderer := output.NewTextRenderer(&buf)

	if err := renderer.Render(core.AgentResult{Result: "ignored", ResultJSON: json.RawMessage(`{"ok":true}`)}); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if got := buf.String(); got != `{"ok":true}` {
		t.Fatalf("output = %q", got)
	}
}

func TestJSONRendererEnvelopeIncludesStructuredResult(t *testing.T) {
	var buf bytes.Buffer
	renderer := output.NewJSONRenderer(&buf, false)

	err := renderer.Render(core.AgentResult{
		Result:     "ignored",
		ResultJSON: json.RawMessage(`{"name":"tinyflags"}`),
		Mode:       "tool",
		Model:      "openai/gpt-4.1",
		Session:    "demo",
		ForkedFrom: "demo-base",
		Plan:       true,
		Steps:      2,
		ToolsUsed:  []string{"bash"},
		Commands:   []core.CommandSummary{{Command: "echo hi", CWD: "/tmp", ExitCode: 0}},
		Usage:      core.Usage{InputTokens: 10, OutputTokens: 5},
	})
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", payload["result"])
	}
	if result["name"] != "tinyflags" {
		t.Fatalf("result.name = %#v", result["name"])
	}
	if payload["forked_from"] != "demo-base" {
		t.Fatalf("forked_from = %#v", payload["forked_from"])
	}
}

func TestJSONRendererResultOnly(t *testing.T) {
	var buf bytes.Buffer
	renderer := output.NewJSONRenderer(&buf, true)

	if err := renderer.Render(core.AgentResult{Result: "hello"}); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if got := buf.String(); got != "\"hello\"\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestWriteErrorJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := output.WriteErrorJSON(&buf, 6, "shell_failure", "command exited with status 1"); err != nil {
		t.Fatalf("WriteErrorJSON returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if ok, _ := payload["ok"].(bool); ok {
		t.Fatalf("expected ok=false payload")
	}
	if exitCode, _ := payload["exit_code"].(float64); int(exitCode) != 6 {
		t.Fatalf("exit_code = %#v", payload["exit_code"])
	}
}
