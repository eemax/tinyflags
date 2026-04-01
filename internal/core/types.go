package core

import (
	"encoding/json"
	"errors"
	"time"
)

type Config struct {
	Version             int
	APIKey              string            `toml:"api_key"`
	BaseURL             string            `toml:"base_url"`
	DefaultMode         string            `toml:"default_mode"`
	DefaultModel        string            `toml:"default_model"`
	DefaultFormat       string            `toml:"default_format"`
	DBPath              string            `toml:"db_path"`
	SkillsDir           string            `toml:"skills_dir"`
	Shell               string            `toml:"shell"`
	ShellArgs           []string          `toml:"shell_args"`
	Timeout             time.Duration     `toml:"timeout"`
	MaxSteps            int               `toml:"max_steps"`
	MaxToolRetries      int               `toml:"max_tool_retries"`
	LogLevel            string            `toml:"log_level"`
	PlanModeInstruction string            `toml:"plan_mode_instruction"`
	Models              map[string]string `toml:"-"`
	Modes               map[string]ModeConfig
	Skills              map[string]string `toml:"skills"`
}

type ModeConfig struct {
	Description     string        `toml:"description"`
	Model           string        `toml:"model"`
	Format          string        `toml:"format"`
	System          string        `toml:"system"`
	Tools           []string      `toml:"tools"`
	PersistSession  bool          `toml:"persist_session"`
	StoreRunLog     bool          `toml:"store_run_log"`
	CaptureCommands bool          `toml:"capture_commands"`
	CaptureStdout   bool          `toml:"capture_stdout"`
	CaptureStderr   bool          `toml:"capture_stderr"`
	MaxSteps        int           `toml:"max_steps"`
	MaxToolRetries  int           `toml:"max_tool_retries"`
	Timeout         time.Duration `toml:"timeout"`
}

type ResolvedMode struct {
	Name            string        `json:"name"`
	Description     string        `json:"description"`
	Model           string        `json:"model"`
	Format          string        `json:"format"`
	SystemPrompt    string        `json:"system_prompt"`
	Tools           []string      `json:"tools"`
	PersistSession  bool          `json:"persist_session"`
	StoreRunLog     bool          `json:"store_run_log"`
	CaptureCommands bool          `json:"capture_commands"`
	CaptureStdout   bool          `json:"capture_stdout"`
	CaptureStderr   bool          `json:"capture_stderr"`
	MaxSteps        int           `json:"max_steps"`
	MaxToolRetries  int           `json:"max_tool_retries"`
	Timeout         time.Duration `json:"timeout"`
}

type RuntimeRequest struct {
	Prompt          string
	StdinText       string
	ModeName        string
	SessionName     string
	ForkSessionName string
	ForkedFrom      string
	SystemInline    string
	SkillName       string
	ModelOverride   string
	Format          string
	OutputSchema    string
	ResultOnly      bool
	PlanOnly        bool
	FailOnToolError bool
	MaxToolRetries  int
	Timeout         time.Duration
	MaxSteps        int
	CWD             string
	NoSessionSave   bool
	Verbose         bool
	Debug           bool
	ConfigPath      string
}

type Message struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	Name       string            `json:"name,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCallRequest `json:"tool_calls,omitempty"`
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type ToolCallRequest struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type CommandResult struct {
	Command   string `json:"command"`
	CWD       string `json:"cwd,omitempty"`
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Planned   bool   `json:"planned,omitempty"`
	Executed  bool   `json:"executed"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	ToolError bool   `json:"tool_error,omitempty"`
}

type ToolResult struct {
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name"`
	Status     string         `json:"status"`
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Command    *CommandResult `json:"command,omitempty"`
	Raw        map[string]any `json:"raw,omitempty"`
	Arguments  map[string]any `json:"arguments,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type ProviderErrorDetail struct {
	HTTPStatus int            `json:"http_status,omitempty"`
	Code       int            `json:"code,omitempty"`
	Type       string         `json:"type,omitempty"`
	Message    string         `json:"message,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type ProviderMetadata struct {
	ResponseID         string               `json:"response_id,omitempty"`
	ResponseModel      string               `json:"response_model,omitempty"`
	FinishReason       string               `json:"finish_reason,omitempty"`
	NativeFinishReason string               `json:"native_finish_reason,omitempty"`
	SystemFingerprint  string               `json:"system_fingerprint,omitempty"`
	Refusal            string               `json:"refusal,omitempty"`
	Error              *ProviderErrorDetail `json:"error,omitempty"`
	Extra              map[string]any       `json:"extra,omitempty"`
}

type CompletionRequest struct {
	Model      string
	Messages   []Message
	Tools      []ToolSpec
	MaxSteps   int
	Timeout    time.Duration
	JSONSchema []byte
	PlanOnly   bool
}

type CompletionResponse struct {
	AssistantMessage Message
	ToolCalls        []ToolCallRequest
	Usage            Usage
	ProviderMetadata ProviderMetadata
	Raw              []byte
	Refusal          bool
}

type Session struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type StoredMessage struct {
	ID        int64             `json:"id"`
	SessionID int64             `json:"session_id"`
	RunID     *int64            `json:"run_id,omitempty"`
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	ToolName  string            `json:"tool_name,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	Name      string            `json:"name,omitempty"`
	ToolID    string            `json:"tool_call_id,omitempty"`
	ToolCalls []ToolCallRequest `json:"tool_calls,omitempty"`
}

type RunRecord struct {
	ID                   int64      `json:"id"`
	SessionID            *int64     `json:"session_id,omitempty"`
	ModeName             string     `json:"mode_name"`
	ModelName            string     `json:"model_name"`
	ResponseModel        string     `json:"response_model,omitempty"`
	ProviderResponseID   string     `json:"provider_response_id,omitempty"`
	FinishReason         string     `json:"finish_reason,omitempty"`
	NativeFinishReason   string     `json:"native_finish_reason,omitempty"`
	ProviderMetadataJSON string     `json:"provider_metadata_json,omitempty"`
	Prompt               string     `json:"prompt"`
	StdinText            string     `json:"stdin_text,omitempty"`
	SystemText           string     `json:"system_text,omitempty"`
	SkillName            string     `json:"skill_name,omitempty"`
	CWD                  string     `json:"cwd,omitempty"`
	Format               string     `json:"format"`
	PlanOnly             bool       `json:"plan_only"`
	Status               string     `json:"status"`
	ExitCode             int        `json:"exit_code"`
	StartedAt            time.Time  `json:"started_at"`
	FinishedAt           *time.Time `json:"finished_at,omitempty"`
	DurationMS           int64      `json:"duration_ms,omitempty"`
	InputTokens          int        `json:"input_tokens,omitempty"`
	OutputTokens         int        `json:"output_tokens,omitempty"`
}

type ToolCallRecord struct {
	ID           int64      `json:"id"`
	RunID        int64      `json:"run_id"`
	StepIndex    int        `json:"step_index"`
	ToolName     string     `json:"tool_name"`
	RequestJSON  string     `json:"request_json"`
	ResponseJSON string     `json:"response_json,omitempty"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	DurationMS   int64      `json:"duration_ms,omitempty"`
}

type ShellCommandRecord struct {
	ID         int64      `json:"id"`
	ToolCallID int64      `json:"tool_call_id"`
	Command    string     `json:"command"`
	CWD        string     `json:"cwd,omitempty"`
	ExitCode   int        `json:"exit_code,omitempty"`
	StdoutText string     `json:"stdout_text,omitempty"`
	StderrText string     `json:"stderr_text,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	DurationMS int64      `json:"duration_ms,omitempty"`
}

type CommandSummary struct {
	Command  string `json:"command"`
	CWD      string `json:"cwd,omitempty"`
	ExitCode int    `json:"exit_code"`
}

type AgentResult struct {
	Ok           bool             `json:"ok"`
	Result       string           `json:"result,omitempty"`
	ResultJSON   json.RawMessage  `json:"-"`
	Mode         string           `json:"mode,omitempty"`
	Model        string           `json:"model,omitempty"`
	Session      string           `json:"session,omitempty"`
	ForkedFrom   string           `json:"forked_from,omitempty"`
	Plan         bool             `json:"plan"`
	Steps        int              `json:"steps,omitempty"`
	ToolsUsed    []string         `json:"tools_used,omitempty"`
	Commands     []CommandSummary `json:"commands,omitempty"`
	Usage        Usage            `json:"usage,omitempty"`
	RunID        int64            `json:"-"`
	ExitCode     int              `json:"-"`
	ErrorType    string           `json:"-"`
	ErrorMessage string           `json:"-"`
}

type ProviderError struct {
	Err      error
	Metadata ProviderMetadata
}

func (e *ProviderError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func ProviderMetadataFromError(err error) (ProviderMetadata, bool) {
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr == nil {
		return ProviderMetadata{}, false
	}
	return providerErr.Metadata, true
}

type SessionExport struct {
	Session  Session         `json:"session"`
	Messages []StoredMessage `json:"messages"`
}
