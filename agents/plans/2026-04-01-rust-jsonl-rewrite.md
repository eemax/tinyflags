# Headless Agent Rewrite Plan

## Thesis

This rewrite should be a smaller, clearer tool built around one idea:

- every run is a session run
- every run uses an explicit `--agent`
- every run can optionally add a `--role`
- every agent file is a full runnable config

We should not rebuild the current architecture in Rust.

We should build a tiny, session-first CLI called `Headless Agent` with command name `headless`, optimized for:

- low per-process memory
- predictable behavior
- simple local configuration
- easy fan-out to hundreds of parallel processes

## Product Shape

### Keep

- non-interactive CLI
- strict stdout/stderr contract
- stable exit codes
- a clean but powerful built-in tool harness
- persistent sessions

### Cut For V1

- SQLite
- provider registries
- tool registries
- mode systems
- profile systems
- hook systems
- doctor/config wizard commands
- built-in agent catalog
- model catalog preflight on every run
- output format switches
- schema-heavy structured output support

### Hard Rules

- `stdout` is final result only
- `stderr` is diagnostics only
- every run is session-backed
- every run requires `--agent`
- `--role` is optional
- `--model` and `--effort` are override flags, not the primary config mechanism
- `agent-name.toml` should be the primary config unit for an agent
- behavior should primarily live in agent and role files, not in many CLI flags
- no async runtime in v1
- no background daemon in v1
- no unbounded transcript loads
- no unbounded tool output buffering

## Product Name

- product name: `Headless Agent`
- CLI command: `headless`

## Core Model

There is only one run shape.

```text
headless --session <name> --agent <name> [--role <name>] [--effort <level>] "prompt"
```

### Agent

An agent is the execution contract and the full runnable configuration for that agent.

It defines:

- model defaults
- model parameters
- allowed tools
- enabled skills
- skill lookup paths
- system prompt
- provider configuration
- timeout

### Role

A role is prompt context.

It defines perspective and framing that can be injected into:

- system prompt assembly
- current user turn prompt assembly

Example:

- `--agent coder`
  chooses the runnable coding agent
- `--role auditor`
  tells that agent to behave like a risk-focused reviewer for this run
- `--effort high`
  temporarily overrides the agent's default effort for this run

That is simpler than a mode matrix and easier to reason about than built-in profiles.

## Agents And Roles

### Agent Files

Agents live in project-local files:

```text
./agents/<agent-name>.toml
```

Example:

```toml
name = "coder"
description = "General coding agent"
base_url = "https://openrouter.ai/api/v1"
api_key = ""
api_key_env = "OPENROUTER_API_KEY"
default_model = "openai/gpt-4.1"
default_effort = "medium"
max_output_tokens = 12000
compaction_at_tokens = 180000
skills_dir = "./skills"
enabled_skills = ["repo", "testing", "release"]
enabled_tools = [
  "read_file",
  "edit_file",
  "write_file",
  "glob",
  "grep",
  "apply_patch",
  "bash",
  "todo_write",
  "skills",
  "web_search",
  "web_fetch",
]
system_prompt_file = "./prompts/coder.md"
timeout = "2h"
```

### Agent File Philosophy

`agent-name.toml` should be the place where you can understand almost everything about how that agent runs.

That includes:

- which model it prefers
- how much reasoning effort it uses by default
- how much output it asks for
- when to compact context
- which skills it can use
- where skills live
- which tools are enabled
- provider details such as base URL
- optional API key or API key env var override

The goal is that you can switch agent behavior mostly by switching agent files, not by inventing more CLI flags.

Prompt content should live in external text files, not inline TOML strings, so large prompts stay easy to edit.

### Role Files

Roles live in project-local files:

```text
./roles/<role-name>.toml
```

Example:

```toml
name = "auditor"
description = "Risk-focused review framing"
system_prompt_file = "./prompts/auditor.md"
user_prefix_file = "./prompts/auditor-user.md"
```

### Resolution Rules

- resolve from `./agents` and `./roles` under `--cwd` or the current directory
- resolve prompt file paths relative to the TOML file that references them
- accept `.md` and `.txt`, treating both as UTF-8 text
- keep loaded prompt content out of the TOML itself
- do not walk parent directories in v1
- do not support inheritance or imports in v1
- fail fast if the requested agent or role file is missing

This keeps behavior obvious and startup cheap.

## Exact Stack

### Language And Build

- Rust stable
- edition `2024`
- Cargo

### Runtime Dependencies

- `lexopt`
  Tiny argv parser. Chosen over `clap` to keep startup and binary size down.
- `serde`
  Shared serialization for config, provider payloads, session records, and JSON output.
- `serde_json`
  JSON and JSONL encoding/decoding.
- `toml`
  Config, agent, and role parsing.
- `ureq`
  Blocking HTTP client. Chosen specifically to avoid Tokio and async overhead.
- `time`
  RFC3339 timestamps and duration parsing/formatting.
- `home`
  `~` expansion for the config path and sessions directory.
- `thiserror`
  Typed errors and centralized exit-code mapping.
- `fs2`
  File locking for per-session append safety.
- `signal-hook`
  SIGINT and SIGTERM handling.

### Dev/Test Dependencies

- `tempfile`
- `assert_cmd`
- `predicates`

### Explicitly Not In The Stack

- `tokio`
- `clap`
- `tracing`
- `anyhow`
- `sqlx`
- `rusqlite`
- plugin frameworks
- dynamic provider loading

### Release Profile

```toml
[profile.release]
lto = "fat"
codegen-units = 1
strip = true
panic = "abort"
opt-level = 3
```

## Parallelization Strategy

We do want parallelization, but the right place for it is outside the binary.

### V1 Concurrency Model

Optimize for:

- many independent OS processes
- one run per process
- no global coordinator
- minimal shared state

That means `headless` itself stays mostly single-threaded and single-run.

The intended fan-out is:

- shell parallelism
- queue workers
- CI jobs
- parent orchestration processes

### Why This Is The Right First Step

For this product, the main enemy is per-process overhead.

A tiny blocking CLI is a better fit than an internal async runtime because it avoids:

- a larger memory floor
- more moving parts
- background state
- hidden concurrency bugs

### Important Session Concurrency Rule

Different sessions should run fully in parallel.

Same-session concurrency needs stronger semantics than just locking, because two runs can otherwise read the same history and append conflicting continuations.

So v1 should use optimistic concurrency:

1. Read session `meta.json` and its `revision`.
2. Load recent transcript tail.
3. Run the provider loop without holding an exclusive lock.
4. Re-acquire the session lock for append.
5. Re-read `meta.json`.
6. If `revision` changed, fail with a session-conflict exit code instead of silently merging.
7. If `revision` is unchanged, append messages and increment `revision`.

This keeps same-session semantics clean without holding a lock across the entire run.

### Future Option

If we ever need true single-process multiplexing, that should be a separate product tier:

- `headless`
  tiny synchronous CLI
- `headlessd`
  optional async broker

That should not be part of v1.

## CLI Surface

### Main Command

```text
headless --session <name|new> --agent <name> [--role <name>] [flags] "prompt"
```

### Minimal Flags

- `--session <name>`
- `--agent <name>`
- `--role <name>`
- `--model <name>`
- `--effort <none|minimal|low|medium|high|xhigh>`
- `--plan`
- `--cwd <path>`
- `--verbose`
- `--debug`

### Minimal Subcommands

- `headless version`
- `headless agent list`
- `headless role list`
- `headless session new`
- `headless session list`
- `headless session show <name>`
- `headless session stop <name>`

Everything else can wait.

## Configuration Shape

Keep the global config flat and small.

Example:

```toml
sessions_dir = "~/.local/share/headless/sessions"
shell = "/bin/bash"
shell_args = ["-lc"]
max_stdin_bytes = 1048576
artifact_preview_bytes = 16384
catastrophic_output_bytes = 16777216
log_level = "error"
```

### Important Simplification

Global config should mostly contain machine-level defaults.

Agent behavior should mostly live in agent files.

Agents and roles are separate files with their own clear locations:

- `./agents/*.toml`
- `./roles/*.toml`

That makes the system easier to inspect and edit.

Keep the flag surface intentionally small.

If a behavior belongs to the agent's identity or way of working, it should usually live in:

- `agents/*.toml`
- `roles/*.toml`

not in ad hoc CLI flags.

### Global Config vs Agent Config

Global config is for machine-level defaults such as:

- sessions path
- shell path
- shell args
- safety rails
- logging defaults

Agent config is for run behavior such as:

- model choice
- effort
- output token preferences
- compaction thresholds
- enabled skills
- enabled tools
- provider overrides
- optional API credentials

## Provider Strategy

### V1 Choice

OpenRouter only.

### Important Simplification

Do not create a provider trait in v1.

Use a direct module:

- `provider/openrouter.rs`

If we need a second provider later, we can refactor then.

### Provider Config Precedence

Recommended precedence:

1. explicit runtime override such as `--model` or `--effort`
2. agent file config
3. global config
4. process environment

This keeps the agent file useful as a portable runnable definition.

For credentials specifically:

1. `agent.api_key`, if set
2. env var named by `agent.api_key_env`, if set
3. global config credential, if supported
4. default process env fallback

## Storage Strategy

### Session Layout

```text
~/.local/share/headless/
  sessions/
    <session-name>/
      meta.json
      messages.jsonl
      lock
      runs/
        <run-id>/
          tool-outputs/
          fetch/
          patches/
```

### `meta.json`

Stores cheap lookup data only:

- session id
- session name
- created/updated timestamps
- revision
- message count
- active agent name
- optional rolling summary
- summary updated timestamp

### `messages.jsonl`

One line per persisted message.

Suggested record shape:

```json
{"v":1,"ts":"2026-04-01T14:00:00Z","run_id":"r_01","role":"user","content":"summarize this repo"}
{"v":1,"ts":"2026-04-01T14:00:02Z","run_id":"r_01","role":"assistant","content":"..."}
{"v":1,"ts":"2026-04-01T14:00:03Z","run_id":"r_02","role":"assistant","tool_calls":[{"id":"call_1","name":"bash","arguments":{"command":"ls"}}]}
{"v":1,"ts":"2026-04-01T14:00:04Z","run_id":"r_02","role":"tool","name":"bash","tool_call_id":"call_1","content":"listing complete","preview":"README.md\nsrc\n...","artifact":{"path":"runs/r_02/tool-outputs/bash-001.stdout","bytes":182734}}
```

### Artifact Principle

Do not lose fidelity by default.

Large outputs should be preserved in full on disk and referenced from transcript messages using:

- artifact path
- byte count
- short preview
- optional hash

That way:

- the full data exists
- the active prompt stays manageable
- the agent can reopen the full artifact through tools when needed

### Locking Model

- lock per session, not globally
- no database
- no global run-log file
- no lock held during provider execution
- exclusive lock only for:
  - create session
  - clear/delete session
  - append new messages
  - increment `revision`

## Memory Strategy

This is where the rewrite wins.

### Rules

- keep full history on disk
- do not arbitrarily discard history
- do not arbitrarily discard tool output
- cap stdin bytes
- cap provider response bytes
- spill large tool and fetch outputs to artifacts on disk
- only truncate or cap catastrophic cases
- never keep a full transcript in memory
- assume hundreds of concurrent processes and choose defaults accordingly

### Important Distinction

The right design is not:

- keep everything inline forever
- or aggressively throw data away

The right design is:

- preserve full fidelity on disk
- inline only what the current model step actually needs

### Suggested Starting Limits

- `max_stdin_bytes = 1 MiB`
- `artifact_preview_bytes = 16 KiB`
- `catastrophic_output_bytes = 16 MiB`
- `max_steps = 8`

These are not normal operating caps on useful output.

They are safety rails for obviously extreme cases.

### Session Load Strategy

On every run:

1. Read `meta.json`.
2. Build the prompt context from:
   - rolling summary, if present
   - recent unsummarized history
   - any explicitly relevant older messages if the context assembler decides they are needed
3. Keep the full transcript on disk.

Do not load the full transcript into memory by default.

Prompt assembly should be adaptive, not driven by an arbitrary fixed message cap.

### Tool Output Strategy

For `bash`:

- preserve stdout and stderr in full as run artifacts
- store a short preview plus metadata in the transcript
- inline the preview into the model context by default
- let the agent reopen the full artifact when needed
- only truncate if output crosses a clearly catastrophic threshold

The same principle should apply to fetched web pages and large patch artifacts.

## Prompt Assembly

Build prompts in a fixed order.

### System Prompt Stack

1. agent system text
2. role system text
3. session summary, if any

### Conversation Stack

4. recent session history
5. current user turn, optionally prefixed by `role.user_prefix`
6. stdin as a separate user message, if present

This gives roles influence in both system framing and the active user turn without making the assembly logic complicated.

If skills are enabled by the agent, skill-derived prompt material should be injected as part of the agent context before recent conversation history.

## Repo Skeleton

Keep it as a single crate with modules, not a workspace.

```text
Cargo.toml
src/
  main.rs
  cli.rs
  app.rs
  error.rs
  exit.rs
  config.rs
  agent_def.rs
  role_def.rs
  output.rs
  prompt.rs
  artifact.rs
  provider/
    mod.rs
    openrouter.rs
  agent/
    mod.rs
    loop.rs
  session/
    mod.rs
    jsonl.rs
  tools/
    mod.rs
    bash.rs
    files.rs
    glob.rs
    grep.rs
    patch.rs
    todo.rs
    skills.rs
    web.rs
  types/
    mod.rs
    config.rs
    message.rs
    result.rs
tests/
  cli_run.rs
  output_contract.rs
  session_jsonl.rs
  agent_role_loading.rs
  tools_bash.rs
  version.rs
```

### Project Runtime Layout

```text
your-project/
  agents/
    coder.toml
    researcher.toml
  roles/
    auditor.toml
    fixer.toml
```

## Runtime Flow

### Fast Path

1. Parse argv with `lexopt`.
2. Load flat config.
3. Resolve `--cwd`.
4. If `--session new`, create a new lowercase ULID-backed session id.
5. For a new session, require `--agent`.
6. For an existing session, load agent identity from session metadata when `--agent` is omitted.
7. If `--agent` is provided for an existing session, require it to match the stored session agent.
8. Load `./agents/<agent>.toml`.
9. Load optional `./roles/<role>.toml`.
10. Load recent session tail and revision.
11. Read stdin if present, under a hard cap.
12. Build the prompt stack.
13. Run the provider/tool loop.
14. Re-open the session lock and verify `revision`.
15. Append new messages and increment `revision`.
16. Render final result to stdout.
17. Exit through one centralized exit-code mapper.

### Session Creation UX

Use two paths:

- `headless session new`
  creates a lowercase ULID and prints only that id to stdout
- `headless --session new --agent coder "prompt"`
  convenience shortcut that creates a new session first, reports the generated id on stderr, and then runs the prompt

This keeps stdout clean while still making session bootstrapping easy.

### Design Constraint

Every step should be understandable by reading a few functions in order.

No hooks, no registries, no hidden side effects.

## Agent Loop

Keep the loop tiny.

### Rules

- hard cap steps
- hard cap consecutive tool failures
- fixed tool dispatch by `match`
- no dynamic registration
- no special telemetry objects in the hot path

### Tool Dispatch

Use:

```rust
match tool_name {
    "bash" => tools::bash::execute(...),
    "read_file" => tools::files::read(...),
    "edit_file" => tools::files::edit(...),
    "write_file" => tools::files::write(...),
    "glob" => tools::glob::execute(...),
    "grep" => tools::grep::execute(...),
    "apply_patch" => tools::patch::execute(...),
    "todo_write" => tools::todo::execute(...),
    "skills" => tools::skills::execute(...),
    "web_search" => tools::web::search(...),
    "web_fetch" => tools::web::fetch(...),
    _ => error,
}
```

An agent file simply selects a subset of these built-in tools.

The `skills` tool should respect the agent's declared `skills_dir` and `enabled_skills`.

## Output Contract

For normal runs:

- `stdout` contains final assistant text only
- `stderr` contains diagnostics only

Do not add a `--format` switch in v1.

If we need richer machine-readable inspection, the source of truth should be:

- session JSONL
- run artifacts on disk

not alternate stdout envelopes.

### Error Rendering

- human-readable error on `stderr`
- stdout remains reserved for final run output

## Testing Strategy

Focus on the parts that protect the product contract.

### Must-Have Tests

- stdout vs stderr behavior
- exit-code mapping
- agent loading behavior
- agent override precedence for `--model` and `--effort`
- API key precedence behavior
- enabled skill loading behavior
- role loading behavior
- prompt assembly order
- session append and tail load behavior
- session revision conflict behavior
- per-session locking behavior
- large output artifact persistence
- preview generation behavior
- `--plan` preventing side effects
- timeout behavior

### Benchmark Harness

Do not start with Criterion.

Start with a simple shell script under `scripts/` that measures:

- `headless version`
- `headless agent list`
- `headless session list`
- `headless --session s1 --agent coder --plan "..."` with no network
- 100 parallel `version` runs
- 100 parallel agent-file loads
- 100 parallel runs against 100 different sessions
- 20 to 50 parallel runs against the same session to confirm conflict handling is cheap and explicit

Use `/usr/bin/time` and a repeatable shell harness first.

## Performance Targets

These are targets, not promises, but they should guide the implementation.

- `headless version` should feel near-instant and stay well under the current Go baseline
- session admin commands should not need a database open
- 100 concurrent lightweight invocations should be practical on a developer machine
- same-session conflicts should fail fast instead of blocking forever
- different-session fan-out should avoid pathological lock contention
- memory should scale mostly with prompt size and bounded buffers, not with framework overhead

## Implementation Order

### Phase 0

- initialize Cargo project
- add release profile
- add centralized exit code type
- add `version` command

### Phase 1

- implement root run command
- load flat config
- load agent and role definitions
- implement runtime override precedence for `--model` and `--effort`
- add OpenRouter request/response path
- add plain stdout/stderr renderers

### Phase 2

- add agent loop with step caps
- add `--plan`
- add built-in tool dispatch

### Phase 3

- add JSONL sessions
- add per-session locking
- add revision conflict checks
- add tail loading
- add session admin commands

### Phase 4

- add `read_file`, `edit_file`, `write_file`, `glob`, `grep`
- add `apply_patch`, `todo_write`, `skills`
- add `bash`, `web_search`, `web_fetch`
- add artifact persistence, previews, and timeout handling

### Phase 5

- add rolling session summary support
- add perf harness
- tune caps using real measurements

## Non-Goals For V1

- feature parity with the current Go codebase
- multiple providers
- a public plugin API
- generalized persistence backends
- built-in profiles or modes
- output format flags
- schema-heavy structured outputs
- rich audit history
- session auto-merge on concurrent writes

## Final Recommendation

The winning version is:

- one crate
- one provider
- one session-first run shape
- explicit `--agent`
- optional `--role`
- project-local `agents/*.toml`
- project-local `roles/*.toml`
- JSONL sessions
- bounded memory everywhere

That gives us a cleaner and easier tool without giving up the ability to run hundreds of processes in parallel.
