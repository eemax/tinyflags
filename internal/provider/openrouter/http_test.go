package openrouter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/provider/openrouter"
)

func TestCompleteSendsExpectedRequestAndNormalizesResponse(t *testing.T) {
	var capturedPath string
	var capturedAuth string
	var capturedRequest map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&capturedRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content": "done",
						"tool_calls": []any{
							map[string]any{
								"id":   "call-1",
								"type": "function",
								"function": map[string]any{
									"name":      "bash",
									"arguments": `{"command":"echo hi"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     11,
				"completion_tokens": 7,
			},
		})
	}))
	defer server.Close()

	client := openrouter.New(server.URL, "secret", server.Client())
	resp, err := client.Complete(context.Background(), core.CompletionRequest{
		Model: "openai/gpt-4.1",
		Messages: []core.Message{
			{Role: "system", Content: "system"},
			{Role: "user", Content: "hello"},
		},
		Tools: []core.ToolSpec{{
			Name:        "bash",
			Description: "Execute shell commands",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		JSONSchema: []byte(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}

	if capturedPath != "/chat/completions" {
		t.Fatalf("path = %q", capturedPath)
	}
	if capturedAuth != "Bearer secret" {
		t.Fatalf("auth = %q", capturedAuth)
	}
	if capturedRequest["model"] != "openai/gpt-4.1" {
		t.Fatalf("model = %#v", capturedRequest["model"])
	}
	if _, ok := capturedRequest["response_format"]; !ok {
		t.Fatal("expected response_format for schema-guided request")
	}
	if resp.AssistantMessage.Content != "done" {
		t.Fatalf("assistant content = %q", resp.AssistantMessage.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "bash" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestCompleteMapsUnauthorizedToRuntimeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := openrouter.New(server.URL, "secret", server.Client())
	_, err := client.Complete(context.Background(), core.CompletionRequest{
		Model:    "openai/gpt-4.1",
		Messages: []core.Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	exitErr, ok := err.(*cerr.ExitCodeError)
	if !ok || exitErr.Code != cerr.ExitRuntime {
		t.Fatalf("error = %#v", err)
	}
}

func TestCheckConnectivityUsesModelsEndpoint(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Fatalf("auth = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := openrouter.CheckConnectivity(context.Background(), server.URL, "secret", server.Client()); err != nil {
		t.Fatalf("CheckConnectivity returned error: %v", err)
	}
	if !called {
		t.Fatal("expected server to be called")
	}
}
