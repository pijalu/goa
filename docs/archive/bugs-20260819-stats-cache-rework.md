# Bug Fix Plan — /stats:cache rework (per-agent/goal sections)

## Bug

`/stats:cache` is incomplete. Today it shows a flat "# Cache misses" dump
(`T1 - CM: Full 0 (0t) / Partial 0 (0t) T2 - …` on one line) plus a single
horizontal chart line (`T2 - CH: 63.74% |████ No cache drops detected.`).

Required sections:

1. **Last 10 cache-hit levels** as a vertical bar chart, exact percentage
   under each bar, color-coded red <90%, orange <95%, green ≥95%, bar/label
   correctly centered.
2. **Average cache per turn** as horizontal bars (block chars), same color
   code, e.g. `T1: 85.98% ████████`.
3. **Weighted total cache percentage** for the complete session.
4. **List of all cache misses**: Turn — miss % vs non-miss + miss size in
   tokens.

Key point: stats must work across multi-agent/multi-goal — repeat the
sections per "goal/agent".

## Root cause / gap analysis

- The view (`core/commands/stats_cache.go`) renders only the main agent's
  flat history: `writeCacheMisses` is a wall-of-text one-liner, there is no
  vertical last-10 chart, no weighted total, and the miss list lacks the
  percent-vs-non-miss figure.
- `TurnRecord` (`core/agentmanager.go`) carries NO agent/goal identity, and
  the `TurnRecorder` only ever sees the MAIN agent: token stats land via
  `AgentManager.handleTokenStatsEvent`, companion/team stage agents run in
  `multiagent.AgentPool` with their own conversations and never touch the
  recorder. So per-agent sectioning is impossible without instrumentation.

## Design

### Instrumentation (core)

1. `TurnRecord` gains `AgentRole string` (""/"main" for the main agent) and
   `GoalID string` (active goal at finalize time).
2. `TurnRecorder` gets a lightweight, identity-aware accumulation:
   - Main-agent path unchanged (`RecordTokenStats` … `FinalizeTurn`).
   - New `RecordSubAgentTurn(role, goalID string, u TurnTokenUsage)` that
     appends a completed turn directly (sub-agent turns are observed
     end-of-turn via their token stats, not accumulated incrementally).
3. `AgentManager.finalizeTurn` tags the main record with the active goal ID
   (via a new `goalIDProvider` hook wired from the app).
4. `multiagent`: a per-agent cache-stats observer, registered in
   `makeAgentCreatedHook` (covers both `GetOrCreate` and `CreateTaskAgent`),
   that forwards each agent's final `EventTokenStats` (role + the
   orchestrator's bound goal ID) to a callback the app installs
   (`App.observeSubAgentCacheStats` → `TurnRecorder.RecordSubAgentTurn`).
   Companion, workflow stage agents, and team members all flow through the
   pool hook — one instrumentation point.

### View (core/commands/stats_cache.go)

Group the `cacheTurn` series by (AgentRole, GoalID), in first-appearance
order, and render the four sections per group (a single group — the common
solo case — renders without a header, keeping today's look):

1. `writeCacheHitChartLast10` — vertical bars over the last 10 cache-active
   completions (reuse the existing band chart geometry, `cacheChartRows`),
   percentage labels centered under each bar (bars widen to fit labels),
   colored with `ansi.Fg` by band: red `<90`, orange `<95`, green `≥95`.
2. `writeCacheAvgPerTurn` — horizontal block bars of the weighted average
   per turn (per-turn rate is `metrics.CacheHitPct`; the average is
   token-weighted across the group's turns), same color code.
3. `writeCacheSessionTotal` — one line: `Total: NN.NN% (weighted by prompt
   tokens over N turns)`.
4. `writeCacheMissList` — one line per miss:
   `T3 — 12.4% of prefix missed · 8,192 tokens` (percent = missed /
   prevCacheRead; full misses = 100%).

The existing `writeCacheDrops` table is retained (it is the "miss list"
companion at rate granularity); the new miss list adds the token sizes the
bug asks for.

### Color thresholds

Shared helper `cacheLevelColor(pct)`: red `#f85149` <90, orange `#d29922`
<95, green `#3fb950` ≥95 — replaces the old 5-band `cacheBarColor` for these
sections (footer CH colors unchanged).

## Tests

- `turnrecorder_test.go`: identity tagging on main finalize;
  `RecordSubAgentTurn` appends a fully-formed record with role/goal.
- `stats_cache_test.go` (existing): keep current single-group tests passing;
  add golden tests for the 4 sections: last-10 chart (centering + colors),
  per-turn average bars, weighted total, miss list (percent + tokens), and a
  two-agent fixture proving sections repeat per agent/goal.
- `internal/app`: a companion-turn fixture proving sub-agent cache stats land
  in the recorder with the companion role.

## Validation

- All new + existing tests pass under `-race`.
- Terminal validation: render `/stats:cache` on a synthetic session and
  inspect the actual output.
- Gates (each separately): `go vet`, `staticcheck`, `gocognit -over 15`,
  `gocyclo -over 12`, `go test -count=1 -race -cover ./...`.

## Execution log

Status: **verified closed**.

### Instrumentation (core / multiagent / internal/app)

- `core.TurnRecord` gained `AgentRole` + `GoalID`.
- `TurnRecorder.FinalizeTurn(agent, goalID)` tags the main record
  (role `main`); new `RecordSubAgentTurn(role, goalID, usage)` appends
  completed sub-agent turns in the shared numbering.
- `AgentManager.SetActiveGoalIDProvider` + `currentGoalID()` wire the live
  goal ID into `finalizeTurn`.
- `multiagent.ForegroundOrchestrator`: `SubAgentCacheUsage` +
  `SetCacheStatsCallback` + `forwardCacheStats` — the per-agent pool observer
  relays each sub-agent's final `EventTokenStats` (companion, workflow stage,
  team member all flow through `makeAgentCreatedHook`).
- `internal/app/subsystems.go` `wireCacheStatsIdentity` connects the callback
  → `RecordSubAgentTurn` and the goal-ID provider; extracted from
  `InitSubsystems` to keep gocognit under budget.

### View (core/commands/stats_cache.go)

- `groupCacheTurns` partitions by (AgentRole, GoalID) in first-appearance
  order; solo sessions render header-less (today's look preserved).
- `writeCacheHitLast10` — vertical last-10 chart, exact % centered under each
  bar, band-colored (red <90 / orange <95 / green ≥95 via `cacheLevelColor`).
- `writeCacheAvgPerTurn` — horizontal block bars per cache-active turn.
- `writeCacheSessionTotal` — token-weighted session percentage.
- `writeCacheMissList` — per miss: kind, % of prefix, grouped token figure
  (new `cacheMissTurn.prev` + `groupThousands`).
- `writeCacheDrops` retained.

### Tests

- `turnrecorder_test.go`: identity tagging + sub-agent ingestion.
- `foreground_orchestrator_cache_test.go`: observer → callback relay (role,
  goal, usage) + nil/non-token no-ops.
- `stats_cache_test.go`: band thresholds, last-10 centering/color cap,
  per-turn bars, weighted total, miss list, multi-agent section repeat +
  solo no-header; `TestStatsCommand_CacheView` updated to the new sections.

### Terminal validation

Rendered a two-agent fixture — output shows `## main · goal:g1` /
`## companion · goal:g1` sections, the vertical chart with labels under the
bars, per-turn horizontal bars, weighted totals, and the miss list with
`100.0% of prefix · 850 tokens`. ANSI colors confirmed per band.

### Quality gates (each separately)

1. `go vet` — clean
2. `staticcheck` — clean
3. `gocognit -over 15` — clean (`InitSubsystems` back under after extraction)
4. `gocyclo -over 12` — clean on changed files
5. `go test -count=1 -race -cover` — core/commands 61.9%, core 74.1%,
   multiagent 68.1%, internal/app 56.6% — all pass.
