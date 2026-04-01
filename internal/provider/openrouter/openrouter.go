package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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

type ModelCatalog struct {
	models map[string]ModelInfo
}

type ModelInfo struct {
	ID                  string
	Name                string
	SupportedParameters []string
}

const (
	generationMetadataTimeout = 100 * time.Millisecond
	generationMetadataSlack   = 50 * time.Millisecond
	maxResponseBytes          = 32 << 20 // 32 MB
	maxRetries                = 3
)

var retryBackoffs = [maxRetries]time.Duration{500 * time.Millisecond, 1 * time.Second, 2 * time.Second}

func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: defaultHTTPClient(httpClient),
	}
}

var _ provider.Provider = (*Client)(nil)

type chatRequest struct {
	Model          string              `json:"model"`
	Messages       []chatMessage       `json:"messages"`
	Tools          []chatTool          `json:"tools,omitempty"`
	ResponseFormat any                 `json:"response_format,omitempty"`
	Provider       *providerPreference `json:"provider,omitempty"`
}

type providerPreference struct {
	RequireParameters bool `json:"require_parameters,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	Refusal    string         `json:"refusal,omitempty"`
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
	ID                string       `json:"id"`
	Model             string       `json:"model"`
	SystemFingerprint string       `json:"system_fingerprint,omitempty"`
	Choices           []chatChoice `json:"choices"`
	Usage             struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *providerErrorBody `json:"error,omitempty"`
}

type chatChoice struct {
	Message            chatMessage `json:"message"`
	FinishReason       string      `json:"finish_reason"`
	NativeFinishReason string      `json:"native_finish_reason,omitempty"`
}

type providerErrorBody struct {
	Code     int            `json:"code,omitempty"`
	Message  string         `json:"message"`
	Type     string         `json:"type,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type catalogResponse struct {
	Data []catalogModel `json:"data"`
}

type catalogModel struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	SupportedParameters []string `json:"supported_parameters"`
}

type generationResponse struct {
	Data generationData `json:"data"`
}

type generationData struct {
	ID                 string                      `json:"id"`
	UpstreamID         string                      `json:"upstream_id"`
	Model              string                      `json:"model"`
	FinishReason       string                      `json:"finish_reason"`
	NativeFinishReason string                      `json:"native_finish_reason"`
	ProviderName       string                      `json:"provider_name"`
	Router             string                      `json:"router"`
	Usage              float64                     `json:"usage"`
	TotalCost          float64                     `json:"total_cost"`
	Latency            float64                     `json:"latency"`
	ProviderResponses  []generationProviderAttempt `json:"provider_responses"`
}

type generationProviderAttempt struct {
	ID             string  `json:"id,omitempty"`
	EndpointID     string  `json:"endpoint_id,omitempty"`
	ModelPermaslug string  `json:"model_permaslug,omitempty"`
	ProviderName   string  `json:"provider_name,omitempty"`
	Status         int     `json:"status,omitempty"`
	Latency        float64 `json:"latency,omitempty"`
	IsBYOK         bool    `json:"is_byok,omitempty"`
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

	body, err := buildChatRequest(req)
	if err != nil {
		return core.CompletionResponse{}, err
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return core.CompletionResponse{}, cerr.Wrap(cerr.ExitRuntime, "marshal provider request", err)
	}

	raw, statusCode, err := c.doWithRetry(requestCtx, ctx, payload)
	if err != nil {
		return core.CompletionResponse{}, err
	}
	if statusCode >= 400 {
		return core.CompletionResponse{}, wrapProviderHTTPError(statusCode, raw)
	}

	var decoded chatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return core.CompletionResponse{}, cerr.Wrap(cerr.ExitRuntime, "decode provider response", err)
	}
	if decoded.Error != nil {
		metadata := responseMetadata(decoded, nil)
		metadata.Error = &core.ProviderErrorDetail{
			Message:  decoded.Error.Message,
			Type:     decoded.Error.Type,
			Code:     decoded.Error.Code,
			Metadata: decoded.Error.Metadata,
		}
		return core.CompletionResponse{}, &core.ProviderError{
			Err:      cerr.New(cerr.ExitRuntime, decoded.Error.Message),
			Metadata: metadata,
		}
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

	metadata := responseMetadata(decoded, &choice)
	metadata = c.enrichGenerationMetadata(requestCtx, metadata)
	refusal := metadata.Refusal != "" || strings.EqualFold(choice.FinishReason, "content_filter")

	return core.CompletionResponse{
		AssistantMessage: assistant,
		ToolCalls:        toolCalls,
		Usage: core.Usage{
			InputTokens:  decoded.Usage.PromptTokens,
			OutputTokens: decoded.Usage.CompletionTokens,
		},
		ProviderMetadata: metadata,
		Raw:              raw,
		Refusal:          refusal,
	}, nil
}

func (c *Client) doWithRetry(requestCtx, parentCtx context.Context, payload []byte) ([]byte, int, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		httpReq, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			return nil, 0, cerr.Wrap(cerr.ExitRuntime, "create provider request", err)
		}
		setCommonHeaders(httpReq, c.apiKey)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			if requestCtx.Err() == context.DeadlineExceeded || parentCtx.Err() == context.DeadlineExceeded {
				return nil, 0, cerr.New(cerr.ExitTimeout, "provider request timed out")
			}
			return nil, 0, cerr.Wrap(cerr.ExitRuntime, "provider request failed", err)
		}

		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			return nil, 0, cerr.Wrap(cerr.ExitRuntime, "read provider response", readErr)
		}

		if !retryableStatus(resp.StatusCode) || attempt >= maxRetries {
			return raw, resp.StatusCode, nil
		}

		lastErr = fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
		_ = lastErr

		backoff := retryBackoffs[attempt]
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
				parsed := time.Duration(seconds) * time.Second
				if parsed > backoff {
					backoff = parsed
				}
			}
		}
		if deadline, ok := requestCtx.Deadline(); ok && time.Until(deadline) < backoff {
			return raw, resp.StatusCode, nil
		}

		timer := time.NewTimer(backoff)
		select {
		case <-requestCtx.Done():
			timer.Stop()
			return nil, 0, cerr.New(cerr.ExitTimeout, "provider request timed out")
		case <-timer.C:
		}
	}
}

func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func (c *Client) FetchModelCatalog(ctx context.Context) (*ModelCatalog, error) {
	return FetchModelCatalog(ctx, c.baseURL, c.apiKey, c.httpClient)
}

func FetchModelCatalog(ctx context.Context, baseURL, apiKey string, httpClient *http.Client) (*ModelCatalog, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(apiKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := defaultHTTPClient(httpClient).Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("openrouter returned HTTP %d", resp.StatusCode)
	}

	var decoded catalogResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}

	catalog := &ModelCatalog{models: make(map[string]ModelInfo, len(decoded.Data))}
	for _, item := range decoded.Data {
		catalog.models[item.ID] = ModelInfo{
			ID:                  item.ID,
			Name:                item.Name,
			SupportedParameters: append([]string(nil), item.SupportedParameters...),
		}
	}
	return catalog, nil
}

func CheckConnectivity(ctx context.Context, baseURL, apiKey string, httpClient *http.Client) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := defaultHTTPClient(httpClient).Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("openrouter returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *ModelCatalog) Lookup(id string) (ModelInfo, bool) {
	if c == nil {
		return ModelInfo{}, false
	}
	info, ok := c.models[id]
	return info, ok
}

func (m ModelInfo) SupportsParameter(name string) bool {
	for _, item := range m.SupportedParameters {
		if item == name {
			return true
		}
	}
	return false
}

func buildChatRequest(req core.CompletionRequest) (chatRequest, error) {
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
		var schemaDoc any
		if err := json.Unmarshal(req.JSONSchema, &schemaDoc); err != nil {
			return chatRequest{}, cerr.Wrap(cerr.ExitSchemaValidation, "parse output schema", err)
		}
		body.ResponseFormat = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "tinyflags_output",
				"strict": true,
				"schema": schemaDoc,
			},
		}
	}
	if len(req.Tools) > 0 || len(req.JSONSchema) > 0 {
		body.Provider = &providerPreference{RequireParameters: true}
	}
	return body, nil
}

func responseMetadata(decoded chatResponse, choice *chatChoice) core.ProviderMetadata {
	metadata := core.ProviderMetadata{
		ResponseID:        decoded.ID,
		ResponseModel:     decoded.Model,
		SystemFingerprint: decoded.SystemFingerprint,
	}
	if choice != nil {
		metadata.FinishReason = choice.FinishReason
		metadata.NativeFinishReason = choice.NativeFinishReason
		metadata.Refusal = choice.Message.Refusal
	}
	return metadata
}

func (c *Client) enrichGenerationMetadata(ctx context.Context, metadata core.ProviderMetadata) core.ProviderMetadata {
	if metadata.ResponseID == "" || ctx.Err() != nil {
		return metadata
	}
	generationCtx, cancel, ok := generationMetadataContext(ctx)
	if !ok {
		return metadata
	}
	defer cancel()

	info, err := c.fetchGeneration(generationCtx, metadata.ResponseID)
	if err != nil {
		return metadata
	}
	if info.Model != "" {
		metadata.ResponseModel = info.Model
	}
	if info.FinishReason != "" {
		metadata.FinishReason = info.FinishReason
	}
	if info.NativeFinishReason != "" {
		metadata.NativeFinishReason = info.NativeFinishReason
	}
	extra := map[string]any{}
	if info.UpstreamID != "" {
		extra["upstream_id"] = info.UpstreamID
	}
	if info.ProviderName != "" {
		extra["provider_name"] = info.ProviderName
	}
	if info.Router != "" {
		extra["router"] = info.Router
	}
	if info.Usage != 0 {
		extra["usage"] = info.Usage
	}
	if info.TotalCost != 0 {
		extra["total_cost"] = info.TotalCost
	}
	if info.Latency != 0 {
		extra["latency"] = info.Latency
	}
	if len(info.ProviderResponses) > 0 {
		extra["provider_responses"] = info.ProviderResponses
	}
	if len(extra) > 0 {
		if metadata.Extra == nil {
			metadata.Extra = map[string]any{}
		}
		for key, value := range extra {
			metadata.Extra[key] = value
		}
	}
	return metadata
}

func generationMetadataContext(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	if ctx == nil {
		generationCtx, cancel := context.WithTimeout(context.Background(), generationMetadataTimeout)
		return generationCtx, cancel, true
	}
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) <= generationMetadataTimeout+generationMetadataSlack {
			return nil, func() {}, false
		}
	}
	generationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), generationMetadataTimeout)
	return generationCtx, cancel, true
}

func (c *Client) fetchGeneration(ctx context.Context, id string) (generationData, error) {
	query := url.Values{}
	query.Set("id", id)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/generation?"+query.Encode(), nil)
	if err != nil {
		return generationData{}, err
	}
	setCommonHeaders(httpReq, c.apiKey)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return generationData{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return generationData{}, fmt.Errorf("openrouter returned HTTP %d", resp.StatusCode)
	}
	var decoded generationResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return generationData{}, err
	}
	return decoded.Data, nil
}

func wrapProviderHTTPError(statusCode int, raw []byte) error {
	metadata, message := decodeHTTPError(statusCode, raw)
	return &core.ProviderError{
		Err:      cerr.New(cerr.ExitRuntime, message),
		Metadata: metadata,
	}
}

func decodeHTTPError(statusCode int, raw []byte) (core.ProviderMetadata, string) {
	var decoded struct {
		ID                string             `json:"id"`
		Model             string             `json:"model"`
		SystemFingerprint string             `json:"system_fingerprint,omitempty"`
		Error             *providerErrorBody `json:"error,omitempty"`
	}
	_ = json.Unmarshal(raw, &decoded)

	metadata := core.ProviderMetadata{
		ResponseID:        decoded.ID,
		ResponseModel:     decoded.Model,
		SystemFingerprint: decoded.SystemFingerprint,
	}
	message := fmt.Sprintf("provider returned HTTP %d", statusCode)
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		message = "provider authentication failed"
	}
	if decoded.Error != nil {
		if decoded.Error.Message != "" {
			message = decoded.Error.Message
		}
		metadata.Error = &core.ProviderErrorDetail{
			HTTPStatus: statusCode,
			Code:       decoded.Error.Code,
			Type:       decoded.Error.Type,
			Message:    decoded.Error.Message,
			Metadata:   decoded.Error.Metadata,
		}
		return metadata, message
	}
	metadata.Error = &core.ProviderErrorDetail{
		HTTPStatus: statusCode,
		Message:    message,
	}
	return metadata, message
}

func setCommonHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
}

func defaultHTTPClient(httpClient *http.Client) *http.Client {
	if httpClient == nil {
		return http.DefaultClient
	}
	return httpClient
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
