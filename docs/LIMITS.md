# Limits & Defaults Reference

Every hard limit, threshold, timeout and built-in default Goa applies, grouped by area.

## Where defaults come from

Goa resolves configuration through a cascade — later layers override earlier ones:

```text
embedded defaults (config/configs/default.yaml)
  → home (~/.goa/config.yaml)
    → project (.goa/config.yaml)
      → local (.goa/config.local.yaml)
        → env (GOA_* variables)
          → CLI flags
```

- **YAML**: use the dotted key shown in each table (nested keys map to YAML paths).
- **Env**: prefix the dotted path with `GOA_` and join segments by `_`, e.g.
  `GOA_EXECUTION_MAX_TOOL_CALLS=200`, `GOA_GOALS_STALL_TURNS=3`.
- **`/config:set KEY VALUE`** (or `/config` menu): applies live where a runtime
  sync exists and persists to the config; see `/config:set <tab>` for supported keys.
- **CLI flags** exist for the main round caps (`--max-tool-calls`,
  `--max-stream-rounds`, `--max-consecutive-tool-rounds`, …); a flag value of `0`
  means "do not override".

Values marked **(code)** are internal constants not exposed via YAML; values
marked *(legacy)* are retained in the schema but no longer consumed on current
runtime paths.

## Execution & agent rounds (per turn)

| Key | Default | Description |
|---|---|---|
| `execution.max_tool_repeat_total` | `0` (disabled) | Max total identical tool calls per turn before a loop guard fires. |
| `execution.max_tool_repeat_consecutive` | `2` | Max CONSECUTIVE identical tool calls (same tool + args). State-aware: a successful state-changing call (edit/write/bash/python/terminal) resets the counter, so edit → test(fail) → edit cycles never trip it. |
| `execution.max_tool_calls` | `200` | Max duplicate occurrences of the SAME tool call within the rolling window below. Different tools do not count. |
| `execution.tool_call_limit_reset_window` | `10` | Number of recent calls inspected for duplicate detection above; a successful state-changing call resets the window ("state epoch"). |
| `execution.max_tool_error_streak` | `4` | Consecutive failing calls of the SAME tool (any args) tolerated before one guard message tells the model to change approach. Resets on success or tool switch. |
| `execution.max_consecutive_tool_rounds` | `15` | Tool-only LLM rounds with no visible text/thinking before a forced-answer nudge is injected. `0` = disabled (thinking-heavy models may need this). |
| `execution.max_stream_rounds` | `0` (unlimited) | Hard cap on LLM stream rounds per turn. The turn loop is otherwise convergence-driven — there is deliberately no hidden default. |
| `execution.auto_heal_tool_calls` | `false` | Extract/execute XML-markup tool calls emitted inside content streams by some local models. |

## Loop & stream guards

Loop detection runs in three independent detectors; all can be disabled individually.

| Key | Default | Description |
|---|---|---|
| `execution.loop_warning` | `10` *(SDK default 7)* | Repeated-output warning threshold for the generic loop detector. |
| `execution.loop_interrupt` | `15` *(SDK default 10)* | Threshold at which the generic loop detector interrupts the turn. |
| `execution.disable_thinking_loop_detection` | `false` | Disable the reasoning-text loop detector. Internal thresholds: warn after 4 repeats, interrupt at 6 **(code)**. |
| `execution.disable_tool_loop_detection` | `false` | Disable the tool-loop guards listed under "Execution & agent rounds". |
| `execution.disable_stream_loop_detection` | `false` | Disable the stream-repetition detector. |
| `execution.stream_loop_max_repeats` | `5` *(code)* | Repeated stream chunk count that counts as one detection. |
| `execution.stream_loop_min_period` | `50` *(code)* | Minimum period length for stream repetition matching. |
| `execution.stream_loop_max_strikes` | `5` *(embedded; code default 3)* | Detections after which the whole turn is aborted ("strikes"). |
| `execution.disable_thinking_stall_detection` | `false` | Disable the thinking-stall watchdog. |

## Provider & networking

| Key | Default | Description |
|---|---|---|
| `execution.retries` | `2` | Base retry budget passed to provider streaming; `≤ 0` falls back to `5`. |
| Provider retry policy *(code)* | normal mode, `5` retries, backoff `1s → 30s` exponential, jitter `0.25` | Package-wide policy applied when neither the provider config nor its catalog entry declares one. |
| Stream connect timeout *(code)* | `5m` | Dial → first response header bound when no provider timeout is configured; does not cap long generations. |
| MCP request timeout | `30s` | Per-request bound per MCP server (`mcp.<server-id>.timeout`); empty uses the default. |

## Context compression

Proactive compression escalates by fill level of the effective context window;
every layer is opt-in except the hard ceiling.

| Key | Default | Description |
|---|---|---|
| `context_compression.enabled` | `true` | Master switch for the compression ladder. |
| `context_compression.max_tokens` | `0` (auto) | Effective window size; `0` uses the active model's advertised context window. |
| `context_compression.thresholds.soft_percent` | `0` (off) | Soft-layer fill level (%). Opt-in. |
| `context_compression.thresholds.trigger_percent` | `0` (off) | Trigger-layer fill level (%). Opt-in. Legacy `context_compression.threshold_percent` overrides this when set. |
| `context_compression.thresholds.hard_percent` | `95` | Fill level where the hard layer runs (full-window summarize with cache gate bypassed). The only layer enabled by default. |
| `context_compression.on_context_error` | `true` | Reactive safety net on a provider context-length error. |
| `context_compression.on_error_strategy` | `hybrid` | On-error ladder: tool_elision → selective → summarize. |
| `context_compression.micro_compaction.enabled` | `false` | Pre-summarize micro-compaction (runs as dry-run first, applied only if the real summarize overflows). |
| `context_compression.micro_compaction.keep_recent_messages` | `20` | Recent messages kept when micro compaction is applied. |
| `context_compression.micro_compaction.min_content_tokens` | `100` | Messages shorter than this are never cleared. |
| `context_compression.micro_compaction.cache_miss_threshold` | `"1h"` | Age beyond which content counts as cache-dead and may be dropped. |
| `context_compression.fresh_window.enabled` | `false` | Full-window compaction with zero summarization (fresh window + preserved tail). |
| `time_context.enabled` | `false` | Per-turn temporal context injection (zoned timestamp). Off by default. |

Deprecated fallbacks *(legacy)*: `execution.token_critical` (`90`) maps onto the
hard ceiling when no compression thresholds are set; `execution.token_warning`
(`70`), `execution.activity_timeout` (`"30s"`) and `execution.error_threshold`
(`0.5`) remain in the schema but are not consumed by current runtime paths.

## Goals (budgets & watchdogs)

| Key | Default | Description |
|---|---|---|
| `goals.default_turn_budget` | `50` | Implicit hard turn ceiling given to every newly created goal unless a budget was set explicitly. `-1` = unlimited. Editable at /config → Goals; applies live to goals created afterwards. |
| `goals.stall_turns` | `5` | Consecutive continuation turns with an unchanged progress fingerprint (todos + git status) before a stall challenge is injected. `0`/`-1` disables. Tuning applies live. |
| Stall challenge limit *(code)* | `2` | Unanswered stall challenges before the goal auto-blocks for user review. |
| `goals.max_verify_failures` | `3` | Consecutive machine-verification failures before the goal auto-blocks. `-1` = no cap. Any transition out of `active` or a restart resets the streak. |
| `goals.verify_timeout` | `"2m"` | Hard bound on a single verify-command execution at goal completion. |
| `goals.done_gate` | `verify` | Completion gate strictness: `verify` / `evidence` / `off`. |
| `goals.verify_commands` | `true` | Execute recorded verify commands at completion. |
| `goals.judge` | `off` | Independent semantic completion auditor (`off` / `same` / `model:<id>`). Judge errors fail open. |
| `goals.retention` | `enabled, 7 days` | How long terminal goal records are kept. |
| Budget warning band *(code)* | `≥ 75%` | At 75% usage of any active budget the injected guidance flips to "nearing a budget". |
| Wall-clock budget bounds *(code)* | `1s … 24h` | Reasonable-range validation when the model sets a time budget via the goal tool (`set_budget`). |

## Orchestrator & teams

| Key | Default | Description |
|---|---|---|
| `orchestrator.pool.max_total_agents` | `8` | Cap on concurrently delegated orchestrator agents (`0` = unlimited). |
| `orchestrator.pool.max_agents_per_model` | `{}` (unlimited) | Optional per-model caps keyed by model id. |
| `orchestrator.defaults.topology` | `hub` | Topology used when a workflow doesn't specify one. |
| `orchestrator.defaults.run_timeout` | `10m` (embedded); empty/invalid falls back to `1h` | Per-run absolute wall-clock budget for orchestrated runs. |
| `orchestrator.defaults.activity_timeout` | unset → `2m` *(code)* | Inactivity bound: the run cancels only when no runtime event arrives for this long. |
| `orchestrator.retention` | `enabled, 7 days` | Retention of terminal orchestrator run records. |
| `multi_agent.enabled` | `false` | Companion/multi-agent feature flag. |
| `multi_agent.max_companion_cycles` | `2` | Companion review cycles per turn. |
| `multi_agent.message_timeout` | `"120s"` | Bound on inter-agent message exchange. |
| `teams.definitions.<id>.defaults.turn_budget` | unset | Optional per-team goal turn ceiling (no embedded default). |

## Tools

### Bash, Python, Run code

| Key | Default | Description |
|---|---|---|
| bash tool `timeout` argument | `60s`, max `300s` | Foreground command timeout; capped server-side. |
| `tools.bash.jail` | `false` | Jail bash execution to the project directory. |
| `tools.bash.blocked_commands` | destructive list | Commands rejected at shell command position (`rm -rf /`, fork bombs, …). A fixed internal safety blocklist (rm/dd/chmod/sudo/curl/nc/…) always applies on top. |
| `tools.bash.env_mask_patterns` | `*KEY* *TOKEN* *SECRET* *PASSWORD*` | Env vars masked from output. |
| `tools.python.timeout_seconds` | `60` (max `300`) | Python tool foreground timeout. |
| `tools.run_code.timeout_seconds` | `60` | run_code worker foreground timeout. |
| `tools.run_code.jail` | `true` | Confine the program's own file API to the project directory. |

### Files, search, webfetch

| Key | Default | Description |
|---|---|---|
| Tool output truncation *(code)* | `2000` lines / `50 KB` | Generic truncation applied to oversized plain-text tool results. |
| `tools.max_inline_bytes` | `0` (off) | Spill policy: results over the cap are saved verbatim to `~/.goa/spill/<session>/` and replaced by a bounded preview + locator. |
| `tools.edit.allow_fuzz_on_edits` | `true` | Fuzzy-match retry when exact edits fail. |
| `tools.read_file.dedup` | `true` | Byte-identical re-reads return a hint instead of the full body (append-only-context hygiene). |
| `tools.search.threads` | `4` | Parallel search workers. |
| `tools.search.max_results` | `30` | Search result cap. |
| `tools.webfetch.timeout_seconds` | `30` | HTTP fetch deadline. |
| `tools.webfetch.max_lines_default` / `max_lines_hard` | `250` / `4096` | Model-facing line bounds for fetched pages. |
| `tools.webfetch.max_total_bytes` | `20971520` (20 MB) | Download size cap. |
| `tools.webfetch.max_redirects` | `5` | Redirect chain limit. |
| `tools.webfetch.cache.ttl_hours` | `24` | Cached page freshness window. |
| `tools.webfetch.cache.max_entries` / `max_bytes` | `1000` / `524288000` (500 MB) | Web cache eviction limits. |

### Terminals (PTY)

| Key | Default | Description |
|---|---|---|
| read `count` / wait `timeout` | `500` lines / `5s` | Terminal read defaults; PTY opens at 80×24. |
| `tools.terminal.sandbox.*` | off | Command screening lists for the terminals tool; disabled by default (the internal safety blocklist still screens). |

## Memory & dream

| Key | Default | Description |
|---|---|---|
| `memory.enabled` / `auto_summarize` | `true` / `true` | Persistent memory dir `.goa/memory`; sessions summarized automatically. |
| `memory.dream.interval` | `"7d"` | Minimum gap between automatic consolidations. |
| `memory.dream.min_sessions` | `5` | Completed sessions required before dreaming. |
| `memory.dream.max_tokens` / `temperature` | `8192` / `0.2` | Consolidation model parameters. |

## TUI & session history

| Key | Default | Description |
|---|---|---|
| `tui.tools.preview_lines` | `10` | Lines shown per tool block in summary view (Ctrl+O toggles full view). |
| `tui.history.max_loaded` | `100` | Input-history entries loaded from session files (`0` = disabled). |
| `completion.min_usage_threshold` / `max_most_used` | `10` / `3` | Prompt-completion learning thresholds. |

## Retention windows

Terminal records are deleted after the configured window (enabled + days):

| Key | Default |
|---|---|
| `goals.retention` | `7 days` |
| `orchestrator.retention` | `7 days` |
| `plan.retention` | `7 days` |

## Related docs

- [CONFIGURATION](CONFIGURATION.md) — full cascade, schema and env overrides
- [GOALS](GOALS.md) — goal lifecycle, budgets and done-gate details
- [LOOP-DETECTION](LOOP-DETECTION.md) — detector internals and tuning stories
