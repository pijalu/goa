<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Provider Connectivity

This document describes how Goa connects to model providers — intended for
provider operators who want to know what to expect from Goa's HTTP requests.

## User-Agent

All provider API requests carry a `User-Agent` header identifying the client.
The format varies by provider:

| Context | Format | Example |
|---------|--------|---------|
| Default (all providers) | `goa/{version} ({os}/{arch})` | `goa/0.1.0-dev (darwin/arm64)` |
| GitHub Copilot | `goa ({os})` | `goa (darwin)` |

The version component is set at build time via `-ldflags` and defaults to
`dev` when not overridden (see `internal/version.go`). The OS/arch pair comes
from `runtime.GOOS` and `runtime.GOARCH` at the moment the hook runs.

When a custom `user_agent` field is set in the provider config, it is sent
**instead** of the default. The `headers` field on the provider config can
also override `User-Agent`.

When provider requests use the `goa` standalone library as an RPC tool (skill
REST API tool), the same User-Agent format is used.

**What this means for providers:** You will see `goa/X.Y.Z` in request logs
on every API call. The OS/arch suffix helps distinguish CI agents from
desktop clients.

## Authentication

Goa resolves API keys through a pluggable hook pipeline (see
`internal/agentic/provider/hooks/auth.go`). The flow is:

1. If `StreamOptions.APIKey` is set explicitly (e.g. via `/config`), use it.
2. If an `Authorization` header is already present, mark the key as "unused"
   (no override).
3. Otherwise, iterate the provider profile's declared `env_vars` and use the
   first non-empty environment variable.
4. If no key is found and the profile declares auth as `required`, the request
   is rejected before it reaches the network.

| Method | Wire format | Notes |
|--------|-------------|-------|
| Bearer token (`api_key`) | `Authorization: Bearer {token}` | Default for OpenAI, Anthropic, local providers |
| API key header | e.g. `X-API-Key: {key}` | Configurable via profile `auth.header` |
| OAuth | `Authorization: Bearer {token}` | OpenAI OAuth (sets `SystemAsInstructions` compat flag) |
| Basic auth | `Authorization: Basic {base64}` | Via profile `auth.header` override |

API keys are read from environment variables via `{ENV_VAR}` placeholders in
the provider config YAML, **never hardcoded** in config files.

**What this means for providers:** Expect a standard `Authorization` header on
most requests. If you support multiple auth methods, Goa's config structure can
accommodate any header-based scheme. Auth failures produce a visible error with
status code in the TUI.

## Request Format

Goa uses the OpenAI-compatible chat completions API by default, with protocol
variants for Anthropic Messages API and Google Generative AI.

| Protocol variant | Endpoint | Content-Type |
|-----------------|----------|--------------|
| `openai-completions` | `POST {base}/v1/chat/completions` | `application/json` |
| `openai-responses` | `POST {base}/v1/responses` | `application/json` |
| `anthropic-messages` | `POST {base}/v1/messages` | `application/json` |
| `google-generative-ai` | `GET {base}/...:streamGenerateContent` | `application/json` (query) |
| `mistral-conversations` | `POST {base}/v1/chat/completions` | `application/json` |

The protocol is selected automatically from the provider config's `api` field
and resolved via `internal/agentic/provider/protocol/`.

### Tool schema format

Tool definitions are sent inline in each request as part of the message body.
They follow the standard OpenAI tool schema format, adapted per protocol:

```json
{
  "type": "function",
  "function": {
    "name": "bash",
    "description": "Run a shell command.",
    "parameters": {
      "type": "object",
      "properties": { "command": { "type": "string" } },
      "required": ["command"]
    }
  }
}
```

The schema list is computed once per agent session and cached for stable
ordering (alphabetically by tool name) to maximize prompt cache hits.

**What this means for providers:** Expect standard chat-completions JSON bodies
for most providers. Tool schemas are regenerated every turn; however the
request structure is constant once the tool set stabilizes, which benefits
server-side prompt caching.

## Transport

### SSE (default)

The default transport is Server-Sent Events over HTTP/1.1. The request is a
POST with `Accept: text/event-stream` and the response is consumed as an SSE
stream.

Key characteristics:
- **Connection timeout**: Configurable via provider config `timeout`
- **Idle timeout**: 60 seconds between data chunks (configurable)
- **Retries**: Automatic on 429, 5xx, and network errors, up to
  `max_retries` with exponential backoff capped at `max_retry_delay`
- **Signal handling**: The stream is cancelled when `StreamOptions.Signal`
  channel closes (used for Stop() / user cancellation from the TUI)

### WebSocket

For providers that support it (e.g., custom endpoints), Goa can use WebSocket
transport. WebSocket connections are pooled per session ID so that all messages
in a conversation reuse the same connection.

Key characteristics:
- Session multiplexing via `X-Session-ID` header
- Connection timeout: 20 seconds for initial handshake (configurable via
  `websocket_connect_timeout`)
- Pooled connections are reused until failure, then cleaned up
- Frames are JSON-encoded, matching the same protocol format as SSE

**What this means for providers:** The default SSE transport is what most
providers will see. For providers offering WebSocket endpoints, Goa can
reuse connections across an entire session.

## Prompt Caching

Goa has built-in support for prompt caching across multiple levels, designed to
reduce latency and cost for providers that support it (OpenAI, Anthropic).

### Cache Retention Modes

| Mode | Behavior | Provider Support |
|------|----------|-----------------|
| `none` | No cache control markers sent | All |
| `short` | Apply ephemeral cache breakpoints on system prompt, tools, and last messages | OpenAI, Anthropic |
| `long` | Same as `short` but only if provider supports long retention; otherwise no-op | Anthropic (long context) |

Configured per provider via `cache_retention: none|short|long`.

### Cache Breakpoints

Goa places `cache_control` markers on messages according to the provider's
cache policy profile (`internal/agentic/provider/hooks/cache.go`):

- **System prompt**: Always cached (first breakpoint)
- **Tool definitions**: Cached via the first user message (second breakpoint)
- **Tail messages**: Last N messages get breakpoints (up to `breakpoint_cap`)
- **Content-level granularity**: When `granularity: content`, only the first
  content block per message is marked (avoids caching large tool results)

The policy is configured in the provider's variant profile under
`cache_policy`:

```yaml
cache_policy:
  mode: auto              # auto | long | none
  breakpoint_cap: 3       # max breakpoints per request
  granularity: message    # message (default) | content
  messages:
    tools: true           # cache first user message after tools
    system: true          # cache system prompt
    tail: 2               # cache last N messages
  ttl: ""                 # optional cache TTL (Anthropic)
  affinity_header: ""     # header for session affinity
```

### Cache Key Normalization

For OpenAI, session IDs are sanitized to valid prompt cache keys (lowercased,
non-alphanumeric replaced with underscores, truncated to 128 chars with SHA-256
suffix). See `SanitizeCacheKey()` in `hooks/cache.go`.

**What this means for providers:** If you support prompt caching (cache-control
annotations), Goa will use it fully. Requests carry stable cache breakpoints
on system prompt, tool schemas, and the latest messages. Providers that don't
support caching are unaffected — no extra headers are sent.

## Session ID / Conversation ID

Every agent session gets a unique session ID. This ID is used for:

| Purpose | Mechanism | Where |
|---------|-----------|-------|
| OpenAI response chaining | `previous_response_id` in request body | `openai_responses.go` |
| OpenAI prompt cache key | `session_id` → `Cache-Control` key | `openai_completions.go` |
| Anthropic session affinity | `x-session-affinity` header | `anthropic/stream.go` |
| WebSocket multiplexing | `X-Session-ID` header | `transport/websocket.go` |
| Cache hook affinity | Configurable `affinity_header` | `hooks/cache.go` |
| SSE transport fallback | `X-Session-ID` header | `runtime.go` (WebSocket only) |
| Session persistence | Stored in `.goa/state.json` | `core/sessionstore.go` |

The session ID is generated when the first session starts
(`AgentManager.StartSession` in `core/agentmanager.go`) and persists across
turns until the user runs `/new` or quits. The same ID is reused for companion
agents (multi-agent mode).

**What this means for providers:**
- OpenAI Responses API providers will see `previous_response_id` linking
  messages into a conversation chain.
- Providers supporting session affinity (Anthropic) get the `x-session-affinity`
  header, enabling server-side prompt cache persistence across turns.
- For WebSocket providers, all messages from one session share a single
  connection, identified by `X-Session-ID`.
- The session ID is opaque, stable for the lifetime of a Goa session, and
  survives agent restarts from state.json.

## Custom Headers

Custom headers can be added to every request via the `headers` field in the
provider config. Headers support environment variable interpolation via
`{ENV_VAR}` syntax.

From the variant profile, provider-specific headers are injected automatically:

```yaml
# Provider config example
providers:
  - id: my-provider
    endpoint: https://api.example.com/v1
    api_key: ${MY_KEY}
    headers:
      X-Organization-ID: org-123
      X-Custom: static-value
```

Additionally, the variant profile's `headers` array can inject headers
conditionally based on environment variables:

| Field | Description |
|-------|-------------|
| `name` | Header name |
| `value` | Static value (or template with `{ENV_VAR}`) |
| `env_var` | If set, the value is read from this env var |
| `if_set` | If non-empty, only set when the env var is present |

**What this means for providers:** You can request specific headers for your
service. All API-key-derived headers (`Authorization`, etc.) are set by the
auth hook before custom headers, so user headers can override them if needed.

## Retry and Timeout Behavior

| Setting | Default | Description |
|---------|---------|-------------|
| `timeout` | None (per provider) | Total request timeout, including connection + streaming |
| `max_retries` | 0 | Automatic retries on 429, 5xx, and network errors |
| `max_retry_delay` | 2s | Cap for exponential backoff between retries |

When the stream's Signal channel fires (user cancellation via TUI `/stop` or
Ctrl+C in headless mode), the request is cancelled immediately regardless of
retry attempts.

**What this means for providers:** Expect occasional retries on transient
failures. Rate-limit (429) responses are retried. Goa does not retry on 4xx
errors other than 429. The idle timeout is 60 seconds between stream chunks;
providers that pause mid-stream for longer may trigger client disconnection.

## Endpoint Resolution

The request URL is resolved from the provider config in this order:

1. `model.BaseURL` (set via `base_url` in model config)
2. `profile.Match.BaseURL` (default endpoint from variant profile)
3. Hardcoded default for the API (OpenAI, Anthropic, Google, etc.)

URL templates with `{ENV_VAR}` placeholders are resolved at request time from
the process environment. Example:

```yaml
endpoint: "https://{AZURE_REGION}.api.cognitive.microsoft.com/openai/deployments/{DEPLOYMENT_NAME}/chat/completions?api-version=2024-08-01-preview"
```

For local providers (LM Studio, Ollama, and any endpoint containing
`localhost:1234`, `localhost:11434`, `127.0.0.1:1234`, or `127.0.0.1:11434`),
the default base URL is empty, so the `endpoint` field must be set explicitly.

## Context Compression

Goa compresses conversation history to stay within context window limits.
This is managed server-side by the agent before sending the next request.

### Strategies

| Strategy | Description | Cost | When Used |
|----------|-------------|------|-----------|
| `tool_elision` | Remove tool call arguments and results from older messages, replace with placeholders | Free (local, no LLM call) | Default (fallback) |
| `micro` | On cache-miss turns, replaces old tool result bodies with a short marker `[Old tool result content cleared]` | Free | Primary strategy (default config) |
| `selective` | Remove oldest user/assistant messages, keep system prompt + recent turns | Free | Fallback when over threshold |
| `hybrid` | `tool_elision` first, then `selective` if still over threshold | Free | When `hybrid` strategy is selected |
| `summarize` | Use LLM to summarize old messages into a single assistant message | Costly (1 LLM call) | Only when explicitly chosen |

### Trigger Conditions

Compression runs when:

1. **Threshold trigger**: Estimated token usage exceeds `ThresholdPercent`
   (default: 80%) of `MaxTokens` (0 = auto-detect from active model's
   context window).
2. **Error trigger**: The LLM returns a context-length error
   (`on_context_error: true`, default: enabled).

### Micro Compaction Details

The default strategy (`micro`) is specifically designed for cache-hit
efficiency:

- Runs only when the cache is presumed **cold** (after `cache_miss_threshold`,
  default: 1h of inactivity).
- Preserves the last `keep_recent_messages` (default: 20) messages untouched.
- For older messages, tool result bodies are replaced with
  `[Old tool result content cleared]` while keeping the message structure
  (user, assistant, tool call) intact.
- Preserves at least `min_context_ratio` (default: 50%) of the original
  context window.
- Skips messages with fewer than `min_content_tokens` (default: 100) tokens
  to avoid fragmenting meaningful small exchanges.

**What this means for providers:** Goa reduces token consumption between turns.
Most requests arrive with only recent full-content messages; older tool results
are elided or collapsed. This means providers see smaller payloads on cache-hit
turns but may see bursts of full-context requests after idle periods.

## Tool Output Compression

In addition to context-level compression, Goa compresses individual tool
outputs before sending them back to the model. This applies primarily to shell
commands and is controlled by `tools.bash.compress_output`.

| Command | Compression Applied |
|---------|-------------------|
| `ls -la` | Strips permissions/owner/group/size — filenames only |
| `git status` | One line per changed file |
| `git diff` | Condensed per-file diff with only changed lines |
| `git log` | Deduplicated, author email stripped |
| `grep` / `rg` | Grouped by file, long lines truncated at 200 chars |
| `cat` / `head` / `tail` | Line-numbered output |
| `go test` | PASS lines stripped, stack traces compressed, pass/fail summary |

Compression is **enabled by default** for local providers (LM Studio, Ollama,
localhost endpoints) and **disabled by default** for remote providers. It can be
overridden per model via `compress_output: true|false` in the model config.

## Debugging

Set `GOA_DEBUG_PROVIDER=1` in the environment to print the resolved profile
and request headers for each provider call to stderr.

| Debug signal | Output |
|-------------|--------|
| `GOA_DEBUG_PROVIDER=1` | Resolved variant profile, all request headers |
| 4xx/5xx responses | Status code, response body, response headers |

In the TUI, the `/transparency` menu controls which provider details are
visible: streaming progress, tool call arguments, token statistics, and
thinking blocks.
