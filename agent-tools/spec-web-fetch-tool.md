# WebFetchTool — Implementation Spec for Rust Harness

## Overview

WebFetchTool fetches content from a URL, converts HTML to markdown, optionally processes it through a secondary model (Haiku), and returns the extracted information. It has a sophisticated permission model with a preapproved domain list, a domain safety preflight check against `api.anthropic.com`, redirect handling, LRU caching, and binary content persistence.

---

## 1. Tool Identity & Registration

```
name:               "WebFetch"
search_hint:        "fetch and extract content from a URL"
user_facing_name:   "Fetch"
should_defer:       true       // Discoverable via ToolSearch only
is_read_only:       true       // No side effects (besides caching)
is_concurrency_safe: true      // Safe to run in parallel
max_result_size:    100_000    // Characters before result persistence
```

---

## 2. Input Schema

```rust
struct WebFetchInput {
    /// Fully-qualified URL to fetch. Must be valid.
    url: String,

    /// Instruction describing what to extract from the page.
    /// Example: "Extract the API reference for the `reqwest` crate"
    prompt: String,
}
```

### Validation

| Rule | Error Code | Message |
|------|-----------|---------|
| URL fails to parse | 1 | `"Error: Invalid URL \"{url}\". The URL provided could not be parsed."` |

---

## 3. Output Schema

```rust
struct WebFetchOutput {
    /// Size of raw fetched content in bytes
    bytes: usize,

    /// HTTP response status code (200, 301, 404, etc.)
    code: u16,

    /// HTTP status text ("OK", "Not Found", etc.)
    code_text: String,

    /// The processed/extracted result string
    result: String,

    /// Total wall-clock time for fetch + processing in milliseconds
    duration_ms: u64,

    /// The URL that was fetched (original, not redirected)
    url: String,
}
```

---

## 4. Permission Model — Hierarchical Check

Permission checks happen in strict order. First match wins.

```rust
fn check_permissions(&self, input: &WebFetchInput, context: &Context) -> PermissionDecision {
    let parsed_url = Url::parse(&input.url)?;
    let hostname = parsed_url.host_str().unwrap_or("");
    let pathname = parsed_url.path();

    // 1. Preapproved domains — auto-allow, no user prompt
    if is_preapproved_host(hostname, pathname) {
        return PermissionDecision::Allow { reason: "Preapproved host" };
    }

    let rule_content = format!("domain:{}", hostname);

    // 2. Deny rules — block immediately
    if let Some(rule) = find_deny_rule(&rule_content) {
        return PermissionDecision::Deny {
            message: format!("WebFetch denied access to {}.", rule_content),
        };
    }

    // 3. Ask rules — prompt user with existing ask rule
    if let Some(_rule) = find_ask_rule(&rule_content) {
        return PermissionDecision::Ask {
            message: "Claude requested permissions to use WebFetch, but you haven't granted it yet.",
            suggestions: build_suggestions(&rule_content),
        };
    }

    // 4. Allow rules — user has pre-authorized this domain
    if let Some(rule) = find_allow_rule(&rule_content) {
        return PermissionDecision::Allow { reason: "Allow rule matched" };
    }

    // 5. Default — ask user, suggest adding a rule for this domain
    PermissionDecision::Ask {
        message: "Claude requested permissions to use WebFetch, but you haven't granted it yet.",
        suggestions: build_suggestions(&rule_content),
    }
}

fn build_suggestions(rule_content: &str) -> Vec<PermissionSuggestion> {
    vec![PermissionSuggestion {
        type_: "addRules",
        destination: "localSettings",
        rules: vec![PermissionRule {
            tool_name: "WebFetch",
            rule_content: Some(rule_content.to_string()),
        }],
        behavior: "allow",
    }]
}
```

**Critical detail**: The permission `rule_content` is `"domain:{hostname}"` — not the full URL. This means allowing `domain:docs.rust-lang.org` allows ALL paths on that domain.

---

## 5. Preapproved Domains

### 5.1 Full Domain List

These domains are auto-allowed without user permission prompts AND (for markdown content < 100K) skip secondary model processing:

**Anthropic**
`platform.claude.com`, `code.claude.com`, `modelcontextprotocol.io`, `agentskills.io`

**Path-scoped**: `github.com/anthropics` — only matches paths `/anthropics` or `/anthropics/*` (NOT `/anthropics-evil/malware`)

**Programming Languages**
`docs.python.org`, `en.cppreference.com`, `docs.oracle.com`, `learn.microsoft.com`, `developer.mozilla.org`, `go.dev`, `pkg.go.dev`, `www.php.net`, `docs.swift.org`, `kotlinlang.org`, `ruby-doc.org`, `doc.rust-lang.org`, `www.typescriptlang.org`

**Web/JS Frameworks**
`react.dev`, `angular.io`, `vuejs.org`, `nextjs.org`, `expressjs.com`, `nodejs.org`, `bun.sh`, `jquery.com`, `getbootstrap.com`, `tailwindcss.com`, `d3js.org`, `threejs.org`, `redux.js.org`, `webpack.js.org`, `jestjs.io`, `reactrouter.com`

**Python Frameworks**
`docs.djangoproject.com`, `flask.palletsprojects.com`, `fastapi.tiangolo.com`, `pandas.pydata.org`, `numpy.org`, `www.tensorflow.org`, `pytorch.org`, `scikit-learn.org`, `matplotlib.org`, `requests.readthedocs.io`, `jupyter.org`

**PHP**
`laravel.com`, `symfony.com`, `wordpress.org`

**Java**
`docs.spring.io`, `hibernate.org`, `tomcat.apache.org`, `gradle.org`, `maven.apache.org`

**.NET**
`asp.net`, `dotnet.microsoft.com`, `nuget.org`, `blazor.net`

**Mobile**
`reactnative.dev`, `docs.flutter.dev`, `developer.apple.com`, `developer.android.com`

**Data Science/ML**
`keras.io`, `spark.apache.org`, `huggingface.co`, `www.kaggle.com`

**Databases**
`www.mongodb.com`, `redis.io`, `www.postgresql.org`, `dev.mysql.com`, `www.sqlite.org`, `graphql.org`, `prisma.io`

**Cloud/DevOps**
`docs.aws.amazon.com`, `cloud.google.com`, `learn.microsoft.com`, `kubernetes.io`, `www.docker.com`, `www.terraform.io`, `www.ansible.com`, `vercel.com/docs`, `docs.netlify.com`, `devcenter.heroku.com`

**Testing**
`cypress.io`, `selenium.dev`

**Game Dev**
`docs.unity.com`, `docs.unrealengine.com`

**Other**
`git-scm.com`, `nginx.org`, `httpd.apache.org`

### 5.2 Path-Scoped Matching Logic

```rust
fn is_preapproved_host(hostname: &str, pathname: &str) -> bool {
    // Fast path: hostname-only entries (vast majority)
    if HOSTNAME_ONLY_SET.contains(hostname) {
        return true;
    }
    // Slow path: path-prefix entries (e.g., "github.com/anthropics")
    if let Some(prefixes) = PATH_PREFIX_MAP.get(hostname) {
        for prefix in prefixes {
            // Must match exactly OR have a "/" after the prefix
            // "/anthropics" matches, "/anthropics/" matches
            // "/anthropics-evil" does NOT match
            if pathname == prefix || pathname.starts_with(&format!("{}/", prefix)) {
                return true;
            }
        }
    }
    false
}
```

### 5.3 Security Warning

The preapproved list is **ONLY for WebFetch GET requests**. The sandbox network restrictions must NOT inherit this list. Some domains (huggingface.co, kaggle.com, nuget.org) accept file uploads — unrestricted network access to them would enable data exfiltration.

---

## 6. Core Execution Flow

### 6.1 URL Validation (pre-fetch)

```rust
const MAX_URL_LENGTH: usize = 2000;

fn validate_url(url: &str) -> bool {
    if url.len() > MAX_URL_LENGTH { return false; }

    let parsed = match Url::parse(url) {
        Ok(u) => u,
        Err(_) => return false,
    };

    // Block URLs with embedded credentials
    if !parsed.username().is_empty() || parsed.password().is_some() {
        return false;
    }

    // Must have a real domain (at least 2 dot-separated parts)
    let hostname = parsed.host_str().unwrap_or("");
    hostname.split('.').count() >= 2
}
```

### 6.2 Cache Lookup

```rust
// URL content cache: 15min TTL, 50MB max total size
struct UrlCache {
    cache: LruCache<String, CacheEntry>,  // keyed by ORIGINAL url (not upgraded/redirected)
    ttl: Duration,                         // 15 minutes
    max_size: usize,                       // 50 * 1024 * 1024
}

struct CacheEntry {
    bytes: usize,
    code: u16,
    code_text: String,
    content: String,      // markdown or raw text
    content_type: String,
    persisted_path: Option<String>,
    persisted_size: Option<usize>,
}

// Domain check cache: 5min TTL, 128 entries max
// Only caches "allowed" results — blocked/failed re-check next time
struct DomainCheckCache {
    cache: LruCache<String, ()>,  // key = hostname, value = unit (presence = allowed)
    ttl: Duration,                // 5 minutes
    max_entries: usize,           // 128
}
```

**Cache key**: Always the ORIGINAL URL the user provided, not the upgraded (http→https) or redirected URL.

### 6.3 Domain Preflight Check

```rust
const DOMAIN_CHECK_TIMEOUT: Duration = Duration::from_secs(10);

async fn check_domain_blocklist(domain: &str) -> DomainCheckResult {
    // Check cache first
    if domain_check_cache.contains(domain) {
        return DomainCheckResult::Allowed;
    }

    let url = format!(
        "https://api.anthropic.com/api/web/domain_info?domain={}",
        urlencoding::encode(domain)
    );

    match http_get(&url, DOMAIN_CHECK_TIMEOUT).await {
        Ok(response) if response.status == 200 => {
            if response.json().can_fetch == true {
                domain_check_cache.insert(domain.to_string(), ());
                DomainCheckResult::Allowed
            } else {
                DomainCheckResult::Blocked
            }
        }
        Ok(response) => DomainCheckResult::CheckFailed(
            format!("Domain check returned status {}", response.status)
        ),
        Err(e) => DomainCheckResult::CheckFailed(e.to_string()),
    }
}

enum DomainCheckResult {
    Allowed,
    Blocked,
    CheckFailed(String),
}
```

**Bypass**: If `settings.skip_web_fetch_preflight` is `true`, skip this entirely. Enterprise customers may need this when `api.anthropic.com` is blocked by corporate firewalls.

### 6.4 HTTP Fetch with Redirect Handling

```rust
const FETCH_TIMEOUT: Duration = Duration::from_secs(60);
const MAX_HTTP_CONTENT_LENGTH: usize = 10 * 1024 * 1024;  // 10MB
const MAX_REDIRECTS: usize = 10;

async fn get_with_permitted_redirects(
    url: &str,
    signal: &AbortSignal,
    depth: usize,
) -> Result<HttpResponse, FetchError> {
    if depth > MAX_REDIRECTS {
        return Err(FetchError::TooManyRedirects);
    }

    let response = http_get_raw(url, HttpOptions {
        timeout: FETCH_TIMEOUT,
        max_redirects: 0,            // We handle redirects manually!
        response_type: ResponseType::Binary,
        max_content_length: MAX_HTTP_CONTENT_LENGTH,
        headers: vec![
            ("Accept", "text/markdown, text/html, */*"),
            ("User-Agent", get_web_fetch_user_agent()),
        ],
        signal,
    }).await;

    match response {
        Ok(resp) => Ok(resp),
        Err(e) if e.is_redirect() => {
            let status = e.status_code();
            let location = e.header("location")
                .ok_or(FetchError::MissingLocationHeader)?;

            // Resolve relative URLs against the original
            let redirect_url = Url::parse(url)?.join(location)?.to_string();

            if is_permitted_redirect(url, &redirect_url) {
                // Same-domain redirect — follow it
                get_with_permitted_redirects(&redirect_url, signal, depth + 1).await
            } else {
                // Cross-host redirect — return info to caller
                Ok(HttpResponse::Redirect {
                    original_url: url.to_string(),
                    redirect_url,
                    status_code: status,
                })
            }
        }
        Err(e) if e.is_egress_blocked() => {
            // Proxy returned 403 with X-Proxy-Error: blocked-by-allowlist
            Err(FetchError::EgressBlocked(extract_hostname(url)))
        }
        Err(e) => Err(e.into()),
    }
}
```

### 6.5 Redirect Safety Check

```rust
fn is_permitted_redirect(original_url: &str, redirect_url: &str) -> bool {
    let orig = Url::parse(original_url).ok();
    let redir = Url::parse(redirect_url).ok();
    let (Some(orig), Some(redir)) = (orig, redir) else { return false; };

    // Must keep same protocol
    if redir.scheme() != orig.scheme() { return false; }

    // Must keep same port
    if redir.port() != orig.port() { return false; }

    // No credentials in redirect
    if !redir.username().is_empty() || redir.password().is_some() { return false; }

    // Hostname must match ignoring "www." prefix
    let strip_www = |h: &str| h.strip_prefix("www.").unwrap_or(h);
    strip_www(orig.host_str().unwrap_or("")) == strip_www(redir.host_str().unwrap_or(""))
}
```

### 6.6 Content Processing

```rust
const MAX_MARKDOWN_LENGTH: usize = 100_000;

async fn process_fetched_content(
    raw_bytes: &[u8],
    content_type: &str,
    url: &str,
) -> ProcessedContent {
    // 1. Binary content: save to disk
    let mut persisted_path = None;
    let mut persisted_size = None;
    if is_binary_content_type(content_type) {
        let persist_id = format!("webfetch-{}-{}", timestamp_ms(), random_hex(6));
        if let Ok(result) = persist_binary_content(raw_bytes, content_type, &persist_id) {
            persisted_path = Some(result.filepath);
            persisted_size = Some(result.size);
        }
    }

    // 2. Decode as UTF-8
    let text_content = String::from_utf8_lossy(raw_bytes).to_string();

    // 3. Convert HTML to markdown
    let markdown = if content_type.contains("text/html") {
        html_to_markdown(&text_content)  // Use a Turndown equivalent (e.g., htmd crate)
    } else {
        text_content
    };

    ProcessedContent { markdown, persisted_path, persisted_size }
}
```

### 6.7 Secondary Model Processing

```rust
async fn apply_prompt_to_markdown(
    prompt: &str,
    markdown: &str,
    signal: &AbortSignal,
    is_non_interactive: bool,
    is_preapproved: bool,
) -> String {
    // Truncate to fit model context
    let truncated = if markdown.len() > MAX_MARKDOWN_LENGTH {
        format!("{}\n\n[Content truncated due to length...]", &markdown[..MAX_MARKDOWN_LENGTH])
    } else {
        markdown.to_string()
    };

    let guidelines = if is_preapproved {
        "Provide a concise response based on the content above. \
         Include relevant details, code examples, and documentation excerpts as needed."
    } else {
        "Provide a concise response based only on the content above. In your response:\n\
         - Enforce a strict 125-character maximum for quotes from any source document. \
           Open Source Software is ok as long as we respect the license.\n\
         - Use quotation marks for exact language from articles; any language outside of \
           the quotation should never be word-for-word the same.\n\
         - You are not a lawyer and never comment on the legality of your own prompts and responses.\n\
         - Never produce or reproduce exact song lyrics."
    };

    let model_prompt = format!(
        "Web page content:\n---\n{}\n---\n\n{}\n\n{}",
        truncated, prompt, guidelines
    );

    // Call Haiku (small fast model) with no system prompt
    let response = query_haiku(HaikuRequest {
        system_prompt: vec![],
        user_prompt: model_prompt,
        signal,
        query_source: "web_fetch_apply",
    }).await;

    // Extract text from response
    response.content.first()
        .and_then(|block| block.as_text())
        .unwrap_or("No response from model")
        .to_string()
}
```

### 6.8 Preapproved Domain Shortcut

For preapproved domains serving markdown content under 100K, skip the secondary model entirely and return raw content:

```rust
if is_preapproved_url(&url)
    && content_type.contains("text/markdown")
    && content.len() < MAX_MARKDOWN_LENGTH
{
    return content;  // Raw markdown, no Haiku processing
}
```

---

## 7. Redirect Response to Model

When a cross-host redirect is detected, the tool returns a special message instead of content:

```rust
let message = format!(
    "REDIRECT DETECTED: The URL redirects to a different host.\n\n\
     Original URL: {}\n\
     Redirect URL: {}\n\
     Status: {} {}\n\n\
     To complete your request, I need to fetch content from the redirected URL. \
     Please use WebFetch again with these parameters:\n\
     - url: \"{}\"\n\
     - prompt: \"{}\"",
    original_url, redirect_url, status_code, status_text, redirect_url, prompt
);
```

The model is expected to make a second WebFetch call with the redirect URL.

---

## 8. Output Formatting (tool_result → model)

Simply return the `result` field directly:

```rust
fn map_to_tool_result(output: &WebFetchOutput, tool_use_id: &str) -> ToolResultBlockParam {
    ToolResultBlockParam {
        type_: "tool_result".into(),
        tool_use_id: tool_use_id.into(),
        content: output.result.clone(),
    }
}
```

---

## 9. Error Types

```rust
enum WebFetchError {
    /// Domain is on Anthropic's blocklist
    DomainBlocked { domain: String },
    // Message: "Claude Code is unable to fetch from {domain}"

    /// Could not reach api.anthropic.com to verify domain
    DomainCheckFailed { domain: String },
    // Message: "Unable to verify if domain {domain} is safe to fetch.
    //           This may be due to network restrictions or enterprise security policies
    //           blocking claude.ai."

    /// Egress proxy blocked the request (403 + X-Proxy-Error header)
    EgressBlocked { domain: String },
    // Message JSON: {"error_type": "EGRESS_BLOCKED", "domain": "...", "message": "..."}

    /// URL validation failed
    InvalidUrl,

    /// Too many redirects (> 10 hops)
    TooManyRedirects,

    /// Redirect missing Location header
    MissingLocationHeader,
}
```

---

## 10. System Prompt / Tool Description

```text
IMPORTANT: WebFetch WILL FAIL for authenticated or private URLs. Before using this tool,
check if the URL points to an authenticated service (e.g. Google Docs, Confluence, Jira,
GitHub). If so, look for a specialized MCP tool that provides authenticated access.

- Fetches content from a specified URL and processes it using an AI model
- Takes a URL and a prompt as input
- Fetches the URL content, converts HTML to markdown
- Processes the content with the prompt using a small, fast model
- Returns the model's response about the content
- Use this tool when you need to retrieve and analyze web content

Usage notes:
  - IMPORTANT: If an MCP-provided web fetch tool is available, prefer using that tool instead
  - The URL must be a fully-formed valid URL
  - HTTP URLs will be automatically upgraded to HTTPS
  - The prompt should describe what information you want to extract from the page
  - This tool is read-only and does not modify any files
  - Results may be summarized if the content is very large
  - Includes a self-cleaning 15-minute cache for faster responses when repeatedly accessing the same URL
  - When a URL redirects to a different host, the tool will inform you and provide the redirect URL
  - For GitHub URLs, prefer using the gh CLI via Bash instead
```

The auth warning is ALWAYS included regardless of whether ToolSearch is present. Toggling it would invalidate the Anthropic API prompt cache.

---

## 11. Implementation Notes for Rust Harness

### 11.1 HTTP Client Requirements
- Must support binary response body (for PDFs, images)
- Must support manual redirect handling (maxRedirects=0 + Location header inspection)
- Must support abort/cancellation via signal
- Must set `Accept: text/markdown, text/html, */*`
- Must set a recognizable User-Agent string

### 11.2 HTML-to-Markdown
Use a Rust HTML-to-markdown library (e.g., `htmd`, `html2md`). The TypeScript implementation uses Turndown. Key requirement: lazy initialization (the parser can be heavy) and stateless reuse across calls.

### 11.3 Memory Management
After reading the HTTP response into a `Vec<u8>`, release the original response body reference. HTML-to-markdown conversion can consume 3-5x the input size in DOM nodes — holding both the raw bytes and the DOM simultaneously can spike memory.

### 11.4 Cache Size Accounting
The LRU cache uses content size (in bytes) for eviction, not entry count. When inserting, pass the actual byte length of the markdown content. Clamp to minimum 1 for empty responses.

### 11.5 Protocol Upgrade
Always upgrade `http://` to `https://` before making the request. But cache the result under the ORIGINAL URL (before upgrade).

### 11.6 Binary Content Persistence
When `is_binary_content_type(content_type)` is true (PDFs, images, archives, etc.), save the raw bytes to a temp file with the appropriate extension derived from the MIME type. Include the file path in the result: `"[Binary content (application/pdf, 2.3 MB) also saved to /tmp/webfetch-1234-abc.pdf]"`.

### 11.7 The `description` callback
Dynamic based on input URL: `"Claude wants to fetch content from {hostname}"` (or `"Claude wants to fetch content from this URL"` if URL parsing fails). Shown to user in permission prompts.

### 11.8 The `toAutoClassifierInput` callback
Returns `"{url}: {prompt}"` if prompt is non-empty, otherwise just `"{url}"`. Used by the permission auto-classifier to determine if the action should be allowed.

### 11.9 `clearWebFetchCache()`
Expose a function to clear both the URL cache and domain check cache. Called when the user wants a fresh fetch or during session reset.
