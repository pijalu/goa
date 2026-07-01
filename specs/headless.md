<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Headless Mode Specification

## Status
Implemented. See `internal/app/headless.go`, `internal/app/bootstrap.go`, and
`core/execution.go`.

## Goal
Allow `goa` to execute a single user prompt from the command line with no TUI,
no interactive chat loop, and no screen-oriented buffering. Output is streamed
directly to `STDOUT`. Per-turn statistics are printed after each turn, and a
final summary is emitted when the session finishes.

Headless mode is a **different renderer** for the same agent session; the
underlying tools, provider, model, and context loading remain unchanged.

## CLI

### Headless-triggering flags

Providing either `--prompt` or `--prompt-file` implies headless mode. If
neither is given, Goa launches the interactive TUI.

| Flag | Short | Type | Required | Default | Description |
|------|-------|------|----------|---------|-------------|
| `--prompt` | `-p` | string | **yes** for headless | | The user prompt to execute. |
| `--prompt-file` | | string | **yes** for headless | | Read the prompt from this file path. |

### Other headless flags

| Flag | Short | Type | Required | Default | Description |
|------|-------|------|----------|---------|-------------|
| `--plain` | | bool | no | `false` | Force plain, uncolored output. |
| `--yes` | `-y` | bool | no | `false` | Auto-approve all gates/tool confirmations (sets yolo autonomy). |
| `--max-turns` | | int | no | `0` (unlimited) | Hard cap on agent turns; non-zero exit if exceeded. |
| `--timeout` | | duration | no | `0` (none) | Overall session timeout; non-zero exit if exceeded. |
| `--color` | | string | no | `auto` | `auto`, `always`, or `never`. `auto` enables ANSI only when `STDOUT` is a TTY. |
| `--memory-budget` | | int | no | `0` (auto) | Maximum tokens to inject from memory summaries. |

### Shared flags

| Flag | Meaning |
|------|---------|
| `--no-memory` | Do not inject long-term memory into the system prompt (TUI or headless). |

### Inherited flags

All existing flags continue to work unchanged:
`--model`, `--provider`, `--api-key`, `--endpoint`, `--profile`,
`--execution-mode`, `--thinking-level`, `--reasoning`, `--compression`,
`--temperature`, `--max-tokens`, `--config`, `--logfile`, etc.

### Examples

```bash
# Fully non-interactive, yolo
$ goa --yes --prompt "list go files in current directory"

# Prompt from file
$ goa --yes --prompt-file prompt.md

# Use a specific provider/model
$ goa --yes --provider openai --model gpt-5 --prompt "refactor main.go"

# Plain output, suitable for piping
$ goa --yes --plain --prompt "summarize README.md" > summary.txt

# Minimal interactive approval (TTY only)
$ goa --prompt "update go.mod"
Approve bash: go get -u [y/N]? y
```

## Execution semantics

1. Parse CLI and load config cascade as normal.
2. If `--prompt` or `--prompt-file` is provided, run in headless mode:
   initialize subsystems **without** the TUI.
   - Do not create `tui.TUI`, `tui.ChatViewport`, `tui.Editor`, `tui.Footer`.
   - Do not start the keyboard/render loop.
3. Load project context files (`AGENTS.md`) as normal.
4. Load memory summaries into the system prompt unless `--no-memory` is set.
5. Resolve model/provider, start agent session, send the prompt.
6. Run until the request is fully resolved:
   - Execute all tool calls and any follow-up model turns.
   - Stop when the assistant reaches idle with no pending tool calls.
7. Print per-turn stats after each completed turn.
8. Print final session summary.
9. Exit.

Headless mode is **stateless**: no session history, mode state, or chat log is
written to disk.

## Output format

### Default (ANSI)

When `STDOUT` is a TTY and `--plain` is not set, emit minimal ANSI styling:

- Role markers (`User:`, `Assistant:`, `Thinking:`, `Tool:`, `Companion:`)
  in distinct colors.
- Code blocks/bold rendered via the existing ANSI helpers, but **no block-level
  screen updates, no CSI 2026, no cursor movement**.
- Stats line in dim color.

### Plain format (`--plain`)

Line-oriented, strictly bounded markers. Each marker is on its own line.
Content is streamed line-by-line and is never interleaved.

```text
-- user
<prompt>

-- assistant
<streaming assistant content>

-- thinking start
<thinking content>
-- thinking end

-- tool call <name> id=<id>
<input JSON>

-- tool result <name> id=<id>
<result>

-- companion
<companion content>

-- stats turn=N in=... out=... speed=... ctx=...% cache=... cost=... compacts=...

-- summary turns=N total_in=... total_out=... total_cost=... total_time=...
```

Rules:

- A marker line begins with `-- ` and is never considered content.
- Tool input is always compact JSON on the lines immediately after the
  `-- tool call` marker.
- Tool result is printed verbatim after the `-- tool result` marker.
- Thinking blocks are always wrapped with `-- thinking start` / `-- thinking end`.
- Assistant content never appears inside a thinking block.
- Multiple assistant/thinking blocks may appear within a single turn.

### Stats line fields

Inline per-turn stats (printed on `EventEnd`):

| Field | Meaning |
|-------|---------|
| `turn` | Turn number (1-based) |
| `in` | Input tokens this turn |
| `out` | Output tokens this turn |
| `speed` | Output tokens per second |
| `ctx` | Context usage percent (`estimate/max`) |
| `cache` | Cache read/write and hit percent when applicable |
| `cost` | Estimated USD cost when pricing is configured |
| `compacts` | Micro/full compaction counts when non-zero |

Final summary (printed once at session end):

| Field | Meaning |
|-------|---------|
| `turns` | Total turns executed |
| `total_in` | Cumulative input tokens |
| `total_out` | Cumulative output tokens |
| `total_cache_read` | Cumulative cache read tokens |
| `total_cache_write` | Cumulative cache write tokens |
| `total_cost` | Cumulative estimated cost |
| `total_time` | Wall-clock session duration |

## Approval and gates

### `--yes` / yolo

If `--yes` is passed, or the active profile/mode is `yolo`, all tool
executions and workflow gates are approved automatically.

### Non-yolo with TTY

If stdin is a TTY, print a simple yes/no prompt:

```text
Approve <tool-name>: <one-line description> [y/N]?
```

Read one line. Only `y`/`Y`/`yes` approves; anything else rejects.
Rejection is reported as a tool error and the agent continues or stops
according to its error handling.

### Non-yolo without TTY

If stdin is not a TTY and neither `--yes` nor yolo is active, exit
immediately with:

```text
Error: headless non-yolo mode requires an interactive terminal or --yes
```

and exit code `1`.

## Companion mode

When the active configuration enables companion/workflow execution, companion
output is forwarded to stdout using the same markers:

```text
-- companion start cycle=1
-- companion thinking start
...
-- companion thinking end
-- companion
<companion content>
-- companion end cycle=1
```

Companion activity does not block the main agent stream; markers from the
main agent and companion may alternate, but each individual stream follows
the non-interleaving rule above.

## Exit codes

| Code | Condition |
|------|-----------|
| `0` | Session completed successfully (tool errors do not cause non-zero). |
| `1` | Missing prompt, invalid flags, config error, non-yolo without TTY. |
| `2` | LLM/provider connection or authentication failure. |
| `3` | Session exceeded `--max-turns`. |
| `4` | Session exceeded `--timeout`. |
| `5` | Unhandled runtime panic. |

## Memory injection

Long-term memory files (`.goa/memory/*.md` and global memory files) are
loaded by the existing `memory.MemoryStore`. For each memory file, only a
short **summary** is injected into the system prompt; the full content is
never included automatically. This keeps the context budget predictable.

Supported summary formats in a memory file:

```markdown
---
summary: Short summary of the memory.
---

# Full content

Detailed content the LLM can read with read_file if needed.
```

or

```markdown
## Summary

Short summary of the memory.

## Details

Detailed content...
```

Memories without a summary are skipped with a warning. The agent can read
the full memory file with the normal `read_file` tool when a summary looks
relevant.

The injected `<memory>` block is assembled in recency order and stops once
it reaches the memory token budget. The default budget is the smaller of
1024 tokens or 10% of the active model's context window. Use
`--memory-budget` to override it in headless mode; the same budget logic
applies to the TUI via configuration.

## Error handling

- Both `--prompt` and `--prompt-file` provided: print error to stderr and
  exit `1`.
- `--prompt-file` cannot be read: print error to stderr and exit `1`.
- Provider/model not configured: reuse existing error messages; exit `2`.
- Connection/stream error during a turn: print a friendly hint (reuse
  `friendlyConnectionHint`) to stdout and exit `2`.
- Tool execution error: print the error inside the `-- tool result` block,
  allow the agent to decide whether to retry/continue; does not change exit
  code unless the session itself cannot recover.

## Implementation

### Files changed

1. `internal/app/bootstrap.go`
   - Renamed `HeadlessOptions` to `RuntimeOptions`.
   - Removed `--headless`; headless is implied by `--prompt` or `--prompt-file`.
   - Added `--prompt-file` and `--memory-budget`.
   - Added `RuntimeOptions.UserPrompt()` to read inline or file prompts.
   - Added `RuntimeOptions.Validate()` for runtime option validation.

2. `internal/app/app.go`
   - `runApp()` now validates headless options and branches to
     `runHeadless()` when `--headless` is set, skipping the TUI entirely.

3. `internal/app/headless.go` (new)
   - `HeadlessRenderer` interface with `plainRenderer` and `ansiRenderer`
     implementations.
   - `ConfirmStrategy` interface with `autoConfirmStrategy`,
     `ttyConfirmStrategy`, and `rejectConfirmStrategy` implementations.
   - `HeadlessApp` orchestrates the session:
     - Starts the agent session via `AgentManager`.
     - Reads `agentic.OutputEvent` and renders directly to stdout.
     - Registers a `ConfirmConsumer` to answer confirmations.
     - Forwards `ForegroundOrchestrator` companion events.
     - Tracks per-turn and cumulative stats.
     - Enforces `--max-turns` and `--timeout` via `AgentManager.Interrupt()`.
     - Prints per-turn stats after each `EventEnd` and a final summary.

4. `internal/app/prompt.go`
   - Extended `buildSystemPrompt` to inject a `<memory>` section with
     summaries from the memory store.
   - Added `buildMemorySection`, `extractMemorySummary`, and budget helpers.

5. `internal/app/stats.go`
   - Extracted `applyCacheStats` and `applyPricing` as package-level helpers
     so both the TUI footer and headless output share the same math.
   - Added `formatFooterStatsPlain` to strip ANSI for `--plain` output.

6. `core/execution.go`
   - Added `ConfirmConsumer` callback API and `SetConfirmConsumer`.
   - `RequestConfirm` now blocks reliably and defaults to `ConfirmNo` when
     no consumer is registered or the consumer fails.

7. `core/agentmanager.go`
   - Added `IsRunning() bool` so headless can detect when the agent turn
     completes and exit cleanly without a TUI stop signal.

7. `tui` package
   - No changes required. Headless bypasses the TUI entirely.

### Design principles (SOLID)

The headless implementation must follow SOLID principles and not become a
second copy of the TUI event loop.

- **Single Responsibility.**
  - `internal/app/headless.go` owns only headless orchestration and stdout
    rendering.
  - `internal/app/app.go` owns the high-level mode decision (TUI vs headless).
  - `internal/app/stats.go` owns token/cost/context math and formatting.
  - `core.AgentManager` and `core.ExecutionController` continue to own agent
    and approval lifecycles; headless consumes them, it does not reimplement
    them.

- **Open/Closed.**
  - Add new output formatters (ANSI, plain, future JSON) as separate small
    types implementing a common `HeadlessRenderer` interface. Do not modify
    the core event consumer when adding a new format.
  - Add new approval strategies (`AutoApprove`, `TTYConfirm`, `RejectAll`) as
    implementations of a `ConfirmStrategy` interface.

- **Liskov Substitution.**
  - The headless event consumer must work with the real `AgentManager` and
    `ForegroundOrchestrator` in production and with test doubles in unit
    tests. No behavior should depend on concrete TUI types.

- **Interface Segregation.**
  - The renderer interface should expose only what a formatter needs:
    `Content(role, text)`, `ThinkingStart/End()`, `ToolCall(name, id, input)`,
    `ToolResult(name, id, output)`, `Stats(stats)`, `Summary(summary)`.
  - The approval strategy interface should expose only `Confirm(toolName,
    input) (bool, error)`.

- **Dependency Inversion.**
  - `HeadlessApp` depends on interfaces (`HeadlessRenderer`,
    `ConfirmStrategy`, `AgentManager`-shaped abstractions), not on the TUI
    package or `os.Stdout` directly.
  - Concrete stdout writing, ANSI coloring, and terminal detection live in
    small, swappable types injected at construction.

Concrete rules:

- **No TUI engine in headless mode.** Do not instantiate `tui.TUI`.
- **No buffering.** Write events to stdout as soon as they arrive. Do not
  wait for a render cycle.
- **Stateless.** Do not persist session or chat history. Do not write mode
  state to disk.
- **Reuse event paths.** Consume the same `agentic.OutputEvent` and
  orchestrator message streams the TUI consumes.
- **Reuse stats logic.** Keep a single source of truth for token/cost/context
  formatting.

## Testing

Implemented tests in `internal/app/headless_test.go` and
`internal/app/headless_integration_test.go`:

- `toolConfirmDescription` formatting.
- `autoConfirmStrategy`, `ttyConfirmStrategy`, and `rejectConfirmStrategy`.
- `HeadlessOptions.Validate`.
- `plainRenderer` marker output (user, assistant, thinking, tool call,
  tool result, stats, summary).
- `ansiRenderer` basic output.
- `HeadlessApp` event handling and stats accumulation.
- End-to-end integration test with a registered fake provider that streams
  text content and verifies all stdout markers.
- No-provider error path.

All tests run with `-race`. Complexity gates (`gocognit -over 15`,
`gocyclo -over 12`) pass for modified files.

## Future extensions (not in MVP)

- Memory management tool gated behind `memory.tool_enabled` config.
- `--output-file` to write stdout to a file while still streaming.
- `--json` output mode for machine parsing.
- `--session-name` to persist a specific headless session.
- Multiple prompts in sequence (`--prompt` repeated or prompt file).
