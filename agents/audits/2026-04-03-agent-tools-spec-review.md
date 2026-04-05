# Agent Tools Spec Review

Date: 2026-04-03

## Summary

These specs are mixed. The strongest ideas are:

- add a real web search tool
- add a lightweight web fetch tool
- improve planning guidance

The weakest parts are the ones that try to import Claude Code's full interactive runtime into `tinyflags`. This repo is a non-interactive CLI with explicit mode wiring, a small tool interface, and provider behavior centered on OpenRouter, not Anthropic-native nested tool flows.

## Highest-Risk Mismatches

1. `spec-plan-mode-tools.md` is not a good fit as written.
   It assumes interactive approval dialogs, mutable permission context state, plan files outside the current working tree, teammate mailboxes, and sub-agent workflows. `tinyflags` currently implements plan mode as a simple per-run `--plan` backstop plus plan instruction, not a resumable state machine.

2. `spec-skill-tool.md` is not a good fit as written.
   It assumes runtime skill invocation as a tool, injected follow-on messages, context modifiers, compaction survival state, sub-agents, remote skill discovery, MCP command merging, and permission-rule hierarchies. Current `tinyflags` skills are plain prompt text loaded before the run starts.

3. `spec-web-search-tool.md` is strategically mismatched.
   The core idea is good, but the proposed implementation depends on Anthropic-native nested tool calls and streaming event formats. `tinyflags` currently has a single OpenRouter provider path and a simple function-tool loop.

## Lower-Risk / Adaptable Ideas

4. `spec-web-fetch-tool.md` has a useful core, but the scope is too large.
   The good part is first-class URL fetch plus HTML-to-markdown extraction. The risky part is the huge preapproved-domain policy, Anthropic domain preflight dependency, binary persistence, secondary model processing, and redirect choreography through extra model turns.

5. `spec-todo-write-tool.md` is the most portable of the five, but still over-designed for current needs.
   A session-local todo list is reasonable. The fragile parts are silent UI assumptions, per-agent state, verifier-agent nudges, and persistence requirements that introduce a lot of state machinery for limited CLI value.

## Suggested Direction

- Keep plan mode as a run-scoped CLI behavior, not a conversation-state machine.
- Keep skills as pre-run prompt loading unless there is a strong reason to support dynamic invocation.
- Implement web search in a provider-agnostic way, or keep it stubbed until the provider contract is clear.
- If adding web fetch, start with a minimal read-only HTTP GET + HTML/text extraction tool.
- If adding todos, keep them ephemeral and transcript-visible before considering persistence.

## Per-Spec Verdict

- `spec-plan-mode-tools.md`: not recommended as written
- `spec-skill-tool.md`: not recommended as written
- `spec-web-search-tool.md`: good product idea, poor fit as specified
- `spec-web-fetch-tool.md`: good product idea, needs a much smaller v1
- `spec-todo-write-tool.md`: acceptable only as a much smaller optional feature
