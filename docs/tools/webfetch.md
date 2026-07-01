<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# webfetch

Fetch any URL and convert the page to structured Markdown. `webfetch` behaves
like a remote `read` tool: after the first fetch, the converted Markdown is
cached for the current session and you can request line ranges such as
`https://example.com/page:1:230`.

## Usage

```json
{"url": "https://example.com"}
{"url": "https://example.com", "start_line": 1, "end_line": 50}
{"url": "https://example.com", "action": "summarize", "prompt": "Extract API endpoints"}
```

## Actions

- `fetch` (default): download the URL, convert HTML to Markdown, cache it, and
  return the requested line range.
- `summarize`: ask the configured sub-agent to summarize the cached Markdown.
  Only available when `tools.webfetch.summary.enabled` is `true` and a model is
  configured.

## Configuration

```yaml
tools:
  webfetch:
    enabled: true
    max_lines_default: 250
    max_lines_hard: 4096
    max_total_bytes: 20971520
    timeout_seconds: 30
    user_agent: "Goa/1.0 (+https://github.com/pijalu/goa)"
    max_redirects: 5
    allowed_schemes: ["https", "http"]
    blocked_hosts: []
    cache:
      enabled: true
      dir: ""
      ttl_hours: 24
      max_entries: 1000
      max_bytes: 524288000
      cleanup_interval_hours: 24
    summary:
      enabled: false
      sub_agent_role: "companion"
      max_input_lines: 1000
      default_prompt: |
        Summarize the following web page in 3-5 concise paragraphs...
```

## Cache

Converted Markdown is stored under `.goa/cache/webfetch/` for the lifetime of
the session. Entries are tagged with the session ID, so a new session starts
with an empty cache view even if files from previous sessions remain on disk.

Cleanup removes entries only when:

- their TTL expires, or
- their associated session no longer exists in `.goa/sessions/`.

Per-session `max_entries` and `max_bytes` limits evict the oldest entries
first.

## Security

- Only schemes listed in `allowed_schemes` are permitted.
- URLs matching `blocked_hosts` are rejected.
- Responses larger than `max_total_bytes` are rejected.
