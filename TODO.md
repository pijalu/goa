# TODO — Remaining items from agentic optimization pass

This file tracks findings identified during the agentic optimization / SOLID review
that were **not** fixed in the current pass. Each item is categorized by type and
has a clear suggestion for a future change.

---

## 🔵 Structural (recommended next)

### 1. Split `internal/agentic/agent.go` — 3150 lines, 136 methods

**Problem**: Classic god-object. `Agent` struct (~30 fields) and its 136 methods
mix compression logic, streaming, budget tracking, guardrails, history management,
event emission, observer management, tool execution, context stats, turn lifecycle,
and migration helpers into one file.

**Suggestion**:
- Extract `compression.go` — `compact`, `compressHybrid`, `compressToolElision`,
  `compressSelective`, `microCompactForced`, `MaybeCompress`, `maybeCompress`,
  `compressHistory`, `checkContextLimit`, etc.
- Extract `streaming.go` — `processTurnWithStream`, `consumeStream`,
  `handleStreamEvent`, `handleTextDelta`, `handleThinkingDelta`,
  `checkStreamLoop`, stream-loop detection helpers.
- Extract `budget.go` — `shouldBufferToolCall`, `checkTotalRepeatGuardrail`,
  `budgetOrRepeatSkipMessage`, `recordToolCallInBudgetWindow`,
  `effectiveToolWindowSize`, `applyToolGuardrail`, `applyToolBudgetSkip`.
- Extract `events.go` — `emitEvent`, `emitMessage`, `emitEndEvent`,
  `emitToolCallEvent`, `emitContentMessage`, `emitToolResult`,
  `emitThinking`, `emitStatelessEvents`.

---

### 2. Remove duplicate `ToolRegistry` — `internal/agentic/` vs `tools/`

**Problem**: Two `ToolRegistry` types with overlapping APIs:
- `internal/agentic/tool_registry.go` — `NewToolRegistry`, `Get`, `Schemas`, `LoopHints`
- `tools/registry.go` — `ToolRegistry`, `Register`, `Get`, `All`, `Schemas`,
  plus `Documentable` / `ToolGroup` support

The agent holds the agentic one; the app holds the tools one and must feed `All()`
into `Config.Tools`, then `Agent` re-wraps in its own registry: two registries, two
`Schemas()` sort paths, double maintenance.

**Suggestion**: Make `tools.ToolRegistry` the single implementation; have agentic
depend on a minimal `ToolLookup` interface (Dependency Inversion) instead of owning
a parallel registry.

---

### 3. `toolcallparser.go` — 4 concerns mixed, one file, no types

**Problem**: JSON tool-call parsing, function-style parsing, brace balancing/
string-state handling, and parameter finalization are all free functions sharing
`content`/`indices`. The `insideOpenParameter` helper re-scans `content[:pos]`
with `FindAllStringIndex` for every function start — O(n²) in content length.

**Suggestion**: Introduce a `toolCallScanner` struct holding `content` + a cursor;
expose `nextJSONCall()` / `nextFunctionCall()`. Removes the repeated
`funcStarts` juggling and the O(n²) insideOpenParameter. Follows the pattern
the project already uses in `observer/xmlstream/state.go`.

---

## 🟠 Correctness / correctness-adjacent

### 4. `BashTool` / `TerminalTool` also ignore turn ctx (like webfetch did)

**Problem**: Like webfetch before A4, `bash.go` (line 156) waits on
`time.After(timeout)` only — no `ctx.Done()` select — and `exec.Command`
is used instead of `exec.CommandContext`. Agent `Stop()` cannot interrupt a
running shell command; the child process tree lives until the bash timeout.

**Suggestion**: Implement `ContextTool` on `BashTool` and `TerminalTool`.
In the timeout select, add `case <-ctx.Done():` and kill the process tree.
For bash, use `exec.CommandContext` or signal the process group. (Requires
OS-specific handling — see `bash_unix.go` / `bash_windows.go`.)

### 5. Pre-existing dead test scaffolding — U1000 in `agent_context_test.go`

**Problem**: The type `contextToolCallProvider` (and its methods `API`, `Stream`,
`StreamSimple`) are defined but never referenced. Present at HEAD before the
current changes. Staticcheck flags U1000.

**Suggestion**: Remove the unused type and methods. Verify no test depends on
them (they aren't referenced by any test function). Safe cleanup.

---

## ⚪ Optimization / Performance

### 6. `ToolScheduler` spawns one watcher goroutine per task

**Problem**: `tool_scheduler.go:83-99` spawns a cancellation watcher goroutine
for **every** task including ones that start immediately, effectively doubling
goroutines for the common all-independent case.

**Suggestion**: Only spawn the cancellation watcher for tasks added to `pending`;
for immediately-started tasks, rely on the execution goroutine + a single
per-scheduler ctx watcher.

### 7. `formatCompressHeader` has unused parameters

**Problem**: `tools/common/compress.go:356` accepts `(cmd string, lines []string,
a, b int)` but ignore `lines`, `a`, `b`. Every caller passes meaningless values
for these parameters (often the same values used later for `strings.Join`).

**Suggestion**: Remove the unused parameters. The function only uses `cmd`. Update
all callers (they already pass values solely to satisfy the signature).

---

## 📝 General technical debt

### 8. `agent.go` line limits

Several files in the repo exceed the 500-line soft target and two exceed the
1000-line hard max (see `find ... wc -l | sort -rn`). The most impactful split
is `agent.go` (3150 lines). Other large files worth splitting:
- `core/commands/config.go` (2015)
- `config/config.go` (1460)
- `config/wizard_render.go` (1429)
- `core/agentmanager.go` (1424)
- `internal/app/headless.go` (1118)
- `tui/editor.go` (1058)
