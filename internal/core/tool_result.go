package core

import "encoding/json"

// ToolResultCapture describes which shell-derived fields may be retained in
// persisted tool results.
type ToolResultCapture struct {
	CaptureCommands bool
	CaptureStdout   bool
	CaptureStderr   bool
}

// RedactToolResult applies capture settings to a tool result before
// persistence.
func (c ToolResultCapture) RedactToolResult(result ToolResult) ToolResult {
	if result.Command == nil {
		return result
	}
	out := result
	cmd := *result.Command
	cmd.Command = capturedString(c.CaptureCommands, cmd.Command)
	cmd.Stdout = capturedString(c.CaptureStdout, cmd.Stdout)
	cmd.Stderr = capturedString(c.CaptureStderr, cmd.Stderr)
	out.Command = &cmd
	if !c.CaptureStdout && !c.CaptureStderr {
		out.Content = ""
	} else if !c.CaptureStdout {
		out.Content = capturedString(c.CaptureStderr, result.Command.Stderr)
	} else if !c.CaptureStderr {
		out.Content = capturedString(c.CaptureStdout, result.Command.Stdout)
	}
	return out
}

// RedactMessage applies capture settings to a serialized tool message before
// session persistence.
func (c ToolResultCapture) RedactMessage(msg Message) Message {
	if msg.Role != "tool" || msg.Content == "" {
		return msg
	}

	var result ToolResult
	if err := json.Unmarshal([]byte(msg.Content), &result); err != nil {
		return msg
	}
	redacted := c.RedactToolResult(result)
	payload, err := json.Marshal(redacted)
	if err != nil {
		return msg
	}

	out := msg
	out.Content = string(payload)
	if redacted.ToolName != "" {
		out.Name = redacted.ToolName
	}
	if redacted.ToolCallID != "" {
		out.ToolCallID = redacted.ToolCallID
	}
	return out
}

func capturedString(enabled bool, value string) string {
	if !enabled {
		return ""
	}
	return value
}
