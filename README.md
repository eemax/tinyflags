# tinyflags

`tinyflags` is a headless, scriptable AI agent harness for the command line.

It is built for non-interactive execution in shell scripts, CI/CD jobs, cron tasks, and automation workflows. The CLI accepts a prompt, optionally reads stdin, runs an agent loop under a selected mode, emits machine-usable output on stdout, writes diagnostics to stderr, and exits with stable exit codes.

By default, stderr stays quiet except for failures. Use `--verbose` or `--debug` when you want operational logs for a run.

## Status

This repository contains a greenfield v2 implementation with:

- Cobra-based CLI with `run`, `session`, `mode`, `skill`, `config`, `doctor`, and `version` commands
- Config and model discovery with explicit precedence: flags > env vars > discovered config/models files > built-in defaults for non-model settings
- Explicit provider and tool registries
- SQLite-backed sessions and run logging
- OpenRouter Chat Completions provider
- Shipped tools: `bash`, `read_file`, `write_file`, and `web_search` stub
- Text and JSON output modes
- Output schema validation via JSON Schema
- A growing automated test suite across CLI, runtime, persistence, tools, and provider layers

## Install and Run

There are three useful ways to run `tinyflags`, and they behave differently during development.

### 1. Install globally

This is the normal user-facing setup. It makes `tinyflags` available on your `PATH`.

```bash
go install ./cmd/tinyflags
```

Or:

```bash
make install
```

After that, run:

```bash
tinyflags version
```

If `tinyflags` is not found, make sure your Go bin directory is on `PATH`.
Typical locations are:

- `$GOBIN`
- `$(go env GOPATH)/bin`

Important:
`go install` copies a built snapshot of the current source into your Go bin directory.
If you change the code later, you must run `go install ./cmd/tinyflags` again to refresh the global `tinyflags` binary.

### 2. Build a local dev binary

This is useful when you want a repo-local binary for quick repeated testing.

```bash
go build -o tinyflags ./cmd/tinyflags
```

Or:

```bash
make build
```

Then run:

```bash
./tinyflags version
```

Important:
`./tinyflags` runs the binary file sitting in the current directory.
If you change the source code later, you must rebuild it to update that local binary.

### 3. Run directly from source

This is the fastest option while actively developing.

```bash
go run ./cmd/tinyflags version
```

Or:

```bash
make run ARGS='version'
```

Important:
`go run` recompiles from the current source each time, so you do not need a separate rebuild step between code changes.

## Quick Start

Inspect the resolved default mode:

```bash
tinyflags mode show commander
```

Run a prompt:

```bash
tinyflags "summarize this repository"
```

Pass stdin as an additional user-content block:

```bash
cat notes.txt | tinyflags "extract action items"
```

Request JSON output:

```bash
tinyflags --format json "describe the current directory"
```

For local-only development, the equivalent `./tinyflags ...` commands work too after `make build`.

## Safety Model

The capability boundary is the selected mode.

- `text` has no tools
- `tool` allows local file read/write tools
- `commander` allows shell execution through `bash`

`commander` is intentionally powerful. If the selected mode allows `bash`, model-generated commands run with the current host privileges and inherited environment. There is no built-in command safety filter in v1.

Use `--plan` to request a plan-only run that avoids side effects and causes tools to return synthetic non-executing results.

## Configuration

Config discovery when `--config` is not set:

```text
~/.tinyflags/config.toml
nearest repo config.toml walking up from the command anchor
built-in defaults for non-model settings
```

Minimal `config.toml` example:

```toml
version = 1
api_key = "sk-..."
default_mode = "commander"
default_model = "openai/gpt-4.1"
db_path = "~/.tinyflags/tinyflags.db"
skills_dir = "~/.tinyflags/skills"
```

Built-in defaults do not include a runnable model. Runs need a model from `--model`, `default_model`, a mode-specific `model`, or `TINYFLAGS_DEFAULT_MODEL`.

Model aliases live in `models.toml`, not in `config.toml`. When a config file is loaded, repo-style `models.toml` discovery starts from that config path; when `--config` points at a missing file it starts from the explicit config path; otherwise it starts from the command anchor. This repository ships a repo-root [config.toml](/Users/ysera/tinyflags/config.toml) and [models.toml](/Users/ysera/tinyflags/models.toml) so local runs resolve aliases like `fast`, `smart`, and `ops` without relying on harness-built-in model defaults.

Environment support uses the inherited OS process environment only. `tinyflags` does not auto-load a `.env` file.

See:

- [docs/flags.md](/Users/ysera/tinyflags/docs/flags.md) for flags, env vars, config keys, and defaults
- [docs/architecture.md](/Users/ysera/tinyflags/docs/architecture.md) for runtime flow and package layout

## Repository Guide

- [AGENTS.md](/Users/ysera/tinyflags/AGENTS.md) is the repo playbook for implementation and review agents
- [docs/architecture.md](/Users/ysera/tinyflags/docs/architecture.md) describes the runtime design
- [docs/flags.md](/Users/ysera/tinyflags/docs/flags.md) is the CLI/config reference
- [agents/plans/README.md](/Users/ysera/tinyflags/agents/plans/README.md) explains how to store implementation plans
- [agents/audits/README.md](/Users/ysera/tinyflags/agents/audits/README.md) explains how to store audits and reviews

## Testing

Run the main suite:

```bash
go test ./...
```

Or:

```bash
make test
```

Optional OpenRouter smoke test:

```bash
TINYFLAGS_OPENROUTER_SMOKE=1 TINYFLAGS_API_KEY=sk-... go test ./internal/provider/openrouter -run TestOpenRouterSmoke
```

The env-gated smoke suite exercises plain text, tool-calling, and strict-schema OpenRouter requests.

## Current Limitations

- Only the OpenRouter provider is implemented
- `web_search` is registered as a stub and returns `unavailable`
- Session trimming/summarization is not implemented yet
- There is no sandbox or shell-command safety filtering
