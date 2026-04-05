# Todo Write — Essence Spec

## Purpose

Todo write is a lightweight task-tracking mechanism for non-trivial work.

Its value is not in fancy UI. Its value is in keeping the agent's current working set explicit:

- what remains
- what is in progress
- what is already complete

This reduces drift, forgotten follow-up work, and vague status reporting.

---

## Non-Goals

This spec does not require:

- a sidebar or status bar
- hidden state outside the normal transcript
- verifier-agent nudges
- sub-agent-specific todo lists
- a project-management system
- complex incremental patch semantics

Those may be useful in some hosts, but they are not part of the core idea.

---

## Core Contract

Todo write manages the current checklist as a full replacement.

Each update should send the complete list as it stands now, not just the delta.

This keeps the state easy to reason about and avoids partial-update drift.

---

## Suggested Input Shape

```rust
struct TodoWriteInput {
    todos: Vec<TodoItem>,
}

struct TodoItem {
    content: String,
    status: TodoStatus,
    active_form: Option<String>,
}

enum TodoStatus {
    Pending,
    InProgress,
    Completed,
}
```

Notes:

- `content` is the stable task description.
- `active_form` is optional, but useful when the host wants a present-tense activity label.
- The exact field names are not the important part. The important part is a small, explicit checklist model.

---

## Suggested Output Shape

At minimum, the host should acknowledge the accepted current list.

A reasonable shape is:

```rust
struct TodoWriteOutput {
    previous: Vec<TodoItem>,
    current: Vec<TodoItem>,
}
```

Returning the previous list is helpful, but optional.

---

## Usage Rules

The agent should update the todo list when:

- starting a non-trivial task
- beginning a new step
- completing a step
- discovering additional required work
- changing scope in a meaningful way
- encountering a blocker that changes the working plan

The todo list should not be updated for every trivial micro-action.

---

## Task-State Rules

Good task hygiene matters more than the exact schema:

- keep tasks concrete
- avoid stale or irrelevant items
- prefer zero or one `InProgress` item at a time
- do not mark tasks `Completed` until they are actually done
- if blocked, either keep the task in progress with a clear blocker note or add a separate blocker task

Hosts may enforce stricter validation if they want, but the main value is behavioral discipline.

---

## When To Use

Use todo write when:

- the work has multiple meaningful steps
- the task will span several tool calls or messages
- the user asked for a plan or checklist
- the agent would benefit from explicit progress tracking

---

## When Not To Use

Do not use it for:

- one-shot trivial work
- purely conversational responses
- tasks where the checklist would add more ceremony than clarity

---

## Persistence

The checklist may be:

- ephemeral for a single run
- scoped to a conversation or session
- persisted in structured state
- embedded in normal transcript messages

This spec does not require a particular persistence strategy.

---

## Host Integration Notes

The host may expose this as:

- a tool
- a structured response section
- a session-state helper
- a visible checklist widget

The core idea remains the same:

- small explicit task model
- full replacement updates
- disciplined progress tracking

That simplicity is the part worth keeping.
