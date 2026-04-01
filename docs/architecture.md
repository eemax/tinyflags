# Architecture

## Overview

`tinyflags` is a non-interactive CLI agent runtime. Each invocation resolves configuration and mode settings, assembles a prompt stack, runs a provider/tool loop under a single invocation timeout, optionally persists state, renders a final result to stdout, and exits with a stable exit code.

The implementation is intentionally explicit:

- providers are registered directly
- tools are registered directly
- runtime behavior flows through a fully resolved `ResolvedMode`
- packages below the CLI do not call `os.Exit`
- logging and persistence are attached through agent hooks instead of being hard-coded into the loop

## Invocation Flow

High-level flow for `tinyflags [flags] "prompt"`:

1. `cmd/tinyflags/main.go` constructs the CLI app and delegates to `internal/cli`.
2. `internal/cli` parses args and global flags, reads stdin when present, resolves the working directory, and loads config.
3. `internal/mode` converts config plus runtime overrides into an immutable `ResolvedMode`.
4. `internal/skill` resolves optional skill content from project-local, global, or inline config sources.
5. `internal/schema` loads JSON schema bytes when `--output-schema` is set.
6. `internal/session` loads or creates the session, or forks it when requested.
7. `internal/agent` assembles the prompt stack and drives the provider/tool loop.
8. `internal/hooks` composes stderr logging hooks and optional SQLite run-log hooks for run/tool telemetry.
9. `internal/cli` validates schema output, persists session messages when enabled, and finalizes the run record with the true terminal status.
10. `internal/output` renders the final text or JSON result to stdout.
11. `internal/cli` maps failures to stable exit codes and writes user-facing errors to stderr.

## Prompt Assembly

The agent loop builds messages in this order:

1. mode system prompt
2. skill prompt
3. inline `--system`
4. plan instruction, when `--plan` is set
5. JSON-schema instruction, when `--output-schema` is set
6. prior session history
7. prompt argument
8. stdin content as a separate user message

The current implementation injects the schema instruction as an additional system message after the plan message so the provider is told to return JSON-only output before the first round trip.

## Package Layout

Core packages:

- `internal/cli`
  Cobra command tree, runtime request construction, top-level error rendering, doctor/config/session/mode/skill commands.
- `internal/config`
  Defaults, config file parsing, explicit env-var merge, path expansion.
- `internal/mode`
  Resolves immutable `ResolvedMode` values.
- `internal/skill`
  Skill discovery and loading.
- `internal/provider`
  Provider interface and registry.
- `internal/provider/openrouter`
  OpenRouter Chat Completions HTTP client and health check.
- `internal/agent`
  Iterative completion/tool loop and lifecycle hook interface.
- `internal/hooks`
  Hook composition helper.
- `internal/tools`
  Tool interface, registry, exec context, tool-result message helper.
- `internal/tools/bash`
  Shell execution tool with plan-mode backstop and separate stdout/stderr capture.
- `internal/tools/files`
  Local read/write tools with plan-mode backstop.
- `internal/tools/websearch`
  Stub tool that returns `unavailable`.
- `internal/session`
  SQLite-backed session and session-admin operations.
- `internal/store`
  Database bootstrap, run logger, and persistence hooks.
- `internal/output`
  Text and JSON renderers.
- `internal/schema`
  JSON Schema loading and final-output validation.
- `internal/errors`
  `ExitCodeError` and stable exit-code constants.
- `internal/logging`
  stderr logger and logging hooks.

## Provider Layer

`internal/provider/openrouter` converts normalized runtime requests into OpenRouter Chat Completions requests:

- `messages` map from internal `core.Message`
- `tools` map to function tools
- `response_format = {"type":"json_object"}` is set when schema output is requested

The response is normalized back into:

- assistant text
- tool-call requests
- token usage
- refusal detection when the finish reason indicates content filtering

## Tool System

Tools are admitted only when:

- the tool is registered in the binary
- the tool is allowed by the selected mode

Current shipped tools:

- `bash`
- `read_file`
- `write_file`
- `web_search` stub

The `ExecContext` is resolved before tool execution and carries:

- effective working directory
- resolved mode
- run-scoped logging handle
- plan-only flag
- shell binary and shell args

## Persistence Model

SQLite is the single persistence backend.

Tables:

- `sessions`
- `messages`
- `runs`
- `tool_calls`
- `shell_commands`

Important persistence behaviors:

- session delete cascades messages
- run rows keep history by nulling `runs.session_id` on session deletion
- session clear removes messages but preserves the session row
- persisted session messages record their originating `run_id` when run logging is enabled
- run, tool-call, and shell-command logging are attached via hooks
- run logging hooks are installed only when `ResolvedMode.StoreRunLog` is true
- final run status is written after schema validation and session persistence so the `runs` table reflects the real CLI outcome
- shell-command rows honor `capture_commands`, `capture_stdout`, and `capture_stderr`
- hook persistence failures are treated as run failures instead of being silently ignored
- database schema state is versioned through SQLite `PRAGMA user_version`

## Output Contract

- `stdout` contains final program output only
- `stderr` contains logs, warnings, diagnostics, and failures
- operational summaries are quiet by default; `--verbose`, `--debug`, or config `log_level = "info"/"debug"` enable them

Renderers:

- text renderer writes only the assistant text, or validated JSON bytes when schema mode is active
- JSON renderer writes either the stable result envelope or only the result payload when `--result-only` is set
- when the effective run format is `json`, top-level failures are emitted as JSON error envelopes on stdout as well

## Testing Strategy

The repository relies on a mix of:

- unit tests for config, schema, output, and tools
- agent-loop tests with fake providers/tools
- CLI tests with fake provider injection and temp SQLite databases
- persistence tests over real temp SQLite files
- HTTP-level provider tests with `httptest`
- optional env-gated OpenRouter smoke coverage
