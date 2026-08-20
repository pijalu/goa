# Bug and feature Tracking — archived 2026-08-11

Archived from bugs.md after the fix was implemented, tested, and validated.
All items below are CLOSED.

---

## Must fix

### BUG: context compressions are invisible — no status-bar cue, no conversation bubble, nothing in the session log

**Status:** FIXED + validated (2026-08-11). Implemented per the fix plan in §3:
structured `CompactionInfo` emitted by every compression path (ceiling, elision,
selective, micro, summarize, hybrid, overflow, truncation); the app renders a
⚡ conversation bubble and counts the footer `c:` segment from the same event;
per-round `CompactionRound` records feed the session stats + headless summary;
the session JSONL documents each pass. Validated: `go vet`, `staticcheck`,
`gocognit -over 15`, `gocyclo -over 12`, `go test -count=1 -race -cover`
(agentic 82.3% / app 55.2% / core 73.0%), plus interactive-shell rendering of
the bubble, footer, headless summary, and JSONL round-trip.

---

#### 1. Symptom (observed in a real session)

Session `/Users/muaddib/dev/frigolite/.goa/sessions/1786435066_n3pt8nxd.jsonl`
(83,386 lines, goal mode, 2 goal continuation turns) shows **two real context
compressions** with **zero visible/documented traces**:

| # | Warning line | Before | After | Δ |
|---|---|---|---|---|
| 1 | L53942 | 435 msgs, 191,804 tok, **95%** | 197 msgs, 86,115 tok, **43%** | −238 msgs, −105,689 tok |
| 2 | L76554 | 478 msgs, 190,759 tok, **95%** | 208 msgs, 87,962 tok, **43%** | −270 msgs, −102,797 tok |

- Both drops are preceded by the silent-overflow warning
  `"warning: context usage ≥ 95% without provider error — compression will fire on next turn"`
  (emitted by `checkSilentOverflow`, `agent_compression.go:588`).
- The session log has **zero `{"Type":"compact",...}` events** (grep count = 0).
- The footer/status bar **never shows a compression counter** — no `c:…`
  segment appears anywhere in the rendered state.
- The conversation shows **no compression bubble** — the user only sees a
  warning and then an unexplained context drop.

**Goal-switch double check (user-requested):** the two drops are **NOT goal
switches**. The only `context_reset` event is at L4 (file start — the goal
fresh-context begin, documented by the system message at L2). The goal
continuation at L61687 carries the context forward unchanged (294 msgs at
L61683 → 294 msgs at L61696) and has no fresh-context marker. Both drops occur
**mid-turn** between tool calls and match the reactive ceiling cut exactly
(95% → 43%; target = effectiveHard 95 − ReactiveSavings 50 = 45%, token-based,
so 43% observed).

#### 2. Root cause

The compression that actually ran is **`enforceContextCeiling()`**
(`internal/agentic/agent_context.go:44-171`) — the reactive last-resort safety
net that **stays on even when proactive compression is disabled** (the
frigolite config has `context_compression.enabled: false`,
`threshold_percent: 0`, `on_context_error: false`, empty strategy, so
`maybeCompress` → `proactiveTier` → `tierNone`; only the ceiling enforcer
fires at the default effectiveHard 95).

**`enforceContextCeiling` performs the message drop but emits NO `OutputEvent`
at all** — it only writes to the agent logger (`Warn`, lines 153-161). Because
every visible surface is event-driven, nothing surfaces:

1. **Footer counter never appears** — `app.recordCompact` (`stats.go:124`) is
   only invoked on `EventCompact` (`handleAgentStatsEvent`, `stats.go:117-118`).
   No event → `MicroCompacts`/`Compacts` stay 0 → the `c:%dm-%d` segment
   (`stats.go:1184-1186`) never renders.
2. **Session log documents nothing** — `SessionStore.WriteEvent`
   (`core/sessionstore.go:251`) serializes every `OutputEvent` to JSONL. No
   event → no `compact` line.
3. **No conversation bubble** — nothing emits a content/system event for the
   drop.

**Secondary gap (broader than this session):** even for the strategies that DO
emit `EventCompact` (micro: `compaction.go:93` `Text:"micro"`; summarize:
`agent_compression.go:79` `Text:summary`), the app only **counts** them
(`recordCompact`) — it never renders a bubble. And the remaining strategies —
`compressToolElision` (`agent_compression.go:352`), `compressSelective`
(`agent_compression.go:497`), `compressHybrid` (`agent_compression.go:327`) —
emit **nothing** when called through `compressHistoryWith`. So even with
compression enabled, elision/selective/hybrid compressions are invisible in the
same way.

**In short:** *every* compression except micro/summarize emits no `EventCompact`
at all, and *even* micro/summarize `EventCompact` are only counted, never
rendered. The status bar's compression counter is therefore dead in practice
unless a micro or summarize pass happens to run.

#### 3. Fix plan (clean/elegant)

**Design principle:** one structured `EventCompact` emitted by *every*
compression path that actually changed history, at the agentic layer (the layer
that owns the mutation); the app layer renders it (bubble) and counts it
(footer) from the same event. No duplicated rendering logic, no stringly-typed
special cases in the app.

##### A. Agentic layer — structured compact event

1. **Add a typed payload** to `OutputEvent` (mirrors the existing
   `ContextStats`/`PromptProgress` pattern in `observer.go`):

   ```go
   // CompactionInfo describes a completed compression pass.
   type CompactionInfo struct {
       Strategy    string `json:"strategy"`               // elision|selective|micro|summarize|hybrid|ceiling|overflow|truncation
       BeforePct   int    `json:"before_pct"`             // usage % before
       AfterPct    int    `json:"after_pct"`              // usage % after
       FreedTokens int    `json:"freed_tokens,omitempty"` // estimated tokens freed (0 = unknown)
       Removed     int    `json:"removed,omitempty"`      // messages removed (0 = none)
       Detail      string `json:"detail,omitempty"`       // summarize summary text, "" otherwise
   }
   ```
   `OutputEvent.Compaction *CompactionInfo` (`json:"compaction,omitempty"`).
   This serializes cleanly into the session JSONL (fixes "nothing documented").

2. **One emission point per compression pass.** Internal mutators must NOT emit
   (they run under `a.mu`; `emitEvent` re-acquires `a.mu` → deadlock —
   `agent_events.go:7-11`). Instead:

   - `compressToolElision(force) bool` → returns whether it elided
     (boundary > 1 or escalation ran).
   - `compressSelective() bool` → returns `removed > 0`.
   - `microCompactForced(force) bool` → returns `changed > 0`; **remove** its
     internal `emitEvent` (`compaction.go:93`) — callers emit.
   - `enforceContextCeiling()` → split into
     `enforceContextCeilingLocked() int` (returns droppedTokens) + the public
     wrapper that unlocks then emits when `droppedTokens > 0`.
   - `Compact(ctx)` keeps its internal emission (public API contract +
     `TestAgent_CompactEmitsCompactEvent`), now with
     `Compaction{Strategy:"summarize", Detail:summary, BeforePct, AfterPct}`.

3. **Top-level entry points emit exactly once after unlock** (helper
   `emitCompaction(strategy string, before, after ContextStats, removed, freed int, detail string)`):
   - `compressHistoryWith` (`agent_compression.go:289`): elision → "elision";
     selective → "selective"; micro → "micro" (from the returned bool);
     summarize → `Compact` emits itself; hybrid → `compressHybrid` emits.
   - `compressHybrid` (`agent_compression.go:327`): emit "hybrid" when
     elision/selective did work and it did NOT escalate to `Compact`; if it
     escalates, `Compact` emits (no double-emit).
   - `compressOverflowRecovery` (`agent_compression.go:676`): emit "overflow"
     when elision/selective did work (unless `Compact` escalated and emitted).
   - `maybeCompressAfterLengthTruncation` (`agent_compression.go:604`): emit
     "truncation" when work done.
   - `enforceContextCeiling`: emit "ceiling" when `droppedTokens > 0`
     (restructured wrapper).

   The `checkSilentOverflow` warning (`agent_compression.go:588`) stays as-is:
   it is the "compression will fire" cue that complements the completed
   "ceiling" bubble.

##### B. App layer — render a dedicated bubble + keep counting

In `handleAgentStatsEvent` (`stats.go:110`):

```go
case agentic.EventCompact:
    a.recordCompact(ev)
    a.showCompactionBubble(ev)
```

- `recordCompact` classifies from `ev.Compaction.Strategy` (fallback to
  `ev.Text`): `"micro"` → `microCompacts++`, else `compacts++`.
- New `showCompactionBubble(ev)` renders a dedicated conversation element via
  `a.subs.chat.AddSystemMessage(...)` (the existing bordered system-message
  bubble, `chat_viewport.go:619-622`), e.g.:
  `⚡ Context compacted (ceiling): 95% → 43% · 238 messages dropped`.
  Guard `a.subs.chat == nil`. Because `handleAgentOutputEvent` runs inside
  `a.apply` (`events.go:112-117`), the chat mutation is already on the
  commandLoop (single-owner invariant preserved).
- Optional dedup: use `AddFlashMessage` (⚡-kind dedup, `chat_viewport.go:655-677`)
  so a ceiling enforcer that fires repeatedly updates the last bubble of the
  same kind instead of stacking — recommend `AddSystemMessage` for full
  durability, or flash-dedup if repeated ceiling cuts prove noisy.

##### C. Status bar / footer

With A+B the existing `c:%dm-%d` counter (`stats.go:1184-1186`) now actually
appears when any compression runs (ceiling/selective/hybrid now emit).
Keep the format; consider showing a total `c:2` instead of the m/s split if the
new kinds make `c:0m-2` ambiguous. Minimal change: leave the format, the bubble
carries the detail.

##### D. Session stats — document each compression round

Beyond the footer counter, session stats must **document the compression
rounds** — a per-round record, not just aggregates. Today `sessionStats`
(`stats.go:62-78`) only carries `MicroCompacts int` / `Compacts int` counters,
which lose every detail (strategy, before/after %, freed tokens, removed,
time). Headless `--plain` output (`headless_renderers.go:98-120`) prints only
`-- stats … c:…` and a summary with no compaction detail.

Add a typed per-round record to the app's session stats:

```go
// CompactionRound documents one completed compression pass in the session.
type CompactionRound struct {
    Strategy    string    `json:"strategy"`              // elision|selective|micro|summarize|hybrid|ceiling|overflow|truncation
    BeforePct   int       `json:"before_pct"`
    AfterPct    int       `json:"after_pct"`
    FreedTokens int       `json:"freed_tokens,omitempty"`
    Removed     int       `json:"removed,omitempty"`
    At          time.Time `json:"at"`                    // when the round completed
}
```

- `sessionStats` gains `Compactions []CompactionRound` (in addition to the
  aggregate counters, which stay for the footer).
- The app accumulates rounds in `recordCompact`/`handleAgentStatsEvent`
  (`stats.go:117-132`) from each `EventCompact`'s `CompactionInfo` (+ `time.Now()`),
  and `buildFooterStatsLocked`/`buildStatsLocked` copy them into `sessionStats`
  (same pattern as `st.MicroCompacts = a.microCompacts`, `stats.go:943-944`).
- Surfaces:
  - Headless `--plain` Summary (`headless_renderers.go:105-120`): when
    `len(stats.Compactions) > 0`, print one `-- compression N: strategy before%→after% freed=X removed=Y` line per round.
  - Headless ansi renderer Summary (`headless_renderers.go:264-269`): same
    detail in the colored summary block.
  - `orch_tabs.go` agent rows (`formatOrchAgentLine`) inherit the footer line
    automatically; no change needed unless per-agent rounds are wanted.
- Clearing: `clearStats` (`stats.go:134-157`) resets `a.compactions` alongside
  `a.microCompacts`/`a.compacts` so a `/clear` or new session starts fresh.

This makes the session stats self-documenting: a user who missed the live
bubble can read the compression rounds from the final stats (TUI footer
aggregate + headless summary detail), and the JSONL `EventCompact` lines carry
the same data for export/archive.

##### E. Session log

No change needed: `SessionStore` already persists every `OutputEvent`
(`sessionstore.go:251`); with A the log now contains
`{"Type":"compact","Text":"ceiling","Compaction":{"Strategy":"ceiling","BeforePct":95,"AfterPct":43,"Removed":238}}`.

#### 4. Test approach

Agentic unit tests (table-driven, following existing patterns):
- `TestEnforceContextCeiling_EmitsCompactEvent` — history over ceiling;
  assert exactly one `EventCompact`, `Strategy=="ceiling"`, `BeforePct/AfterPct`
  sane, `Removed > 0`.
- `TestEnforceContextCeiling_NoEventWhenUnderCeiling` — no event when nothing
  dropped.
- `TestCompressSelective_EmitsCompactEvent` / `TestCompressToolElision_EmitsCompactEvent`
  — event when work done, no event when nothing to do.
- `TestCompressHybrid_EmitsSingleCompactEvent` — elision+selective path emits
  exactly one event (regression for double-emit).
- `TestCompressHistoryWith_MicroEmitsOnce` — micro path emits exactly one event
  after the internal-emission removal.
- `TestCompact_EmitsCompactEventWithInfo` — extend existing
  `TestAgent_CompactEmitsCompactEvent` to assert `Compaction.Strategy` and
  before/after pct.

App unit tests:
- `TestHandleAgentOutputEvent_CompactRendersBubble` — feed an `EventCompact`,
  assert `subs.chat` received a system message containing the strategy label.
- `TestRecordCompact_ClassifiesNewStrategies` — ceiling/selective → `compacts`,
  micro → `microCompacts`.
- Footer test — `c:…` segment appears once `Compacts > 0`.
- `TestRecordCompact_AppendsCompactionRound` — each `EventCompact` appends a
  `CompactionRound` (strategy, before/after pct, freed, removed, non-zero `At`)
  to session stats; `buildStatsLocked` copies it into `sessionStats.Compactions`.
- `TestClearStats_ResetsCompactionRounds` — `clearStats` empties
  `a.compactions`.
- Headless renderer test — `--plain` Summary emits one
  `-- compression N: …` line per round when `len(Compactions) > 0`.

Session-log test:
- Extend a `SessionStore.WriteEvent` test (or add one) asserting an
  `EventCompact` with `Compaction` survives JSONL round-trip.

#### 5. Validation steps

1. `go vet ./...`
2. `staticcheck ./...`
3. `gocognit -over 15 .`
4. `gocyclo -over 12 .`
5. `go test -count=1 -race -cover ./...`
6. Manual: run a session with compression disabled (the frigolite config),
   let context climb past 95%, verify: a conversation bubble
   `⚡ Context compacted (ceiling): 95% → 43% …` appears, the footer shows
   `c:…`, and the session JSONL contains a `"Type":"compact"` line with
   `Compaction` data.
7. Manual (session stats): after the same run, verify the headless `--plain`
   summary prints one `-- compression N: ceiling 95%→43% freed=… removed=…`
   line per round, and the TUI session stats carry `Compactions` (per-round
   records) in addition to the aggregate footer counter.

#### 6. Residual risks

- Moving micro's internal emission to callers requires updating
  `compressHistoryWithStrategy` (`agent_compression.go:697`) and verifying no
  test depends on `microCompactForced` emitting internally
  (`TestMicroCompactForced_ReducesTokensOnSmallHistory` does not assert events —
  confirmed).
- Double-emit in hybrid/overflow when they escalate to `Compact`: must ensure
  only the escalated `Compact` emits (pre-escalation elision/selective must
  not). Covered by `TestCompressHybrid_EmitsSingleCompactEvent`.
- `enforceContextCeiling` restructure changes its locking shape
  (`defer a.mu.Unlock()` → locked core + emit after unlock); callers at
  `agent_streaming.go:170,1559` keep the same signature. Race-safe because the
  emit path never runs under `a.mu`.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

---

## Teams feature: PHASE 4 — goal binding (feature/team branch)

Phase 4 threads the `Team` field through goal create/queue/promote and applies a
team overlay for the duration of a team-bound goal (TEAMS.md §5.1–5.2).

### Implemented
- `core/goal/model.go` — `CreateGoalInput.Team`, `GoalSnapshot.Team`,
  `UpcomingGoal.Team`, `UpcomingGoalInput.Team`, `goalStage.team`,
  `GoalEventRecord.Team`.
- `core/goal/mode.go` — `CreateGoal` stages `input.Team`; `RestoreCreate` reads
  `record.Team`; `toSnapshot` carries `state.team`.
- `core/goal_queue.go` — queue insert/append/prepend carry `Team`.
- `tools/goal/goal.go` — `/goal` tool `team` arg + schema property; create +
  enqueue pass `Team` into `CreateGoalInput`/`UpcomingGoalInput`.
- `core/commands/goal.go` + `internal/app/events.go` — resume/promote pass the
  queued goal's `Team` into the promoted `CreateGoalInput`.
- `core/goal_driver.go` — `TeamOverlayManager` interface; `syncTeamOverlay`
  applies the bound team's overlay while a team-bound goal is active and removes
  it when the goal clears (mirrors FreshContext per-goal tracking). Wired to the
  TeamManager in `subsystems.go`.

### Remaining (follow-up, non-blocking)
- `/goal:new --team` CLI flag (binding is by name string + tool arg today; an
  interactive picker is a follow-up).
- Missing-team → paused: today an undefined team name is a logged no-op (the goal
  runs session-default); a hard pause-on-undefined-team can be added if desired.
- Tests for the missing-team path once that contract is finalized.

### Phases already committed
- f963672 — phase 3: /team command (model-like), footer badge, /config CRUD
- 6681e1f — phase 2: TeamManager snapshot/restore + adapters
- c8661f0 — phase 1: teams config schema + validation
- f69048e / 78cc5af — docs: TEAMS.md spec + TEAMS-PLAN.md microsteps
