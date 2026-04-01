# AGENTS.md

This file is the working playbook for humans and coding agents contributing to `tinyflags`.

## Mission

Keep `tinyflags` reliable as a non-interactive CLI harness for automation. Favor predictable behavior, clear boundaries, stable machine-readable output, and well-covered changes over cleverness.

## Core Rules

- Preserve the stdout/stderr contract.
  `stdout` is for final result output only.
  `stderr` is for logs, warnings, diagnostics, and failures.
- Keep exit-code mapping centralized through `ExitCodeError`.
- Do not bypass the mode boundary when adding tools or execution behavior.
- Prefer explicit registration and explicit wiring over reflection or implicit discovery.
- Avoid cross-package concrete coupling. Reach across packages through interfaces when possible.
- Match docs to implementation. If you change flags, config, modes, or runtime semantics, update [docs/flags.md](/Users/ysera/tinyflags/docs/flags.md) and [docs/architecture.md](/Users/ysera/tinyflags/docs/architecture.md) in the same change.

## Working Style

- Start by reading the relevant package and current tests.
- Extend tests before or alongside behavior changes, especially for:
  - exit-code semantics
  - stdout/stderr behavior
  - session persistence
  - provider/tool admission
  - JSON/schema output
- Keep patches small and composable when possible.
- Favor temp directories, fake providers, and isolated SQLite files in tests.

## Expected Artifacts

Use the `agents/` workspace when a task is large enough to deserve explicit planning or auditing.

- Put implementation plans in [agents/plans](/Users/ysera/tinyflags/agents/plans)
- Put review/audit notes in [agents/audits](/Users/ysera/tinyflags/agents/audits)

Recommended naming:

- `agents/plans/YYYY-MM-DD-short-topic.md`
- `agents/audits/YYYY-MM-DD-short-topic.md`

## Change Checklist

- Add or update tests.
- Run `gofmt -w` on changed Go files.
- Run `go test ./...`.
- Update README/docs/playbook files if user-facing behavior changed.
- Call out any remaining gaps or deferred work in the final summary.

## Preferred Test Targets

- `internal/cli` for command behavior and JSON/text envelopes
- `internal/agent` for loop semantics and prompt assembly
- `internal/session` and `internal/store` for persistence guarantees
- `internal/tools/*` for tool behavior and plan-mode backstops
- `internal/provider/openrouter` for HTTP normalization and smoke checks
