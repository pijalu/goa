<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Orchestrator Tabbed-Run UI — Assessment & Micro-Task Plan

Status: **Ready for single-take implementation** (hand this file to the
implementing agent; every micro-task is independently testable & committable).

## 0. TL;DR

The orchestration UI is broken because the live agent table is a **transient
overlay** that floats over the chat viewport and competes for vertical space
with the streaming agent transcript. There is **no persistent, navigable view**
and **cache stats are tracked but never displayed**. This plan replaces the
overlay with a persistent **tabbed run view** (Stats + one tab per agent +
All), puts a thin **tab bar just above the input line**, plumbs
provider/model/thinking/in/out/**CH** (cache hits) into the stats, and routes
per-agent streamed messages into per-agent conversation tabs. It also adds
steering that adapts to the active tab and a full validation strategy
(local-LM e2e, filmstrip UI tests, produced-items checks, cache-hit checks,
multi-model checks).

---

## 1. Assessment (root-cause, current state, gaps)

### 1.1 What the user sees today and why it is broken

Observed screen (paraphrased): orchestrator + coder agent streams render in the
main chat viewport (`ChatViewport`) **and** a bordered agent table renders as a
floating overlay at the bottom. The two overlap / fight for the same rows, the
input line label is rewritten to `steer all:`, and when the run finishes the
overlay disappears entirely — there is nothing to come back to.

### 1.2 Root causes (all verified against source)

| # | Defect | Evidence |
|---|--------|----------|
| **D1** | The Summary table is a **transient overlay**, not a persistent component. | `internal/app/orchestrator_panel_forwarder.go`: `ShowOverlay(panel, OverlayOptions{Width:0, Height:0, CaptureInput:false})`. Overlays are positioned at the bottom (`overlayStartRow`) and float over base layers (`tui/tui.go` `buildScene`). It is hidden on run end (`defer handle.Hide()`). |
| **D2** | **No navigation** between orchestration view and per-agent conversations. | The `Panel` is display-only (`HandleInput` is a no-op). `orchestrate-v2.md §10` calls for a "Summary tab" but it was implemented as an overlay. There is no per-agent message buffer on the UI side. |
| **D3** | **Cache stats are invisible.** `AgentStats` tracks `CacheRead`/`CacheCreation` and the runtime emits them (`runtime.go` `statsPayload`: `cache_read`, `cache_creation`), but `Panel.Row` has no cache fields and the table prints only `in`/`out`. | `tui/orchestrator/panel.go` `Row` struct + `tableLines`. `Runtime.Snapshot()` (`runtime.go`) also drops cache fields. |
| **D4** | The orchestrator's `EventAgentMessage` (per-agent streamed text) is **not routed into any per-agent UI**. It is consumed only by the panel forwarder (which ignores `AgentMessage`) and stored in `Runtime.msgs` for pipeline carry / delegate results. The chat viewport only receives messages from the *foreground* orchestrator (`handleInterAgentEvent`), not from `core/orchestrator`. | `internal/app/events.go:599` (`AddAgentMessage` from `event.InterAgent`); `orchestrator_panel_forwarder.go` selects on `rt.Subscribe()` but only calls `panel.ApplyEvent`. |
| **D5** | Steering is hardcoded to `id=all` whenever the panel overlay is visible; it does not honor a selected agent. | `internal/app/submithandler.go:120` `maybeSteerOrchestrator` always runs `steer id=all`. |
| **D6** | Per-role **thinking level** is not surfaced. `OrchestratorRole` (`config/config.go:260`) has `Model/Provider/AllowedTools` but no thinking field; the stats have nowhere to read it from. (Thinking is derived from the agent's stream options, not the role config.) |
| **D7** | The `Browser` (`tui/orchestrator/browser.go`) exists and lists persisted runs, but it is a separate modal with **no command/keybinding to open it** — it is dead code from the v2 plan. (`grep` finds no caller of `NewBrowser` in non-test code.) |

### 1.3 What already works (reuse, do not rebuild)

- `core/orchestrator` runtime + event store + bounded pool are correct and
  well-tested. **Do not touch runtime semantics.**
- `EventAgentMessage` (AgentID/Role + `text`) and `EventAgentStats`
  (`tokens_in/out`, `cache_read`, `cache_creation`, `turns`, `status`,
  `tool_calls`) are already emitted. **The UI just needs to consume them.**
- Multi-model is already supported: the adapter passes `rcfg.Provider` to
  `multiagent.AgentPool` and `ProviderModelFactory` resolves per-provider
  models (`orchestrator_adapter.go`, `multiagent/agent_pool.go`).
- Live local-LM test harness exists: `loadLiveConfig`, `lmstudioReachable`,
  `newLiveRuntime`, `buildOrchestratorConfig`, `assertRunSnapshotFinished`
  (`internal/app/orchestrator_integration_helpers_test.go`).
- TUI is fully testable as data via `tui.AgentFrame` + `tui.Filmstrip` and the
  `uiScenario` harness (`internal/app/ui_scenario_test.go`). **All UI
  validation must use this, never a live terminal** (see tui-test skill).

### 1.4 What is out of scope for this plan (do not expand)

- Changing run IDs, retention, `/config` menu, goal integration, colon-syntax
  parser — all done in the v2 plan (`orchestrate-v2.md`). Leave as-is.
- Foreground orchestrator / companion rendering (`runOrchestratorEventForwarder`).
- Hub topology's DelegateTool behavior.

---

## 2. Target Design

### 2.1 Layout (orchestration mode active)

```
┌──────────────────────────────────────────────────────────┐
│ header                                                   │
├──────────────────────────────────────────────────────────┤
│                                                          │
│  <content for the active tab>                            │ ← AgentContent
│    • Stats tab  → enhanced agent table                   │   (replaces ChatViewport
│    • <agent> tab → that agent's streamed transcript      │    region; ChatViewport
│    • All tab     → interleaved transcript                │    returns nil here)
│                                                          │
├──────────────────────────────────────────────────────────┤
│ pending status (spinner)                                 │
│ status bar                                               │
│ goal bubble (if any)                                     │
├──────────────────────────────────────────────────────────┤
│  Stats | orchestrator | coder | reviewer | All      [1/5]│ ← AgentTabBar (NEW, 1 line)
├──────────────────────────────────────────────────────────┤
│  steer coder: ▏                                          │ ← Editor (input line)
├──────────────────────────────────────────────────────────┤
│  footer / aggregate statistic                            │
└──────────────────────────────────────────────────────────┘
```

Non-orchestration mode: `AgentContent.Render` and `AgentTabBar.Render` return
`nil`; `ChatViewport` renders normally. **Zero impact on normal chat.**

The tab bar sits **immediately above the input editor** (inserted as a child
just before `inp` in `assembleEngine`). This keeps redraw localized: switching
tabs only re-renders the content region + the 1-line tab bar; the input line
and footer are untouched. (Per the user: "the tab can be done on top of the
input line if this lowers the complexity to redraw the TUI".)

### 2.2 Component & state architecture (SOLID, actor-model clean)

**One shared state owner + two thin render-only views + a source-agnostic
event seam.**

> **Generalization note (design now, rename later).** The view is built
> against a **neutral** agent-event type, not against `core/orchestrator.Event`
> directly. Today the only source is the orchestrator runtime; tomorrow the
> *same* view serves the foreground orchestrator (companion), the pipeline
> runner, and the swarm — each source just supplies a translation adapter.
> This is Dependency Inversion + Open/Closed: adding a source never touches
> the view. The package is named `tui/orchestrator` for now; the future
> generalization (§9) is a near-pure **rename to `tui/multiagent`** plus new
> adapters — no view rewrite. To make that rename trivial, **nothing in the
> neutral seam references orchestration concepts** (no "objective/topology"
> in the event type; those are source metadata carried as a free-form map).

```
tui/orchestrator/view.go        (NEW) MultiAgentView state owner (source-agnostic; mutated ONLY on commandLoop)
tui/orchestrator/event.go       (NEW) AgentViewEvent — the neutral event type sources translate INTO
tui/orchestrator/content.go     (NEW) AgentContent — Component, renders active tab content
tui/orchestrator/tabbar.go      (NEW) AgentTabBar  — Component, renders the tab strip
tui/orchestrator/panel.go       (MOD) keep for the Stats table rendering, reused by AgentContent
internal/app/orch_view_source.go (NEW) orchestrator.Event → AgentViewEvent adapter (the ONLY orchestration-specific seam)
```

- `MultiAgentView` holds: `source string` (e.g. "orchestration"), `meta
  map[string]string` (objective/topology/etc., display-only), `tabs
  []AgentTab`, `active int`, `agentLogs map[string]*AgentLog`, `rows
  []EnhancedRow` (stats), `finished`, `failed`. All mutators run **only inside
  `a.apply(...)`** (single-owner R1).
- `AgentContent` and `AgentTabBar` hold a `*MultiAgentView` pointer and only
  implement `Render`/`HandleInput`/`Invalidate`. They own **no** mutable state.
  Single-responsibility; compositor single-owner invariant intact.
- `AgentTab` = `{Key string; Label string; Kind AgentTabKind}` where
  `AgentTabKind ∈ {TabStats, TabAgent, TabAll}`. Agent tabs keyed by AgentID.
- `AgentViewEvent` = `{Kind AgentEventKind; AgentID, Role, Provider, Model,
  Thinking, Status, Text string; Stats *AgentStatsDelta; Meta map[string]string}`
  with `AgentEventKind ∈ {EvSourceStarted, EvSourceFinished, EvAgentStarted,
  EvAgentMessage, EvAgentStats, EvAgentSteered, EvAgentFinished}`. **Zero
  imports from `core/orchestrator`.**

### 2.3 Data flow (events → state → render)

The existing `runOrchestratorPanelForwarder` becomes
`runOrchestratorViewForwarder`. It still subscribes via `rt.Subscribe()` and
drains on the command loop, but now (a) translates each `orchestrator.Event`
into a neutral `AgentViewEvent` via the adapter, then (b) feeds `MultiAgentView`:

| `orchestrator.Event` | adapter → `AgentViewEvent` | `MultiAgentView` mutation |
|-------|-------|-------------------|
| `EventRunStarted` | `EvSourceStarted` (+ Meta: objective/topology) | set meta; ensure `Stats` + `All` tabs; agent tabs appear as agents start. |
| `EventAgentStarted` | `EvAgentStarted` (Provider/Model from payload) | upsert agent tab (keyed by AgentID, label=Role); upsert `EnhancedRow`; init `agentLogs[AgentID]`. |
| `EventAgentMessage` | `EvAgentMessage` (Text) | append content line to `agentLogs[AgentID]`. |
| `EventAgentStats` | `EvAgentStats` (Stats delta incl. cache_read) | update `EnhancedRow`: Turns/In/Out/**CacheRead**/ToolCalls/Thinking. |
| `EventAgentSteered` | `EvAgentSteered` | append `[steer]` marker to the agent log (transparency). |
| `EventAgentFinished` | `EvAgentFinished` | set row status; append `[finished]` marker. |
| `EventRunFinished` | `EvSourceFinished` (ok) | set finished/failed; **persist** the view (no auto-hide). |

**Provider/Thinking source of truth**: the adapter already knows
`oCfg.Roles[role].Provider`. The factory sets `Provider`/`Thinking` on the
handle; `Runtime.driveOne`/`Delegate` include `h.Provider`/`h.Thinking` in the
`EventAgentStarted` payload, and the **app adapter** copies them into the
neutral `EvAgentStarted`. (See T1/T3.) Keeping the neutral event the only
shape the view knows is what makes §9 a rename, not a rewrite.

### 2.4 Navigation & steering

- **Cycle tabs**: `Alt+]` / `Alt+[` (next/prev) — wire via `TUI` app-shortcut
  callbacks (same mechanism as `OnChangeModel`). Also number keys `1..9` map to
  tab index **when the AgentTabBar is focused** (it is never focused for input;
  instead the engine routes bare digits to it only in orchestration mode — see
  T6 for the routing decision).
- **Slash command**: `/orchestrate:tab:<key|index>` (only valid in
  orchestration mode; otherwise a flash error).
- **Input label**: when `MultiAgentView` active, the input editor prompt becomes
  `steer <role>:` for an agent tab, `steer all:` for Stats/All tabs.
  `maybeSteerOrchestrator` is changed to target the **active tab's agent**
  (fall back to `all`). Explicit `/orchestrate:steer:...` still takes precedence.

### 2.5 Stats columns (the "Tab A" requirement)

Per-agent row, exact columns requested by the user
`(provider) model - thinking / token in / token out / CH`:

```
role        (provider) model              think   in     out    CH
orchestrator (lmstudio) qwen2.5-coder     med    1234   456   1024
coder        (google)   gemma-3-e4b       off     820   210      0
```

- `CH` = cache-hit tokens (`CacheRead`). Render dim/`-` when 0.
- `think` = effective thinking level (`off/min/low/med/high/xhigh`) or `-`.
- Aggregate run total is rendered as a single footer line under the table:
  `Σ in=NNNN out=NNNN CH=NNNN · turns=N`.

---

## 3. Micro-Task Breakdown

**Rules for the implementing agent:**
- Do the tasks **in order**. Each task is independently testable and a natural
  commit boundary.
- After **every** task run the gate commands **separately** (do not chain):
  `go vet ./...` · `go test -count=1 -race -cover ./...` (relevant packages)
  · `gocognit -over 15 .` · `gocyclo -over 12 .`. Fix any new violation.
- Complexity budgets (AGENTS.md): config 20/12, TUI render 18/12, other 15/12.
- All user-facing strings for prompts go in `prompts/orchestrate/*.md` (none
  needed here — these are UI labels, not LLM prompts; UI labels are allowed in
  Go per existing `panel.go`).
- Prefer the `tui-test` skill for **all** UI behavior. Never spin a live model
  to "see" the UI.

---

### T1 — Plumb cache + provider through the stats data model

**Goal:** make cache-hit and per-role provider data available to the UI layer.

**Scope:**
- `core/orchestrator/handle.go`: `AgentStatsSnapshot` already has
  `CacheRead`/`CacheCreation`. No change there.
- `core/orchestrator/runtime.go`:
  - Add fields to `AgentRow`: `Provider string`, `Thinking string`,
    `CacheRead int`, `CacheCreation int`.
  - In `Snapshot()`, populate `CacheRead`/`CacheCreation` from
    `h.Stats.Snapshot()`.
- `core/orchestrator/runtime.go` `statsPayload`: already includes cache fields
  — **verify** only.
- `internal/app/orchestrator_adapter.go`:
  - In the role factory, after creating `h`, record the provider on the handle.
    Add `AgentHandle.Provider string` (and a setter or constructor arg) and set
    it from `rcfg.Provider` (fall back to the active provider id).
  - Add `Provider` to the `EventAgentStarted` payload emitted from the factory
    is **not** possible (emit is in core). Instead: have the adapter emit a
    thin "agent meta" via the existing observer, OR simpler — set provider on
    the handle and have `Runtime.driveOne`/`Delegate` include
    `h.Provider` in the `EventAgentStarted` payload. Add `Provider` to the
    `Event` emission in `driveOne` and `Delegate`.
  - Thinking: at `EventAgentStats` emission, include `thinking` = the agent's
    effective level. Expose it from the handle (the adapter knows the agent's
    `agentic.Config`); store `h.Thinking` set in the factory from
    `agent.ThinkingLevel()` (add accessor if missing) or from stream options.
- `core/orchestrator/pool.go`: `BoundedAgentPool.Acquire` already returns a
  handle with Role/Model; ensure `Provider`/`Thinking` survive acquire/release
  (they are set once at creation on the handle, not per-acquire — verify the
  factory path sets them on the same handle instance the pool caches).

**Tests (TDD — write first, make pass):**
- `core/orchestrator/runtime_test.go`: assert `Snapshot()` rows carry
  `CacheRead`/`CacheCreation` after `AddUsage` with cache deltas.
- `internal/app/orchestrator_adapter_integration_test.go` (extend, live,
  skipped without LMStudio): assert an `EventAgentStarted` carries `Provider`
  in its payload.

**Done when:** `go test ./core/orchestrator/ ./internal/app/` green and the new
fields are populated end-to-end.

---

### T2 — Define the neutral event seam + `MultiAgentView` state owner

**Goal:** a source-agnostic view model (the single source of truth both views
read). Building it neutral now is what makes the §9 generalization a rename.

**Files:**
- `tui/orchestrator/event.go` (NEW) — the neutral `AgentViewEvent` type and
  `AgentEventKind` constants. **Must not import `core/orchestrator`.**
- `tui/orchestrator/view.go` (NEW) — `MultiAgentView` + `ApplyEvent(ev
  AgentViewEvent)`:

```go
// MultiAgentView is the mutable state for the persistent multi-agent run
// view. It is source-agnostic: any multi-agent source (orchestrator runtime,
// foreground orchestrator, pipeline, swarm) feeds it neutral AgentViewEvents.
// ALL mutators run on the TUI commandLoop (R1 single-owner).
type MultiAgentView struct {
    source   string                 // "orchestration" | "pipeline" | "swarm" | ...
    meta     map[string]string      // objective/topology/etc., display-only
    finished bool
    failed   bool
    tabs     []AgentTab
    active   int
    rows     []AgentEnhancedRow
    logs     map[string]*AgentLog   // keyed by AgentID
    order    []string               // agentIDs in first-seen order (stable tabs)
}

type AgentTab struct {
    Key   string
    Label string
    Kind  AgentTabKind // tabStats | tabAgent | tabAll
}

type AgentEnhancedRow struct {
    AgentID, Role, Provider, Model, Thinking, Status string
    Turns, TokensIn, TokensOut, CacheRead, ToolCalls int
}

type AgentLog struct {
    AgentID, Role string
    lines         []AgentLogLine
}
type AgentLogLine struct { Kind int; Text string } // content | thinking | marker
```

- `internal/app/orch_view_source.go` (NEW) — the ONLY orchestration-specific
  seam: `func translateOrchEvent(ev orchestrator.Event) (AgentViewEvent,
  bool)`. A thin switch; unknown events → `bool=false`. This is the file §9
  duplicates per new source.
- Implement pure, well-named methods, each ≤ complexity budget:
  - `ApplyEvent(ev AgentViewEvent)` — switch on `ev.Kind`; delegate to helpers
    (`ensureAgentTab`, `upsertRow`, `appendLog`).
  - `ActiveTab() *AgentTab`, `SelectByKey(key string) bool`, `Cycle(dir int)`,
    `TabIndex() string` (e.g. `"2/5"`).
  - `AggregateTokens() (in, out, ch, turns int)`.
- Tests (write first):
  - `tui/orchestrator/view_test.go`: table-driven over `ApplyEvent` with
    **neutral** events (no orchestrator import) — feed EvSourceStarted → 2×
    EvAgentStarted → EvAgentMessage → EvAgentStats → EvAgentFinished →
    EvSourceFinished; assert tabs=[stats,a1,a2,all], active=stats, Cycle(+1)→a1,
    SelectByKey("all") works, rows carry cache, logs have streamed text.
    **This test is the proof the view is source-agnostic.**
  - `internal/app/orch_view_source_test.go`: assert `translateOrchEvent`
    maps each orchestrator event kind correctly (incl. cache_read → Stats.CacheRead,
    provider/thinking passthrough) and returns false for unknown kinds.

**Done when:** both test files green; `go vet` clean; the view compiles with
**zero** dependency on `core/orchestrator`.

---

### T3 — Build `AgentContent` (active-tab content renderer)

**Goal:** render Stats table / agent transcript / All transcript.

**Files:**
- `tui/orchestrator/content.go` (NEW): `type AgentContent struct{ view
  *MultiAgentView }`.
- `AgentContent.Render(width)`:
  - `tabStats`: reuse the table builder from `panel.go` (extract
    `tableLines` into `RenderStatsTable(rows, width) []string` enhanced with
    Provider/Thinking/CH columns). Show the source header + meta + aggregate
    footer line.
  - `tabAgent`: render `view.logs[activeKey]` lines (content + faint thinking
    + markers). Plain wrapped text is acceptable for v1.
  - `tabAll`: interleave all agent logs in first-seen order, each line prefixed
    with a faint `[role]`.
  - Return `nil` when `view == nil` or not active.
- `HandleInput`: no-op (display only). `Invalidate`: no-op.

**Tests:**
- `tui/orchestrator/content_test.go`: build a `MultiAgentView` via `ApplyEvent`
  with **neutral** events, render each tab kind at width 80, assert
  (ANSI-stripped) substrings: stats tab contains `CH` and a role; agent tab
  contains the streamed text; all tab contains both roles.

**Done when:** content renders all three tab kinds; tests green.

---

### T4 — Build `AgentTabBar` (the navigation strip)

**Goal:** the 1-line tab strip above the input.

**Files:**
- `tui/orchestrator/tabbar.go` (NEW): `type AgentTabBar struct{ view
  *MultiAgentView }`.
- `Render(width)`:
  - Join tab labels with ` │ ` (faint). Active tab: bold + its color.
  - Right-pad with spaces; right-justify `[active/total]`.
  - Prefix the strip with the `source` label (e.g. `orchestration:`) so the
    same component reads correctly when reused for pipeline/swarm (§9).
  - If `view == nil`/inactive → return `nil`.
- `HandleInput`: **no-op** (navigation is handled at the app layer via
  shortcuts, T6).

**Tests:**
- `tui/orchestrator/tabbar_test.go`: render with 3 tabs, active=1 → assert
  the 2nd label is bold and `[2/3]` appears; inactive view → `nil`.

**Done when:** tab bar renders correctly and disappears outside orchestration.

---

### T5 — Wire `AgentContent` + `AgentTabBar` into the component tree; replace the overlay forwarder

**Goal:** persistent view instead of transient overlay.

**Scope:**
- `internal/app/subsystems.go`: add `agentView *orchpanel.MultiAgentView`,
  `agentContent *orchpanel.AgentContent`, `agentTabBar *orchpanel.AgentTabBar`.
  Remove (or keep-but-unused) `orchPanel`/`orchPanelHandle` only after T8
  removes all readers; for now keep fields to avoid a flag day.
- `internal/app/tui.go` `assembleEngine`: insert `agentContent` right after
  `chat`, and `agentTabBar` right before `inp`. Construct them in
  `createTUIComponents` (both initially point at a nil/inactive view).
- `internal/app/orchestrator_panel_forwarder.go` → rename to
  `internal/app/orchestrator_view_forwarder.go` (or keep filename, rewrite
  body). New logic:
  - On active-run notify: create `MultiAgentView{source:"orchestration"}`,
    set it on `agentContent`/`agentTabBar` (via `a.apply`), subscribe
    `rt.Subscribe()`.
  - Loop: on each `orchestrator.Event` → `ne, ok := translateOrchEvent(ev)`;
    if ok → `a.apply(func(){ agentView.ApplyEvent(ne) })`. **The forwarder,
    not the view, knows about `orchestrator.Event`.**
  - On `rt.Done()`: final flush; **do not hide** the view (persistent). It
    stays until the user navigates away or a new run resets it.
- `ChatViewport` must yield its space during orchestration: add a
  `chat.SetSuppressed(bool)` so `chat.Render` returns `nil` while
  `agentContent` is active. This keeps the interleaved "All" transcript
  owned by `MultiAgentView`, avoiding double-render.

**Tests (filmstrip, tui-test skill):**
- `internal/app/orchestrator_view_forwarder_test.go`: drive a synthetic
  `orchestrator.Event` sequence through the forwarder (use a fake runtime
  exposing `Subscribe()`/`Done()` — extract a tiny interface), then
  `engine.AgentFrame()` and assert: a layer named `AgentTabBar` exists with
  `Stats | … | All`, and `AgentContent` shows the table. Assert `ChatViewport`
  layer is absent (suppressed). This is the **canonical UI validation test**
  and also proves the translate seam end-to-end.

**Done when:** a driven event sequence renders the tabbed view with no overlay;
normal chat still works when no run is active.

---

### T6 — Navigation: hotkeys + `/orchestrate:tab` + active-tab steering

**Goal:** let the user switch tabs and steer the selected agent.

**Scope:**
- `tui/tui.go`: add callbacks `OnAgentTabNext`, `OnAgentTabPrev` (mirror
  `OnChangeModel`); resolve `alt+]`/`alt+[` (and `ctrl+shift+tab` variants) in
  `resolveAppShortcut`. Named generically (not `OnOrch*`) so §9 reuses them.
- `internal/app/shortcuts.go` (or `tui.go` wiring): bind the callbacks to
  `a.cycleAgentTab(+1/-1)` → `a.apply(func(){ agentView.Cycle(±1); update input
  prompt })`.
- `core/commands/orchestrate.go`: add `tab` subcommand:
  `/orchestrate:tab:<key|index>` — only when an active run exists; calls into
  the app via a `core.Context` callback `SelectAgentTab func(key string) bool`.
  On success flash `tab: <label>`. (Generic name so other sources can add
  `/pipeline:tab` etc. in §9.)
- `internal/app/submithandler.go` `maybeSteerOrchestrator`: replace the
  hardcoded `id=all` with: if active tab is an agent tab →
  `id=<agentID>`; else `id=all`. Update the input prompt accordingly
  (`steer <role>:` vs `steer all:`) whenever the active tab changes.

**Tests:**
- `internal/app/shortcuts_test.go` (extend): send `alt+]` via the engine,
  assert `agentView.active` incremented and the input prompt label changed.
- `core/commands/orchestrate_command_test.go` (extend): `/orchestrate:tab:all`
  selects the All tab; `/orchestrate:tab:stats` works; unknown key → flash
  error; outside orchestration → flash "no active run".
- `internal/app/submithandler_test.go` (extend): with an agent tab active,
  typing + Enter issues `steer id=<agentID>` (assert via a stubbed command).

**Done when:** user can cycle/select tabs and the input steers the right agent.

---

### T7 — Surface the persisted-run Browser (close dead-code gap D7)

**Goal:** make `tui/orchestrator/browser.go` reachable.

**Scope:**
- Add `/orchestrate:browser` (or reuse `/orchestrate:list`) that opens the
  `Browser` as a capturing overlay (`ShowOverlay(b, {CaptureInput:true})`).
  Selecting a finished run flashes its summary; selecting an unfinished run
  offers resume.
- Help text: update `core/commands/help/orchestrate.long.md` with the `tab` and
  `browser` subcommands (and the new Stats columns).

**Tests:**
- `core/commands/orchestrate_command_test.go`: `/orchestrate:browser` opens
  overlay (assert via a fake app surface or by checking the command calls the
  overlay opener callback).
- `tui/orchestrator/browser_test.go` already covers render/nav — keep.

**Done when:** browser is reachable and documented.

---

### T8 — Cleanup: remove the old overlay path

**Goal:** delete dead code now that the persistent view replaces it.

**Scope:**
- Delete `orchPanel`/`orchPanelHandle` fields and all readers
  (`submithandler.go:120` already rewritten in T6).
- Delete `snapshotRows` (replaced by `MultiAgentView.ApplyEvent`).
- Remove the `Panel.MarkFinished`/dwell-hide behavior (the view is now
  persistent).
- Keep `tui/orchestrator/panel.go` **only** as the stats-table renderer
  (rename to `stats_table.go` if it clarifies), used by `AgentContent`.

**Tests:** existing `panel_test.go` becomes `stats_table_test.go`; keep
coverage. Full `go test -race ./...` green.

**Done when:** no references to the old overlay; build + tests green.

---

## 4. Validation Strategy (mandatory, per user request)

Every item below must have a **green test** before the plan is declared done.
Live (local-LM) tests **skip automatically** when LMStudio is unreachable
(follow the `lmstudioReachable` pattern), so CI stays hermetic.

### 4.1 E2E with local LM provider (fanout + hub)
- Extend `internal/app/orchestrator_adapter_integration_test.go`:
  - `TestOrchestrator_LiveFanout_RendersTabbedView`: run a 2-role fanout against
    LMStudio, subscribe to events, translate via `translateOrchEvent`, feed to
    a `MultiAgentView`, assert the view shows 2 agent tabs + stats + all, and
    finishes.
  - `TestOrchestrator_LiveHub_DelegateProducesAgentTabs`: hub topology; the
    orchestrator delegates to `coder`; assert a `coder` agent tab appears and
    its log contains the delegated answer text.

### 4.2 UI validation (filmstrip / tui-test — never a live terminal)
- `internal/app/orchestrator_view_forwarder_test.go` (T5): the canonical
  layer/DOM assertion.
- Add `internal/app/orchestrator_tabs_filmstrip_test.go`: drive the **full**
  event sequence (start → stream → stats → steer → finish) and assert the
  `Filmstrip` shows: tab bar present in every frame after start, content
  changes with the active tab, and the view **persists** after finish (last
  frame still has the AgentTabBar layer). This is the regression guard for "UI
  not correctly drawn" and "no navigation".

### 4.3 Produced-items validation
- The "items" an orchestration produces are the persisted run log + the
  agents' streamed answers. Assert via `assertRunSnapshotFinished` (already
  exists) plus a new check that `Runtime.MessageFor(role)` is non-empty for
  each managed role after a live fanout (proves content was captured).
- For hub: assert the orchestrator's final `MessageFor("orchestrator")`
  references the delegated specialist's output (delegate result was folded in).

### 4.4 Caching validation (closes D3)
- `core/orchestrator/runtime_test.go`: after two `AddUsage` calls with
  `cacheRead>0`, `Snapshot()` rows report `CacheRead` (unit, no LM).
- Live: `TestOrchestrator_LiveHub_CacheHitReported` — hub with **two
  delegations** to the same role (same system prompt) → assert the 2nd
  `EventAgentStats` has `cache_read > 0` in its payload (local llama.cpp
  reports `tokens_cached`). This proves cache is both occurring **and**
  surfaced. If the local model does not cache, the test asserts `CacheRead` is
  correctly *parsed* (≥0) and is *displayed* (the column renders `-` or a
  number), so the display path is covered regardless.

### 4.5 Multi-model validation (closes the per-role provider gap)
- Extend `loadLiveConfig` helper usage: `TestOrchestrator_LiveMultiModel` builds
  a config where `orchestrator` → provider A / model X and `coder` → provider B
  / model Y (e.g. local Qwen for orchestration, smaller Gemma for coding — both
  via LMStudio with two loaded models, or two endpoints). Assert:
  - `EventAgentStarted` payloads carry **distinct** `Provider` values per role.
  - The Stats tab renders both providers in the `(provider) model` column.
  - `max_agents_per_model` is respected (spawn >cap of one model → second
    acquire blocks/queues; assert via a unit test on `BoundedAgentPool` with a
    stub factory, no LM needed).
- If only one local model is available, the multi-model *display* path is still
  covered by the filmstrip test in 4.2 (synthetic events with two providers).

### 4.6 Correct UI update using TUI notions (actor model)
- All `MultiAgentView` mutations go through `a.apply(...)` (commandLoop = sole
  owner). Add a `-race` test that drives events from a **separate goroutine**
  (as the real forwarder does) while the render loop runs, and assert no race +
  the final frame is consistent. This validates the single-owner invariant
  under the race detector.

---

## 5. Static-Analysis Gate (run separately, every task)

- `go vet ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...`

If a function exceeds budget, split into named helpers (factories + small
primitives) — do not silence with `//nolint`.

---

## 6. Files Summary

**Create:**
- `tui/orchestrator/event.go` (neutral `AgentViewEvent` — no orchestrator import)
- `tui/orchestrator/view.go`, `view_test.go` (`MultiAgentView` state owner)
- `tui/orchestrator/content.go`, `content_test.go` (`AgentContent`)
- `tui/orchestrator/tabbar.go`, `tabbar_test.go` (`AgentTabBar`)
- `internal/app/orch_view_source.go`, `orch_view_source_test.go` (the only
  `orchestrator.Event → AgentViewEvent` translation seam)
- `internal/app/orchestrator_view_forwarder.go` (replaces panel forwarder)
- `internal/app/orchestrator_view_forwarder_test.go`
- `internal/app/orchestrator_tabs_filmstrip_test.go`
- `core/commands/help/orchestrate.long.md` edits (tab/browser subcommands)

**Modify:**
- `core/orchestrator/runtime.go` (`AgentRow` + Snapshot + event payloads)
- `core/orchestrator/handle.go` (`Provider`, `Thinking` on handle, if needed)
- `internal/app/orchestrator_adapter.go` (set Provider/Thinking on handles)
- `internal/app/subsystems.go` (agentView/Content/TabBar fields)
- `internal/app/tui.go` (assembleEngine child order)
- `internal/app/submithandler.go` (active-tab steering)
- `internal/app/shortcuts.go` + `tui/tui.go` (tab-cycle hotkeys)
- `core/commands/orchestrate.go` (`tab` + `browser` subcommands)
- `tui/orchestrator/panel.go` (extract `RenderStatsTable`, add columns)

**Delete (T8):**
- old overlay fields/forwarder body, `snapshotRows`.

---

## 7. Risk Notes

- **Layering**: `tui` must not import `internal/app`. Keeping
  `MultiAgentView` in `tui/orchestrator` avoids this. The app holds a
  `*tuiorch.MultiAgentView` and the two components share the same pointer.
  The **neutral `AgentViewEvent`** (not `orchestrator.Event`) is the only
  type crossing this boundary, so the view stays reusable for other sources.
- **ChatViewport suppression**: introducing `SetSuppressed` is the only touch
  to the chat component; keep it a one-liner read in `Render`. Verify the
  filmstrip still shows normal chat when suppressed=false.
- **Stable agent tabs**: agent IDs are generated per run; tabs must be keyed by
  AgentID but **labeled** by Role (stable, human-readable). Use `order` slice
  for deterministic tab order.
- **Race**: the forwarder runs in its own goroutine; every `MultiAgentView`
  write MUST be inside `a.apply`. The 4.6 race test enforces this.
- **Don't widen the seam by accident**: if a feature truly needs an
  orchestration-specific concept (e.g. topology-aware rendering), put it in
  `meta map[string]string` or a source-tagged renderer branch — do **not**
  leak `orchestrator.*` types into `tui/orchestrator/view.go`.

---

## 8. Definition of Done

- Tasks T1–T8 complete; every test in §4 green (live tests skip without LMStudio).
- The 5 gates green separately.
- A live smoke check (developer machine, LMStudio) shows: persistent tabbed
  view, Stats with provider/model/thinking/in/out/CH, per-agent tabs with
  streamed text, tab cycling + `/orchestrate:tab`, and steering the selected
  agent. Recorded as a filmstrip snapshot committed under
  `docs/archive/orch-tabs-smoke.<date>.md` (ANSI-free text only).

---

## 9. Future Generalization — `MultiAgentView` for all multi-agent sources

**Not in scope for this plan**, but the design above is deliberately shaped
so this is a near-pure extension, **not a rewrite**. Today only
`core/orchestrator` feeds the view; the codebase has several other
multi-agent sources that currently render ad-hoc:

| Source | Today's rendering | Generalized rendering |
|--------|-------------------|-----------------------|
| `core/orchestrator` runtime | this plan (`translateOrchEvent`) | unchanged |
| `multiagent.ForegroundOrchestrator` (companion) | `runOrchestratorEventForwarder` → `CompanionSectionComponent` (a 2nd, parallel UI) | add `translateForegroundOrchEvent` → same `MultiAgentView` |
| `multiagent.PipelineRunner` | `runPipelineEventForwarder` → `AddSystemMessage` lines | add `translatePipelineEvent` |
| `multiagent` task/workflow agents | `agent_tool`/`task_orchestrator` | add `translateWorkflowEvent` |
| `tools/swarm` | `tui/swarm/renderer.go` | add `translateSwarmEvent` |

### Migration path (when prioritized)

1. **Rename** `tui/orchestrator` → `tui/multiagent` (mechanical; the neutral
   types already carry no orchestration concepts — `source string` +
   `meta map[string]string` carry source identity).
2. **One adapter per source**: each is a single `translate<Source>Event(ev)
   (AgentViewEvent, bool)` function + a forwarder goroutine that feeds the
   shared `MultiAgentView`. This is exactly the `orch_view_source.go` shape.
3. **One shared component pair**: all sources reuse `AgentContent` +
   `AgentTabBar`; the `source` label on the tab bar distinguishes them. If two
   sources run concurrently in future, the app can host one view per source
   and the tab-bar `source:` prefix disambiguates.
4. **Hotkeys are already generic**: `OnAgentTabNext/Prev` (T6) and
   `SelectAgentTab` are source-agnostic, so `/pipeline:tab`, `/swarm:tab` …
   are drop-in.
5. **Companion migration is the highest-value follow-up**: today the
   companion (`▾ companion · cycle N` / `▾ thinking...` block seen in the bug
   report) renders through a *separate* `CompanionSectionComponent`. Folding
   it into the same `MultiAgentView` would unify the two competing UI regions
   that currently overlap in the bug screenshot — directly addressing the
   "UI not correctly drawn" symptom at its root for the companion path too.

### Why this plan already pays for half of it

- The neutral `AgentViewEvent` seam (T2) means the view never learns a new
  source's types.
- `MultiAgentView.ApplyEvent` is already source-agnostic — its unit test
  (T2) uses **no** orchestrator events.
- The Stats columns (provider/model/thinking/in/out/CH) and per-agent logs are
  generic to *any* agent execution, not orchestration-specific.

So the generalization cost when prioritized ≈ "write N small adapters +
rename one package", not "redesign the UI".
