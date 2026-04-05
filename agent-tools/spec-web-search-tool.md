# WebSearchTool — Implementation Spec for Rust Harness

## Overview

WebSearchTool provides web search capability by delegating to Anthropic's native `web_search_20250305` server-side tool. The harness does NOT perform web searches directly — it constructs a sub-request to the Anthropic Messages API with the search tool attached, streams the response, and reformats the results for the outer conversation.

---

## 1. Tool Identity & Registration

```
name:               "WebSearch"
search_hint:        "search the web for current information"
user_facing_name:   "Web Search"
should_defer:       true       // Only discoverable via ToolSearch
is_read_only:       true       // No side effects
is_concurrency_safe: true      // Safe to run in parallel
max_result_size:    100_000    // Characters before result persistence kicks in
```

**Deferred tool**: This tool is NOT included in the initial tool list sent to the model. The model must first call `ToolSearch` to discover it. This reduces prompt bloat on turns where web search isn't needed.

---

## 2. Input Schema

```rust
struct WebSearchInput {
    /// The search query. Required. Minimum 2 characters.
    query: String,

    /// Restrict results to only these domains. Optional.
    /// Example: ["docs.rust-lang.org", "crates.io"]
    allowed_domains: Option<Vec<String>>,

    /// Exclude results from these domains. Optional.
    /// Example: ["pinterest.com", "quora.com"]
    blocked_domains: Option<Vec<String>>,
}
```

### Validation Rules

| Rule | Error Code | Message |
|------|-----------|---------|
| `query` is empty or length < 2 | 1 | `"Error: Missing query"` |
| Both `allowed_domains` and `blocked_domains` are non-empty | 2 | `"Error: Cannot specify both allowed_domains and blocked_domains in the same request"` |

Validation happens in `validate_input()`, BEFORE permission checks.

---

## 3. Output Schema

```rust
struct WebSearchOutput {
    /// The search query that was actually executed (may differ from input if model rewrites)
    query: String,

    /// Interleaved search results and text commentary from the model
    results: Vec<SearchResultOrText>,

    /// Wall-clock duration of the entire search operation in seconds
    duration_seconds: f64,
}

enum SearchResultOrText {
    /// A batch of search hits from one web_search invocation
    SearchResult(SearchResult),
    /// Text commentary/analysis from the model between searches
    Text(String),
}

struct SearchResult {
    /// The tool_use_id from the API (links back to the server_tool_use block)
    tool_use_id: String,
    /// The actual search hits
    content: Vec<SearchHit>,
}

struct SearchHit {
    title: String,
    url: String,
}
```

---

## 4. Permission Model

```rust
fn check_permissions(&self, _input: &WebSearchInput) -> PermissionDecision {
    PermissionDecision::Passthrough {
        message: "WebSearchTool requires permission.".into(),
        suggestions: vec![PermissionSuggestion {
            type_: "addRules",
            rules: vec![PermissionRule { tool_name: "WebSearch", rule_content: None }],
            behavior: "allow",
            destination: "localSettings",
        }],
    }
}
```

`Passthrough` means the tool requires explicit permission — the user must approve each invocation OR add a blanket allow rule. The suggestion helps the user create a persistent allow rule.

---

## 5. Enablement / Availability

The tool is conditionally available based on the API provider and model:

```rust
fn is_enabled(&self) -> bool {
    match get_api_provider() {
        // Anthropic's own API — always supports web search
        Provider::FirstParty => true,

        // Google Cloud Vertex AI — only Claude 4.0+ models
        Provider::Vertex => {
            let model = get_main_loop_model();
            model.contains("claude-opus-4")
                || model.contains("claude-sonnet-4")
                || model.contains("claude-haiku-4")
        }

        // AWS Foundry — only ships compatible models, always enabled
        Provider::Foundry => true,

        // Third-party proxies, Bedrock, etc. — no web search support
        _ => false,
    }
}
```

**Important for harness**: If your harness only targets firstParty, you can hardcode `true`. If multi-provider, implement the above logic.

---

## 6. Core Execution Flow

### 6.1 Overview

```
User query → Construct inner API call → Stream response → Parse content blocks → Format output
```

The tool makes a **nested API call** to the Anthropic Messages endpoint with the `web_search_20250305` beta tool attached. The API performs the actual search server-side and returns results as structured content blocks.

### 6.2 Step-by-Step

#### Step 1: Build the inner API request

```rust
let user_message = format!("Perform a web search for the query: {}", input.query);

let tool_schema = json!({
    "type": "web_search_20250305",
    "name": "web_search",
    "allowed_domains": input.allowed_domains,
    "blocked_domains": input.blocked_domains,
    "max_uses": 8  // Hardcoded cap — prevents runaway search loops
});

let system_prompt = "You are an assistant for performing a web search tool use";
```

#### Step 2: Select model and configure

```rust
// Feature flag determines whether to use a small/fast model
let use_haiku = get_feature_flag("tengu_plum_vx3", false);

let model = if use_haiku {
    get_small_fast_model()  // e.g., claude-haiku-4-5
} else {
    context.options.main_loop_model.clone()
};

let thinking_config = if use_haiku {
    ThinkingConfig::Disabled
} else {
    context.options.thinking_config.clone()
};

// Force the model to use the web_search tool when using Haiku
let tool_choice = if use_haiku {
    Some(ToolChoice::Tool { name: "web_search".into() })
} else {
    None
};
```

#### Step 3: Stream and collect events

The API returns a stream of events. The harness must track several pieces of state:

```rust
let mut all_content_blocks: Vec<ContentBlock> = vec![];
let mut current_tool_use_id: Option<String> = None;
let mut current_tool_use_json: String = String::new();
let mut progress_counter: usize = 0;
let mut tool_use_queries: HashMap<String, String> = HashMap::new();
```

Process each streaming event:

**Event: `content_block_start` with `server_tool_use`**
```rust
// A new web search is starting
current_tool_use_id = Some(content_block.id.clone());
current_tool_use_json.clear();
```

**Event: `content_block_delta` with `input_json_delta`**
```rust
// Accumulate partial JSON from the tool input
current_tool_use_json.push_str(&delta.partial_json);

// Try to extract the query from partial JSON for progress reporting
// Uses regex because JSON may be incomplete
let re = Regex::new(r#""query"\s*:\s*"((?:[^"\\]|\\.)*)""#).unwrap();
if let Some(captures) = re.captures(&current_tool_use_json) {
    let query = unescape_json_string(&captures[1]);
    if let Some(ref id) = current_tool_use_id {
        if tool_use_queries.get(id) != Some(&query) {
            tool_use_queries.insert(id.clone(), query.clone());
            progress_counter += 1;
            on_progress(WebSearchProgress::QueryUpdate { query });
        }
    }
}
```

**Event: `content_block_start` with `web_search_tool_result`**
```rust
// Search results have arrived
let tool_use_id = content_block.tool_use_id;
let actual_query = tool_use_queries
    .get(&tool_use_id)
    .cloned()
    .unwrap_or_else(|| input.query.clone());

let result_count = if content_block.content.is_array() {
    content_block.content.as_array().unwrap().len()
} else {
    0  // Error case
};

progress_counter += 1;
on_progress(WebSearchProgress::SearchResultsReceived {
    result_count,
    query: actual_query,
});
```

**Event: `assistant` (final message)**
```rust
all_content_blocks.extend(event.message.content);
```

#### Step 4: Parse content blocks into output

Iterate through the collected content blocks and build the output:

```rust
fn make_output_from_search_response(
    blocks: &[ContentBlock],
    query: &str,
    duration_seconds: f64,
) -> WebSearchOutput {
    let mut results: Vec<SearchResultOrText> = vec![];
    let mut text_acc = String::new();
    let mut in_text = true;

    for block in blocks {
        match block {
            ContentBlock::ServerToolUse(_) => {
                // Transition from text to search — flush accumulated text
                if in_text && !text_acc.trim().is_empty() {
                    results.push(SearchResultOrText::Text(text_acc.trim().to_string()));
                }
                text_acc.clear();
                in_text = false;
            }

            ContentBlock::WebSearchToolResult(result) => {
                match &result.content {
                    // Success: array of search hits
                    WebSearchContent::Results(hits) => {
                        results.push(SearchResultOrText::SearchResult(SearchResult {
                            tool_use_id: result.tool_use_id.clone(),
                            content: hits.iter().map(|r| SearchHit {
                                title: r.title.clone(),
                                url: r.url.clone(),
                            }).collect(),
                        }));
                    }
                    // Error: content is an error object, not an array
                    WebSearchContent::Error(err) => {
                        let msg = format!("Web search error: {}", err.error_code);
                        log_error(&msg);
                        results.push(SearchResultOrText::Text(msg));
                    }
                }
            }

            ContentBlock::Text(text_block) => {
                if !in_text {
                    in_text = true;
                    text_acc = text_block.text.clone();
                } else {
                    text_acc.push_str(&text_block.text);
                }
            }

            _ => {} // Ignore citation blocks etc.
        }
    }

    // Flush any trailing text
    if !text_acc.trim().is_empty() {
        results.push(SearchResultOrText::Text(text_acc.trim().to_string()));
    }

    WebSearchOutput { query: query.to_string(), results, duration_seconds }
}
```

**Critical detail**: The content blocks interleave `server_tool_use`, `web_search_tool_result`, `text`, and `citation` blocks. The parser must handle this interleaving correctly. Citation blocks can be ignored for the output — they're implicitly captured in the text.

---

## 7. Output Formatting (tool_result → model)

This is what the model sees as the result of calling `WebSearch`:

```rust
fn map_to_tool_result(output: &WebSearchOutput, tool_use_id: &str) -> ToolResultBlockParam {
    let mut formatted = format!("Web search results for query: \"{}\"\n\n", output.query);

    for result in &output.results {
        match result {
            SearchResultOrText::Text(text) => {
                formatted.push_str(text);
                formatted.push_str("\n\n");
            }
            SearchResultOrText::SearchResult(sr) => {
                if sr.content.is_empty() {
                    formatted.push_str("No links found.\n\n");
                } else {
                    // Serialize hits as JSON array
                    let json = serde_json::to_string(&sr.content).unwrap();
                    formatted.push_str(&format!("Links: {}\n\n", json));
                }
            }
        }
    }

    // CRITICAL: This reminder ensures the model cites sources
    formatted.push_str(
        "\nREMINDER: You MUST include the sources above in your response \
         to the user using markdown hyperlinks."
    );

    ToolResultBlockParam {
        type_: "tool_result".into(),
        tool_use_id: tool_use_id.into(),
        content: formatted.trim().to_string(),
    }
}
```

**Null safety**: Guard against null/undefined entries in `results` — they can appear after JSON round-tripping (compaction, transcript deserialization).

---

## 8. Progress Events

The tool emits progress events during streaming for UI rendering:

```rust
enum WebSearchProgress {
    /// The model is composing a search query
    QueryUpdate { query: String },
    /// Search results have been received from the API
    SearchResultsReceived { result_count: usize, query: String },
}
```

Each progress event carries a unique `tool_use_id` formatted as `"search-progress-{counter}"` or the actual tool_use_id from the API when available.

---

## 9. System Prompt / Tool Description

The tool's prompt (what the model sees as the tool description) must include:

```text
- Allows Claude to search the web and use the results to inform responses
- Provides up-to-date information for current events and recent data
- Returns search result information formatted as search result blocks, including links as markdown hyperlinks
- Use this tool for accessing information beyond Claude's knowledge cutoff
- Searches are performed automatically within a single API call

CRITICAL REQUIREMENT - You MUST follow this:
  - After answering the user's question, you MUST include a "Sources:" section at the end of your response
  - In the Sources section, list all relevant URLs from the search results as markdown hyperlinks: [Title](URL)
  - This is MANDATORY - never skip including sources in your response
  - Example format:

    [Your answer here]

    Sources:
    - [Source Title 1](https://example.com/1)
    - [Source Title 2](https://example.com/2)

Usage notes:
  - Domain filtering is supported to include or block specific websites
  - Web search is only available in the US

IMPORTANT - Use the correct year in search queries:
  - The current month is {CURRENT_MONTH_YEAR}. You MUST use this year when searching for recent information, documentation, or current events.
```

`{CURRENT_MONTH_YEAR}` must be dynamically replaced with the current month and year at prompt construction time (e.g., "April 2026").

---

## 10. Implementation Notes for Rust Harness

### 10.1 Streaming Integration
The inner API call MUST be streamed. The harness should use the Anthropic SDK's streaming mode or implement SSE parsing. Collecting the full response before processing would prevent progress reporting.

### 10.2 Abort Handling
The `signal` / abort controller from the parent context must be propagated to the inner API call. If the user cancels, the inner stream should be aborted.

### 10.3 Max Uses = 8
The `max_uses: 8` on the tool schema is critical. Without it, the model could invoke web_search indefinitely within the inner call, consuming tokens and time.

### 10.4 Query Extraction Regex
The regex `/"query"\s*:\s*"((?:[^"\\]|\\.)*)"/` is used on PARTIAL JSON. It will sometimes fail (incomplete string), which is expected — just catch/ignore parse errors. The extracted query is only for progress display, not correctness-critical.

### 10.5 Model Choice Trade-offs
- **Haiku**: Faster, cheaper, but may produce lower-quality search queries and summaries. Used when the feature flag is on.
- **Main loop model**: Higher quality but slower and more expensive. Gets the same thinking config as the outer loop.

### 10.6 Error Handling
- If the inner API call fails entirely: propagate the error as a tool error.
- If individual searches fail (error in `web_search_tool_result`): log the error and include it as text in results — don't fail the entire tool.
- The `WebSearchToolResultError` has an `error_code` field (string like `"max_uses_exceeded"`, `"no_results"`, etc.).

### 10.7 Caching Consideration
Unlike WebFetch, WebSearch has NO caching. Each invocation makes a fresh API call. The API itself may cache, but the harness doesn't.

### 10.8 The `description` callback
The description is dynamic based on input: `"Claude wants to search the web for: {query}"`. This is shown to the user in the permission prompt.
