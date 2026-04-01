package openrouter_test

import (
	"context"
	"os"
	"testing"

	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/provider/openrouter"
)

func TestOpenRouterSmoke(t *testing.T) {
	if os.Getenv("TINYFLAGS_OPENROUTER_SMOKE") == "" {
		t.Skip("set TINYFLAGS_OPENROUTER_SMOKE=1 to enable")
	}
	apiKey := os.Getenv("TINYFLAGS_API_KEY")
	if apiKey == "" {
		t.Skip("set TINYFLAGS_API_KEY to enable")
	}
	client := openrouter.New("https://openrouter.ai/api/v1", apiKey, nil)
	resp, err := client.Complete(context.Background(), core.CompletionRequest{
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
