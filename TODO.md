# TODO — agentic optimization pass

All items from the agentic optimization / SOLID review have been resolved.
This file is retained for history; each entry below records what was done
and the commit that closed it.

---

## ✅ Structural

### 1. Split `internal/agentic/agent.go` — was 3150 lines

The god-object has been decomposed into focused, same-package files (all
under the 1000-line hard limit):

- `agent_events.go` — output-event emission (`emit*`).
- `agent_budget.go` — tool-call budget + repeat guardrails.
- `agent_streaming.go` — stream-round drivers, event consumption, loop
  detection, and stream-recovery/provider-context.
- `agent_compression.go` — context-compression machinery.
- `agent_context_stats.go` — context-usage stats + token estimation.
- `agent_tools.go` — tool-call scheduling/execution + content-block helpers.
- `agent_migrate.go` — provider message/schema migration.
- `agent_turn_stats.go` — generation timing, turn stats, history helpers.

`agent.go` is now 904 lines (was 3150). Pure relocations; no behavior change.

### 2. Remove duplicate `ToolRegistry`

Dependency Inversion applied: `agentic` now defines a minimal `ToolLookup`
interface (`Get`/`Schemas`/`LoopHints`) and the `Agent` depends on it
instead of the concrete `*ToolRegistry`. `agentic.ToolRegistry` remains the
canonical immutable, caching implementation (used by the agent and the MCP
publisher). The dead duplicate sort path (`tools.ToolRegistry.Schemas`,
never called in production) was removed.

### 3. `toolcallparser.go` — cursor-based scanner

Replaced the free-function orchestration (with precomputed `funcStarts` and
the O(n²) `insideOpenParameter` rescan) with a `toolCallScanner` struct
holding `content` + a forward cursor, exposing `nextJSONCall`/
`nextFunctionCall`. Because the cursor advances through consumed parameter
values, a nested `<function=` token inside a value is absorbed and never
treated as a top-level boundary.

---

## ✅ Correctness

### 4. `BashTool` / `TerminalTool` now respect the turn ctx

Both implement `ContextTool` (`ExecuteContext`). `BashTool`'s run select
now includes `<-ctx.Done()` and kills the process tree; `TerminalTool`
threads `ctx` into `sandbox.Run` via `RunOpts.Cancel`. A cancelled turn is
surfaced as a `cancelled` tool error. Tests verify a 30s `sleep` is
interrupted in ~0.3s on cancellation.

### 5. Dead test scaffolding removed

The unused `contextToolCallProvider` type and methods (U1000) were removed
from `agent_context_test.go`.

---

## ✅ Performance

### 6. `ToolScheduler` watcher goroutines

`Add` now registers the cancellation watcher only for tasks added to
`pending` (blocked). Immediately-started tasks rely on their execution
goroutine (which already receives `s.ctx`), halving goroutines in the common
all-independent case.

### 7. `formatCompressHeader` unused parameters

Reduced from `(cmd, lines, a, b)` to `(cmd)`; all call sites updated.

---

## ✅ General technical debt — file size limits

All production Go files now respect the 1000-line hard max. The following
were split (each a pure same-package relocation, behavior unchanged):

| File (was) | Now | Extracted into |
|---|---|---|
| `core/commands/config.go` (2015) | 958 | `config_completion.go`, `config_models.go`, `config_compression.go`, `config_cli.go` |
| `config/config.go` (1460) | 806 | `config_validate.go`, `config_merge.go` |
| `config/wizard_render.go` (1429) | 955 | `wizard_render_views.go` |
| `core/agentmanager.go` (1424) | 995 | `agentmanager_lifecycle.go`, `agentmanager_events.go` |
| `internal/app/headless.go` (1118) | 782 | `headless_renderers.go` |
| `tui/editor.go` (1058) | 750 | `editor_input.go` |
| `internal/agentic/provider/protocol/openai_completions.go` (1052) | 837 | `openai_completions_timings.go` |

Gates verified: `go vet ./...`, `go test -count=1 -race ./...` (64 packages,
all passing), and `find ... > 1000 lines` returns no production files.
