# Plan Mode — Essence Spec

## Purpose

Plan mode creates an explicit planning phase before execution for work that is ambiguous, risky, or multi-step.

Its value is not in UI flows or permission machinery. Its value is in forcing the agent to:

- explore before changing things
- make assumptions visible
- break work into an executable sequence
- identify validation steps before implementation

---

## Non-Goals

This spec does not require:

- a TUI or approval dialog
- a persistent permission state machine
- special team, mailbox, or sub-agent workflows
- plan files on disk
- a specific host application's mode system

Those may exist in a host implementation, but they are not part of the core idea.

---

## Core Contract

When plan mode is active:

1. The agent should gather context before proposing changes.
2. The agent should avoid side effects.
3. The agent should produce a concrete implementation plan.
4. The plan should be detailed enough that an execution phase can follow with fewer surprises.

The execution phase may happen in the same conversation, a later turn, or a separate run. This spec does not require a specific approval boundary.

---

## When To Use

Use plan mode when one or more of these are true:

- the task is large enough to span multiple meaningful steps
- the correct implementation is not obvious
- several approaches are possible and tradeoffs matter
- the work touches multiple files, systems, or behaviors
- the cost of a wrong first move is high
- the user explicitly asks for a plan first

---

## When Not To Use

Do not force plan mode for:

- tiny or mechanical edits
- tasks with a single obvious implementation path
- pure explanation requests
- cases where the user explicitly wants immediate execution and the risk is low

---

## Planning Behavior

The planning phase should emphasize:

- reading existing code, docs, config, or other available context
- identifying constraints and invariants
- surfacing assumptions and unanswered questions
- sequencing work in a practical order
- naming how success will be checked

The plan should not be vague. "Update code and run tests" is not enough. The useful output is a short, actionable sequence.

---

## Suggested Output Shape

Hosts may render the plan however they want, but the plan should contain these concepts:

```text
summary
assumptions
open_questions
steps
risks
verification
ready_for_execution
```

A reasonable structured form is:

```rust
struct PlanModeOutput {
    summary: String,
    assumptions: Vec<String>,
    open_questions: Vec<String>,
    steps: Vec<PlanStep>,
    risks: Vec<String>,
    verification: Vec<String>,
    ready_for_execution: bool,
}

struct PlanStep {
    description: String,
    rationale: Option<String>,
    dependencies: Option<Vec<String>>,
}
```

This is illustrative, not mandatory. The essence is the content, not the exact schema.

---

## Capability Boundary

During planning, the host should prefer allowing:

- read-only exploration
- static analysis
- search and retrieval
- dry-run or synthetic previews, if supported
- clarifying questions, if the host supports them

During planning, the host should prevent or clearly neutralize:

- file writes
- external mutations
- destructive shell commands
- irreversible operations

If the host cannot reliably distinguish safe from unsafe execution, it should disable all effectful actions during planning.

---

## Exit Criteria

Plan mode has succeeded when:

- the agent has enough context to act intentionally
- the next steps are concrete and ordered
- major risks are named
- verification is specified
- unresolved questions, if any, are explicit

If those conditions are not met, the agent should stay in planning behavior rather than pretending it is ready.

---

## Host Integration Notes

The host may expose this idea as:

- a command-line flag
- a mode
- a tool
- a system instruction
- a separate planning run

The host may store the resulting plan in:

- the transcript
- a file
- structured session state
- a returned JSON payload

None of those storage choices are essential to the concept itself.
