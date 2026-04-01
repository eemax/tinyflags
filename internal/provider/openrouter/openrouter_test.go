package openrouter_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/provider/openrouter"
	"github.com/eemax/tinyflags/internal/schema"
)

func TestOpenRouterSmokeText(t *testing.T) {
	client := newSmokeClient(t)
	resp, err := client.Complete(smokeContext(t), core.CompletionRequest{
		Model: "openai/gpt-4o-mini",
		Messages: []core.Message{{
			Role:    "user",
			Content: "Reply with ok",
		}},
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if resp.AssistantMessage.Content == "" {
		t.Fatal("expected non-empty assistant response")
	}
}

func TestOpenRouterSmokeToolCalling(t *testing.T) {
	client := newSmokeClient(t)
	resp, err := client.Complete(smokeContext(t), core.CompletionRequest{
		Model: "openai/gpt-4o-mini",
		Messages: []core.Message{{
			Role:    "user",
			Content: `Call the tool exactly once with {"message":"ok"} and do not answer directly.`,
		}},
		Tools: []core.ToolSpec{{
			Name:        "echo_tool",
			Description: "Echo the requested message",
			InputSchema: json.RawMessage(`{"type":"object","required":["message"],"properties":{"message":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Name != "echo_tool" {
		t.Fatalf("tool name = %q", resp.ToolCalls[0].Name)
	}
}

func TestOpenRouterSmokeStructuredSchema(t *testing.T) {
	client := newSmokeClient(t)
	schemaBytes := []byte(`{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`)
	resp, err := client.Complete(smokeContext(t), core.CompletionRequest{
		Model:      "openai/gpt-4o-mini",
		JSONSchema: schemaBytes,
		Messages: []core.Message{{
			Role:    "user",
			Content: `Return JSON with a single field "name" set to "tinyflags".`,
		}},
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if _, err := schema.ValidateJSON(context.Background(), schemaBytes, []byte(resp.AssistantMessage.Content)); err != nil {
		t.Fatalf("schema validation failed: %v", err)
	}
}

func newSmokeClient(t *testing.T) *openrouter.Client {
	t.Helper()
	if os.Getenv("TINYFLAGS_OPENROUTER_SMOKE") == "" {
		t.Skip("set TINYFLAGS_OPENROUTER_SMOKE=1 to enable")
	}
	apiKey := os.Getenv("TINYFLAGS_API_KEY")
	if apiKey == "" {
		t.Skip("set TINYFLAGS_API_KEY to enable")
	}
	return openrouter.New("https://openrouter.ai/api/v1", apiKey, nil)
}

func smokeContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}
