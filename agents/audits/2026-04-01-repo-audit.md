# Repo Audit: tinyflags

Date: 2026-04-01

## Verdict

`tinyflags` is a strong early-stage project, but it is not "perfect" yet.

The repo already has several traits that usually take teams much longer to establish:

- crisp package boundaries
- docs that mostly match implementation
- stable exit-code thinking through `ExitCodeError`
- good test discipline for a greenfield codebase
- explicit, non-magical wiring

That said, there are still a few gaps that matter specifically because the project positions itself as a reliable automation harness.

## What Is Already Strong

- The codebase is small enough to reason about end to end.
- The CLI/runtime/session/provider/tool split is easy to follow.
- The stdout/stderr contract is treated as a real design constraint.
- Tests pass cleanly under `go test ./...`, `go test -race ./...`, and `go vet ./...`.
- `gofmt -l .` is clean.
- The docs are unusually candid about limitations such as the `web_search` stub and lack of sandboxing.

## Findings

### 1. Invocation timeout is configured but not actually enforced

Severity: high

The CLI resolves a timeout into `RuntimeRequest.Timeout`, and the agent forwards `ResolvedMode.Timeout` to the provider request, but no top-level `context.WithTimeout` is created around the run. The OpenRouter client also ignores `CompletionRequest.Timeout`, and the bash tool only times out when the model explicitly supplies `timeout_seconds`.

This means `--timeout` and mode-level timeouts do not currently provide the hard execution bound promised by the CLI docs. For a non-interactive automation harness, that is a core reliability gap.

Relevant code:

- `internal/cli/app.go:233-247`
- `internal/cli/app.go:347-362`
- `internal/agent/agent.go:104-112`
- `internal/tools/bash/bash.go:76-81`

### 2. Run logs can record success before the run has actually succeeded

Severity: high

Run completion is persisted inside the agent loop hook, before schema validation, session persistence, and final rendering happen in the CLI layer. If schema validation fails, session save fails, or output rendering fails, the user sees a failed invocation, but the `runs` table has already been marked `success` with exit code `0`.

That breaks the audit trail for the exact cases where operators will most want it to be trustworthy.

Relevant code:

- `internal/agent/agent.go:222-226`
- `internal/store/store.go:311-328`
- `internal/cli/app.go:367-380`

### 3. Default stderr logging behavior does not match the documented CLI contract

Severity: medium

The docs say `--verbose` enables stderr operational summaries, but the default config sets `log_level = "info"`, and the logger emits info-level lines whenever the level is `info` or `debug`. In practice, normal runs log to stderr by default unless the config is overridden.

This is not a crash bug, but it is a contract mismatch in a tool whose value proposition depends on predictable machine-oriented behavior.

Relevant code and docs:

- `internal/config/config.go:41-56`
- `internal/cli/app.go:782-789`
- `internal/logging/logger.go:22-41`
- `docs/flags.md:32-38`

## What Is Missing To Reach "Perfect"

### Product and runtime

- Make the invocation timeout real across provider calls, tool execution, and the whole run lifecycle.
- Move final run-status persistence to the true end of execution, after validation and session writes, or add a final reconciliation phase.
- Either make quiet stderr the default or update the docs and defaults so they agree.
- Either implement `web_search` or remove the shipped stub until it is real.

### Quality and testing

- Add end-to-end integration tests for the built binary, not just package-level tests.
- Add regression tests for the post-agent failure cases:
  - schema validation failure after a model response
  - session append failure after a successful loop
  - renderer/output failure
- Increase coverage on currently light surfaces such as:
  - session admin paths (`List`, `Show`, `Export`)
  - skill listing
  - logging hooks
  - provider/tool registries

Current total statement coverage from `go tool cover -func`: about 60.4%.

### Repo maturity

- Add CI so formatting, vetting, tests, and race tests run automatically on every change.
- Add a license file if this is meant to be shared outside a private workspace.
- Add a release workflow or at least a documented release checklist for version stamping and binary distribution.
- Add one or two realistic automation examples that demonstrate the stdout/stderr contract and JSON mode in practice.

## Suggested Priority Order

1. Enforce timeout end to end.
2. Fix run-log truthfulness for post-loop failures.
3. Align stderr/logging defaults with the documented UX.
4. Add CI and a small set of end-to-end contract tests.
5. Decide whether `web_search` stays as a stub or is removed until implemented.

## Commands Run During Audit

- `go test ./...`
- `go test -race ./...`
- `go test ./... -coverprofile=/tmp/tinyflags.cover && go tool cover -func=/tmp/tinyflags.cover`
- `go vet ./...`
- `gofmt -l .`
