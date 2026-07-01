<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Spec: WebFetch Tool

## Status

Draft — pending implementation.

## Summary

`webfetch` is a first-class Goa tool that fetches a URL, converts the returned
HTML page to structured Markdown, caches the result for the session, and lets
the model read the Markdown in line ranges like a local file
(`https://example.com/index.html:1:230`).

A configurable sub-agent summarization action allows the model to distill large
pages without pulling the full Markdown into context.

The HTML-to-Markdown conversion is implemented as a clean Go port of
[`../turndown`](https://github.com/mixmark-io/turndown) with a minimal but
extensible rule API, so additional options/plugin behavior can be added later
without breaking callers.

## Goals

- Retrieve any URL and present the page as structured Markdown.
- Support per-line reads of the Markdown with the same UX as `read`.
- Cache converted Markdown for the complete Goa session on disk, with TTL and
  size limits, so large pages do not blow up context.
- Allow optional sub-agent summarization, gated by configuration.
- Keep the Turndown port small, idiomatic, and extensible.

## Non-Goals

- Full feature parity with upstream Turndown on day one (plugin/options system
  can be layered later).
- JavaScript execution; `webfetch` operates on static HTML only.
- Authentication, cookies, or browser automation.

## Tool Schema

```json
{
  "name": "webfetch",
  "description": "Fetch a URL, convert the page to Markdown, and cache it for the session. Subsequent calls can read line ranges and optionally summarize via a sub-agent.",
  "parameters": {
    "type": "object",
    "properties": {
      "url": {
        "type": "string",
        "description": "Absolute URL to fetch (required)."
      },
      "action": {
        "type": "string",
        "description": "Action to perform.",
        "enum": ["fetch", "summarize"],
        "default": "fetch"
      },
      "start_line": {
        "type": "integer",
        "description": "First line to return (1-indexed, default: 1). Only for fetch."
      },
      "end_line": {
        "type": "integer",
        "description": "Last line to return (1-indexed, default: end of file). Only for fetch."
      },
      "max_lines": {
        "type": "integer",
        "description": "Maximum lines to return (default from config). Only for fetch."
      },
      "prompt": {
        "type": "string",
        "description": "Optional steering prompt for summarize action (e.g., 'Extract API endpoints'). Ignored for fetch."
      }
    },
    "required": ["url"]
  }
}
```

### Schema behavior

- `action: fetch` (default)
  - Fetches `url` if not already cached.
  - Converts HTML to Markdown.
  - Writes the Markdown to the session cache.
  - Returns the requested line range plus metadata:
    - total lines, lines shown, cache key, TTL remaining, whether content was
      truncated by `max_lines`.
- `action: summarize`
  - Requires the URL to be cached first (implicitly via a prior `fetch`).
  - Delegates the cached Markdown to a lightweight sub-agent with a
    summarization prompt.
  - Returns the summary plus the original metadata.
  - The `summarize` action is **omitted from the enum** when summarization is
    disabled in configuration or when no provider/model is configured, so the
    model cannot see or call it.

## Output format

```
webfetch https://example.com/index.html:1:230
[Markdown lines...]
(end — 230 lines shown, 1240 remaining; cache: <key>; ttl: 23h59m)
```

For summarize:

```
webfetch summarize https://example.com/index.html
[sub-agent summary]
(end summary — source: 1470 lines)
```

## Configuration

New config section under `tools.webfetch`:

```yaml
tools:
  webfetch:
    enabled: true
    max_lines_default: 250          # default max_lines when not specified
    max_lines_hard: 4096            # absolute cap for any fetch call
    max_total_bytes: 20971520       # 20 MiB: refuse to convert pages larger than this
    timeout_seconds: 30
    user_agent: "Goa/1.0 (+https://github.com/pijalu/goa)"
    max_redirects: 5
    allowed_schemes: ["https", "http"]
    blocked_hosts: []               # list of host suffixes to reject

    cache:
      enabled: true
      dir: ""                       # default: <project>/.goa/cache/webfetch
      ttl_hours: 24                 # TTL from original website fetch time
      max_entries: 1000             # per session
      max_bytes: 524288000          # 500 MiB per session
      cleanup_interval_hours: 24

    summary:
      enabled: false                # if false, summarize action is hidden
      sub_agent_role: "companion"   # role used by AgentPool
      max_input_lines: 1000         # lines fed to summarizer
      default_prompt: |
        Summarize the following web page in 3-5 concise paragraphs. Preserve key facts, names, numbers, and any URLs that are central to the content.
```

All values have sane defaults.

### Wizard integration

- The first-run setup wizard asks whether to enable `webfetch` summarization
  after the model/provider setup flow.
- The question is shown only once. The answer is persisted as
  `tools.webfetch.summary.enabled`.
- If the user skips the wizard or does not configure a model, the default is
  `false`.

### Model-gated summarize action

Even when `tools.webfetch.summary.enabled` is `true`, the `summarize` action is
**hidden from the model's schema enum** unless at least one provider/model is
configured. If no model is set up, the tool behaves as if summarization is
disabled. This avoids offering a sub-agent feature that cannot execute.

## Disk cache design

- Storage: `<cache.dir>/<sha256(url)>.md` with a sidecar JSON file
  `<sha256(url)>.meta` containing `url`, `session_id`, `fetched_at`,
  `ttl_hours`, `etag`, `content_type`, and `byte_size`.
- Session affinity:
  - The cache is associated with the current `core.SessionStore.SessionID()`.
  - Cache entries are only returned if their `session_id` matches the current
    session.
  - A new session always starts with an empty cache view, even though physical
    files from previous sessions may still exist on disk.
- Lookup on `fetch`:
  1. If cache file and meta exist, `session_id` matches the current session,
     and `fetched_at + ttl > now`, return cached Markdown.
  2. Otherwise fetch fresh, convert, and overwrite.
- Cleanup rules:
  - Cleanup only removes entries whose associated session no longer exists in
    `<project>/.goa/sessions/`.
  - A janitor goroutine starts when the tool is constructed. It scans the cache
    once at startup and then every `cleanup_interval_hours`, deleting entries
    whose `session_id` is missing or whose TTL has expired.
  - If the current session is deleted (e.g., via `/session:delete` or the
    session file is removed), the janitor removes matching entries on the next
    pass.
  - No cleanup-on-exit logic is required; orphaned entries are reclaimed by the
    janitor on the next Goa start.
  - Per-session `max_entries` and `max_bytes` are enforced only for the current
    session; when exceeded, the current session's oldest entries are evicted by
    `fetched_at`.
- Thread safety: cache operations protected by a mutex.

## Sub-agent summarization

- Only available when `tools.webfetch.summary.enabled` is true.
- Implementation uses the existing `multiagent.AgentPool` / `DelegateTool`
  machinery.
- Flow:
  1. Load cached Markdown.
  2. Truncate to `max_input_lines`.
  3. Build a prompt: `default_prompt` + truncated Markdown + optional user
     `prompt`.
  4. Run the configured sub-agent role.
  5. Return the sub-agent's text output.
- Errors (pool not configured, role unavailable) are returned as `ToolError`
  with a hint.

## Turndown Go port

Package: `internal/turndown`

### Design principles

- Minimal, idiomatic Go.
- Public `Converter` type with `Convert(html string) (string, error)`.
- Public `Rule` type: a matcher + replacement function.
- Default rules cover CommonMark-equivalent output for:
  - Headings (`h1`–`h6`)
  - Paragraphs
  - Line breaks
  - Links
  - Images
  - Emphasis (`strong`, `em`)
  - Code (`code`, `pre`)
  - Blockquotes
  - Ordered/unordered lists
  - Tables
  - Horizontal rules
  - Strikethrough (`s`, `del`, `strike`)
- Whitespace collapse modeled after `collapse-whitespace.js` but implemented
  with a single DOM-normalization pass.
- Use the standard library `golang.org/x/net/html` parser.

### Core API

```go
package turndown

type Rule struct {
    Filter   Filter
    Replacement func(node *html.Node, content string) string
}

type Filter interface {
    Match(node *html.Node) bool
}

type Converter struct {
    Rules []Rule
}

func New() *Converter
func (c *Converter) Convert(html string) (string, error)
func (c *Converter) AddRule(rule Rule)
```

Filter helpers:

```go
func TagFilter(names ...string) Filter
func NodeTypeFilter(t html.NodeType) Filter
```

This API is intentionally stable so future work can add options (e.g.,
`headingStyle`, `bulletListMarker`, `linkStyle`) and a plugin system without
breaking `webfetch`.

### Scope of first implementation

- Convert the upstream Turndown source files into the minimal equivalent Go
  code:
  - `src/turndown.js` → `internal/turndown/converter.go`
  - `src/commonmark-rules.js` → `internal/turndown/rules.go`
  - `src/collapse-whitespace.js` → `internal/turndown/whitespace.go`
  - `src/utilities.js` → `internal/turndown/util.go`
  - `src/html-parser.js`, `src/node.js`, `src/root-node.js` → folded into
    `converter.go` using `golang.org/x/net/html`.
- Preserve upstream behavior where practical, but favor simplicity over
  bit-exact output. Tests should verify structure, not character-for-character
  identity with upstream.

## Tool implementation

File: `tools/webfetch.go`

Key types:

```go
type WebFetchTool struct {
    WorktreeMgr    *internal.WorktreeManager
    Fetcher        *netutil.Fetcher
    Cache          *WebFetchCache
    Summarizer     *WebSummarizer
    Config         WebFetchConfig
}
```

`Execute` parses JSON input, validates the URL (scheme, host blocklist), picks
`fetch` or `summarize`, applies limits, and formats output consistent with
`ReadFileTool`.

Implements `Documentable` for `/help` output.

## TUI renderer

File: `tools/webfetch_renderer.go`

- Renders the call as the URL plus optional line range.
- Collapses the result to `PreviewLines()` (5) by default.
- Expands to the full returned range when the user presses the expand key.
- Registered in `tui/register_renderers.go`.

## Integration points

1. **Config loading** — add `WebFetchConfig` to `config/config.go` and bind to
   env/flags.
2. **Config UI** — add `webfetch.enabled` and `webfetch.summary.enabled` to the
   setup/config TUI (same pattern as `bg_exec`, `memento`, etc.).
3. **Bootstrap** — register `WebFetchTool` in `internal/app/bootstrap.go` when
   `cfg.Tools.Enabled.WebFetch` is true.
4. **Optional tools list** — add `webfetch` to
   `tools.ConfigurableToolNames()`.
5. **Agent pool wiring** — pass the agent pool to `WebFetchTool` via
   `subsystems.go` so summarization can delegate.

## Testing strategy

- Unit tests in `internal/turndown/` for each rule and whitespace collapse.
- Unit tests in `internal/netutil/` for fetch, redirect, timeout, and retry.
- Unit tests in `tools/webfetch_test.go` for:
  - URL validation and blocklist
  - Cache hit/miss/expiry
  - Line range clamping and limit enforcement
  - Error formatting (`ToolError`)
  - Schema enum changes when summarization disabled
- A small set of table-driven tests using real HTML fixtures converted to
  Markdown.
- Mock the `Fetcher` and summarizer for fast, deterministic tests.
- Cache tests cover session isolation, janitor cleanup of orphaned sessions,
  and per-session size/entry limits.

## Complexity budget

- `tools/webfetch.go` and helpers: max gocognit 15 / gocyclo 12.
- `internal/turndown/converter.go`: max gocognit 18 / gocyclo 12.
- All new packages must pass `go vet ./...`, `go test -race -cover ./...`,
  `gocognit -over 15`, and `gocyclo -over 12`.

## Future work (out of scope)

- Full Turndown options API (`headingStyle`, `linkStyle`, etc.).
- Plugin rules loaded from configuration.
- Browser-like behavior (cookies, JS execution).
- Streaming fetch for very large responses.
- Fetching non-HTML content (plain text, JSON).
