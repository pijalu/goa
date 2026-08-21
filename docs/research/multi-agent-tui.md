<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Research — Multi-Agent TUI: Tabs over Isolated, Per-Agent Transcripts

Status: **research / design** (no code changes yet).
Companion spec: `specs/async-delegation.md` (the delegation lifecycle events
this view consumes).

## 0. TL;DR

When several agents run, **every stream is currently interleaved into one
shared chat viewport** (main + companion + each delegated coder/planner all
append to the same transcript). This does not scale: too many messages,
impossible to follow one agent, and — worse — it is the root enabler of the
scrollback-corruption bug (bug 1), because concurrently-growing regions force
the compositor to reconcile multiple shifting row-blocks against rows already
committed to terminal scrollback.

**Proposal:** give each agent its **own transcript** and its **own
compositor**, expose them as **tabs** (one tab per agent + an optional "All"
interleaved view), and on tab switch **replay that agent's history** into the
terminal (scrollback + screen). Goa runs in **normal screen mode (no alternate
screen)**, so transcripts already live in real terminal scrollback — the
design keeps that and owns the replay explicitly.

---

## 1. Why the single-view mix cannot work

### 1.1 Verified in source

- `delegate_to` / `request_review` sub-agent streams are routed into the
  **same** `ChatViewport` as the main conversation:
  `App.handleOrchestratorStreamMsg` → `ensureSection` →
  `chat.AddCompanionCycle(cycle, role)` (`internal/app/orchestrator.go:193-210`,
  `tui/chat_viewport_components.go:474-487`). Every role appends
  `CompanionSectionComponent` entries to the one append-only transcript.
- The transcript is **append-only** and rendered **chronologically**
  (`ChatViewport.fullRebuild`, `tui/chat_viewport.go:471-512`) — there is no
  per-agent zone. Concurrent streams interleave line-by-line in time order.
- The footer can only show ONE "active agent" (`SetActiveAgent`,
  `tui/footer_render.go:197`), so with coder + planner + companion all live,
  the visible attribution flaps and the bodies are all in one list anyway.

### 1.2 Why it breaks in practice

1. **Unfollowable.** Three agents streaming thinking + tool lines + content
   into one chronological list is unreadable — you cannot track one agent's
   reasoning.
2. **Layout corruption (bug 1 amplifier).** Each live section is a dirty entry
   that re-renders at a changing height (`MarkEntryDirty`, `fullRebuild`).
   Several of these growing at once shift the `lineOffset` of everything below
   them while the compositor clamps repaints to the scrollback watermark
   (`advanceScrollback`, `tui/compositor_scroll.go:12-18`). The frozen rows
   above the watermark and the re-flowed rows below it disagree → the
   malformed display from the bug report. Isolating each agent's rows into its
   own transcript removes the multi-region-growth coupling entirely.
3. **No per-agent identity.** Token/cache stats, model, and status per agent
   exist in the data model (`EventAgentStats`) but have nowhere to live when
   everything shares one list.

## 2. Constraints discovered (the hard part)

These are verified against `tui/compositor*.go` and are what make this
non-trivial:

- **C1 — Normal screen, real scrollback.** Goa does **not** use the terminal
  alternate screen (no `?1049h`; verified — zero matches). Transcript rows are
  committed to the terminal's genuine scrollback and are **never repainted**.
- **C2 — One compositor, one watermark.** `Compositor` holds a single
  `scrollTop` watermark, a single `vt` (viewport top), and a single
  `prevLines` baseline (`tui/compositor.go:20-54`). The invariant "rows
  `[0, scrollTop)` are frozen forever" is global, not per-view.
- **C3 — A single canvas.** `Scene.compose` produces ONE `[]string` canvas
  (`tui/compositor_scene.go:88`). There is no notion of "which agent owns row
  i" — so you cannot selectively re-render agent B's rows without re-rendering
  the frame.
- **C4 — History replay must be explicit.** Because scrollback is real, the
  only way to "show agent B's history" is to re-emit it: replay agent B's
  committed rows back into the terminal (they scroll into scrollback again),
  then draw agent B's visible tail. The terminal does not keep per-agent
  buffers for us.

## 3. Design options

### Option A — View-switch tabs over per-agent transcripts (RECOMMENDED)

One screen region; the **active tab** decides which agent's transcript is
mounted. Each agent has its own `ChatViewport` (its own entries, its own
`CompanionSectionComponent`s, its own scrollback accounting). Switching tabs
unmounts the current transcript and **replays** the newly-selected agent's
transcript.

```
┌──────────────────────────────────────────────────────────┐
│ main │ ✱coder │ planner │ Stats                          │ ← tab bar
├──────────────────────────────────────────────────────────┤
│                                                          │
│   <active agent's OWN transcript, own scrollback>        │
│                                                          │
├──────────────────────────────────────────────────────────┤
│ status / footer (shows the ACTIVE tab's agent stats)     │
└──────────────────────────────────────────────────────────┘
```

- **Pros:** each transcript is a single-owner, single-growth-region view →
  kills the corruption amplifier; gives each agent a clean scrollback; the
  footer/stats become per-tab and unambiguous; matches the existing
  `AgentTabBar` direction already prototyped for orchestration mode
  (`specs/orchestrator-tabs-plan.md`), generalized to ALL multi-agent sources.
- **Cons:** the tab-switch replay (§4.3) is the genuinely hard part — it must
  restore terminal scrollback faithfully without duplicating or dropping rows.

### Option B — Split panes (side-by-side / stacked)

Multiple live viewports on one screen (e.g. main | coder | planner columns).

- **Pros:** true simultaneity; no replay.
- **Cons:** terminal width is scarce (each pane too narrow for code/diffs);
  the single-canvas compositor (C3) cannot do independent vertical scrollback
  per pane (DECSTBM regions conflict); resizing/reflow becomes per-pane chaos.
  **Rejected** for v1 — revisit only with a pane-aware compositor.

### Option C — Alternate screen per agent buffer

Switch to `?1049h` and manage a private screen buffer per agent in memory.

- **Pros:** no reliance on terminal scrollback; perfect replay fidelity.
- **Cons:** abandons native scrollback (users lose scroll-back-into-history
  with the mouse / terminal scrollbar) — a major UX regression for a CLI agent
  whose whole value is a persistent transcript. **Rejected.**

**Decision: Option A.**

## 4. Architecture

### 4.1 Per-agent transcript + per-agent compositor

```
tui/agentctx/  (NEW package)
  transcript.go   AgentTranscript — owns one agent's ChatViewport + entries
  compositor.go   AgentCompositor — a Compositor keyed to one transcript
  registry.go     AgentViewRegistry — map[agentID]*AgentView, active pointer
  replay.go       ReplayRunner — dedicated goroutine emitting committed rows
                  on tab switch (§4.3.1) without stalling the main render loop
```

- `AgentTranscript` = the existing `ChatViewport` fed ONLY that agent's
  entries. Main agent = the current ChatViewport unchanged. Delegated agents =
  one transcript each, fed by the delegation lifecycle events from
  `specs/async-delegation.md` (PENDING → RUNNING → terminal) instead of
  `AddCompanionCycle` appending into the main list.
- Each `AgentView` holds `{transcript, compositorState}`. Only the ACTIVE
  view's compositor is attached to the terminal; inactive views are pure data
  (their "compositor" is just a serialized state: rendered lines + watermark),
  so background agents keep accumulating rows without touching the screen.
- **Single-owner invariant preserved:** all mutations still happen on the
  command loop (`a.apply(...)`); the registry only swaps which view is mounted.

### 4.2 Tab bar & switching

- Tab strip directly above the input editor (same insertion point the
  orchestration tabs plan used — minimal redraw surface).
- One tab per **delegation id** (not just per role) so two concurrent coder
  delegations are distinguishable: `main │ coder·dlg-03 │ coder·dlg-07 │ …`.
- Keys: `Alt+]` / `Alt+[` cycle; `Alt+<digit>` jumps; `/agent:tab:<id>`
  explicit. Inactive-tab activity badge (✱ / ▲ on error) driven by the
  registry.

### 4.3 Tab switch = scrollback replay (the core algorithm)

On switching from agent A to agent B:

1. **Freeze A.** Snapshot A's compositor state: `prevLines` (current canvas),
   `scrollTop` (watermark), `vt`. Detach A's compositor.
2. **Restore B's baseline.** B already has its own saved `prevLines_B` and
   `scrollTop_B` from when it was last active (or from its background render).
3. **Re-emit B's visible window.** Home cursor, clear the screen region
   (`ED2` on the transcript region only — NOT the scrollback), then re-run
   `emitFirstFrameScroll`/`emitTopDownScroll` against **B's canvas** so B's
   rows re-fill the screen and B's `scrollTop_B` rows scroll into terminal
   scrollback exactly once. This is the existing first-frame path
   (`compositor_scroll.go:143-153`) reused — it already guarantees "exactly
   `[0,to)` in scrollback, `[to,to+windowH)` on screen, no duplicates".
4. **Mount B.** Attach B's compositor (`scrollTop_B`, `vt_B`, `prevLines_B`)
   so subsequent live deltas resume the steady incremental path.

Key correctness rule: **the terminal's scrollback is append-only**, so a
switch *adds* B's rows to the real scrollback (they become part of the
user's scrollable history) — we never try to *remove* A's rows from
scrollback (impossible without alt-screen). A's rows stay in the user's
history above; B's appear below. This is acceptable and matches how the
single view already commits rows; the win is that **B's screen region now
shows only B**, followable and uncorrupted.

> Open risk to prototype first: rapid A↔B switching re-emits scrollback each
> time, growing the user's history. Mitigate with a switch debounce and by
> only re-emitting B's *visible window* (not its full history) on re-activation,
> keeping full-history replay for a deliberate `/agent:replay` action.

### 4.3.1 Scrollback replay runs on a dedicated goroutine (IMPORTANT)

Re-emitting an agent's committed history is a **bulk, blocking write** — it
can be hundreds/thousands of rows and must never stall the main TUI (input
echo, live streaming of the *other* agents, spinner animation, footer clock).
The compositor's whole design assumes the render loop is fast and
single-owner; a synchronous multi-screen replay on the render/command loop
would freeze the UI for the duration of the replay.

Therefore scrollback replay is performed by a **dedicated replay goroutine**:

- **Ownership split.** The replay goroutine owns **only the scrollback
  emission** (the historical rows `[0, B.scrollTop_B)`); the main render loop
  keeps owning the **live visible window** (`[B.scrollTop_B, …]`). The two
  never write the same screen region: the goroutine writes rows that scroll
  *up into history* and finishes by homing the cursor and restoring the scroll
  region before the render loop resumes painting the live band.
- **Serialization (single writer at a time).** A `replayMu` / dedicated write
  channel ensures at most one replay is emitting at any instant, and that a
  replay and a live-frame commit never interleave bytes on the terminal.
  A new tab-switch while a replay is in flight **cancels** it (context) and
  coalesces to the latest target — never two concurrent replays.
- **Back-pressure & yield.** The replay writes in bounded chunks and yields /
  checks for cancellation between chunks so a huge history does not starve the
  input path for longer than one chunk; cancellation is honoured mid-replay.
- **No compositor-state races.** The goroutine does **not** mutate the live
  compositor's `prevLines`/`scrollTop`/`vt` directly; it emits the precomputed
  byte stream for B's committed rows and hands the final watermark back to the
  command loop via a single channel message, which the command loop applies
  under the normal `a.apply(...)` single-owner rule (R1). The terminal byte
  stream and the compositor's logical state stay consistent because the replay
  goroutine is the only writer to the terminal during the replay window.
- **Failure isolation.** A replay error (write failure, cancelled context) is
  contained to the goroutine; it reports the error and leaves the main UI live
  and consistent rather than wedging the render loop.

This keeps the strict render-loop latency budget intact while still giving
each agent a faithful, scrollable history.

### 4.4 Event routing

The neutral `AgentViewEvent` seam from `specs/orchestrator-tabs-plan.md` §2.2
is adopted unchanged: each source (orchestrator runtime, foreground
orchestrator/companion, swarm, async-delegation registry) translates its
events into `AgentViewEvent`; the `AgentViewRegistry` routes by `AgentID` to
the right `AgentTranscript`. Adding a source = adding an adapter, never
touching the view (Open/Closed).

## 5. What this fixes / enables

- **Bug 1 (corruption):** eliminates the multi-region-growth coupling — the
  amplifier is gone. (The single-region `SetExpanded`-on-committed-rows hole
  still needs its own guard; tabs do not replace that fix, they shrink its
  blast radius.)
- **Bug 2 (delegate_to invisible):** a delegation now spawns a visible tab +
  badge + per-agent footer the moment it's created, so even a silent 400
  failure (see `specs/async-delegation.md` §9) leaves a marked FAILED tab and
  an error card instead of nothing.
- **Followability:** one screen = one agent's clean, scrollable story.

## 6. Test strategy (filmstrip, never a live terminal)

- Per-agent transcript isolation: two agents stream; assert each transcript's
  entries contain only its own lines.
- Switch fidelity: A streams, switch to B, assert screen region == B's canvas
  and B's watermark rows were emitted to scrollback exactly once (count `\n`
  scrolls), then switch back and assert A resumes with zero duplicate rows.
- Corruption regression: a tall committed tool box in agent A's transcript;
  stream B heavily; switch A↔B; assert A's committed rows are byte-stable.
- Badge: background delegation finishing sets the ✱ badge on its tab.
- Harness: existing `tui.Filmstrip` + `uiScenario` (`internal/app/ui_scenario_test.go`).

## 7. Phasing

| Phase | Deliverable |
|-------|-------------|
| T0 | Generalize `AgentViewEvent` seam to accept the async-delegation source (adapter). |
| T1 | `AgentTranscript` extraction — wrap `ChatViewport` so an agent's entries can mount/unmount; main agent uses it first (no behavior change). |
| T2 | `AgentViewRegistry` + tab bar (view-switch only; single compositor; switch triggers full repaint, no scrollback replay yet). |
| T3 | Per-agent compositor state + **scrollback replay** on switch via the dedicated `ReplayRunner` goroutine (§4.3.1) — the hard part; behind a feature flag. |
| T4 | Route `delegate_to`/`request_review` streams into per-delegation transcripts (replaces `AddCompanionCycle` interleave). |
| T5 | Badges, `/agent:tab`, steering-to-active-tab, per-tab footer stats. |

## 8. Alternatives summary

| Option | Verdict | Reason |
|--------|---------|--------|
| **A. View-switch tabs + replay** | **Adopt** | Single-owner transcripts kill the corruption amplifier; reuses the existing scroll engine; keeps native scrollback. |
| B. Split panes | Reject (v1) | Width-starved; single-canvas compositor can't do per-pane scroll regions. |
| C. Alt-screen buffers | Reject | Abandons native terminal scrollback — core UX regression for a CLI agent. |
