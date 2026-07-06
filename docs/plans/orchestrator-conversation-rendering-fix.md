# Fix Plan — Orchestrator "does nothing" + Broken Conversation Rendering

SPDX-License-Identifier: GPL-3.0-or-later

Source bugs: `bugs.md` → "Orchestrate" + "Incorrect rendering of conversation".
Target agent: **any agent, low cognitive level**. Follow tasks in order. Do NOT skip the RED tests. Do NOT chain code-quality tools with `;` or `&&`.

---

## 0. Context the executing agent MUST read first (do not re-investigate)

The `/orchestrate` command uses **`core/orchestrator.Runtime`** (NOT `multiagent.ForegroundOrchestrator`, which is for companion/workflows). Key files:

| File | Role |
|------|------|
| `core/commands/orchestrate.go` | `/orchestrate` command; `forwardEvents` → `handleOrchEvent` flashes only. |
| `core/orchestrator/runtime.go` | `Runtime.Run` → `runHub`/`runFanout`/`runPipeline`; emits `core/orchestrator.Event`. |
| `core/orchestrator/handle.go` | `AgentHandle`, `RunTurn`. |
| `core/orchestrator/store.go` | `EventType` constants + durable NDJSON store. |
| `internal/app/orchestrator_adapter.go` | `OrchestratorAdapter.NewRuntime`; `applyOutputEvent` = observer that bridges `agentic.OutputEvent` → runtime. **Only content is forwarded today.** |
| `internal/app/orchestrator_view_forwarder.go` | `runOrchestratorViewForwarder`/`drainOrchView` = App-side single owner; translates events → `MultiAgentView`. |
| `internal/app/orch_view_source.go` | `translateOrchEvent` = only orchestration-specific seam. |
| `tui/orchestrator/view.go` | `MultiAgentView`; `handleAgentMessage` **appends one log line per chunk** (root of the broken output). |
| `tui/orchestrator/content.go` | `AgentContent.Render` → "All" tab prints `[role] <line>` per line. |
| `tui/orchestrator/event.go` | Neutral `AgentViewEvent` seam + `AgentEventKind`. |
| `internal/app/stats.go` | `handleAgentOutputEvent` → normal chat widget pipeline (thinking/content/tool). The **target model** to reuse. |
| `tui/chat_viewport.go` + `tui/chat_viewport_components.go` | Chat widgets: `thinkingBlock`, `assistantMessage`, `agentMessage`, `ToolExecutionComponent`. All support in-place `SetText`. |
| `internal/app/orchestrator_view_forwarder_test.go` | `orchViewScenario` harness + `lifecycleEvents()`. |
| `internal/app/orchestrator_tabs_filmstrip_test.go` | Filmstrip regression pattern. |
| `prompts/orchestrate/hub_orchestrator.md` | Minimal orchestrator prompt. |

---

## 1. Root-cause summary (already localized — do not re-derive)

### Bug A — "Orchestrate agent does not seem to do much"
The orchestration **logic is correct**: hub runs the orchestrator role, the `OrchestratorDelegateTool` is wired (`orchestrator_adapter.go`), and `TestOrchestratorAdapter_LiveHub` proves end-to-end delegation. The bug is almost entirely a **symptom of Bug B**: the user cannot *see* the orchestrator's thinking, its `delegate` tool calls, or the coder's work, so it looks idle. Secondary contributor: `hub_orchestrator.md` is too terse to guarantee a visible analyze→delegate flow.

### Bug B — "Incorrect rendering of conversation" (the real defects)
- **B1 (chat hidden):** `attachOrchView` calls `a.subs.chat.SetSuppressed(true)`, so the normal chat returns nil and `AgentContent` (a stats-table + transcript-log panel) replaces it. Conversation is NOT rendered "as other chat".
- **B2 (one line per chunk):** `MultiAgentView.handleAgentMessage` → `appendLine` adds a new `AgentLogLine` per streamed chunk; the "All" tab prefixes each with `[role] `. This produces exactly the reported `[coder] he` / `[coder] llo` / … output. Content must accumulate in one in-place-updating block.
- **B3 (thinking never emitted):** `applyOutputEvent` forwards only `ev.State == agentic.StateContent`. The `StateThinking` branch is missing → no thinking block ever exists.
- **B4 (tool calls invisible):** `EventToolCall` only does `h.Stats.IncToolCall()`. No event/widget for tool start/result → delegations & tool runs are invisible (counter only).
- **B5 (no concurrent streaming):** No per-agent in-place streaming block. Parallel agents must each have their own thinking/content/tool widgets that stay at their start position and update in place until complete.

---

## 2. Design decision (follow exactly — do not redesign)

**Render the orchestrator conversation in the NORMAL chat viewport** using the existing widget primitives (`thinkingBlock`, `agentMessage`, `ToolExecutionComponent`), with **per-agent stream state** so N agents stream concurrently into distinct in-place-updating blocks. Keep the stats table as a togglable Stats panel (Ctrl+x tabs).

Concretely:
1. The chat viewport is **NOT suppressed** during orchestration. The interleaved agent conversation renders there, each block labeled with the agent's role (reuse the colored `[role]` prefix already in `agentMessage`).
2. `Ctrl+x` tabs toggle between **Conversation** (the chat, default) and **Stats** (the existing `MultiAgentView` stats table). Suppression now means "show stats panel instead of chat", driven by the active tab — reuses the existing `SetSuppressed` mechanism.
3. The adapter forwards the **full** per-agent stream (thinking/content/toolcall/toolresult) into the runtime event bus as new `core/orchestrator.Event` types; the App forwarder routes them to agent-scoped chat widgets.
4. The `MultiAgentView` loses its "All" and per-agent **transcript** tabs (replaced by the chat). It keeps only the **Stats** tab + stats rows (still fed by `EventAgentStats`).

**Rejected alternative:** duplicate streaming widgets inside `MultiAgentView`. Rejected because the chat viewport already owns these widgets with in-place update + incremental render cache; duplicating diverges and doubles the work.

**Why this is the "huge correct implementation":** it unifies all multi-agent output (main agent + orchestrator agents) under one widget pipeline, satisfies the "rendered as other chat" requirement, and supports true parallel streaming. Per project Hard Rule #4/#6, do the full clean design now.

---

## 3. Test strategy (mandatory, RED first)

- **TUI behavior:** use the `orchViewScenario` harness (`internal/app/orchestrator_view_forwarder_test.go`) and the `uiScenario` harness (`internal/app/ui_scenario_test.go`). Drive `orchestrator.Event` sequences, capture `tui.Filmstrip`, assert on **ANSI-free** structured frames (`frame.FindNode`, `Filmstrip.Render()`, `StatusTrace()`). NEVER assert on escape bytes. NEVER run goa against a live model to "see" the bug. (See `.agents/skills/tui-test/SKILL.md`.)
- **Pure logic:** table-driven unit tests in `core/orchestrator` and `tui/` packages with `<100ms` per test.
- **Adapter:** extend `internal/app/orchestrator_adapter_integration_test.go` patterns; a fake-pool test that asserts the new events are emitted for thinking/toolcall/toolresult.

---

## PHASE 0 — Reproduce with failing tests (RED)

> Do these BEFORE any production change. They must fail for the right reason, then pass after the fix. Put tests in `internal/app/` (filmstrip) and `core/orchestrator/` (logic).

### Task 0.1 — Failing filmstrip: single agent streams a thinking block then content block then a tool widget, in the CHAT (not suppressed)
- **File:** `internal/app/orchestrator_conversation_render_test.go` (NEW).
- **Harness:** `newOrchViewScenario(t, 100, 30)`.
- **Steps in test:**
  1. `sc.app.attachOrchView(newFakeOrchSource())`; `sc.flush()`.
  2. Feed a synthetic `orchestrator.Event` sequence via a new fake-source helper that mimics: `EventRunStarted` → `EventAgentStarted{Role:"coder"}` → several `EventAgentThinking{Role:"coder",Text:chunks}` → several `EventAgentMessage{Role:"coder",Text:chunks}` → `EventAgentToolCall{Role:"coder",ToolName:"writefile"}` → `EventAgentToolResult{...}` → `EventAgentFinished`.
  3. `film := captureLifecycleFilmstrip(t, sc)` (extend the helper if needed to return the filmstrip for a custom sequence).
- **Assertions (all must hold after fix):**
  - The chat viewport is **NOT** suppressed in the conversation frame: a chat node exists and is non-empty.
  - Exactly **one** thinking block for `coder` that grows in place: assert the thinking text in consecutive frames equals the accumulated chunks (not one block per chunk). Use `Filmstrip.Render()` text and check the thinking header appears once and its body length grows.
  - Exactly **one** content/agent block for `coder` accumulating in place.
  - A **tool execution widget** node is present for `writefile`.
  - No `[coder] <single chunk>` per-line pattern in the rendered text.
- **Mark** the test with `t.Logf` showing the actual filmstrip on failure.

### Task 0.2 — Failing filmstrip: two agents stream thinking CONCURRENTLY into two distinct in-place blocks
- **File:** same new test file.
- **Sequence:** `EventAgentStarted{coder}` and `EventAgentStarted{reviewer}`; interleave `EventAgentThinking{coder,"a1"}`, `EventAgentThinking{reviewer,"b1"}`, `EventAgentThinking{coder,"a2"}`, `EventAgentThinking{reviewer,"b2"}`.
- **Assertions:**
  - Two distinct thinking blocks exist, labeled `coder` and `reviewer`.
  - The `coder` block text ends with accumulated `a1a2`; the `reviewer` block ends with `b1b2`.
  - Each block stays at its original position (its line do not jump) across frames — assert via frame line indices that the `coder` thinking header line index is stable while only its body grows.

### Task 0.3 — Failing unit test: adapter emits thinking/tool events
- **File:** `internal/app/orchestrator_adapter_events_test.go` (NEW) mirroring `orchestrator_adapter_integration_test.go` but with a fake pool/agent that replays `agentic.OutputEvent`s.
- **Assertion:** after driving `StateThinking`/`StateContent`/`EventToolCall`/`EventToolResult` through the observer, the runtime's event stream contains the new event types (see Phase 1) carrying the right `Role`/`Text`/`ToolName`.

### Task 0.4 — Failing unit test: chat viewport agent-scoped stream registry
- **File:** `tui/chat_viewport_agent_stream_test.go` (NEW).
- **Assertion:** calling new agent-scoped methods (see Phase 3) for two agents produces two independent in-place widgets; updating one does not mutate the other.

> After Phase 0, run `go test -run 'OrchestratorConversation|OrchestratorAdapterEvents|ChatViewportAgentStream' ./internal/app/ ./tui/ ./core/orchestrator/` and confirm all four FAIL. This is the gate to start Phase 1.

---

## PHASE 1 — Emit full per-agent stream fidelity from the adapter

### Task 1.1 — Add new orchestrator event types
- **File:** `core/orchestrator/store.go`.
- **Change:** add constants to the `EventType` block:
  - `EventAgentThinking  EventType = "agent_thinking"`
  - `EventAgentToolCall  EventType = "agent_tool_call"`
  - `EventAgentToolResult EventType = "agent_tool_result"`
- **Doc:** each carries `AgentID`/`Role`; payload fields: thinking→`{"text":..., "active":bool}`; toolcall→`{"tool":name, "input":argsJSON, "call_id":id}`; toolresult→`{"call_id":id, "text":..., "ok":bool}`.

### Task 1.2 — Add runtime record methods (mirror `RecordAgentMessage`)
- **File:** `core/orchestrator/runtime.go`.
- **Add methods** (all nil-safe, mutex-free except where they touch `r.msgs`; follow `RecordAgentMessage`'s pattern of `r.emit`):
  - `func (r *Runtime) RecordAgentThinking(h *AgentHandle, text string)` → emits `EventAgentThinking` with payload `{"text": text}`. Accumulate nothing (thinking is display-only; do NOT store in `r.msgs`).
  - `func (r *Runtime) RecordAgentToolCall(h *AgentHandle, tool, input, callID string)` → emits `EventAgentToolCall`.
  - `func (r *Runtime) RecordAgentToolResult(h *AgentHandle, callID, text string, ok bool)` → emits `EventAgentToolResult`.
- **Constraint:** each method must early-return on `h == nil` (or empty text where relevant), matching `RecordAgentMessage`.

### Task 1.3 — Forward thinking + tool events from the adapter observer
- **File:** `internal/app/orchestrator_adapter.go`, function `applyOutputEvent`.
- **Current:** only `EventContent` with `StateContent` → `RecordAgentMessage`.
- **Change:** extend the `agentic.EventContent` case:
  - `ev.Role == Assistant && ev.State == StateThinking && ev.Text != ""` → `rt.RecordAgentThinking(h, ev.Text)`.
  - keep the existing `StateContent` branch → `RecordAgentMessage`.
- Add new cases:
  - `agentic.EventToolCall` → keep `h.Stats.IncToolCall()` AND add `rt.RecordAgentToolCall(h, ev.ToolName, ev.ToolInput, ev.ToolCallID)`.
  - `agentic.EventToolResult` → `rt.RecordAgentToolResult(h, ev.ToolCallID, ev.Text, /*ok*/ !isErrorResult(ev.Text))`. Add a tiny helper `isErrorResult(s string) bool` returning true for `"Error:"` prefix or `agentic.ToolBudgetResultPrefix` prefix (mirror `toolStatusFromResult` in `stats.go`).
- **Do NOT** remove the existing `EmitLiveStats` call on `EventTokenStats`.
- **Validation:** Task 0.3 now passes.

### Task 1.4 — Unit tests for the new runtime methods
- **File:** `core/orchestrator/runtime_events_test.go` (NEW).
- Table-driven: drive `RecordAgentThinking/ToolCall/ToolResult` with a fake handle and a captured `Event` channel; assert type, Role, and payload fields. Include nil-handle and empty-text no-op cases.

> Gate: `go test -count=1 -race ./core/orchestrator/`

---

## PHASE 2 — Neutral seam + forwarder routing

### Task 2.1 — Extend the neutral `AgentViewEvent` seam with tool/thinking kinds
- **File:** `tui/orchestrator/event.go`.
- **Add kinds:**
  - `EvAgentThinking AgentEventKind = "agent_thinking"`
  - `EvAgentToolCall AgentEventKind = "agent_tool_call"`
  - `EvAgentToolResult AgentEventKind = "agent_tool_result"`
- **Add fields to `AgentViewEvent`:** `Tool string`, `ToolInput string`, `CallID string`, `OK bool`. (Thinking reuses `Text`.)

### Task 2.2 — Translate the new orchestrator events
- **File:** `internal/app/orch_view_source.go`, function `translateOrchEvent`.
- **Add cases** returning the new neutral kinds, mapping payload fields via existing `orchStr`/`orchInt` + a new `orchBool` helper (copy `orchInt`'s shape for `bool`). Keep returning `ok=true`.
- **Update test:** `internal/app/orch_view_source_test.go` — add table rows for the 3 new event types.

### Task 2.3 — Route conversation events to the chat; stats/steer/finish stay on the view
- **File:** `internal/app/orchestrator_view_forwarder.go`, function `drainOrchView`.
- **Change the apply closure:** after `translateOrchEvent`, branch on `ne.Kind`:
  - `EvAgentThinking` / `EvAgentMessage` / `EvAgentToolCall` / `EvAgentToolResult` → call **new App methods** (Phase 3) that drive agent-scoped chat widgets. Do NOT apply these to `view` (transcript logs are gone).
  - `EvSourceStarted` / `EvSourceFinished` / `EvAgentStarted` / `EvAgentStats` / `EvAgentSteered` → `view.ApplyEvent(ne)` as today (stats table).
  - On `EvAgentStarted` ALSO call a new `a.beginAgentStream(role, agentID)` (Phase 3) so the chat knows a new agent's stream begins (used to start fresh blocks + reset per-agent state).
  - On `EvAgentFinished` call `a.endAgentStream(agentID)`.
- Keep everything inside `a.apply(func(){...})` (command-loop ownership invariant).

> Gate: `go test -count=1 -race ./internal/app/` — the view-forwarder tests will need updating (next task).

### Task 2.4 — Update existing forwarder/tab tests for the new routing
- **Files:** `internal/app/orchestrator_view_forwarder_test.go`, `internal/app/orchestrator_tabs_filmstrip_test.go`.
- These currently assert the "All"/agent transcript tabs and `[role]` log lines. Adjust them to the new model:
  - The Stats tab still exists and still shows the CH column / rows / persistence → keep those assertions.
  - Remove/replace assertions about transcript log lines and the "All"/agent tabs (those tabs no longer exist).
- Add positive assertions that conversation events now appear in the **chat** viewport (not the panel).

> Gate: `go test -count=1 -race ./internal/app/`

---

## PHASE 3 — Chat viewport: multi-agent concurrent streaming

### Task 3.1 — Agent-scoped stream registry on the App
- **File:** `internal/app/subsystems.go` (add fields) + a new file `internal/app/agent_streams.go`.
- **New type** in `agent_streams.go`:
  ```go
  type agentStreamState struct {
      label       string                 // role (disambiguated, e.g. "coder", "coder·2")
      thinking    strings.Builder        // accumulated thinking text
      content     strings.Builder        // accumulated assistant content
      thinkView   *tui.ThinkingBlock      // current in-place thinking widget
      contentView tui.Component          // current in-place content widget (*agentMessage)
      tools       map[string]*tui.ToolExecutionComponent // callID → widget
  }
  type agentStreamRegistry struct {
      mu      sync.Mutex
      streams map[string]*agentStreamState // key = agentID
  }
  ```
- Expose on `App` via `a.subs` (add field `agentStreams *agentStreamRegistry`; initialize in subsystem assembly). Nil-safe everywhere (main-agent path must not break when registry is nil).
- **Note:** reuse the EXACT disambiguation rule from `MultiAgentView.disambiguateLabel` (`coder`, then `coder·2`). Factor that rule into a shared helper to avoid drift, OR replicate it with a unit test pinning the behavior.

### Task 3.2 — Agent-scoped chat widget factories + updaters
- **File:** `tui/chat_viewport.go` (add methods) — keep them thin and reuse existing components.
- **Add methods** (all append a new `MessageEntry` on first call per stream, then `UpdateLast`/`setViewText` in place):
  - `AddAgentThinkingBlock(label, text string, expanded bool) *thinkingBlock` — like `AddThinkingBlock` but the header shows `<label> thinking...`. Add a `label` field to `thinkingBlock` (`chat_viewport_components.go`) and render it in `buildHeader`. Default color by `hashColor(label)`.
  - `UpdateAgentThinking(label, text string)` — update the LAST thinking block whose label matches. Add a `LastAgentThinking(label)` helper using `Conversation.LastWhere` on a new `Data.Meta["agent"]` key.
  - `AddAgentContent(label, text string) Component` — append an `agentMessage` (already role-prefixed).
  - `UpdateAgentContent(label, text string)` — update the last `agentMessage` with matching meta agent.
  - `AddAgentToolExecution(label, name, argsJSON string) *ToolExecutionComponent` — wrap `AddToolExecution` but stamp `Meta["agent"]=label`; render the label as a small prefix line above the tool box (add an optional `agentLabel` to `ToolExecutionComponent` header, or prepend a one-line `agentMessage`-style label — pick the simpler one and document it).
- **Meta key:** use `Data.Meta["agent"] = label` consistently so `LastWhere` lookups work.
- **Validation:** Task 0.4 passes.

### Task 3.3 — App methods that the forwarder calls
- **File:** `internal/app/agent_streams.go`.
- Implement (all run inside `a.apply` by the caller; these bodies must be quick and side-effect the chat only):
  - `func (a *App) beginAgentStream(role, agentID string)` — create/lookup `agentStreamState` for agentID; set label via disambiguation rule.
  - `func (a *App) handleAgentThinking(agentID, text string)` — append chunk to state.thinking; if `thinkView==nil` create via `chat.AddAgentThinkingBlock`; else `chat.UpdateAgentThinking`. Also call `a.subs.statusMsg.Show(label+" thinking...")`.
  - `func (a *App) handleAgentContent(agentID, text string)` — same pattern for content; status `label+" answering..."`.
  - `func (a *App) handleAgentToolCall(agentID, name, input, callID string)` — finalize any active content/thinking stream for this agent (end-of-segment), then `tc := chat.AddAgentToolExecution(label,name,input)`; store in `state.tools[callID]`; `tc.SetStatus(ToolRunning)`; status `label+" tool calling"`.
  - `func (a *App) handleAgentToolResult(agentID, callID, text string, ok bool)` — look up `state.tools[callID]`; if present set output+status (ok→`ToolSuccess` else `ToolError`)+`SetPartial(false)`; else fall back to `chat.AddToolResult`. Mirror `applyToolResultToWidget`/`toolStatusFromResult` in `stats.go`.
  - `func (a *App) endAgentStream(agentID string)` — finalize any open thinking/content segment for the agent (flush state); leave widgets in place. Do NOT remove them.
- **End-of-segment rule (critical for B2/B5):** when a thinking chunk arrives after a content segment started, or a content chunk arrives after thinking, or a tool call arrives, **close** the previous segment for that agent so the next kind opens a NEW block at the bottom. Reuse the logic shape of `endStreamIfDifferent` in `stats.go` but scoped per-agent inside `agentStreamState`.

### Task 3.4 — Stop suppressing chat by default; tie suppression to the Stats tab
- **File:** `internal/app/orchestrator_view_forwarder.go` (`attachOrchView`).
- **Change:** remove the unconditional `a.subs.chat.SetSuppressed(true)`. Instead:
  - Default active tab = a new **Conversation** pseudo-tab (chat visible, `agentContent` returns nil, chat not suppressed).
  - `Ctrl+x` (handled in `internal/app/orch_tabs.go`) toggles to the **Stats** tab: when Stats is active, `chat.SetSuppressed(true)` + `agentContent` renders the table; when Conversation is active, `chat.SetSuppressed(false)` + `agentContent` returns nil.
- **File:** `tui/orchestrator/view.go` — reduce tabs to `[{Stats},{Conversation}]` order is your choice; **remove the `TabAll` and per-agent `TabAgent` transcript tabs**. Update `ActiveTab`/`ensureBookendTabs` accordingly. Remove now-dead transcript code (`AgentLog`, `appendLine`, `handleAgentMessage` log appends, `renderAll`, `renderAgent`, `OrderedLogs`, `LogFor`) **only after** no caller references them (grep first). Keep `Rows`/stats rendering.
- **File:** `tui/orchestrator/content.go` — `renderStats` stays; remove `renderAll`/`renderAgent`/`styleLogLine`/`roleLabel` if unused after the tab removal. `Render` switches only on Stats vs (Conversation→return nil).
- **Validation:** Tasks 0.1 and 0.2 pass; the chat is visible and shows proper widgets.

### Task 3.5 — Input prompt + steering still target the active agent
- **File:** `internal/app/orch_tabs.go`, `internal/app/submithandler.go` (the `subs.agentView.ActiveAgentID()` usage).
- Ensure `ActiveAgentID()` still returns a steerable agent when on Conversation tab (steering targets the most-recently-active agent) or require switching to Stats to steer — **pick: steering on Conversation tab targets the last-started agent**; document in a comment. Update `updateOrchInputPrompt` to show the active agent label on both tabs.

> Gate: `go test -count=1 -race ./internal/app/ ./tui/ ./core/orchestrator/`

---

## PHASE 4 — Bug A: make the orchestrator visibly analyze → delegate

### Task 4.1 — Strengthen the hub orchestrator prompt
- **File:** `prompts/orchestrate/hub_orchestrator.md`.
- **Rewrite** so the orchestrator MUST (a) briefly analyze/decompose the objective, (b) call `delegate` for each specialist with a concrete task, (c) after specialists report, write a concise summary. Keep it short and imperative. The thinking it produces will now be VISIBLE (Phase 3), so the user sees the analysis.
- Keep the `{{.Objective}}` template variable (consumed by `runtime.renderPrompt`).

### Task 4.2 — Verify delegation is visible end-to-end (filmstrip)
- **File:** `internal/app/orchestrator_hub_render_test.go` (NEW).
- Drive a fake event sequence that represents a real hub run: orchestrator `agent_started` → orchestrator thinking → orchestrator **tool_call** `delegate{role:coder}` → coder `agent_started` (delegated) → coder thinking → coder content → coder `agent_finished` → orchestrator content (summary) → `run_finished`.
- **Assertions:**
  - An orchestrator `delegate` tool widget is present.
  - A distinct `coder` content block appears AFTER the delegate call (proving the orchestrator delegated, then the coder produced visible work).
  - The orchestrator summary content block appears last.
- This is the direct regression guard for Bug A's "does nothing" symptom.

> Gate: `go test -count=1 -race ./internal/app/ ./core/orchestrator/`

---

## PHASE 5 — Cleanup, docs, regression sweep

### Task 5.1 — Remove dead code
- Grep for callers of removed `MultiAgentView` transcript APIs (`LogFor`, `OrderedLogs`, `AgentLog`, `Lines`, `appendLine`, `handleAgentMessage`, `renderAll`, `renderAgent`, `styleLogLine`, `roleLabel`, `TabAll`, `TabAgent`). Delete the unused ones. If `handleAgentMessage` is still referenced by `EvAgentMessage`, repoint it to a no-op or remove `EvAgentMessage` handling from the view (conversation now lives in chat).
- Run `go vet ./...` to catch unused code/imports.

### Task 5.2 — Update docs
- **File:** `docs/ORCHESTRATOR.md` — describe the new Conversation/Stats tabs, agent-labeled streaming blocks, and that `delegate` calls render as tool widgets.
- **File:** `docs/TUI.md` — add a note that orchestrator conversation now renders in the main chat viewport via the agent-stream registry (cross-reference the Filmstrip testing section).

### Task 5.3 — Regression: main-agent path unaffected
- **File:** `internal/app/stats_status_test.go` (or a new sibling). Re-run an existing main-agent filmstrip scenario and assert the single-stream behavior is unchanged (spinner lifecycle, one thinking block, one content block, tool widget). Confirms the new registry did not regress the main agent.

---

## 4. Validation gate (run EACH command separately; do not chain)

Run these after every phase and again at the end. Fix any NEW violation this change introduces (pre-existing unrelated warnings: note explicitly).

```
go vet ./...
```
```
staticcheck ./...
```
```
gocognit -over 15 .
```
```
gocyclo -over 12 .
```
```
go test -count=1 -race -cover ./...
```

Complexity budgets (from `AGENTS.md`): config 20/12, TUI render 18/12, all other logic 15/12. If a new function exceeds budget, extract a helper (the per-agent methods in Phase 3 are good extraction candidates).

---

## 5. Interactive verification (guideline #5 of bugs.md)

After all tests pass, build and run goa with a local model (LM Studio on :1234, matching `TestOrchestratorAdapter_LiveHub`'s gate) and run:

```
/orchestrate:new:topology=hub,objective=Create a single HTML file that runs a blue/orange/white fire-burning simulation
```

Visually confirm in a real terminal:
1. The orchestrator's **thinking block** streams (labeled `orchestrator`).
2. A **`delegate` tool widget** appears for the coder.
3. The coder's **thinking + content** stream in distinct labeled blocks, accumulating in place (NOT `[coder] <chunk>` per line).
4. `Ctrl+x` toggles to the **Stats** table and back to the **Conversation**.
5. The orchestrator's final summary appears last.

If the live model does not call `delegate`, that is a prompt/model issue — iterate Task 4.1, not the wiring (the filmstrip test 4.2 already proves the wiring).

---

## 6. Done criteria (all must be true)

- [ ] Phase 0 tests (0.1–0.4) pass.
- [ ] Orchestrator conversation renders in the chat viewport as thinking/content/tool widgets, agent-labeled, in-place updating (B1, B2, B3, B4 fixed).
- [ ] Two agents streaming concurrently produce two distinct in-place blocks that do not jump position (B5 fixed).
- [ ] `Ctrl+x` toggles Conversation ↔ Stats.
- [ ] Hub run visibly shows analyze → delegate → coder work → summary (Bug A fixed).
- [ ] `go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12`, `go test -count=1 -race -cover ./...` all green (no new violations).
- [ ] Docs updated; dead code removed; main-agent regression test green.
- [ ] Move the two bugs to `docs/archive/bugs.<date>.md` per bugs.md workflow step #8, leaving only the guidelines in `bugs.md`.

---

## 7. Risk callouts for the executing agent

- **Do NOT** remove `EventAgentMessage`/`RecordAgentMessage` — pipeline carry (`runPipeline`, `MessageFor`) and `Delegate` rely on it. Keep forwarding content both to the chat (Phase 3) and to `r.msgs` (existing).
- **Do NOT** touch `multiagent.ForegroundOrchestrator` — it powers companion/workflows/pair/review, not `/orchestrate`. Its TUI path (`internal/app/orchestrator.go` `handleOrchestratorStreamMsg` + `CompanionSectionComponent`) is a DIFFERENT renderer; leave it alone unless a test breaks.
- **Observer dedupe:** `OrchestratorAdapter.NewRuntime` attaches the observer once per (process, role) (`a.seen`). The new event forwarding happens inside that same observer, so no extra dedupe is needed — but verify with `-race`.
- **Single-owner invariant:** all `MultiAgentView` AND chat mutations happen inside `a.apply(func(){...})`. Do not mutate widgets from the observer goroutine directly.
- **Event bus backpressure:** the runtime bus drops when full (non-blocking). Thinking is high-frequency; the forwarder must handle dropped chunks gracefully (in-place accumulation means a dropped chunk just misses text — acceptable; the store is the source of truth for content, not thinking).
