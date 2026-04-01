package openrouter_test

import (
	"context"
	"encoding/json"
	"errors"
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
	var generationCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/completions":
			capturedPath = r.URL.Path
			capturedAuth = r.Header.Get("Authorization")
			if err := json.NewDecoder(r.Body).Decode(&capturedRequest); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":                 "gen-123",
				"model":              "openai/gpt-4.1",
				"system_fingerprint": "fp-test",
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
		case "/generation":
			generationCalls++
			if got := r.URL.Query().Get("id"); got != "gen-123" {
				t.Fatalf("generation id = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"id":                   "gen-123",
					"upstream_id":          "chatcmpl-abc",
					"model":                "openai/gpt-4.1",
					"finish_reason":        "tool_calls",
					"native_finish_reason": "tool_use",
					"provider_name":        "OpenAI",
					"router":               "openrouter/auto",
					"usage":                0.001,
					"total_cost":           0.002,
					"latency":              1234,
					"provider_responses": []any{
						map[string]any{"id": "provider-resp-1", "provider_name": "OpenAI", "status": 200, "latency": 1234, "is_byok": false},
					},
				},
			})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
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
	responseFormat, ok := capturedRequest["response_format"].(map[string]any)
	if !ok {
		t.Fatal("expected response_format for schema-guided request")
	}
	if responseFormat["type"] != "json_schema" {
		t.Fatalf("response_format.type = %#v", responseFormat["type"])
	}
	jsonSchema, ok := responseFormat["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("response_format.json_schema = %#v", responseFormat["json_schema"])
	}
	if strict, _ := jsonSchema["strict"].(bool); !strict {
		t.Fatalf("strict = %#v", jsonSchema["strict"])
	}
	providerConfig, ok := capturedRequest["provider"].(map[string]any)
	if !ok {
		t.Fatalf("provider config missing: %#v", capturedRequest["provider"])
	}
	if required, _ := providerConfig["require_parameters"].(bool); !required {
		t.Fatalf("require_parameters = %#v", providerConfig["require_parameters"])
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
	if resp.ProviderMetadata.ResponseID != "gen-123" {
		t.Fatalf("response id = %q", resp.ProviderMetadata.ResponseID)
	}
	if resp.ProviderMetadata.ResponseModel != "openai/gpt-4.1" {
		t.Fatalf("response model = %q", resp.ProviderMetadata.ResponseModel)
	}
	if resp.ProviderMetadata.NativeFinishReason != "tool_use" {
		t.Fatalf("native finish reason = %q", resp.ProviderMetadata.NativeFinishReason)
	}
	if generationCalls != 1 {
		t.Fatalf("generation calls = %d", generationCalls)
	}
}

func TestCompleteMapsUnauthorizedToRuntimeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":     401,
				"message":  "bad auth",
				"metadata": map[string]any{"provider": "openrouter"},
			},
		})
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
	var exitErr *cerr.ExitCodeError
	if !errors.As(err, &exitErr) || exitErr.Code != cerr.ExitRuntime {
		t.Fatalf("error = %#v", err)
	}
	metadata, ok := core.ProviderMetadataFromError(err)
	if !ok {
		t.Fatalf("expected provider metadata on error: %#v", err)
	}
	if metadata.Error == nil || metadata.Error.Message != "bad auth" {
		t.Fatalf("provider error metadata = %+v", metadata)
	}
}

func TestCompleteParsesStructuredErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "gen-failed",
			"model": "openai/gpt-4.1",
			"error": map[string]any{
				"code":     400,
				"type":     "invalid_request_error",
				"message":  "schema rejected",
				"metadata": map[string]any{"field": "response_format"},
			},
		})
	}))
	defer server.Close()

	client := openrouter.New(server.URL, "secret", server.Client())
	_, err := client.Complete(context.Background(), core.CompletionRequest{
		Model:      "openai/gpt-4.1",
		Messages:   []core.Message{{Role: "user", Content: "hello"}},
		JSONSchema: []byte(`{"type":"object"}`),
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var exitErr *cerr.ExitCodeError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %#v", err)
	}
	metadata, ok := core.ProviderMetadataFromError(err)
	if !ok {
		t.Fatalf("expected provider metadata on error: %#v", err)
	}
	if metadata.ResponseID != "gen-failed" || metadata.ResponseModel != "openai/gpt-4.1" {
		t.Fatalf("metadata = %+v", metadata)
	}
	if metadata.Error == nil || metadata.Error.Type != "invalid_request_error" {
		t.Fatalf("error metadata = %+v", metadata)
	}
}

func TestFetchModelCatalogParsesSupportedParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{
				map[string]any{
					"id":                   "anthropic/claude-opus-4.5",
					"name":                 "Claude Opus 4.5",
					"supported_parameters": []any{"tools", "structured_outputs"},
				},
			},
		})
	}))
	defer server.Close()

	catalog, err := openrouter.FetchModelCatalog(context.Background(), server.URL, "", server.Client())
	if err != nil {
		t.Fatalf("FetchModelCatalog returned error: %v", err)
	}
	info, ok := catalog.Lookup("anthropic/claude-opus-4.5")
	if !ok {
		t.Fatal("expected model in catalog")
	}
	if !info.SupportsParameter("tools") || !info.SupportsParameter("structured_outputs") {
		t.Fatalf("supported parameters = %#v", info.SupportedParameters)
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
