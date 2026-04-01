# Flags, Config, and Defaults

This document is the reference for CLI flags, config keys, environment variables, and shipped defaults in the current implementation.

## Invocation Forms

Default run form:

```bash
tinyflags [flags] "prompt"
```

Explicit run form:

```bash
tinyflags run [flags] "prompt"
```

Subcommands:

- `session`
- `mode`
- `skill`
- `config`
- `doctor`
- `version`

## Global Flags

These flags are available on the root command and inherited by subcommands:

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `--config` | string | empty | Override config file location |
| `--format` | string | resolved mode format or `text` | `text` or `json` |
| `--result-only` | bool | `false` | For JSON output, emit only the result payload |
| `--verbose` | bool | `false` | Enable stderr operational summaries for the current run |
| `--debug` | bool | `false` | Enable deeper stderr diagnostics for the current run |

Notes:

- Unsupported `--format` values fail with exit code `8`.
- When the effective run format resolves to `json` through flags, mode settings, or config defaults, failures are emitted as JSON error envelopes on `stdout`.
- By default, stderr is quiet except for failures. `--verbose` promotes the run log level to `info`, and `--debug` promotes it to `debug`.
- When `--config` is not set, config discovery checks `~/.tinyflags/config.toml`, then the nearest repo `config.toml` walking up from the command anchor, then falls back to built-in defaults for non-model settings.

## Run Flags

These flags apply to `tinyflags "prompt"` and `tinyflags run "prompt"`:

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `--mode` | string | resolved from config, default `commander` | Select named mode |
| `--session` | string | empty | Load or create a persistent session |
| `--fork-session` | string | empty | Requires `--session`; forks the source session and runs against the fork |
| `--system` | string | empty | Inline system prompt layer |
| `--skill` | string | empty | Load named skill content |
| `--model` | string | empty | Override mode model with a full model ID or an alias from discovered `models.toml` |
| `--output-schema` | string | empty | Path to JSON Schema for native structured-output requests plus final output validation |
| `--plan` | bool | `false` | Plan-only mode; tools return non-executing results |
| `--timeout` | duration | resolved from mode/config, default `2m` | Hard cap for the full invocation, including post-loop validation and persistence |
| `--max-steps` | int | resolved from mode/config | Provider/tool loop step cap |
| `--max-tool-retries` | int | resolved from mode/config | Consecutive tool failure budget |
| `--fail-on-tool-error` | bool | `false` | Exit immediately on first tool error |
| `--cwd` | string | process working directory | Working directory for bash/file tools |
| `--no-session-save` | bool | `false` | Load session but do not persist the new turn |

## Subcommands

### `tinyflags session`

Subcommands:

- `list`
- `show <name>`
- `delete <name>`
- `clear <name>`
- `export <name>`
- `fork <source> <destination>`

No additional subcommand-specific flags are currently implemented beyond the inherited global flags.

### `tinyflags mode`

Subcommands:

- `list`
- `show <name>`

### `tinyflags skill`

Subcommands:

- `list`
- `show <name>`

### `tinyflags config`

Subcommands:

- `show`
- `path`
- `validate`

`config validate` resolves configured/default models through discovered `models.toml` and validates OpenRouter model compatibility when the public model catalog is reachable. Catalog lookup failures are reported as warnings instead of hard failures.

### `tinyflags doctor`

Runs checks for:

- config parse
- API key presence
- DB accessibility
- skills directory
- shell path
- OpenRouter connectivity
- OpenRouter model catalog and resolved-model capability validation

### `tinyflags version`

Prints version/build info.

## Exit Codes

| Code | Meaning |
| --- | --- |
| `0` | success |
| `1` | internal/config/runtime error |
| `2` | timeout |
| `3` | provider refusal |
| `4` | schema/output validation failure |
| `5` | tool failure |
| `6` | shell command failure |
| `7` | session or persistence failure |
| `8` | invalid CLI usage |

## Environment Variables

Supported explicit `TINYFLAGS_*` variables:

| Variable | Maps To |
| --- | --- |
| `TINYFLAGS_API_KEY` | `api_key` |
| `TINYFLAGS_BASE_URL` | `base_url` |
| `TINYFLAGS_DEFAULT_MODE` | `default_mode` |
| `TINYFLAGS_DEFAULT_MODEL` | `default_model` |
| `TINYFLAGS_DEFAULT_FORMAT` | `default_format` |
| `TINYFLAGS_DB_PATH` | `db_path` |
| `TINYFLAGS_SKILLS_DIR` | `skills_dir` |
| `TINYFLAGS_SHELL` | `shell` |
| `TINYFLAGS_TIMEOUT` | `timeout` |
| `TINYFLAGS_MAX_STEPS` | `max_steps` |
| `TINYFLAGS_MAX_TOOL_RETRIES` | `max_tool_retries` |
| `TINYFLAGS_LOG_LEVEL` | `log_level` |
| `TINYFLAGS_PLAN_MODE_INSTRUCTION` | `plan_mode_instruction` |

Precedence order:

1. CLI flags
2. Environment variables
3. Config file
4. Built-in defaults

These values come from the inherited OS process environment. `tinyflags` does not auto-load a `.env` file.

## Config File

Discovery order when `--config` is not set:

```text
~/.tinyflags/config.toml
nearest repo config.toml walking up from the command anchor
built-in defaults for non-model settings
```

Built-in defaults do not include a runnable model default. Runs still require a model from `--model`, `default_model`, a mode-specific `model`, or `TINYFLAGS_DEFAULT_MODEL`.

Current config keys and defaults:

| Key | Type | Default |
| --- | --- | --- |
| `version` | int | `1` |
| `api_key` | string | empty |
| `base_url` | string | `https://openrouter.ai/api/v1` |
| `default_mode` | string | `commander` |
| `default_model` | string | empty |
| `default_format` | string | `text` |
| `db_path` | string | `~/.tinyflags/tinyflags.db` |
| `skills_dir` | string | `~/.tinyflags/skills` |
| `shell` | string | `/bin/bash` |
| `shell_args` | `[]string` | `["-lc"]` |
| `timeout` | duration | `2m` |
| `max_steps` | int | `12` |
| `max_tool_retries` | int | `3` |
| `log_level` | string | `error` |
| `plan_mode_instruction` | string | empty in config, runtime falls back to built-in plan text |
| `[modes]` | table | shipped mode definitions shown below |
| `[skills]` | table | optional inline skill strings |

Mode definitions are merged field-by-field with shipped defaults. A partial override such as:

```toml
[modes.commander]
system = "custom only"
```

keeps the unspecified `commander` fields intact.

## Model Catalog File

Model aliases are loaded from `models.toml`, not from `config.toml`.

Discovery order:

- When a config file is loaded, search for the nearest `models.toml` walking up from that loaded config path.
- When `--config` is set and the target file does not exist, search for the nearest `models.toml` walking up from the explicit config path.
- When no config file is loaded, search for the nearest `models.toml` walking up from the command anchor.
- In all cases, `~/.tinyflags/models.toml` is loaded after the repo file and wins on conflicts.

If both files exist, entries are merged and the home file wins on conflicts.

Supported format:

```toml
[models.fast]
id = "openai/gpt-4o-mini"

[models.smart]
id = "anthropic/claude-opus-4.5"
```

Only `id` is read today. Extra keys are ignored for forward compatibility.

### Shipped Modes

#### `text`

| Field | Value |
| --- | --- |
| `description` | `Plain text interaction` |
| `model` | empty |
| `format` | `text` |
| `tools` | `[]` |
| `persist_session` | `true` |
| `store_run_log` | `true` |
| `capture_commands` | `false` |
| `capture_stdout` | `false` |
| `capture_stderr` | `false` |
| `max_steps` | `4` |
| `max_tool_retries` | `0` |
| `timeout` | inherits global timeout |

#### `tool`

| Field | Value |
| --- | --- |
| `description` | `Bounded tool-enabled mode` |
| `model` | empty |
| `format` | `text` |
| `tools` | `["read_file", "write_file"]` |
| `persist_session` | `true` |
| `store_run_log` | `true` |
| `capture_commands` | `true` |
| `capture_stdout` | `true` |
| `capture_stderr` | `true` |
| `max_steps` | `8` |
| `max_tool_retries` | `3` |
| `timeout` | inherits global timeout |

#### `commander`

| Field | Value |
| --- | --- |
| `description` | `Full shell execution mode` |
| `model` | empty |
| `format` | `text` |
| `tools` | `["bash"]` |
| `persist_session` | `true` |
| `store_run_log` | `true` |
| `capture_commands` | `true` |
| `capture_stdout` | `true` |
| `capture_stderr` | `true` |
| `max_steps` | `12` |
| `max_tool_retries` | `3` |
| `timeout` | inherits global timeout |

## Mode Schema

Each mode currently supports:

| Field | Type | Meaning |
| --- | --- | --- |
| `description` | string | Human-readable summary |
| `model` | string | Model alias from discovered `models.toml` or full model name |
| `format` | string | `text` or `json` |
| `system` | string | Mode-level system prompt |
| `tools` | `[]string` | Allowed tool names |
| `persist_session` | bool | Whether session-backed runs persist messages |
| `store_run_log` | bool | Whether run, tool-call, and shell-command rows are stored in SQLite |
| `capture_commands` | bool | Whether shell command strings appear in persisted shell-command rows and final JSON command summaries |
| `capture_stdout` | bool | Whether shell stdout is stored in persisted shell-command rows |
| `capture_stderr` | bool | Whether shell stderr is stored in persisted shell-command rows |
| `max_steps` | int | Loop step cap |
| `max_tool_retries` | int | Consecutive tool failure budget |
| `timeout` | duration | Mode-specific timeout override |

## Skill Lookup Order

For `--skill <name>`, the current lookup order is:

1. `<cwd>/.tinyflags/skills/<name>.md`
2. `~/.tinyflags/skills/<name>.md`
3. inline config entry under `[skills]`

## Current Tool Set

Registered tools:

- `bash`
- `read_file`
- `write_file`
- `web_search`

Default mode availability:

- `text`: none
- `tool`: `read_file`, `write_file`
- `commander`: `bash`

`web_search` is registered but currently behaves as a stub and returns `status = "unavailable"`.
