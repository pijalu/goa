<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Fix plan — bugs.md items of 2026-08-21

Scope: the three items under `bugs.md` `# TODO`. The multi-agent TUI
restructure (`docs/research/multi-agent-tui.md`) and the full async-delegation
protocol (`specs/async-delegation.md` P1+) are **out of scope** — only the
bug-level fixes below.

Status legend: `[ ]` open, `[x]` done.

---

## Bug 1 — `delegate_to` reports success without clear UI feedback (silent sub-agent 400)

Root cause (confirmed from export `goa-export-20260820-232441.zip`, spec §9):
`AgentPool.assembleConfig` (`multiagent/agent_pool.go:359-363`) force-sets
`opts.MaxTokens = 4096` when the configured value is `0`/`<2048`. The codex
responses transport (`internal/agentic/provider/openai_responses/provider.go:215-217`)
then emits `max_output_tokens`, which the ChatGPT Codex **subscription** backend
rejects with `400 Unsupported parameter: max_output_tokens`. The main agent is
unaffected (`BuildStreamOptions` leaves `MaxTokens = 0` → field omitted), so
only the sub-agent fails — and it fails **silently**: `runDelegatedAgent`'s
error path only calls `EmitStreamEnd(role)` (closes the stream) and never
surfaces the error in the chat.

### Fix (per spec §9 + design principle G6 "sub-agents are normal agents")

1. **Remove the pool divergence** — delete the `MaxTokens = 4096` floor in
   `AgentPool.assembleConfig` so sub-agents inherit the main agent's stream
   options verbatim (`multiagent/agent_pool.go`). Audit the same function for
   other sub-agent-only special-casing (none found: the only other mutations
   are `ToolChoice`/`appendToolDirective` for workflow stage agents, which are
   role requirements, not transport divergence, and
   `StickyProvider`/`inheritGoaConfig` which mirror the main agent).
2. **Transport guard (defense in depth)** — in `buildResponsesBody`, do not
   emit `max_output_tokens` when `flavor == "codex"` (the subscription
   transport already omits `store`/`previous_response_id` for the same class
   of backend rejection). Non-codex responses flavors keep emitting it.
3. **Visible failure** — in `DelegateTool.runDelegatedAgent`'s error path,
   additionally `Orchestrator.Emit(role, "main", "delegation failed: "+err)`
   so the failure lands as an agent-attributed chat entry via the existing
   InterAgent → `AddAgentMessage` path (not only `EmitStreamEnd`).
4. **Visible spawn + status** — add `tools/delegate_renderer.go` rendering
   `delegate_to` and `request_review` tool bubbles (call: `⇒ delegate_to
   <agent> — "<task>"`; result: delegated ack; error: red), registered for
   both tools in `tui/register_renderers.go`.

### Test approach (each would have caught the bug)

- `multiagent/agent_pool_test.go`: pool-built config keeps `MaxTokens == 0`
  when the model config is `0` (before fix: was forced to 4096).
- `internal/agentic/provider/openai_responses/provider_test.go`: codex-flavor
  body with `MaxTokens > 0` omits `max_output_tokens`; non-codex responses
  body still carries it (before fix: codex body carried it → the 400).
- Renderer unit test: call/result/error output of the DelegateRenderer.
- Failure visibility: scripted mock-LLM provider (commit f704bf6) or a direct
  orchestrator-message assertion — a sub-agent whose provider errors produces
  an `Emit(role,"main",…)` message containing the error, i.e. a visible chat
  entry (before fix: only EmitStreamEnd, nothing in chat).

### Validation steps

- Gates run separately: `go vet ./...`, `staticcheck ./...`,
  `gocognit -over 15 .`, `gocyclo -over 12 .`,
  `go test -count=1 -race -cover ./...`.
- Filmstrip/uiScenario: run a scenario with a delegate_to call against a
  failing scripted provider; verify the terminal shows the delegate bubble
  and the failure entry.

---

## Bug 2 — schedule tools should be deferred to `tool_search`

`schedule_create/delete/list` are always-loaded despite being rarely used.
This reverses the 2026-08-17 NOT-A-BUG decision (now a requested feature).

### Fix

1. Add `func (*ScheduleCreateTool) Deferred() bool { return true }` (same for
   Delete/List) in `tools/deferred.go`.
2. Invert `TestScheduleToolsAreEager` → `TestScheduleToolsAreDeferred` in
   `tools/tool_search_test.go`: assert the three tools are withheld from the
   eager schema block, are loadable via `select:schedule_create,…`, and work
   after loading.

### Test approach

- The inverted test fails before the fix (tools are eager) and passes after;
  it also guards against someone removing the `Deferred()` markers.

### Validation steps

- Gates as above. `tool_search` catalog must list the schedule tools; a
  `select:` load returns their schemas; a loaded `schedule_list` executes.

---

## Bug 3 — out-of-screen tool call results corrupt the terminal UI

Root cause: rows committed to terminal scrollback (below the compositor
watermark) can never be repainted. Expanding such a tool widget
(per-widget Enter/Ctrl+O via `setExpandedExplicit`, or the global Ctrl+O
`ToggleAllToolsView` → `invalidateAllToolWidgets`) rebuilds the widget with a
different line count, shifting every later entry's geometry — the compositor
then repaints against rows that no longer match the terminal → corruption.

`ChatViewport.IsScrolledOff` (`tui/chat_viewport_messages.go:43`) already
identifies exactly these widgets.

### Fix

1. Extend `tui.ToolViewPolicy` with `IsScrolledOff(c Component) bool` (the
   ChatViewport already implements it) and a notice sink `FlashNotice(string)`.
2. `setExpandedExplicit` (`tui/tool_execution.go`): when the widget is
   scrolled off, no-op (do not set the override, do not rebuild) and flash a
   one-line notice instead.
3. `invalidateAllToolWidgets` (`tui/chat_viewport.go`): skip scrolled-off
   widgets; if any were skipped, flash one summary notice. Covers both the
   Ctrl+O toggle-all and config-change paths.
4. `SetExpanded` is only reachable via `setExpandedExplicit` (verified by
   call-graph), so guarding `setExpandedExplicit` covers all expansion
   mutations.

### Test approach

- Unit: a fake `ToolViewPolicy` reporting scrolled-off — `HandleInput("ctrl+o")`
  leaves the widget collapsed, records no explicit override, and emits the
  notice.
- Unit: `invalidateAllToolWidgets` on a viewport with a scrolled-off tall tool
  box leaves that widget's geometry untouched.
- Filmstrip/compositor regression: commit a tall tool box to scrollback, then
  Ctrl+O; assert the rows above the watermark stay byte-identical and the
  chrome stays aligned (before fix: repaint misaligns).

### Validation steps

- Gates as above; filmstrip scenario validating actual terminal output.
