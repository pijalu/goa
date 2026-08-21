<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Multi-Agent TUI — Implementation Plan (Tabs over Isolated Per-Agent Transcripts)

Status: **Ready for implementation** (hand this file to the implementing agent;
every micro-task is independently testable & committable).

Source design: `docs/research/multi-agent-tui.md` (read it first — this plan
operationalizes it; it does not restate the rationale).
Companion specs consumed here:
- `specs/async-delegation.md` — delegation lifecycle (PENDING→RUNNING→terminal),
  delegation ids, result envelope. The TUI consumes this event feed.
- `specs/orchestrator-tabs-plan.md` — **already-implemented** prior art: the
  neutral `AgentViewEvent` seam, the `AgentTabBar` above the input line, and the
  `uiScenario`/`tui.Filmstrip` test harness.

> **Branch:** ALL work for this plan lands on **`feature/multi-agent`** (created
> off `main`). Do not merge to `main` until the full T0–T5 set is green.

---

## 0. TL;DR

Today every agent stream (main + companion + each delegated coder/planner) is
interleaved into ONE shared `ChatViewport`, which is both unfollowable and the
amplifier for the scrollback-corruption bug. The fix is **Option A** from the
research doc: give each agent its **own transcript + own compositor state**,
expose them as **tabs keyed by delegation id**, and on tab switch **replay**
that agent's history into the real terminal scrollback via a dedicated
**ReplayRunner goroutine** — all in **normal screen mode (no alternate
screen)**.

This plan breaks that into micro-tasks T0–T5. Each task is a natural commit
boundary with its own definition-of-done and tests. The hard part (per-agent
compositor state + ReplayRunner scrollback replay, §4.3.1) is isolated in **T3
behind a feature flag** so T0–T2 and T4–T5 can land safely without it.

---

## 1. Hard constraints (preserve in EVERY task)

These are verified against `tui/compositor*.go` and are what make this
non-trivial. Every micro-task must respect them; the plan calls out the ones
each task touches.

- **C1 — Normal screen, real scrollback.** Goa does NOT use the terminal
  alternate screen (no `?1049h`; verified — zero matches). Transcript rows are
  committed to genuine terminal scrollback and are **never repainted**. The
  design keeps native scrollback (users keep mouse/scrollbar history).
- **C2 — One compositor, one watermark.** `Compositor` holds a single
  `scrollTop` watermark, single `vt`, single `prevLines` baseline
  (`tui/compositor.go`). The invariant "rows `[0, scrollTop)` are frozen" is
  global, not per-view. Per-agent state is saved/restored around this single
  attached compositor.
- **C3 — A single canvas.** `Scene.compose` produces ONE `[]string` canvas
  (`tui/compositor_scene.go`). You cannot selectively re-render agent B's rows
  without re-rendering the frame.
- **C4 — History replay must be explicit.** The only way to "show agent B's
  history" is to re-emit it: replay B's committed rows back into the terminal
  (they scroll into scrollback again), then draw B's visible tail.

Plus the cross-cutting invariants:

- **R1 — Single-owner.** ALL state mutations happen on the command loop via
  `a.apply(...)` (`internal/app/events.go:73`). Background goroutines (the
  ReplayRunner, event forwarders) never mutate live compositor/view state
  directly; they hand results back over a channel applied under `a.apply`.
- **Inactive agents are pure data.** A background agent's transcript accumulates
  rows without touching the screen. Only the ACTIVE view's compositor is
  attached to the terminal.
- **ReplayRunner owns ONLY scrollback emission.** It writes the historical rows
  `[0, B.scrollTop)` and hands the final watermark back to the command loop; it
  never mutates `prevLines`/`scrollTop`/`vt` of the live compositor.

---

## 2. What already exists (reuse — do NOT rebuild)

The `orchestrator-tabs-plan` was **implemented**. Verify each item, then build
on it:

- **Neutral event seam** — `tui/orchestrator/event.go`: `AgentViewEvent` +
  `AgentEventKind` (`EvSourceStarted/Finished`, `EvAgentStarted/Message/
  Thinking/ToolCall/ToolResult/Stats/Steered/Finished`, `EvAskUser`) +
  `AgentStatsDelta`. **Zero imports from `core/orchestrator`.** This is the
  seam T0 generalizes to carry a per-delegation key.
- **State owner** — `tui/orchestrator/view.go`: `MultiAgentView` with
  `ApplyEvent`, tab list, `Cycle`, `SelectByKey`-style navigation, stats rows.
  Today it keeps per-agent **logs** (plain text lines), NOT per-agent
  compositors. The delta of THIS plan is exactly "logs → isolated transcripts
  with saved compositor state + scrollback replay."
- **Tab strip + content** — `tui/orchestrator/tabbar.go` (`AgentTabBar`),
  `content.go` (`AgentContent`), `stats_table.go`, `tabpicker.go`
  (`SteerTargetPicker`).
- **Chat suppression** — `ChatViewport.SetSuppressed(bool)`
  (`tui/chat_viewport.go:254`) so the interleaved main view yields during
  orchestration.
- **Stream forwarder** — `internal/app/orchestrator.go`
  `handleOrchestratorStreamMsg` (line 97) routes every sub-agent stream
  (companion AND delegates) through `ensureCompanionSection` →
  `AddCompanionCycle(cycle)` (`tui/chat_viewport_components.go:414`, called at
  `internal/app/orchestrator.go:110`) into the ONE shared `ChatViewport`. This
  single interleave path is what T4 replaces with per-delegation routing.
- **Mock-LLM provider** — `internal/agentic/provider/mock/` implements the
  commit `f704bf6` pattern: per-model FIFO scripted turns (text/thinking/tool
  calls), last-turn replay fallback, `Turn.Hold`/`SetGate` channels to freeze a
  reply mid-stream deterministically. ⚠️ **This package currently lives only on
  `feature/team` (commit `f704bf6`); it is NOT yet on `main`/`feature/multi-agent`.**
  Because every phase's mandated test ladder requires it, **T0 first ports this
  self-contained package** (`mock.go` + `mock_test.go`, ~350 lines, zero
  production deps) onto `feature/multi-agent` — see T0.
- **Test harness** — `internal/app/ui_scenario_test.go` (`uiScenario`: full
  production component tree on a fake terminal, records a `tui.Filmstrip` per
  event) and `tui/filmstrip.go`. **All UI validation uses this, never a live
  terminal.**
- **Feature-gate mechanism** — `config/config_features.go`: `FeaturesConfig`
  with tri-state `*bool` gates + `<Name>Enabled()` resolvers, default off.
  T3's replay flag follows this exact pattern.

---

## 3. Critical dependency discovered (read before scheduling)

**The async-delegation registry is NOT implemented yet.** Verified:
`multiagent/agent_driven_tools.go` `DelegateTool.Execute` still uses
`Pool.GetOrCreate(role)` + `collectAgentOutput` + `sendToMain` free-text; there
is no `DelegationRegistry`, no delegation id, no `delegate_status`/
`delegate_result` tool. `OrchestratorMessage` (`multiagent/orchestrator.go:18`)
carries only `From`/`To` **roles** — no per-delegation id.

The multi-agent TUI's **per-delegation tabs** (research §4.2: `coder·dlg-03` vs
`coder·dlg-07`) require a **stable per-delegation identity** on the stream.
This plan therefore includes, as **T0**, the *minimal* delegation-identity work
the TUI needs — minting a delegation id per `delegate_to` call and threading it
through the stream/observer path into the neutral `AgentViewEvent` — WITHOUT
building the full `DelegationRegistry`/result-envelope/companion-tool surface
of `specs/async-delegation.md` (that full spec is a separate, larger effort;
the TUI only needs the *identity* + lifecycle events).

> If the full async-delegation spec lands first, T0 shrinks to "consume the
> registry's id + lifecycle events" — the rest of the plan is unchanged.

---

## 4. Micro-task rules for the implementing agent

- Do the tasks **in order** (T0 → T5). Each is independently testable and a
  natural commit boundary. Commit per task with a `feat`/`refactor`/`test`
  Conventional Commit message.
- After **every** task run the gates **separately** (do not chain):
  1. `go vet ./...`
  2. `go test -count=1 -race -cover ./...` (at minimum the touched packages)
  3. `gocognit -over 15 .`
  4. `gocyclo -over 12 .`
  Fix any new violation by splitting into named helpers — never `//nolint`.
- Complexity budgets (AGENTS.md): config 20/12, TUI render 18/12, other 15/12.
- **Layering:** `tui` must not import `internal/app`. The neutral
  `AgentViewEvent` (not any source-specific type) is the only thing crossing
  the app↔tui boundary.
- **Prompts:** no new LLM prompts are needed (these are UI labels / event
  plumbing). UI labels in Go are fine (existing `panel.go` precedent).
- **Test order is mandatory for every task:** (1) filmstrip/`uiScenario` checks
  verifying ACTUAL terminal output; (2) scripted mock-LLM validation
  (`internal/agentic/provider/mock`, commit `f704bf6` pattern); (3) only then
  advanced e2e against the Local LM at `http://localhost:1234` (`.goa/skills/
  qa-e2e`). Never open a live terminal to "see" the UI.

---

## 5. Phase / micro-task breakdown

Each task lists: **Goal**, **Files**, **Tests (TDD — write first)**, **Done
when**. The mandated test ladder (filmstrip → mock-LLM → Local-LM e2e) applies
to every phase; each task names the specific checks.

---

### T0 — Neutral seam carries a per-delegation identity

**Phase goal (research §7 T0):** "Generalize the `AgentViewEvent` seam to accept
the async-delegation source (adapter)." Concretely: add a stable **delegation
id** to the neutral event and mint it on the delegation path, so tabs can be
keyed per-delegation (two concurrent coder delegations distinguishable).

**Why first:** every later phase keys transcripts/tabs by this id. Without it,
per-delegation tabs are impossible. T0 also ports the mock-LLM provider package
onto this branch, since every phase's test ladder depends on it and it is not
yet on `main`.

**Files:**
- `internal/agentic/provider/mock/mock.go` + `mock_test.go` (NEW on this
  branch): **port** the self-contained scripted mock-LLM provider from commit
  `f704bf6` (currently only on `feature/team`). Per-model FIFO scripted turns
  (text/thinking/tool calls), last-turn replay fallback so tool-looping agents
  never deadlock, `Turn.Hold`/`SetGate` channels to hold a reply mid-stream.
  Zero production dependencies — pure test/provider package. This is the
  harness every later phase's mock-LLM-first validation uses.
- `tui/orchestrator/event.go` (MOD): add `DelegationID string` to
  `AgentViewEvent`; document that for orchestration-source events it may equal
  `AgentID`, and for delegation-source events it is the minted `dlg-*` id. No
  new imports.
- `multiagent/agent_driven_tools.go` (MOD): in `DelegateTool.Execute`, mint a
  delegation id per call (`dlg-<role>-<NN>` per-role sequence; see
  `specs/async-delegation.md` §5.1 ID allocation) and attach it to the spawned
  sub-agent run so it flows to the observer/stream. Keep the existing ack shape
  (additive: include the id in the returned JSON's `id` field). Do NOT build
  the full registry here — just the id + its propagation.
- `multiagent/orchestrator.go` (MOD): add an optional `DelegationID string`
  field to `OrchestratorMessage` (line 18) so the forwarder can attribute a
  chunk to a specific delegation, not just a role.
- `internal/app/orchestrator.go` (NEW adapter seam): the delegation source is
  `multiagent.OrchestratorMessage`, NOT `core/orchestrator.Event` — so it needs
  its **own** translator `translateDelegationMsg(msg OrchestratorMessage)
  (AgentViewEvent, bool)` (distinct from the existing `translateOrchEvent`,
  which serves the orchestrator runtime). Add it here (or in a new
  `internal/app/delegation_view_source.go`), copying `DelegationID`/`From`/
  `Kind` into the neutral event. This is the Open/Closed adapter the research
  §4.4 describes: adding a source = adding an adapter, never touching the view.

**Tests (TDD — write first):**
- `internal/agentic/provider/mock/mock_test.go` (ported): scripted FIFO turns,
  last-turn replay fallback, `Hold`/`SetGate` freeze — all green on this branch.
- `tui/orchestrator/view_test.go` (extend): two `EvAgentStarted` events with
  the same `Role` but different `DelegationID` produce TWO distinct tabs/logs.
  Proves the seam distinguishes concurrent same-role delegations.
- `multiagent/agent_driven_tools_test.go` (extend): `delegate_to` returns a
  non-empty unique `id`; two calls mint different ids.
- `internal/app/orchestrator_test.go` or `delegation_view_source_test.go`
  (extend/NEW): `translateDelegationMsg` maps `OrchestratorMessage` kinds to
  the correct `AgentViewEvent` and passes `DelegationID` through; unknown kinds
  → `false`.

**Done when:** the mock provider package builds and its tests pass on
`feature/multi-agent`; the neutral event + delegation path carry a
per-delegation id end-to-end; same-role concurrent delegations are
distinguishable in the view model; all gates green. **No UI behavior change
yet.**

---

### T1 — `AgentTranscript` extraction (wrap `ChatViewport`; main agent first)

**Phase goal (research §7 T1):** "Wrap `ChatViewport` so an agent's entries can
mount/unmount; main agent uses it first (no behavior change)."

This is the foundational refactor: introduce `tui/agentctx` with an
`AgentTranscript` that owns one agent's `ChatViewport` + entries, plus the
saved compositor state needed to detach/reattach it. T1 wires ONLY the main
agent through it, so behavior is provably unchanged.

**Files:**
- `tui/agentctx/transcript.go` (NEW): `AgentTranscript` — owns one agent's
  `ChatViewport` and its entry list. Exposes `Mount()`/`Unmount()` and the
  serialized compositor state (rendered lines + watermark) the registry saves
  per agent.
- `tui/agentctx/compositor.go` (NEW): `AgentCompositor` — a thin holder for the
  per-agent compositor snapshot `{prevLines, scrollTop, vt}` keyed to one
  transcript. Inactive = pure data (no terminal writes).
- `tui/agentctx/registry.go` (NEW, minimal in T1): `AgentViewRegistry` —
  `map[id]*AgentView` + active pointer. In T1 it holds exactly one view (main).
- `internal/app/tui.go` (MOD): construct the main agent as an `AgentTranscript`
  in the registry; route the existing `chat` through it. Keep
  `SetSuppressed` semantics intact.

**Tests (TDD):**
- `tui/agentctx/transcript_test.go` (NEW): mount/unmount preserves entries;
  two transcripts hold disjoint entry sets.
- **Filmstrip regression (mandatory):** `internal/app/ui_scenario_test.go`
  (extend or add `internal/app/agentctx_filmstrip_test.go`) — drive a normal
  main-agent chat sequence (user msg → streamed reply → tool call) through
  `uiScenario`, assert the `Filmstrip` frames are **byte-identical** to the
  pre-T1 baseline (golden). This proves "no behavior change."
- **Mock-LLM (mandatory):** use the `internal/agentic/provider/mock` package
  (ported in T0) to drive one main-agent turn; assert rendered output matches
  the golden filmstrip.

**Done when:** main agent runs through `AgentTranscript`; filmstrip shows zero
rendering delta vs baseline; gates green. Single-owner (R1) preserved — all
transcript mutations on `a.apply`.

---

### T2 — `AgentViewRegistry` + tab bar + switching (view-switch only; NO scrollback replay yet)

**Phase goal (research §7 T2):** "`AgentViewRegistry` + tab bar (view-switch
only; single compositor; switch triggers full repaint, no scrollback replay
yet)."

Add real multi-view switching: each agent gets an `AgentTranscript`; the tab
bar (generalized from `AgentTabBar`) lists one tab **per delegation id**;
switching mounts the target transcript and triggers a **full repaint of the
visible window only** (no scrollback re-emission — that is T3).

**Files:**
- `tui/agentctx/registry.go` (MOD): full registry — add/remove views, active
  pointer, `Cycle(dir)`, `SelectByID(id)`, activity bookkeeping for badges
  (badge rendering lands in T5; the *state* lives here).
- `tui/agentctx/tabbar.go` (NEW): `AgentTabBar` for the agentctx registry —
  reuses the rendering approach of `tui/orchestrator/tabbar.go` but keyed by
  delegation id (`main │ coder·dlg-03 │ coder·dlg-07`). Placed **immediately
  above the input editor** (same insertion point as orchestration tabs —
  minimal redraw surface).
- `internal/app/tui.go` (MOD): insert the agentctx tab bar right before `inp`
  in `assembleEngine`; wire switching to unmount/mount transcripts and force a
  full visible-window repaint of the newly active view.
- Switch behavior in T2: clear the screen region (`ED2` on the transcript
  region ONLY — NOT scrollback), recompose the active transcript's visible
  window from its current canvas. Do NOT re-emit committed rows.

**Tests (TDD):**
- `tui/agentctx/registry_test.go` (NEW): add 3 views, cycle/select, active
  pointer correct; inactive views accumulate rows as pure data (no screen
  writes — assert via a spy terminal that only the active view renders).
- `tui/agentctx/tabbar_test.go` (NEW): 3 tabs, active=1 → 2nd label bold,
  `[2/3]` shown; labels keyed by delegation id.
- **Filmstrip (mandatory):** `internal/app/agentctx_switch_filmstrip_test.go`
  — two agents streaming (synthetic events); switch tabs; assert each frame's
  screen region shows ONLY the active agent's lines and the tab bar reflects
  the active tab; assert inactive agent's rows do NOT appear.
- **Mock-LLM (mandatory):** scripted provider with two concurrent roles
  (planner+coder) via `Turn.Hold` to freeze one mid-stream; assert the visible
  tab shows only the active role.

**Done when:** user can switch per-delegation tabs; each tab shows only its own
transcript's visible window; full repaint on switch (no scrollback replay yet);
inactive agents accumulate off-screen; filmstrip + mock-LLM green; gates green.

---

### T3 — Per-agent compositor state + ReplayRunner scrollback replay (FEATURE-FLAGGED)

**Phase goal (research §7 T3, the hard part):** "Per-agent compositor state +
**scrollback replay** on switch via the dedicated `ReplayRunner` goroutine
(§4.3.1) — behind a feature flag."

This is the core algorithm (research §4.3/§4.3.1). On switch A→B it re-emits
B's committed rows into real terminal scrollback so B gets a faithful,
scrollable history — without stalling the render loop.

**Feature flag (mandatory):** add `MultiAgentScrollbackReplay *bool` to
`config.FeaturesConfig` (`config/config_features.go`) with a
`MultiAgentScrollbackReplayEnabled()` resolver (default OFF), mirroring
`RemoteCompaction`. All ReplayRunner behavior is gated; with the flag off the
T2 switch behavior (visible-window repaint only) is used. This lets T3 land and
be tested in isolation without changing default behavior.

**Files:**
- `tui/agentctx/replay.go` (NEW): `ReplayRunner` — a **dedicated goroutine**
  emitting an agent's committed rows `[0, scrollTop)` on tab switch:
  - **Ownership split:** the goroutine owns ONLY scrollback emission; the main
    render loop keeps owning the live visible window. The two never write the
    same region; the goroutine finishes by homing the cursor and restoring the
    scroll region before the render loop resumes.
  - **Serialization:** a dedicated write channel / `replayMu` ensures at most
    one replay emits at a time and never interleaves bytes with a live-frame
    commit. A new tab-switch during a replay **cancels** it (context) and
    coalesces to the latest target — never two concurrent replays.
  - **Back-pressure & yield:** writes in bounded chunks, checks cancellation
    between chunks so a huge history does not starve the input path longer than
    one chunk; cancellation honored mid-replay.
  - **No compositor-state races (R1):** the goroutine does NOT mutate the live
    compositor's `prevLines`/`scrollTop`/`vt`. It emits the precomputed byte
    stream for B's committed rows and hands the final watermark back to the
    command loop via a single channel message, applied under `a.apply`.
  - **Failure isolation:** a write error / cancelled context is contained to
    the goroutine; it reports the error and leaves the main UI live.
  - Reuses the existing first-frame path `emitFirstFrameScroll`/
    `emitTopDownScroll` (`tui/compositor_scroll.go:143,208`) which already
    guarantee "exactly `[0,to)` in scrollback, `[to,to+windowH)` on screen, no
    duplicates."
- `tui/agentctx/compositor.go` (MOD): freeze/restore — `Snapshot()` A's
  `{prevLines, scrollTop, vt}` on switch away; restore B's baseline on switch
  to it.
- `internal/app/tui.go` (MOD): on switch with the flag ON, drive the
  ReplayRunner instead of the synchronous repaint; apply the returned watermark
  via `a.apply`.
- `config/config_features.go` (MOD): the gate + resolver + docs comment.
- `docs/CONFIGURATION.md` (MOD): document `features.multi_agent_scrollback_replay`.

**Correctness rule (research §4.3):** terminal scrollback is append-only — a
switch ADDS B's rows to real scrollback; we never remove A's rows. Mitigate
history growth with a **switch debounce** and by re-emitting only B's *visible
window* on re-activation (full-history replay reserved for an explicit action).

**Tests (TDD):**
- `tui/agentctx/replay_test.go` (NEW):
  - **Switch fidelity:** A streams, switch to B → assert B's watermark rows
    were emitted to scrollback **exactly once** (count `\n` scrolls on a spy
    terminal) and the screen region == B's canvas; switch back → A resumes with
    **zero duplicate rows**.
  - **Cancellation:** start a large replay, issue a second switch mid-replay →
    first is cancelled, only the latest target completes (no interleaved
    bytes).
  - **No race:** `-race` test driving a replay goroutine while the render loop
    runs; assert no data race and a consistent final frame (R1 validated).
- **Filmstrip (mandatory):** `internal/app/agentctx_replay_filmstrip_test.go`
  — **corruption regression** (research §6): a tall committed tool box in
  agent A's transcript; stream B heavily; switch A↔B repeatedly; assert A's
  committed rows are **byte-stable** (the bug-1 amplifier is gone). Assert the
  filmstrip shows B's rows committed once and the live band repaints cleanly.
- **Mock-LLM (mandatory):** scripted provider, two concurrent roles with a tall
  tool output on one; drive switches deterministically via `Turn.Hold`; assert
  scrollback counts and byte-stability.
- **Local-LM e2e (advanced, after filmstrip+mock green):** via `.goa/skills/
  qa-e2e` against `http://localhost:1234` — a real delegation producing a long
  transcript; toggle the flag; assert the terminal shows the switched agent's
  history scrollable and uncorrupted.

**Done when:** with the flag ON, tab switch replays the target's committed
history exactly once with no duplicates/races/corruption; with the flag OFF the
T2 behavior is unchanged; `-race` clean; filmstrip + mock-LLM + Local-LM green;
all gates green.

---

### T4 — Route `delegate_to`/`request_review` into per-delegation transcripts

**Phase goal (research §7 T4):** "Route `delegate_to`/`request_review` streams
into per-delegation transcripts (replaces `AddCompanionCycle` interleave)."

Now that transcripts + tabs + (flagged) replay exist, cut the actual streams
over: a delegation spawns a visible per-delegation tab/transcript instead of
appending `CompanionSectionComponent`s into the main list.

**Files:**
- `internal/app/orchestrator.go` (MOD): `handleOrchestratorStreamMsg` (line 97)
  — replace the `ensureCompanionSection` → `AddCompanionCycle` interleave
  (line 110) with routing by `DelegationID` into the matching
  `AgentTranscript` via the registry (T0 id + T2 registry). Main agent still
  uses its own transcript. Keep the InterAgent fallback for non-stream kinds.
- `internal/app/orchestrator_view_forwarder.go` (MOD): translate the delegation
  lifecycle (PENDING→RUNNING→terminal) into neutral `AgentViewEvent`s feeding
  the registry so a tab appears the moment a delegation is created and its
  terminal state marks the tab (success / FAILED error card — research §5
  bug-2 fix).
- `multiagent/agent_driven_tools.go` (MOD): ensure `request_review` also
  carries an id so its stream lands in its own transcript.
- Remove/retire the `AddCompanionCycle` interleave path **only after** the new
  routing is green (avoid a flag day — keep the old path behind the same code
  until T4 tests pass).

**Tests (TDD):**
- **Mock-LLM (mandatory, primary):** build a new
  `internal/app/agentctx_delegation_mockllm_test.go` on the `f704bf6`
  scripted-provider pattern (package ported in T0) — `delegate_to` two
  concurrent coders (distinct ids) + a planner; assert each delegation's stream
  lands ONLY in its own transcript/tab (isolation), the main transcript has no
  interleaved companion sections, and a FAILED delegation (scripted provider
  400) leaves a marked FAILED tab + error card.
- **Filmstrip (mandatory):** `internal/app/agentctx_delegation_filmstrip_test.go`
  — drive a delegation lifecycle; assert a tab appears on PENDING, streams
  under RUNNING, and is marked terminal; assert the screen shows only the
  active delegation.
- **Local-LM e2e (advanced):** qa-e2e scenario — `delegate_to` a real task;
  assert a tab appears and the result renders in its transcript; a forced
  failure (e.g. codex `max_output_tokens` class) surfaces a FAILED tab.

**Done when:** delegations render in per-delegation tabs/transcripts (not the
main interleave); failures are always visible (bug 2); isolation + filmstrip +
mock-LLM + Local-LM green; gates green.

---

### T5 — Badges, `/agent:tab`, steering-to-active-tab, per-tab footer stats

**Phase goal (research §7 T5):** "Badges, `/agent:tab`, steering-to-active-tab,
per-tab footer stats."

Polish the navigation/attribution surface now that routing is correct.

**Files:**
- `tui/agentctx/tabbar.go` (MOD): activity badge — `✱` on background activity,
  `▲` on error, driven by the registry (state added in T2).
- `core/commands/agent.go` (NEW or MOD): `/agent:tab:<id>` explicit tab select
  (mirrors the orchestration `:tab:` subcommand pattern); `/agent:replay`
  triggers a deliberate full-history replay of the active tab (uses the T3
  ReplayRunner path).
- `internal/app/shortcuts.go` + `tui/tui.go` (MOD): `Alt+]`/`Alt+[` cycle tabs,
  `Alt+<digit>` jump (reuse the generic `OnAgentTabNext/Prev` mechanism added
  for orchestration tabs — keep names source-agnostic).
- `internal/app/submithandler.go` (MOD): steering targets the **active tab's**
  delegation/agent (fall back to `all`); input prompt label reflects the active
  tab (`steer coder·dlg-03:` vs `steer all:`).
- `tui/footer_render.go` (MOD): per-tab footer stats — the footer shows the
  ACTIVE tab's agent stats (replaces the single flapping `SetActiveAgent`).

**Tests (TDD):**
- `tui/agentctx/tabbar_test.go` (extend): background delegation finishing sets
  the `✱` badge; an error sets `▲`.
- `core/commands/agent_command_test.go` (NEW/extend): `/agent:tab:<id>` selects
  the tab; unknown id → actionable flash error; `/agent:replay` triggers a
  full-history re-emit (assert via spy terminal scroll count).
- `internal/app/shortcuts_test.go` (extend): `alt+]` cycles the active tab and
  updates the input prompt.
- `internal/app/submithandler_test.go` (extend): typing + Enter with an agent
  tab active steers that delegation id.
- **Filmstrip (mandatory):** assert the badge appears on the correct tab in the
  recorded frames and the footer reflects the active tab's stats after a switch.
- **Mock-LLM (mandatory):** scripted provider; a background role completes
  while the user is on the main tab → its badge sets; switch to it → footer
  shows its stats.
- **Local-LM e2e (advanced):** qa-e2e — cycle tabs with hotkeys, steer the
  active delegation, verify per-tab footer stats on a real run.

**Done when:** badges, explicit `/agent:tab`, hotkey cycling, active-tab
steering, and per-tab footer all work; filmstrip + mock-LLM + Local-LM green;
gates green.

---

## 6. Validation strategy (mandatory, every phase)

For EVERY task, in this order:

1. **TUI filmstrip / uiScenario (actual terminal output, never a live
   terminal).** Per bugs.md guideline 5, verify real rendered output via
   `tui.Filmstrip` + `internal/app/ui_scenario_test.go`. Each phase names its
   specific filmstrip test above.
2. **Scripted mock-LLM provider first** (`internal/agentic/provider/mock`,
   commit `f704bf6` pattern). Deterministic, no live model; `Turn.Hold`/
   `SetGate` to freeze one role mid-stream while another completes. This is the
   primary driver for concurrency/isolation assertions.
3. **Advanced e2e vs the Local LM** at `http://localhost:1234` via
   `.goa/skills/qa-e2e`. Only after (1) and (2) are green. Live tests skip
   automatically when the LM is unreachable (the `lmstudioReachable` /
   `GOA_ENABLE_LIVE_LM_TESTS` pattern), keeping CI hermetic.

Key phase-level validations (from research §6):
- **Per-agent transcript isolation:** two agents stream; each transcript holds
  only its own lines.
- **Switch fidelity:** A streams → switch to B → screen == B's canvas and B's
  watermark rows emitted exactly once → switch back → A resumes with zero
  duplicates.
- **Corruption regression:** tall committed tool box in A; stream B heavily;
  switch A↔B; A's committed rows byte-stable.
- **Badge:** background delegation finishing sets the `✱` badge.

---

## 7. Static-analysis gate (run separately, every task)

- `go vet ./...`
- `go test -count=1 -race -cover ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`

If a function exceeds budget, split into named helpers (factories + small
primitives) — do not silence with `//nolint`.

---

## 8. Files summary

**Create (ported to this branch in T0):**
- `internal/agentic/provider/mock/mock.go` + `mock_test.go` (scripted mock-LLM
  provider, commit `f704bf6` pattern — prerequisite for all mock-LLM validation)

**Create (`tui/agentctx/`):**
- `transcript.go` + `transcript_test.go` (`AgentTranscript`)
- `compositor.go` (`AgentCompositor` — per-agent compositor snapshot)
- `registry.go` + `registry_test.go` (`AgentViewRegistry`)
- `tabbar.go` + `tabbar_test.go` (per-delegation-id tab strip)
- `replay.go` + `replay_test.go` (`ReplayRunner` dedicated goroutine, §4.3.1)

**Create (`internal/app/`):**
- `delegation_view_source.go` + `delegation_view_source_test.go` (T0:
  `OrchestratorMessage → AgentViewEvent` adapter — the only delegation-specific
  seam)
- `agentctx_filmstrip_test.go` (T1 no-behavior-change golden)
- `agentctx_switch_filmstrip_test.go` (T2)
- `agentctx_replay_filmstrip_test.go` (T3 corruption regression)
- `agentctx_delegation_filmstrip_test.go` (T4)
- `agentctx_delegation_mockllm_test.go` (T4 scripted-provider isolation)

**Modify:**
- `tui/orchestrator/event.go` (add `DelegationID`)
- `multiagent/agent_driven_tools.go` (mint + propagate delegation id)
- `multiagent/orchestrator.go` (`OrchestratorMessage.DelegationID`)
- `internal/app/orchestrator.go` (route streams per-delegation, T4)
- `internal/app/orchestrator_view_forwarder.go` (lifecycle → AgentViewEvent)
- `internal/app/tui.go` (construct transcripts, tab bar, ReplayRunner wiring)
- `internal/app/shortcuts.go`, `tui/tui.go` (tab hotkeys, T5)
- `internal/app/submithandler.go` (active-tab steering, T5)
- `tui/footer_render.go` (per-tab footer stats, T5)
- `config/config_features.go` (T3 feature gate) + `docs/CONFIGURATION.md`
- `core/commands/agent.go` (T5 `/agent:tab`, `/agent:replay`)

---

## 9. Risk notes

- **Layering:** keep `AgentViewEvent` the only type crossing app↔tui. New
  per-delegation concepts ride the neutral event's `DelegationID`/`Meta` fields
  — do not leak `multiagent.*` types into `tui/agentctx`.
- **Replay is the riskiest part** (research §4.3 open risk): rapid A↔B switching
  re-emits scrollback each time, growing user history. Mitigate with a switch
  debounce + re-emit only the visible window on re-activation; full-history
  replay stays behind `/agent:replay`. Prototype this FIRST within T3.
- **Single-owner under replay:** the ReplayRunner is the ONLY new goroutine
  that writes terminal bytes. It must never race the live-frame commit — the
  write channel / `replayMu` + watermark-hand-back-over-channel pattern is the
  mechanism; the `-race` test in T3 enforces it.
- **Dependency on delegation ids:** if the full `specs/async-delegation.md`
  registry lands later, T0/T4 adapt to consume its ids instead of minting them
  in the tool — keep the id-generation logic small and swappable.
- **Don't remove the old interleave path early:** keep `AddCompanionCycle`
  until T4's per-delegation routing is verified green, then retire it.

---

## 10. Definition of done (whole plan)

- Tasks T0–T5 complete; every test in §6 green (live tests skip without the LM).
- The 4 gates green separately after every task.
- Hard constraints C1–C4 + R1 preserved throughout (native scrollback, no
  alt-screen; single-owner on `a.apply`; ReplayRunner owns only scrollback
  emission; inactive agents are pure data; per-delegation tabs).
- A live smoke check (Local LM, flag ON) shows: per-delegation tabs, faithful
  scrollback replay on switch, no corruption under A↔B churn, badges + per-tab
  footer. Recorded as an ANSI-free filmstrip snapshot under
  `docs/archive/multi-agent-tui-smoke.<date>.md`.
- All work committed on `feature/multi-agent`.
