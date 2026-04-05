# EnterPlanModeTool & ExitPlanModeTool — Implementation Spec for Rust Harness

## Overview

Plan mode is a state machine that restricts the model to read-only exploration and planning before writing code. `EnterPlanMode` transitions into this state; `ExitPlanMode` presents the plan for user approval and restores the previous permission mode. These tools manage permission context state, plan file I/O, and mode lifecycle flags.

---

## Part A: EnterPlanModeTool

### A.1 Tool Identity & Registration

```
name:               "EnterPlanMode"
search_hint:        "switch to plan mode to design an approach before coding"
user_facing_name:   ""         // Empty — no distinct user-facing label
should_defer:       true       // Discoverable via ToolSearch only
is_read_only:       true       // No file modifications
is_concurrency_safe: true
max_result_size:    100_000
```

### A.2 Input Schema

```rust
struct EnterPlanModeInput {
    // No parameters. Empty object.
}
```

### A.3 Output Schema

```rust
struct EnterPlanModeOutput {
    /// Confirmation message
    message: String,
}
```

### A.4 Enablement

```rust
fn is_enabled(&self) -> bool {
    // Disabled when messaging channels (Telegram, Discord) are active.
    // Reason: ExitPlanMode requires a TUI approval dialog that can't
    // render on channels. Without this gate, the model could enter
    // plan mode and be trapped — unable to exit.
    if channels_feature_active() && get_allowed_channels().len() > 0 {
        return false;
    }
    true
}
```

### A.5 Permission Model

No explicit permission check. The tool itself triggers a user approval dialog implicitly (the model calls it, and the harness should show the user that plan mode is being entered).

### A.6 Core Execution

```rust
fn call(&self, _input: EnterPlanModeInput, context: &mut Context) -> Result<EnterPlanModeOutput> {
    // HARD BLOCK: Cannot be called from a subagent
    if context.agent_id.is_some() {
        return Err("EnterPlanMode tool cannot be used in agent contexts");
    }

    let current_mode = context.app_state.tool_permission_context.mode;

    // 1. Handle plan mode transition flags
    handle_plan_mode_transition(current_mode, PermissionMode::Plan);

    // 2. Prepare context for plan mode (saves pre-plan mode, handles auto mode)
    let prepared_context = prepare_context_for_plan_mode(
        &context.app_state.tool_permission_context
    );

    // 3. Apply the mode change
    context.app_state.tool_permission_context = apply_permission_update(
        prepared_context,
        PermissionUpdate::SetMode {
            mode: PermissionMode::Plan,
            destination: Destination::Session,
        },
    );

    Ok(EnterPlanModeOutput {
        message: "Entered plan mode. You should now focus on exploring the \
                  codebase and designing an implementation approach.".into(),
    })
}
```

### A.7 State Transition: `prepare_context_for_plan_mode`

This function is critical — it saves the current mode and manages auto mode state:

```rust
fn prepare_context_for_plan_mode(context: &ToolPermissionContext) -> ToolPermissionContext {
    let current_mode = context.mode;

    // Already in plan mode — no-op
    if current_mode == PermissionMode::Plan {
        return context.clone();
    }

    // Check if plan mode should use auto mode (feature flag / setting)
    let plan_auto_mode = should_plan_use_auto_mode();

    if current_mode == PermissionMode::Auto {
        if plan_auto_mode {
            // Keep auto mode active during plan
            return ToolPermissionContext {
                pre_plan_mode: Some(PermissionMode::Auto),
                ..context.clone()
            };
        } else {
            // Deactivate auto mode, save for restoration
            set_auto_mode_active(false);
            set_needs_auto_mode_exit_attachment(true);
            return ToolPermissionContext {
                pre_plan_mode: Some(PermissionMode::Auto),
                // Restore any dangerous permissions that were stripped for auto
                ..restore_dangerous_permissions(context)
            };
        }
    }

    if plan_auto_mode && current_mode != PermissionMode::BypassPermissions {
        // Activate auto mode during plan (even if user wasn't in auto before)
        set_auto_mode_active(true);
        return ToolPermissionContext {
            pre_plan_mode: Some(current_mode),
            ..strip_dangerous_permissions_for_auto_mode(context)
        };
    }

    // Default: just save the current mode
    ToolPermissionContext {
        pre_plan_mode: Some(current_mode),
        ..context.clone()
    }
}
```

### A.8 State Transition Flags

```rust
fn handle_plan_mode_transition(from_mode: PermissionMode, to_mode: PermissionMode) {
    if to_mode == PermissionMode::Plan && from_mode != PermissionMode::Plan {
        // Entering plan mode — clear any stale exit attachment flag
        set_needs_plan_mode_exit_attachment(false);
    }
    if from_mode == PermissionMode::Plan && to_mode != PermissionMode::Plan {
        // Exiting plan mode — signal that exit instructions should be attached
        set_needs_plan_mode_exit_attachment(true);
    }
}
```

### A.9 Output Formatting (tool_result → model)

Two variants based on whether "interview phase" is enabled:

**Interview phase ON** (the more structured workflow):
```
Entered plan mode. You should now focus on exploring the codebase and designing an implementation approach.

DO NOT write or edit any files except the plan file. Detailed workflow instructions will follow.
```

**Interview phase OFF** (the simpler free-form workflow):
```
Entered plan mode. You should now focus on exploring the codebase and designing an implementation approach.

In plan mode, you should:
1. Thoroughly explore the codebase using Glob, Grep, and Read tools
2. Understand existing patterns and architecture
3. Design an implementation approach
4. Use AskUserQuestion if you need to clarify the approach
5. When ready, use ExitPlanMode to present your plan for approval

Remember: DO NOT write or edit any files yet. This is a read-only exploration and planning phase.
```

### A.10 System Prompt (tool description)

The prompt is quite long and varies by user type (ant vs external). Key points:

**When to use** (for external users — more aggressive):
- New feature implementation
- Multiple valid approaches exist
- Code modifications affecting existing behavior
- Architectural decisions
- Multi-file changes (>2-3 files)
- Unclear requirements
- User preferences matter

**When NOT to use**:
- Single-line or few-line fixes
- Adding a single function with clear requirements
- User gave very specific detailed instructions
- Pure research/exploration tasks

**For internal users** (more conservative — fewer triggers):
- Only for significant architectural ambiguity
- Only when requirements are genuinely unclear
- Only for high-impact restructuring
- Prefer starting work + AskUserQuestion over full planning

---

## Part B: ExitPlanModeTool

### B.1 Tool Identity & Registration

```
name:               "ExitPlanMode"
search_hint:        "present plan for approval and start coding (plan mode only)"
user_facing_name:   ""         // Empty
should_defer:       true
is_read_only:       false      // Writes plan to disk
is_concurrency_safe: true
max_result_size:    100_000
requires_user_interaction: true (for non-teammates), false (for teammates)
```

### B.2 Input Schema

```rust
struct ExitPlanModeInput {
    /// Prompt-based permissions the plan needs for implementation.
    /// Currently only Bash commands are supported.
    allowed_prompts: Option<Vec<AllowedPrompt>>,
}

struct AllowedPrompt {
    /// The tool this permission applies to. Currently only "Bash".
    tool: AllowedPromptTool,
    /// Semantic description: "run tests", "install dependencies", etc.
    prompt: String,
}

enum AllowedPromptTool {
    Bash,
}
```

**SDK-facing input** (after normalization — includes fields injected by the harness):
```rust
struct ExitPlanModeSDKInput {
    allowed_prompts: Option<Vec<AllowedPrompt>>,
    /// Plan content read from disk by normalizeToolInput — injected for SDK/hooks
    plan: Option<String>,
    /// Path to the plan file — injected
    plan_file_path: Option<String>,
}
```

**Harness note**: Before the tool executes, the harness's `normalizeToolInput` reads the plan from disk and injects `plan` and `planFilePath` into the SDK-visible input. This lets hooks and the web UI see/edit the plan content.

### B.3 Output Schema

```rust
struct ExitPlanModeOutput {
    /// The plan content (None if no plan file exists)
    plan: Option<String>,

    /// Whether this was called from a subagent context
    is_agent: bool,

    /// Path where the plan file lives
    file_path: Option<String>,

    /// Whether the Agent tool is available (for team parallelization hint)
    has_task_tool: Option<bool>,

    /// True if the user edited the plan in the web UI before approving
    plan_was_edited: Option<bool>,

    /// True if this is a teammate that submitted for leader approval
    awaiting_leader_approval: Option<bool>,

    /// Unique ID for the approval request (teammate flow only)
    request_id: Option<String>,
}
```

### B.4 Input Validation

```rust
fn validate_input(&self, _input: &ExitPlanModeInput, context: &Context) -> ValidationResult {
    // Teammates: always valid (their AppState may show leader's mode)
    if is_teammate() {
        return ValidationResult::Valid;
    }

    // Must be in plan mode
    let mode = context.app_state.tool_permission_context.mode;
    if mode != PermissionMode::Plan {
        return ValidationResult::Invalid {
            message: "You are not in plan mode. This tool is only for exiting \
                      plan mode after writing a plan. If your plan was already \
                      approved, continue with implementation.".into(),
            error_code: 1,
        };
    }

    ValidationResult::Valid
}
```

### B.5 Permission Check

```rust
fn check_permissions(&self, input: &ExitPlanModeInput, context: &Context) -> PermissionDecision {
    if is_teammate() {
        // Teammates: bypass UI entirely
        // If isPlanModeRequired: sends approval to leader (handled in call())
        // Otherwise: exits locally
        PermissionDecision::Allow { updated_input: input.clone() }
    } else {
        // Non-teammates: require user confirmation via dialog
        PermissionDecision::Ask {
            message: "Exit plan mode?".into(),
            updated_input: Some(input.clone()),
        }
    }
}
```

### B.6 Core Execution

```rust
fn call(&self, input: ExitPlanModeInput, context: &mut Context) -> Result<ExitPlanModeOutput> {
    let is_agent = context.agent_id.is_some();
    let file_path = get_plan_file_path(context.agent_id.as_deref());

    // --- Step 1: Retrieve plan ---
    // Check if user edited the plan in the web UI
    let input_plan = input.plan.clone(); // injected by normalizeToolInput from permission result
    let plan = input_plan.clone().or_else(|| get_plan(context.agent_id.as_deref()));

    // If user edited the plan, write the edited version to disk
    if let Some(ref edited_plan) = input_plan {
        if let Some(ref path) = file_path {
            let _ = std::fs::write(path, edited_plan);
            persist_file_snapshot_if_remote(); // For CCR/remote sessions
        }
    }

    // --- Step 2: Teammate approval flow ---
    if is_teammate() && is_plan_mode_required() {
        let plan = plan.ok_or("No plan file found. Write your plan before calling ExitPlanMode.")?;
        let agent_name = get_agent_name().unwrap_or("unknown");
        let team_name = get_team_name();
        let request_id = generate_request_id("plan_approval", &agent_name);

        write_to_mailbox("team-lead", ApprovalRequest {
            type_: "plan_approval_request",
            from: agent_name,
            timestamp: now_iso(),
            plan_file_path: file_path.clone(),
            plan_content: plan.clone(),
            request_id: request_id.clone(),
        }, team_name);

        // Update task state to show awaiting approval
        if let Some(task_id) = find_in_process_teammate_task_id(&agent_name) {
            set_awaiting_plan_approval(task_id, true);
        }

        return Ok(ExitPlanModeOutput {
            plan: Some(plan),
            is_agent: true,
            file_path,
            awaiting_leader_approval: Some(true),
            request_id: Some(request_id),
            ..Default::default()
        });
    }

    // --- Step 3: Restore permission mode ---
    let pre_plan_mode = context.app_state.tool_permission_context.pre_plan_mode
        .unwrap_or(PermissionMode::Default);

    // Circuit breaker: if pre_plan_mode was Auto but gate is now off, fall back
    let mut restore_mode = pre_plan_mode;
    if restore_mode == PermissionMode::Auto && !is_auto_mode_gate_enabled() {
        restore_mode = PermissionMode::Default;
        notify_user("plan exit → default · auto mode unavailable");
    }

    // Set lifecycle flags
    set_has_exited_plan_mode(true);
    set_needs_plan_mode_exit_attachment(true);

    // Handle auto mode state
    let auto_was_used_during_plan = is_auto_mode_active();
    set_auto_mode_active(restore_mode == PermissionMode::Auto);
    if auto_was_used_during_plan && restore_mode != PermissionMode::Auto {
        set_needs_auto_mode_exit_attachment(true);
    }

    // Handle dangerous permission restoration
    let mut base_context = context.app_state.tool_permission_context.clone();
    if restore_mode == PermissionMode::Auto {
        base_context = strip_dangerous_permissions_for_auto_mode(&base_context);
    } else if base_context.stripped_dangerous_rules.is_some() {
        base_context = restore_dangerous_permissions(&base_context);
    }

    // Apply final state
    context.app_state.tool_permission_context = ToolPermissionContext {
        mode: restore_mode,
        pre_plan_mode: None,
        ..base_context
    };

    // --- Step 4: Return ---
    let has_task_tool = is_agent_swarms_enabled()
        && context.options.tools.iter().any(|t| t.name() == "Agent");

    Ok(ExitPlanModeOutput {
        plan,
        is_agent,
        file_path,
        has_task_tool: if has_task_tool { Some(true) } else { None },
        plan_was_edited: if input_plan.is_some() { Some(true) } else { None },
        ..Default::default()
    })
}
```

### B.7 Output Formatting (tool_result → model)

Four distinct output paths:

```rust
fn map_to_tool_result(output: &ExitPlanModeOutput, tool_use_id: &str) -> ToolResultBlockParam {
    let content = if output.awaiting_leader_approval == Some(true) {
        // PATH 1: Teammate awaiting leader approval
        format!(
            "Your plan has been submitted to the team lead for approval.\n\n\
             Plan file: {}\n\n\
             **What happens next:**\n\
             1. Wait for the team lead to review your plan\n\
             2. You will receive a message in your inbox with approval/rejection\n\
             3. If approved, you can proceed with implementation\n\
             4. If rejected, refine your plan based on the feedback\n\n\
             **Important:** Do NOT proceed until you receive approval. \
             Check your inbox for response.\n\n\
             Request ID: {}",
            output.file_path.as_deref().unwrap_or("unknown"),
            output.request_id.as_deref().unwrap_or("unknown"),
        )

    } else if output.is_agent {
        // PATH 2: Subagent — plan approved by parent
        "User has approved the plan. There is nothing else needed from you now. \
         Please respond with \"ok\"".into()

    } else if output.plan.as_ref().map_or(true, |p| p.trim().is_empty()) {
        // PATH 3: No plan / empty plan
        "User has approved exiting plan mode. You can now proceed.".into()

    } else {
        // PATH 4: Normal plan approval — most common path
        let plan = output.plan.as_ref().unwrap();
        let plan_label = if output.plan_was_edited == Some(true) {
            "Approved Plan (edited by user)"
        } else {
            "Approved Plan"
        };

        let team_hint = if output.has_task_tool == Some(true) {
            "\n\nIf this plan can be broken down into multiple independent tasks, \
             consider using the TeamCreate tool to create a team and parallelize the work."
        } else {
            ""
        };

        format!(
            "User has approved your plan. You can now start coding. \
             Start with updating your todo list if applicable\n\n\
             Your plan has been saved to: {}\n\
             You can refer back to it if needed during implementation.{}\n\n\
             ## {}:\n{}",
            output.file_path.as_deref().unwrap_or("unknown"),
            team_hint,
            plan_label,
            plan,
        )
    };

    ToolResultBlockParam {
        type_: "tool_result".into(),
        tool_use_id: tool_use_id.into(),
        content,
    }
}
```

### B.8 System Prompt (tool description)

```text
Use this tool when you are in plan mode and have finished writing your plan to the
plan file and are ready for user approval.

## How This Tool Works
- You should have already written your plan to the plan file specified in the plan
  mode system message
- This tool does NOT take the plan content as a parameter - it will read the plan
  from the file you wrote
- This tool simply signals that you're done planning and ready for the user to
  review and approve
- The user will see the contents of your plan file when they review it

## When to Use This Tool
IMPORTANT: Only use this tool when the task requires planning the implementation
steps of a task that requires writing code. For research tasks where you're gathering
information, searching files, reading files or in general trying to understand the
codebase - do NOT use this tool.

## Before Using This Tool
Ensure your plan is complete and unambiguous:
- If you have unresolved questions about requirements or approach, use
  AskUserQuestion first (in earlier phases)
- Once your plan is finalized, use THIS tool to request approval

**Important:** Do NOT use AskUserQuestion to ask "Is this plan okay?" or "Should I
proceed?" - that's exactly what THIS tool does. ExitPlanMode inherently requests
user approval of your plan.
```

---

## Part C: Plan File I/O

### C.1 Plan File Paths

```rust
fn get_plan_file_path(agent_id: Option<&str>) -> Option<String> {
    let plan_slug = get_plan_slug(&get_session_id());
    let plans_dir = get_plans_directory(); // e.g., ~/.claude/plans/

    let filename = match agent_id {
        None => format!("{}.md", plan_slug),
        Some(id) => format!("{}-agent-{}.md", plan_slug, id),
    };

    Some(plans_dir.join(filename).to_string_lossy().into())
}

fn get_plan(agent_id: Option<&str>) -> Option<String> {
    let path = get_plan_file_path(agent_id)?;
    std::fs::read_to_string(&path).ok()
}
```

The `plan_slug` is a deterministic, human-readable slug generated from the session ID (e.g., `"moonlit-fluttering-neumann"`).

### C.2 Plan Recovery on Session Resume

When resuming a session, the plan file may need to be recovered:

1. **Check if plan file exists on disk** — if yes, use it
2. **Check file snapshots in message history** — for remote sessions where files don't persist
3. **Parse message history** — look for ExitPlanMode tool_use blocks or user messages containing plan content

---

## Part D: Plan Mode State Machine

### D.1 Permission Mode Values

```rust
enum PermissionMode {
    Default,           // Normal interactive mode
    Plan,              // Read-only exploration + planning
    Auto,              // Classifier-based auto-approval
    BypassPermissions, // Skip all permission checks (dangerous)
}
```

### D.2 State Variables

```rust
struct PlanModeState {
    /// Has plan mode been exited at least once in this session?
    has_exited_plan_mode: bool,

    /// Should plan mode exit instructions be attached to the next system message?
    /// One-shot flag — set on exit, cleared after attachment.
    needs_plan_mode_exit_attachment: bool,

    /// Should auto mode exit instructions be attached to the next system message?
    needs_auto_mode_exit_attachment: bool,
}
```

### D.3 Lifecycle Diagram

```
                    EnterPlanMode
   [Default/Auto] ──────────────────► [Plan]
        ▲                                │
        │                                │ ExitPlanMode
        │                                ▼
        └──────── restore pre_plan_mode ─┘
```

### D.4 Dangerous Permission Stripping

When entering auto mode (during plan or otherwise), certain Bash rules are stripped to prevent the classifier from auto-approving dangerous commands:

```rust
fn strip_dangerous_permissions_for_auto_mode(ctx: &ToolPermissionContext) -> ToolPermissionContext {
    // Identify rules matching wildcards, script interpreters, etc.
    let (safe_rules, dangerous_rules) = partition_bash_rules(&ctx.always_allow_rules);

    ToolPermissionContext {
        always_allow_rules: safe_rules,
        stripped_dangerous_rules: Some(dangerous_rules),
        ..ctx.clone()
    }
}

fn restore_dangerous_permissions(ctx: &ToolPermissionContext) -> ToolPermissionContext {
    if let Some(stripped) = &ctx.stripped_dangerous_rules {
        let mut rules = ctx.always_allow_rules.clone();
        merge_rules(&mut rules, stripped);
        ToolPermissionContext {
            always_allow_rules: rules,
            stripped_dangerous_rules: None,
            ..ctx.clone()
        }
    } else {
        ctx.clone()
    }
}
```

---

## Part E: Implementation Notes for Rust Harness

### E.1 Plan Mode Restrictions

When `mode == Plan`, the harness must enforce:
- **ALLOWED tools**: Glob, Grep, Read, Agent (explore only), AskUserQuestion, WebSearch, WebFetch, ToolSearch
- **ALLOWED write target**: Only the plan file itself (Write/Edit to `get_plan_file_path()`)
- **BLOCKED**: All other Write/Edit operations, Bash (non-readonly), config changes, git commits
- The system message attachment must explicitly state these restrictions

### E.2 `pre_plan_mode` Preservation

`pre_plan_mode` MUST be stored in `ToolPermissionContext` (not a separate variable) so it survives serialization, session resume, and compaction. It's set when entering plan mode and cleared when exiting.

### E.3 Interview Phase

The interview phase is a more structured plan mode workflow with explicit phases:
1. **Phase 1: Initial Understanding** — explore with subagents
2. **Phase 2: Design** — plan with Plan agent
3. **Phase 3: Review** — verify alignment
4. **Phase 4: Final Plan** — write plan file
5. **Phase 5: ExitPlanMode** — request approval

When interview phase is enabled, detailed phase instructions are injected via a system message attachment (not the tool result). The tool result just says "DO NOT write or edit any files except the plan file. Detailed workflow instructions will follow."

### E.4 Subagent Restriction

`EnterPlanMode` is ONLY for the main thread. Subagents cannot enter plan mode because:
- Plan mode affects the global permission context
- Multiple agents entering/exiting plan mode would create race conditions
- The TUI approval dialog is tied to the main conversation

### E.5 Channel Safety Gate

Both tools share the same enablement check. If channels (Telegram/Discord) are active, both tools are disabled to prevent the model from entering a state it can't exit. This is a safety invariant — always pair entry and exit enablement.

### E.6 Auto Mode Circuit Breaker

On ExitPlanMode, if the user was previously in auto mode but the auto mode gate has been tripped (e.g., too many dangerous operations detected), fall back to default mode instead of restoring auto. This prevents bypassing the circuit breaker by entering and exiting plan mode.
