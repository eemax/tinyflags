package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
	"github.com/eemax/tinyflags/internal/provider"
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: httpClient,
	}
}

var _ provider.Provider = (*Client)(nil)

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Tools          []chatTool    `json:"tools,omitempty"`
	ResponseFormat any           `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (c *Client) Complete(ctx context.Context, req core.CompletionRequest) (core.CompletionResponse, error) {
	if c.apiKey == "" {
		return core.CompletionResponse{}, cerr.New(cerr.ExitRuntime, "missing API key")
	}
	requestCtx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > req.Timeout {
			requestCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		}
	}
	defer cancel()

	body := chatRequest{
		Model:    req.Model,
		Messages: make([]chatMessage, 0, len(req.Messages)),
	}
	for _, msg := range req.Messages {
		body.Messages = append(body.Messages, toChatMessage(msg))
	}
	for _, tool := range req.Tools {
		body.Tools = append(body.Tools, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}
	if len(req.JSONSchema) > 0 {
		body.ResponseFormat = map[string]any{"type": "json_object"}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return core.CompletionResponse{}, cerr.Wrap(cerr.ExitRuntime, "marshal provider request", err)
	}

	httpReq, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return core.CompletionResponse{}, cerr.Wrap(cerr.ExitRuntime, "create provider request", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if requestCtx.Err() == context.DeadlineExceeded || ctx.Err() == context.DeadlineExceeded {
			return core.CompletionResponse{}, cerr.New(cerr.ExitTimeout, "provider request timed out")
		}
		return core.CompletionResponse{}, cerr.Wrap(cerr.ExitRuntime, "provider request failed", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.CompletionResponse{}, cerr.Wrap(cerr.ExitRuntime, "read provider response", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return core.CompletionResponse{}, cerr.New(cerr.ExitRuntime, "provider authentication failed")
	}
	if resp.StatusCode >= 400 {
		return core.CompletionResponse{}, cerr.New(cerr.ExitRuntime, fmt.Sprintf("provider returned HTTP %d", resp.StatusCode))
	}

	var decoded chatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return core.CompletionResponse{}, cerr.Wrap(cerr.ExitRuntime, "decode provider response", err)
	}
	if decoded.Error != nil {
		return core.CompletionResponse{}, cerr.New(cerr.ExitRuntime, decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return core.CompletionResponse{}, cerr.New(cerr.ExitRuntime, "provider returned no choices")
	}
	choice := decoded.Choices[0]
	content := chatContentToString(choice.Message.Content)
	assistant := core.Message{
		Role:    "assistant",
		Content: content,
		Name:    choice.Message.Name,
	}
	toolCalls := make([]core.ToolCallRequest, 0, len(choice.Message.ToolCalls))
	for _, call := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, core.ToolCallRequest{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: json.RawMessage(call.Function.Arguments),
		})
	}

	refusal := strings.EqualFold(choice.FinishReason, "content_filter")
	return core.CompletionResponse{
		AssistantMessage: assistant,
		ToolCalls:        toolCalls,
		Usage: core.Usage{
			InputTokens:  decoded.Usage.PromptTokens,
			OutputTokens: decoded.Usage.CompletionTokens,
		},
		Raw:     raw,
		Refusal: refusal,
	}, nil
}

func CheckConnectivity(ctx context.Context, baseURL, apiKey string, httpClient *http.Client) error {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("openrouter returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func toChatMessage(msg core.Message) chatMessage {
	out := chatMessage{
		Role:       msg.Role,
		Content:    msg.Content,
		Name:       msg.Name,
		ToolCallID: msg.ToolCallID,
	}
	for _, call := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, chatToolCall{
			ID:   call.ID,
			Type: "function",
			Function: chatFunctionCall{
				Name:      call.Name,
				Arguments: string(call.Arguments),
			},
		})
	}
	return out
}

func chatContentToString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if chunk, ok := item.(map[string]any); ok {
				if text, ok := chunk["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		data, _ := json.Marshal(value)
		return string(data)
	}
}
