# SkillTool — Implementation Spec for Rust Harness

## Overview

SkillTool is the runtime executor for "skills" — prompt-based command packages that extend Claude's capabilities. When invoked, it discovers a skill by name, validates and permission-checks it, then executes it via one of three paths: **inline** (inject prompt into conversation), **forked** (run in isolated sub-agent), or **remote** (load from cloud storage). It's the most complex tool in terms of execution paths and state management.

---

## 1. Tool Identity & Registration

```
name:               "Skill"
search_hint:        "invoke a slash-command skill"
user_facing_name:   (not set — uses default)
should_defer:       false      // ALWAYS in tool list (not deferred!)
is_read_only:       (default)
is_concurrency_safe: (default)
max_result_size:    100_000
```

**Unlike the other tools in this spec**, SkillTool is NOT deferred. It's always present in the model's tool list because skills need to be invocable at any time.

---

## 2. Input Schema

```rust
struct SkillInput {
    /// The skill name. Examples: "commit", "review-pr", "pdf", "ms-office-suite:pdf"
    skill: String,

    /// Optional arguments passed to the skill.
    /// Examples: "-m 'Fix bug'", "123", "--verbose"
    args: Option<String>,
}
```

---

## 3. Output Schema

```rust
enum SkillOutput {
    /// Skill prompt was injected inline into the conversation
    Inline {
        success: bool,
        command_name: String,
        /// Tools that this skill has auto-allowed (e.g., specific Bash commands)
        allowed_tools: Option<Vec<String>>,
        /// Model override requested by the skill
        model: Option<String>,
    },

    /// Skill was executed in an isolated sub-agent
    Forked {
        success: bool,
        command_name: String,
        /// The sub-agent's ID
        agent_id: String,
        /// The text result from the sub-agent
        result: String,
    },
}
```

---

## 4. Input Validation

Sequential checks — first failure stops execution:

```rust
fn validate_input(&self, input: &SkillInput, context: &Context) -> ValidationResult {
    // 1. Trim and check non-empty
    let trimmed = input.skill.trim();
    if trimmed.is_empty() {
        return ValidationResult::Invalid {
            message: format!("Invalid skill format: {}", input.skill),
            error_code: 1,
        };
    }

    // 2. Strip leading "/" for backward compatibility
    let has_leading_slash = trimmed.starts_with('/');
    if has_leading_slash {
        log_event("tengu_skill_tool_slash_prefix");
    }
    let command_name = if has_leading_slash {
        &trimmed[1..]
    } else {
        trimmed
    };

    // 3. Remote canonical skill check (experimental, ant-only)
    //    Names like "_canonical_my-skill" are intercepted before local lookup
    if feature_enabled("EXPERIMENTAL_SKILL_SEARCH") && is_ant_user() {
        if let Some(slug) = strip_canonical_prefix(command_name) {
            let meta = get_discovered_remote_skill(&slug);
            if meta.is_none() {
                return ValidationResult::Invalid {
                    message: format!(
                        "Remote skill {} was not discovered in this session. \
                         Use DiscoverSkills to find remote skills first.",
                        slug
                    ),
                    error_code: 6,
                };
            }
            return ValidationResult::Valid; // Loading happens in call()
        }
    }

    // 4. Look up in command registry (local + bundled + MCP)
    let commands = get_all_commands(context);
    let command = find_command(command_name, &commands);

    if command.is_none() {
        return ValidationResult::Invalid {
            message: format!("Unknown skill: {}", command_name),
            error_code: 2,
        };
    }
    let command = command.unwrap();

    // 5. Check model invocation flag
    if command.disable_model_invocation {
        return ValidationResult::Invalid {
            message: format!(
                "Skill {} cannot be used with Skill tool due to disable-model-invocation",
                command_name
            ),
            error_code: 4,
        };
    }

    // 6. Must be a prompt-based command (not a built-in action)
    if command.type_ != CommandType::Prompt {
        return ValidationResult::Invalid {
            message: format!("Skill {} is not a prompt-based skill", command_name),
            error_code: 5,
        };
    }

    ValidationResult::Valid
}
```

---

## 5. Permission Model — 5-Level Hierarchy

```rust
fn check_permissions(
    &self,
    input: &SkillInput,
    context: &Context,
) -> PermissionDecision {
    let command_name = normalize_skill_name(&input.skill);
    let commands = get_all_commands(context);
    let command = find_command(&command_name, &commands);

    // Helper: check if a rule matches this skill
    let rule_matches = |rule_content: &str| -> bool {
        let normalized_rule = rule_content.strip_prefix('/').unwrap_or(rule_content);
        // Exact match
        if normalized_rule == command_name { return true; }
        // Prefix match: "review:*" matches "review-pr"
        if let Some(prefix) = normalized_rule.strip_suffix(":*") {
            return command_name.starts_with(prefix);
        }
        false
    };

    let perm_context = &context.app_state.tool_permission_context;

    // ═══ LEVEL 1: Deny rules (highest priority) ═══
    for (rule_content, rule) in get_deny_rules_for_tool(perm_context, "Skill") {
        if rule_matches(&rule_content) {
            return PermissionDecision::Deny {
                message: "Skill execution blocked by permission rules".into(),
                decision_reason: Some(DecisionReason::Rule(rule)),
            };
        }
    }

    // ═══ LEVEL 2: Remote canonical skills (ant-only, experimental) ═══
    if feature_enabled("EXPERIMENTAL_SKILL_SEARCH") && is_ant_user() {
        if strip_canonical_prefix(&command_name).is_some() {
            return PermissionDecision::Allow {
                updated_input: input.clone(),
                decision_reason: None,
            };
        }
    }

    // ═══ LEVEL 3: Allow rules ═══
    for (rule_content, rule) in get_allow_rules_for_tool(perm_context, "Skill") {
        if rule_matches(&rule_content) {
            return PermissionDecision::Allow {
                updated_input: input.clone(),
                decision_reason: Some(DecisionReason::Rule(rule)),
            };
        }
    }

    // ═══ LEVEL 4: Safe properties heuristic ═══
    if let Some(cmd) = command {
        if cmd.type_ == CommandType::Prompt && skill_has_only_safe_properties(&cmd) {
            return PermissionDecision::Allow {
                updated_input: input.clone(),
                decision_reason: None,
            };
        }
    }

    // ═══ LEVEL 5: Default — ask user ═══
    PermissionDecision::Ask {
        message: format!("Execute skill: {}", command_name),
        suggestions: vec![
            // Suggestion 1: Allow this exact skill
            PermissionSuggestion {
                type_: "addRules",
                rules: vec![PermissionRule {
                    tool_name: "Skill",
                    rule_content: Some(command_name.clone()),
                }],
                behavior: "allow",
                destination: "localSettings",
            },
            // Suggestion 2: Allow this skill with any args (prefix wildcard)
            PermissionSuggestion {
                type_: "addRules",
                rules: vec![PermissionRule {
                    tool_name: "Skill",
                    rule_content: Some(format!("{}:*", command_name)),
                }],
                behavior: "allow",
                destination: "localSettings",
            },
        ],
        updated_input: Some(input.clone()),
        metadata: command.map(|c| json!({ "command": c })),
    }
}
```

### Safe Properties Allowlist

A command is auto-allowed if it has ONLY properties from this set (with no unexpected properties holding meaningful values):

```rust
const SAFE_SKILL_PROPERTIES: &[&str] = &[
    // PromptCommand properties
    "type", "progressMessage", "contentLength", "argNames", "model", "effort",
    "source", "pluginInfo", "disableNonInteractive", "skillRoot", "context",
    "agent", "getPromptForCommand", "frontmatterKeys",
    // CommandBase properties
    "name", "description", "hasUserSpecifiedDescription", "isEnabled", "isHidden",
    "aliases", "isMcp", "argumentHint", "whenToUse", "paths", "version",
    "disableModelInvocation", "userInvocable", "loadedFrom", "immediate",
    "userFacingName",
];

fn skill_has_only_safe_properties(command: &Command) -> bool {
    for (key, value) in command.properties() {
        if SAFE_SKILL_PROPERTIES.contains(&key.as_str()) {
            continue;
        }
        // Unknown property with a meaningful value → requires permission
        if !is_empty_or_null(value) {
            return false;
        }
    }
    true
}
```

**Design rationale**: New properties added to `Command` in the future default to requiring permission. Only reviewed, known-safe properties are in the allowlist. This is a security-first design.

---

## 6. Core Execution — Three Paths

### 6.1 Path A: Inline Skills (default)

This is the most common path. The skill's prompt is injected into the conversation as a user message.

```rust
async fn execute_inline_skill(
    command_name: &str,
    args: &str,
    commands: &[Command],
    context: &mut Context,
    parent_message: &AssistantMessage,
) -> ToolResult<SkillOutput> {
    // 1. Process the slash command — expands the skill prompt
    let processed = process_prompt_slash_command(
        command_name,
        args,
        commands,
        context,
    ).await;

    if !processed.should_query {
        return Err("Command processing failed");
    }

    // 2. Extract metadata from the command
    let allowed_tools = processed.allowed_tools.unwrap_or_default();
    let model = processed.model;
    let effort = command.effort;

    // 3. Get the tool_use_id from the parent assistant message
    //    (links the injected messages back to this tool call)
    let tool_use_id = get_tool_use_id_from_parent_message(parent_message, "Skill");

    // 4. Filter and tag messages for injection
    //    - Remove progress messages
    //    - Remove <command-message> XML tags (SkillTool handles display)
    //    - Tag with sourceToolUseID for transience tracking
    let new_messages = processed.messages
        .into_iter()
        .filter(|m| !is_progress_message(m) && !is_command_message(m))
        .map(|m| tag_with_tool_use_id(m, &tool_use_id))
        .collect();

    // 5. Build the context modifier
    //    This function will be called to modify the context for subsequent tool calls
    let context_modifier = move |ctx: Context| -> Context {
        let mut modified = ctx;

        // Add skill's allowed tools to permission context
        if !allowed_tools.is_empty() {
            let prev_rules = modified.app_state.tool_permission_context
                .always_allow_rules.command.clone();
            modified.app_state.tool_permission_context
                .always_allow_rules.command = merge_dedup(prev_rules, &allowed_tools);
        }

        // Override model if skill specifies one
        if let Some(ref model_override) = model {
            // Carry [1m] suffix from parent model to avoid context window downgrade
            modified.options.main_loop_model =
                resolve_skill_model_override(model_override, &ctx.options.main_loop_model);
        }

        // Override effort level
        if let Some(ref effort_override) = effort {
            modified.app_state.effort_value = Some(effort_override.clone());
        }

        modified
    };

    Ok(ToolResult {
        data: SkillOutput::Inline {
            success: true,
            command_name: command_name.into(),
            allowed_tools: if allowed_tools.is_empty() { None } else { Some(allowed_tools) },
            model,
        },
        new_messages: Some(new_messages),
        context_modifier: Some(Box::new(context_modifier)),
    })
}
```

**What `process_prompt_slash_command` does internally**:
1. Calls `command.get_prompt_for_command(args, context)` — returns the skill's text content
2. Replaces `$ARGUMENTS` with the provided args
3. Replaces `${CLAUDE_SKILL_DIR}` with the skill's base directory path
4. Replaces `${CLAUDE_SESSION_ID}` with the current session ID
5. Registers skill hooks (`command.hooks`) if any
6. Calls `add_invoked_skill(name, path, content, agent_id)` to ensure the skill survives message compaction

### 6.2 Path B: Forked Skills

When `command.context == "fork"`, the skill runs in an isolated sub-agent with its own token budget.

```rust
async fn execute_forked_skill(
    command: &PromptCommand,
    command_name: &str,
    args: &str,
    context: &Context,
    can_use_tool: &CanUseToolFn,
    parent_message: &AssistantMessage,
    on_progress: Option<&ProgressFn>,
) -> ToolResult<SkillOutput> {
    let start_time = Instant::now();
    let agent_id = create_agent_id();

    // 1. Prepare the forked context
    let forked = prepare_forked_command_context(command, args, context);
    // forked contains:
    //   - modified_get_app_state (with allowed tools injected)
    //   - base_agent (agent definition, possibly from command.agent)
    //   - prompt_messages (skill content as user messages)
    //   - skill_content (raw text for progress reporting)

    // Merge skill effort into agent definition
    let agent_def = if let Some(effort) = command.effort {
        AgentDefinition { effort: Some(effort), ..forked.base_agent }
    } else {
        forked.base_agent
    };

    // 2. Run the sub-agent
    let mut agent_messages: Vec<Message> = vec![];

    let agent_stream = run_agent(AgentConfig {
        agent_definition: agent_def,
        prompt_messages: forked.prompt_messages,
        tool_use_context: Context {
            get_app_state: forked.modified_get_app_state,
            ..context.clone()
        },
        can_use_tool,
        is_async: false,
        query_source: "agent:custom",
        model: command.model.as_deref(),
        available_tools: &context.options.tools,
        override_: AgentOverride { agent_id: agent_id.clone() },
    });

    for message in agent_stream {
        agent_messages.push(message.clone());

        // 3. Report progress for tool uses
        if let Some(on_progress) = on_progress {
            if message.has_tool_content() {
                on_progress(SkillProgress {
                    tool_use_id: format!("skill_{}", parent_message.id),
                    data: SkillProgressData {
                        message: message.clone(),
                        type_: "skill_progress",
                        prompt: forked.skill_content.clone(),
                        agent_id: agent_id.clone(),
                    },
                });
            }
        }
    }

    // 4. Extract the result text from the last assistant message
    let result_text = extract_result_text(&agent_messages, "Skill execution completed");
    // extract_result_text: finds last assistant message, joins text content blocks

    // 5. Cleanup — release skill from invoked state
    clear_invoked_skills_for_agent(&agent_id);  // MUST happen even on error

    Ok(ToolResult {
        data: SkillOutput::Forked {
            success: true,
            command_name: command_name.into(),
            agent_id,
            result: result_text,
        },
        new_messages: None,
        context_modifier: None,
    })
}
```

**Sub-agent context details**:
- Cloned file state cache
- New abort controller linked to parent's (parent abort → child abort)
- Fresh nested memory/skill/toolDecision sets
- `should_avoid_permission_prompts = true` — background agents can't show UI
- Skill content registered with `add_invoked_skill` for compaction survival

### 6.3 Path C: Remote Canonical Skills (experimental, ant-only)

```rust
async fn execute_remote_skill(
    slug: &str,
    command_name: &str,
    parent_message: &AssistantMessage,
    context: &Context,
) -> ToolResult<SkillOutput> {
    // 1. Get discovery metadata (validated in validate_input)
    let meta = get_discovered_remote_skill(slug)
        .ok_or("Remote skill not discovered in this session")?;

    // 2. Load from remote storage (with local cache)
    let load_result = load_remote_skill(slug, &meta.url)?;
    // load_result: { cache_hit, latency_ms, skill_path, content, file_count, total_bytes }

    // 3. Process content
    //    a. Strip YAML frontmatter (---\nname: x\n---)
    let body_content = strip_frontmatter(&load_result.content);

    //    b. Inject base directory header
    let skill_dir = parent_dir(&load_result.skill_path);
    let mut final_content = format!(
        "Base directory for this skill: {}\n\n{}",
        skill_dir, body_content
    );

    //    c. Substitute placeholders
    final_content = final_content
        .replace("${CLAUDE_SKILL_DIR}", &skill_dir)
        .replace("${CLAUDE_SESSION_ID}", &get_session_id());

    // 4. Register for compaction survival
    add_invoked_skill(command_name, &load_result.skill_path, &final_content, None);

    // 5. Wrap in user message and inject
    let tool_use_id = get_tool_use_id_from_parent_message(parent_message, "Skill");
    let messages = vec![
        tag_with_tool_use_id(
            create_user_message(UserMessageContent {
                content: final_content,
                is_meta: true,
            }),
            &tool_use_id,
        ),
    ];

    Ok(ToolResult {
        data: SkillOutput::Inline {
            success: true,
            command_name: command_name.into(),
            allowed_tools: None,
            model: None,
        },
        new_messages: Some(messages),
        context_modifier: None,
    })
}
```

---

## 7. Output Formatting (tool_result → model)

```rust
fn map_to_tool_result(output: &SkillOutput, tool_use_id: &str) -> ToolResultBlockParam {
    let content = match output {
        SkillOutput::Forked { command_name, result, .. } => {
            format!(
                "Skill \"{}\" completed (forked execution).\n\nResult:\n{}",
                command_name, result
            )
        }
        SkillOutput::Inline { command_name, .. } => {
            format!("Launching skill: {}", command_name)
        }
    };

    ToolResultBlockParam {
        type_: "tool_result".into(),
        tool_use_id: tool_use_id.into(),
        content,
    }
}
```

---

## 8. Skill Discovery & Registry

### 8.1 Command Sources

Skills come from multiple sources, unified through a single `Command` registry:

| Source | `loaded_from` | Priority | Description |
|--------|--------------|----------|-------------|
| Bundled | `"bundled"` | Highest | Compiled into binary |
| User skills | `"skills"` | High | From `/.claude/skills/` or project `/skills/` directory |
| Plugin | `"plugin"` | Medium | From marketplace plugins |
| MCP | `"mcp"` | Medium | From MCP server connections |
| Legacy | `"commands_DEPRECATED"` | Low | From deprecated `/commands/` directory |

### 8.2 Model-Discoverable Filter

Not all commands are exposed to the model. The model sees skills that pass this filter:

```rust
fn get_skill_tool_commands(commands: &[Command]) -> Vec<Command> {
    commands.iter().filter(|cmd|
        cmd.type_ == CommandType::Prompt          // Must be prompt-based
        && !cmd.disable_model_invocation          // Not blocked from model use
        && cmd.source != "builtin"                // Not a built-in CLI command
        && (cmd.loaded_from == "bundled"           // Is bundled
            || cmd.loaded_from == "skills"         // Is a user skill
            || cmd.loaded_from == "commands_DEPRECATED" // Is legacy
            || cmd.has_user_specified_description  // Has a user-provided description
            || cmd.when_to_use.is_some())          // Has discovery hint
    ).cloned().collect()
}
```

### 8.3 Skill Listing in Prompt

Skills are listed in system-reminder messages with budget constraints:
- **Budget**: 1% of context window (in characters), default 8,000 chars
- **Per-entry cap**: 250 chars max for description (discovery only — full content loads on invoke)
- **Bundled skills**: Never truncated — always get full descriptions
- **Non-bundled**: Descriptions truncated to fit budget, or names-only in extreme cases

Format: `- skill-name: description text`

### 8.4 MCP Skill Integration

MCP skills are loaded from `AppState.mcp.commands`:
```rust
let mcp_skills = context.app_state.mcp.commands.iter()
    .filter(|cmd| cmd.type_ == "prompt" && cmd.loaded_from == "mcp")
    .collect();

// Merge with local commands, dedup by name (local takes priority)
let all_commands = dedup_by_name(local_commands, mcp_skills);
```

### 8.5 Bundled Skills

Examples of bundled skills in the codebase:
`updateConfig`, `keybindings`, `verify`, `debug`, `loremIpsum`, `skillify`, `remember`, `simplify`, `batch`, `stuck`, `loop`, `scheduleRemoteAgents`, `claudeApi`

Each is registered via:
```rust
fn register_bundled_skill(def: BundledSkillDefinition) {
    // Creates a Command with source="bundled", loadedFrom="bundled"
    // Stores getPromptForCommand as async closure
    // Handles file extraction on first invocation if def.files is set
}
```

---

## 9. Skill Definition Structure

```rust
struct SkillDefinition {
    name: String,
    description: String,
    aliases: Vec<String>,
    when_to_use: Option<String>,       // Discovery hint for model
    argument_hint: Option<String>,      // Example args for model
    allowed_tools: Vec<String>,         // Tools auto-allowed by this skill
    model: Option<String>,              // Model override (e.g., "opus", "sonnet")
    effort: Option<String>,             // Effort level override
    disable_model_invocation: bool,     // If true, model can't invoke via SkillTool
    user_invocable: bool,               // If true, user can invoke via /name
    context: SkillContext,              // Inline or fork
    agent: Option<String>,              // Base agent type for forked execution
    hooks: Vec<SkillHook>,              // Lifecycle hooks
    files: Option<Vec<SkillFileRef>>,   // Reference files extracted on first use
    get_prompt_for_command: AsyncFn,    // Returns the skill's prompt content
}

enum SkillContext {
    /// Inject prompt into conversation (default)
    Inline,
    /// Run in isolated sub-agent
    Fork,
}
```

---

## 10. System Prompt (tool description)

```text
Execute a skill within the main conversation

When users ask you to perform tasks, check if any of the available skills match.
Skills provide specialized capabilities and domain knowledge.

When users reference a "slash command" or "/<something>" (e.g., "/commit",
"/review-pr"), they are referring to a skill. Use this tool to invoke it.

How to invoke:
- Use this tool with the skill name and optional arguments
- Examples:
  - `skill: "pdf"` - invoke the pdf skill
  - `skill: "commit", args: "-m 'Fix bug'"` - invoke with arguments
  - `skill: "review-pr", args: "123"` - invoke with arguments
  - `skill: "ms-office-suite:pdf"` - invoke using fully qualified name

Important:
- Available skills are listed in system-reminder messages in the conversation
- When a skill matches the user's request, this is a BLOCKING REQUIREMENT:
  invoke the relevant Skill tool BEFORE generating any other response about the task
- NEVER mention a skill without actually calling this tool
- Do not invoke a skill that is already running
- Do not use this tool for built-in CLI commands (like /help, /clear, etc.)
- If you see a <command-name> tag in the current conversation turn, the skill has
  ALREADY been loaded - follow the instructions directly instead of calling this tool again
```

---

## 11. Placeholder Substitution

When expanding skill content, these placeholders are replaced:

| Placeholder | Replacement | Notes |
|-------------|-------------|-------|
| `$ARGUMENTS` | The `args` string from input | Only for inline/forked skills |
| `${CLAUDE_SKILL_DIR}` | Absolute path to skill's base directory | For file-relative references |
| `${CLAUDE_SESSION_ID}` | Current session UUID | For session-scoped state |
| `!command` | Output of shell command execution | Legacy, processed by processSlashCommand |

---

## 12. Compaction Survival

Skills must survive message compaction (when context window fills up). This is handled by:

```rust
// Called during skill loading
add_invoked_skill(
    command_name,  // Skill name for identification
    skill_path,    // Source file path (for restoration)
    skill_content, // Full expanded content (post-placeholder-substitution)
    agent_id,      // None for main thread, Some for subagents
);

// Called during forked skill cleanup
clear_invoked_skills_for_agent(agent_id);
```

The harness stores invoked skills in bootstrap state. During compaction, skill content is preserved/restored so the model doesn't lose context about active skills.

---

## 13. Implementation Notes for Rust Harness

### 13.1 The `newMessages` Return

Inline skills return `new_messages` — user messages injected into the conversation AFTER the tool result. These messages contain the skill's prompt content. They are tagged with `sourceToolUseID` so they can be treated as transient (cleaned up if the tool is retried or the conversation is compacted).

### 13.2 The `contextModifier` Return

Inline skills return a `context_modifier` closure that modifies the `Context` for all subsequent tool calls in the same turn. This is how skills inject allowed tools and model overrides. The harness must apply this modifier to the context before processing subsequent tool calls.

### 13.3 Model Override Resolution

When a skill specifies `model: "opus"`, the harness should resolve it against the current main loop model to preserve context window suffixes:

```rust
fn resolve_skill_model_override(skill_model: &str, current_model: &str) -> String {
    // If current model has [1m] suffix, carry it over
    // "opus" + "claude-opus-4-6[1m]" → "claude-opus-4-6[1m]" (not "claude-opus-4-6")
    // This prevents a skill from accidentally downgrading the context window
}
```

### 13.4 `toAutoClassifierInput`

Returns just the skill name: `input.skill`. Used by the permission auto-classifier to match against rules. Also used by the "skill coach" to track which skills have been invoked (preventing false-positive suggestions).

### 13.5 Skill Usage Tracking

After a skill executes, call `record_skill_usage(command_name)` to track usage frequency. This informs skill ranking in the discovery prompt.

### 13.6 The `<command-name>` Tag Guard

The system prompt instructs the model: if it sees a `<command-name>` XML tag in the current turn, the skill has ALREADY been loaded — don't call SkillTool again. This prevents infinite loops where the model sees a skill's output and tries to invoke the skill again.

### 13.7 Forked Skill Cleanup

The `clear_invoked_skills_for_agent(agent_id)` call in the `finally` block is critical. Without it, forked skill content accumulates in memory across multiple skill invocations, causing memory leaks.

### 13.8 One Skill at a Time

The comment "Only one skill/command should run at a time" is enforced by the harness, not the tool itself. The harness should not parallelize SkillTool calls because the skill prompt expansion assumes exclusive access to the conversation context.

### 13.9 Error Codes Summary

| Code | Meaning |
|------|---------|
| 1 | Invalid/empty skill name |
| 2 | Unknown skill (not found in registry) |
| 4 | Skill has `disableModelInvocation` flag |
| 5 | Command exists but is not a prompt-based skill |
| 6 | Remote skill not discovered in session |

(Code 3 is unused/skipped)
