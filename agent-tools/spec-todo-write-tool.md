# TodoWriteTool — Implementation Spec for Rust Harness

## Overview

TodoWriteTool manages an in-session task checklist. It's a state-only tool — it modifies the `AppState.todos` map but writes no files to disk. The tool tracks per-agent todo lists, auto-clears completed lists, and nudges the model to spawn a verification agent when work finishes without a verification step.

**Legacy status**: This tool is the V1 task system. It is disabled when TodoV2 (TaskCreate/TaskGet/TaskList/TaskUpdate) is enabled. Your Rust harness should implement this only if you want V1 compatibility or are not implementing the V2 task tools.

---

## 1. Tool Identity & Registration

```
name:               "TodoWrite"
search_hint:        "manage the session task checklist"
user_facing_name:   ""         // Empty — no distinct user-facing label
should_defer:       true       // Discoverable via ToolSearch only
is_read_only:       N/A        // Modifies session state, not files
is_concurrency_safe: N/A
strict:             true       // Strict schema validation (via tengu_tool_pear)
max_result_size:    100_000
```

### Enablement

```rust
fn is_enabled(&self) -> bool {
    // Only enabled when the V2 task system is NOT active
    !is_todo_v2_enabled()
}
```

---

## 2. Input Schema

```rust
struct TodoWriteInput {
    /// The complete updated todo list. This is a FULL REPLACEMENT —
    /// every call must include ALL items, not just changes.
    todos: Vec<TodoItem>,
}

struct TodoItem {
    /// What needs to be done. Imperative form.
    /// Examples: "Run tests", "Fix authentication bug", "Build the project"
    /// Min length: 1 character.
    content: String,

    /// Current status of the task.
    status: TodoStatus,

    /// Present-continuous description shown during execution.
    /// Examples: "Running tests", "Fixing authentication bug", "Building the project"
    /// Min length: 1 character.
    active_form: String,
}

enum TodoStatus {
    /// Not yet started
    Pending,
    /// Currently being worked on (should be exactly 1 at a time)
    InProgress,
    /// Finished
    Completed,
}
```

**Critical schema detail**: The `todos` field is a **full replacement** — every call sends the entire list. There is no incremental add/remove/update API. To mark one item as completed, the model must resend all items with the updated status.

**Strict mode**: When `strict: true` is active, schema validation is tighter — extra properties are rejected, types are enforced exactly. This prevents the model from sending malformed todo items.

---

## 3. Output Schema

```rust
struct TodoWriteOutput {
    /// The todo list BEFORE this update was applied
    old_todos: Vec<TodoItem>,

    /// The todo list as provided in the input (for display — may differ from stored state)
    new_todos: Vec<TodoItem>,

    /// Whether the model should be nudged to spawn a verification agent
    verification_nudge_needed: Option<bool>,
}
```

---

## 4. Permission Model

```rust
fn check_permissions(&self, input: &TodoWriteInput) -> PermissionDecision {
    // Always auto-allowed — no user confirmation needed
    PermissionDecision::Allow { updated_input: input.clone() }
}
```

Todo operations are purely session-internal. No file I/O, no side effects, no user-facing impact. Always auto-approved.

---

## 5. Core Execution Logic

```rust
fn call(&self, input: TodoWriteInput, context: &mut Context) -> TodoWriteOutput {
    // 1. Determine the storage key
    //    - Subagents get their own todo list (keyed by agent_id)
    //    - Main thread uses session_id
    let todo_key = context.agent_id
        .clone()
        .unwrap_or_else(|| get_session_id());

    // 2. Snapshot the old state
    let old_todos = context.app_state.todos
        .get(&todo_key)
        .cloned()
        .unwrap_or_default();

    // 3. Check if ALL items are completed
    let all_done = input.todos.iter().all(|t| t.status == TodoStatus::Completed);

    // 4. If all done, CLEAR the stored list (don't keep completed items around)
    //    Otherwise, store the new list as-is
    let stored_todos = if all_done {
        vec![]
    } else {
        input.todos.clone()
    };

    // 5. Verification nudge check
    //    Conditions (ALL must be true):
    //    a. VERIFICATION_AGENT feature flag is on
    //    b. tengu_hive_evidence feature value is true
    //    c. This is the main thread (not a subagent)
    //    d. All items are completed
    //    e. There are 3 or more items
    //    f. None of the items mention "verif" (case-insensitive)
    let verification_nudge_needed =
        feature_enabled("VERIFICATION_AGENT")
        && get_feature_value("tengu_hive_evidence", false)
        && context.agent_id.is_none()
        && all_done
        && input.todos.len() >= 3
        && !input.todos.iter().any(|t|
            t.content.to_lowercase().contains("verif")
        );

    // 6. Update app state
    context.app_state.todos.insert(todo_key, stored_todos);

    // 7. Return
    TodoWriteOutput {
        old_todos,
        new_todos: input.todos,  // Return input todos, NOT stored (which may be empty)
        verification_nudge_needed: if verification_nudge_needed {
            Some(true)
        } else {
            None
        },
    }
}
```

### Key Behaviors to Note

**Auto-clear on completion**: When all items are `Completed`, the stored list becomes empty `[]`. But the output's `new_todos` field still contains the completed items (for display/logging). This prevents stale completed items from cluttering the state across subsequent tool calls.

**Per-agent isolation**: Each agent/subagent has its own independent todo list. The main thread uses `session_id` as the key. This prevents subagents from stomping on the main thread's todo list.

**Verification nudge**: This is a behavioral nudge — when the model closes out 3+ tasks without any verification step, the tool result tells it to spawn a verification agent. The nudge only fires on the main thread (subagents don't get nudged).

---

## 6. Output Formatting (tool_result → model)

```rust
fn map_to_tool_result(
    output: &TodoWriteOutput,
    tool_use_id: &str,
) -> ToolResultBlockParam {
    let base = "Todos have been modified successfully. Ensure that you continue to \
                use the todo list to track your progress. Please proceed with the \
                current tasks if applicable";

    let nudge = if output.verification_nudge_needed == Some(true) {
        "\n\nNOTE: You just closed out 3+ tasks and none of them was a verification \
         step. Before writing your final summary, spawn the verification agent \
         (subagent_type=\"verify\"). You cannot self-assign PARTIAL by listing \
         caveats in your summary \u{2014} only the verifier issues a verdict."
    } else {
        ""
    };

    ToolResultBlockParam {
        type_: "tool_result".into(),
        tool_use_id: tool_use_id.into(),
        content: format!("{}{}", base, nudge),
    }
}
```

---

## 7. UI Rendering

```rust
fn render_tool_use_message(&self, _input: &TodoWriteInput) -> Option<UIElement> {
    None  // No visible UI when the tool is invoked — silent update
}
```

The tool renders nothing in the TUI on invocation. The todo list is displayed separately by the harness's status line / sidebar, reading directly from `AppState.todos`.

---

## 8. System Prompt (tool description)

The full prompt is extensive. Key sections:

### When to Use

1. **Complex multi-step tasks** — 3+ distinct steps
2. **Non-trivial tasks** — require careful planning
3. **User explicitly requests** — "make a todo list"
4. **User provides multiple tasks** — numbered or comma-separated list
5. **After receiving new instructions** — capture requirements immediately
6. **When starting a task** — mark as `in_progress` BEFORE beginning
7. **After completing a task** — mark as `completed`, add follow-up tasks

### When NOT to Use

1. Single, straightforward task
2. Trivial task with no organizational benefit
3. Task completable in < 3 trivial steps
4. Purely conversational or informational

### Task States

- `pending` — not yet started
- `in_progress` — currently working on (**limit to ONE at a time**)
- `completed` — finished successfully

### Task Management Rules

- Update status in real-time
- Mark tasks complete **IMMEDIATELY** after finishing (don't batch)
- **Exactly ONE** task must be `in_progress` at any time
- Complete current tasks before starting new ones
- Remove irrelevant tasks from the list entirely

### Task Completion Requirements

- ONLY mark as completed when FULLY accomplished
- Keep as `in_progress` if: tests failing, implementation partial, unresolved errors, missing files/deps
- When blocked, create a new task for the blocker

### Dual-Form Requirement

Every task MUST have both:
- `content`: imperative form ("Fix authentication bug")
- `active_form`: present continuous ("Fixing authentication bug")

### Description (short form for prompt)

```
Update the todo list for the current session. To be used proactively and often to
track progress and pending tasks. Make sure that at least one task is in_progress at
all times. Always provide both content (imperative) and activeForm (present
continuous) for each task.
```

---

## 9. AppState Storage

```rust
struct AppState {
    // ... other fields ...

    /// Todo lists keyed by agent_id (or session_id for main thread)
    todos: HashMap<String, Vec<TodoItem>>,
}
```

**Initialization**: Empty `HashMap` on session start. Entries are created lazily on first `TodoWrite` call for each agent/session.

**Serialization**: The todos map must survive compaction and session restore. It's part of the persistent AppState.

---

## 10. Implementation Notes for Rust Harness

### 10.1 Full Replacement Semantics

The model sends the ENTIRE todo list on every call. This is intentional — it avoids the complexity of partial updates and prevents desync. The harness must not attempt to merge or diff; just replace wholesale.

### 10.2 Auto-Classifier Input

```rust
fn to_auto_classifier_input(input: &TodoWriteInput) -> String {
    format!("{} items", input.todos.len())
}
```

Used by the permission auto-classifier. Since todos are always auto-allowed, this is mostly for logging.

### 10.3 `renderToolUseMessage` Returns Null

Unlike most tools, TodoWrite's `renderToolUseMessage` returns `None`. The todo list is displayed via a separate UI channel (status bar, sidebar), not inline in the conversation. This prevents visual clutter from frequent todo updates.

### 10.4 Feature Flag Dependencies

| Flag | Purpose |
|------|---------|
| `VERIFICATION_AGENT` | Compile-time flag — must be on for nudge logic |
| `tengu_hive_evidence` | Runtime feature value — must be true for nudge |
| TodoV2 enablement | If V2 is on, this entire tool is disabled |

If your Rust harness doesn't implement the verification agent or feature flags, you can simplify by:
- Always setting `verification_nudge_needed` to `None`
- Always enabling the tool (ignore V2 check)

### 10.5 Thread Safety

Multiple agents could call TodoWrite concurrently for different keys. The `HashMap<String, Vec<TodoItem>>` must be wrapped in a `Mutex` or `RwLock`, or access must be serialized per-key. Since each agent has its own key, per-key locking is sufficient.
