<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Configuration

## Config Cascade

Goa uses a **6-level cascade** for configuration. Each level may override values from the previous level:

```
# Priority (lowest → highest)
1. Embedded defaults (compiled into binary)
2. ~/.goa/config.yaml         (home/global)
3. .goa/config.yaml           (project-level)
4. .goa/config.local.yaml     (local overrides, gitignored)
5. GOA_* env vars             (environment overrides)
6. CLI flags                  (highest priority)
```

### Merge Rules

- **Maps**: Deep-merged (nested keys from higher-priority sources win)
- **Scalars**: Last-write-wins (highest priority source for each key)
- **Slices**: Replaced entirely (not merged) by higher-priority sources

## Config File Structure

```yaml
# ── Provider ──────────────────────────────────────────────────────
active_provider: local               # Active provider ID
active_model: llama-3.2-1b-instruct  # Active model name

providers:
  - id: local                        # Unique provider identifier
    name: Local LLM                  # Display name
    endpoint: http://localhost:1234/v1/chat/completions
    api_key: ""                      # Optional API key
    default_model: llama-3.2-1b-instruct
    timeout: 60s                     # Request timeout
    max_retries: 3                   # Max retries on failure
    max_retry_delay: 2s              # Cap exponential backoff
    transport: sse                   # sse | websocket
    cache_retention: none            # none | short | long
    session_id: ""                   # Cache-affinity session id
    headers:                         # Custom HTTP headers
      X-Custom: value
    metadata:                        # Provider-specific metadata
      project: goa
    preferred: true                  # Auto-select this provider

    # Per-provider configuration overrides (optional).
    # These customize provider-specific behavior without code changes.
    extra:
      normalize_null_descriptions: true   # Convert "null" → null in tool schemas
      thinking_extra_body: true            # Send thinking config in extra_body
      tool_call_id_max_length: 64          # Truncate tool call IDs (0 = no limit)
      reasoning_key: "reasoning_content"   # Field name for reasoning content

    # Agentic provider identity. Usually inferred from the preset
    # or endpoint, but can be forced for compat detection.
    provider: lm-studio              # openai | anthropic | google | ...
    api: openai-completions          # openai-completions | anthropic-messages | ...

  - id: openai
    name: OpenAI
    endpoint: https://api.openai.com/v1/chat/completions
    api_key: ${OPENAI_API_KEY}       # Env var interpolation
    default_model: gpt-4o
    provider: openai
    api: openai-completions

# ── Models ────────────────────────────────────────────────────────
models:
  - id: default
    provider: local
    model: llama-3.2-1b-instruct
    temperature: 0.2
    max_tokens: 4096
    reasoning: false                 # Enable thinking/reasoning
    thinking_level: off              # off | minimal | low | medium | high | xhigh
    thinking_budget: 0               # Per-request thinking token budget
    input_types:                     # text | image
      - text
    headers:                         # Per-model HTTP headers
      X-Model-Header: "1"
    compat: '{"toolResultAsUser":true}' # Provider-specific compat JSON
    compress_output: null              # null = auto (local=on, remote=off) | true | false

# ── Execution ──────────────────────────────────────────────────────
execution:
  mode: yolo                         # yolo | confirm | review
  retries: 3                         # Tool retry count
  max_tool_repeat_total: 0          # Max identical tool calls in the entire turn (0 = disabled)
  max_tool_repeat_consecutive: 2    # Max consecutive identical tool calls (soft hint at 2, hard at limit)
  max_tool_calls: 3                 # Max duplicate occurrences of the same call within the rolling window (0 = unlimited)
  tool_call_limit_reset_window: 10  # Number of recent calls inspected for the duplicate-window limit above
  token_warning: 70                  # % of budget → warning
  token_critical: 85                 # % of budget → critical
  loop_warning: 5                    # Consecutive same-tool calls before warning
  loop_interrupt: 10                 # Consecutive same-tool calls → interrupt
  activity_timeout: 120s             # No output → warning
  error_threshold: 0.3               # Error rate % → mode auto-downgrade
  worktree_mode: multi_agent         # always | multi_agent
  auto_save_model: true              # Persist model changes

# ── Context Compression ────────────────────────────────────────────
context_compression:
  enabled: true
  max_tokens: 8192                   # Target context window
  threshold_percent: 80              # Compress when usage exceeds this %
  on_context_error: true             # On error → fall back to tool elision
  strategy: micro                    # Default: micro compaction after 1h idle
                                     # Options: tool_elision | selective |
                                     #          summarize | hybrid | micro
  preserve_recent_turns: 4           # Keep last N turns uncompressed
  micro_compaction:
    keep_recent_messages: 20         # Messages to never truncate
    min_content_tokens: 100          # Min content before truncating
    cache_miss_threshold: "1h"       # Idle time before micro triggers
    truncated_marker: "[Old tool result content cleared]"
    min_context_ratio: 0.5           # Min context usage to trigger (0.0-1.0)

# ── Mode ─────────────────────────────────────────────────────────
mode:
  default:
    major: coder                     # Default major mode (coder|planner|reviewer|<custom>)
    autonomy: solo                   # Default autonomy (yolo|solo|confirm|review)
    skills:                          # Default skill stack
      - test-gen
  defaults:
    coder: solo                      # Per-mode autonomy overrides
    planner: review
    reviewer: review

# Custom modes are defined as .goa/prompts/mode/<name>/definition.md files.
# See docs/PROFILES.md for the definition format.

# ── Skills ─────────────────────────────────────────────────────────
skills:
  dirs:                              # Extra skill directories
    - ~/.goa/skills
    - .goa/skills
  embedded: true
  execution_mode: subagent           # subagent | inline

# ── Tools ──────────────────────────────────────────────────────────
tools:
  # Optional tools toggles. Most are opt-IN (default false).
  # `clarify_disabled` is the exception: it is opt-OUT (default false),
  # so the ask_user_question tool is ENABLED by default.
  enabled:
    bg_exec: false            # background process execution
    memento: false           # thinking artifacts
    pty_exec: false          # PTY-backed command execution
    ssh_bash: false          # remote shell execution
    delegate_to: false       # multi-agent delegation
    request_review: false    # multi-agent review requests
    webfetch: false           # web fetching
    python: true             # embedded gpython interpreter (opt-out)
    clarify_disabled: false  # set true to remove the ask_user_question tool
  edit:
    allow_fuzz_on_edits: true        # DEFAULT: enabled
                                     # 3-tier fuzzy matching:
                                     #   1. Exact match (byte-for-byte)
                                     #   2. Trailing whitespace normalized
                                     #   3. Full fuzzy + auto-reindent
                                     # When disabled: exact match only
  search:
    threads: 4                       # Concurrent search threads
    max_results: 20                  # Max search results per query
    exclude:                         # Directories to skip
      - node_modules
      - .git
      - vendor
      - dist
  bash:
    blocked_commands:                # Never execute these
      - rm -rf /
      - sudo
      - chmod -R
    allowed_commands: []             # Empty = allow all (except blocked)
    env_mask_patterns:               # Mask in logs
      - API_KEY
      - SECRET
      - TOKEN
      - PASSWORD
    max_output_bytes: 10485760       # Max output before truncation (10MB)
    compress_output: null            # null = auto (local=on, remote=off)
    enable_complexity_analysis: false # AST-based command complexity analysis
    max_complexity_score: 5          # Max complexity score (0=disabled)
    jail: false                      # Force project-directory jail
  python:
    timeout_seconds: 60              # Default execution timeout
  smartsearch:
    enabled: false                   # Enable BM25 relevance search (default: false)
    max_results: 20                  # Max results per query
    min_score: 0.0                   # Minimum relevance score threshold
    exclude_dirs: []                 # Additional exclusion dirs
    k1: 1.5                          # BM25 k1 parameter (term frequency saturation)
    b: 0.75                          # BM25 b parameter (length normalisation)
  terminal:
    sandbox:
      enabled: true                  # Enable sandbox (default: true)
      blocked_commands:              # Never execute these in sandbox
        - rm -rf /
        - sudo
        - curl
        - ssh
      allowed_commands: []           # Empty = allow all (except blocked)
      timeout_seconds: 60            # Default timeout
      max_output_chars: 100000       # Max output characters
      bypass_allowed: false          # Allow bypassing sandbox via config
  webfetch:
    enabled: true                    # Enable web fetching
    max_lines_default: 250           # Default max lines per fetch
    max_lines_hard: 4096             # Absolute max lines per fetch
    max_total_bytes: 20971520        # Max response body size (20MB)
    timeout_seconds: 30              # Request timeout
    user_agent: "Goa/1.0"            # Custom User-Agent header
    max_redirects: 5                 # Max redirects to follow
    allowed_schemes:                 # Allowed URL schemes
      - https
      - http
    blocked_hosts: []                # Hosts to block
    cache:
      enabled: true                  # Cache fetched pages
      dir: ""                        # Cache dir (default: .goa/cache/webfetch)
      ttl_hours: 24                  # Cache TTL
      max_entries: 1000              # Max cache entries per session
      max_bytes: 524288000           # Max cache size (500MB)
      cleanup_interval_hours: 24     # Cleanup interval
    summary:
      enabled: false                 # Enable sub-agent summarization
      sub_agent_role: "companion"    # Role for summarization
      max_input_lines: 1000          # Max lines for summarization
      default_prompt: |              # Default summarization prompt
        Summarize the following web page in 3-5 concise paragraphs...
  ssh:
    hosts:
      - id: server1
        host: server1.example.com
        port: 22
        user: deploy
        key_file: ~/.ssh/deploy_key

# ── TUI ────────────────────────────────────────────────────────────
tui:
  theme: dark                        # dark | light | custom
  layout: default                    # default | wide | minimal | debug
  spinner: arc                       # arc, dots, line, star, orbit, quadrant, flare, none
  show_timestamps: false             # Show timestamps in the chat viewport
  transparency:
    show_thinking: true              # Show reasoning/thinking blocks
    show_streaming: true             # Show streaming status indicators
    show_tool_calls: true            # Show tool execution in chat
    show_token_stats: true           # Show token usage in footer
    show_logs: false                 # Show log pane overlay
    thinking_collapsed: false        # Collapse thinking blocks by default
    thinking_pane_position: "side"   # side | bottom
    highlight_tool_input: true
  # Tool call display in the chat.
  tools:
    view: summary                    # summary (N-line preview) | full (everything)
    preview_lines: 10                # lines shown per tool block in summary mode
  # Font style toggles (all enabled by default — disable if your terminal
  # renders them poorly).
  font_styles:
    bold: true                       # **bold** → \e[1m
    italic: true                     # *italic* / _italic_ → \e[3m
    underline: true                  # links → \e[4m
    strikethrough: true              # ~~strikethrough~~ → \e[9m
  # Input history configuration.
  history:
    max_loaded: 100                  # Max history entries loaded from disk on
                                     # startup and session restore (0 = disabled)

# ── Memory ─────────────────────────────────────────────────────────
memory:
  enabled: true
  dir: .goa/memory
  auto_summarize: true
  dream:
    enabled: false                # enable memory consolidation (dream mode)
    auto: false                   # automatically run dreams after sessions
    interval: 7d                  # minimum time between auto-dreams
    min_sessions: 5               # minimum sessions since last dream
    model: ""                     # dedicated dream model (empty = active model)
    provider: ""                  # dedicated dream provider (empty = active provider)
    max_tokens: 8192              # max output tokens for the dream model
    temperature: 0.2              # dream model temperature
    output_dir: .goa/memory.dream        # review directory
    consolidated_dir: .goa/memory.consolidated  # applied memory location
    apply_after_review: false     # auto-apply without manual review

# ── Multi-Agent ────────────────────────────────────────────────────
multi_agent:
  enabled: false
  pattern: pair
  max_companion_cycles: 2
  companion_provider: ""
  companion_model: ""
  planner_model: ""
  coder_model: ""
  message_timeout: 120s
  show_inter_agent_messages: true

# ── Orchestrator ───────────────────────────────────────────────────
orchestrator:
  roles:
    orchestrator:
      model: my-model-id
    coder:
      model: my-model-id
      provider: ""                   # optional per-role provider override
      allowed_tools: [bash, edit]     # optional tool allowlist
  pool:
    max_total_agents: 8              # max concurrent agents (0 = unlimited)
    max_agents_per_model:
      my-model-id: 4                 # optional per-model cap
  defaults:
    topology: hub                    # hub | fanout | pipeline

# ── Plugins ────────────────────────────────────────────────────────
plugins:
  enabled: ["*"]                     # Enable all plugins, or list specific IDs
  dirs:
    - ~/.goa/plugins
    - .goa/plugins
  bundled:                           # Disable built-in bundled plugins
    provider-quota: true             #   false → skip the provider-quota plugin

# ── Aliases ───────────────────────────────────────────────────────
aliases:
  n: session:new              # /n → /session:new
  r: session:restore          # /r → session restore picker
  # Aliases support baked-in args: "session:new" is a shortcut for
  # "/session:new". Plain command names like "session" also work.

Define custom shortcuts for any command. The key is the alias name,
the value is the target command (optionally with colon-separated args).
Aliases appear in tab completion and support the full command syntax
including subcommands and doc suffixes.

# ── Logging ────────────────────────────────────────────────────────
logging:
  level: info                        # error | warn | info | debug
  file: ""                           # Log file path (empty=stderr)
```

## Env Var Overrides

Environment variables use the `GOA_` prefix with `_` as path separator:

```bash
# Set active model
export GOA_ACTIVE_MODEL="gpt-4o"

# Override provider endpoint
export GOA_PROVIDERS_0_ENDPOINT="http://192.168.1.100:8080/v1/chat/completions"

# Change execution mode
export GOA_EXECUTION_MODE="confirm"
export GOA_EXECUTION_MAX_TOOL_CALLS="10"

# Enable auto-dreams
export GOA_MEMORY_DREAM_ENABLED="true"
export GOA_MEMORY_DREAM_AUTO="true"
export GOA_TUI_THEME="light"
export GOA_TUI_TRANSPARENCY_SHOW_THINKING="true"
export GOA_TUI_TRANSPARENCY_THINKING_COLLAPSED="false"

# Set API key via env (avoids plaintext in config)
export OPENAI_API_KEY="sk-..."
```

Env vars support `${VAR}` and `${VAR:-default}` interpolation in config values.

## Provider Custom Registry

Goa can fetch provider and model definitions from remote JSON URLs, enabling
team-shared configurations:

```yaml
registry_loaders:
  sources:
    - url: "https://example.com/api.json"
      bearer_token: "${REGISTRY_TOKEN}"   # optional
```

The registry endpoint should return:
```json
{
  "providers": [
    {"id": "team-openai", "endpoint": "https://proxy.example.com/v1", "api_key": "sk-..."}
  ],
  "models": [
    {"id": "gpt-4o", "provider": "team-openai", "model": "gpt-4o", "max_context_size": 128000}
  ]
}
```

## CLI Flags

All CLI flags override the corresponding config file value:

```bash
goa --model gpt-4o                 # Override active model
goa --profile planner              # Override active mode
goa --provider openai              # Override active provider
goa --endpoint http://localhost:1234/v1/chat/completions
goa --api-key sk-...               # Override provider API key
goa --temperature 0.7              # Override model temperature
goa --max-tokens 4096              # Override model max output tokens
goa --max-tool-repeat 5            # Override max identical tool calls in a turn
goa --max-tool-repeat-consecutive 2  # Override max consecutive identical tool calls
goa --max-tool-calls 10            # Override max duplicate calls in the rolling window
goa --tool-call-limit-reset-window 20  # Override rolling-window size
goa --skill-mode inline            # inline | subagent
goa --reasoning                    # Enable model reasoning
goa --thinking-level medium        # off | minimal | low | medium | high | xhigh
goa --thinking-blocks on           # Expand thinking blocks by default
goa --show-thinking                # Show main-agent thinking blocks
goa --theme light                  # dark | light
goa --compression                  # Enable context compression
goa --config ./custom.yaml         # Explicit config path (skips cascade)
goa --debug                        # Enable debug logging

goa --logfile ./goa.log            # Write agent/LLM trace logs to file
# When --logfile is set, --debug is implied and every agent output event is
# traced to the file. Useful for diagnosing hangs or missing responses.
```

## Runtime TUI Toggles

Some TUI settings can be changed without editing config files:

```bash
/thinking-blocks                 # Show current thinking-block state
/thinking-blocks:on              # Expand main-agent thinking blocks
/thinking-blocks:off             # Collapse main-agent thinking blocks
```

`/thinking-blocks` updates `tui.transparency.thinking_collapsed` in
`~/.goa/config.yaml` using a targeted write that preserves your other settings.

**Tool output view (Ctrl+O).** Press `Ctrl+O` at runtime to flip **every** tool
block between **Summary** (a compact N-line preview, the default) and **Full**
(the complete input/output). The toggle applies to all tool blocks for the
rest of the session; the starting mode and the Summary line count come from
`tui.tools.view` and `tui.tools.preview_lines` (see [Config File Structure](#config-file-structure)).

## Tool Call Display

Each tool call renders as a single widget in the chat that progresses through
`pending → running → success/error`. The widget is created the moment a tool
call is detected — even while its arguments are still streaming from the model
— so you always see what is happening as fast as possible:

- **Header** — the tool name and its key argument (file path, command, …),
  with a status icon (`◉` pending, spinner running, `✓` success, `✗` error).
- **Body** — a uniform preview of the call's content: the **first** N lines of
  streamed input (file content, code, diff) and the **last** N lines of output
  (command stdout, search results). N is `tui.tools.preview_lines` (default 10).
- **Stats line** — a live counter (`streaming… 42 lines in · 7 lines out`)
  so long calls stay honest about how much has arrived, plus a `Ctrl+O to
  expand` hint when collapsed.

`Ctrl+O` toggles **all** tool blocks between Summary and Full for the session.
A per-widget expand (focus + `Enter`) is also available as a secondary
affordance.

### Configuration

```yaml
tui:
  tools:
    view: summary        # summary (collapsed, N-line preview) | full (everything)
    preview_lines: 10    # lines per tool block in summary mode
```

Defaults: `view: summary`, `preview_lines: 10`. The `preview_lines` value is
the single source of truth for the collapsed line count across **all** tools.

## First Run Detection

Goa detects whether it has been configured by checking for `~/.goa/config.yaml`. If missing, `Config.FirstRun` is `true` and the [Setup Wizard](SETUP.md) is launched on startup.

## Custom Configuration Files

Beyond the main config cascade, Goa supports custom user override files
in `~/.goa/` for specific subsystems:

| File | Purpose | Format |
|------|---------|--------|
| `~/.goa/spinner.json` | Custom spinner animations | `{"name": {"interval": 100, "frames": ["◜","◠"]}}` |
| `~/.goa/search_priority.json` | Custom file extension priority for search results | `{"extensions": {".go": 10, ".md": 100}}` |

**Spinner customization:** Create `~/.goa/spinner.json` with spinner definitions
in the same format as the built-in list. All spinners defined in the file are
available for selection via `/config` → Spinner. Example:

```json
{
  "customSpinner": {
    "interval": 120,
    "frames": ["▖", "▘", "▝", "▗"]
  },
  "pulse": {
    "interval": 80,
    "frames": ["█", "▓", "▒", "░", "▒", "▓"]
  }
}
```

**Search priority customization:** Create `~/.goa/search_priority.json` to
override which file types appear first in search results. Lower priority
numbers = higher display rank (shown first). The built-in defaults use:
source code (10), config files (50), data/doc files (100), media (200).
User values are merged on top of the embedded defaults.

```json
{
  "extensions": {
    ".go": 5,
    \“.rs": 5,
    ".txt": 200
  }
}
```

## Saving Config Changes

Runtime config changes (e.g., via `/config set`) are persisted to `~/.goa/config.yaml` via `ConfigSaver`:
