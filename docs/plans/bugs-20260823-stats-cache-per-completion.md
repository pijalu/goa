# Fix plan — `/stats:cache` "last completions" must list API completions, not turns

Source: `bugs.md` §1 (2026-08-23).

## Problem

`# Cache hit — last completions` in `/stats:cache`
(`core/commands/stats_cache.go::writeCacheHitLast10`) slices the **turn** series
to 10 rows. A turn is not a completion: the main agent's tool loop performs one
LLM API call per round (each round ends with its own `EventTokenStats` — see
`internal/agentic/agent_turn_stats.go` provider-usage path and the app-level
dedupe comment in `internal/app/stats_tokens.go`), and sub-agent/companion turns
arrive via `RecordSubAgentTurn`. The turn record, however, keeps only the stats
of the **last** stats event seen in the turn (`TurnRecorder.RecordTokenStats`
replaces `turnTokenUsage`). Consequences:

- the last-10 window covers far fewer than 10 actual completions when turns are
  multi-call,
- per-call hit rates are lost,
- rounds other than the last of each turn are invisible in every cache section.

## Fix design

Record every `EventTokenStats` as an individual **completion** in a
session-scoped log next to the turn history, and make section 1 of
`/stats:cache` read that log. Turn-keyed sections (per-turn table, session
total, misses, drops) keep today's semantics (bug explicitly allows this).

1. `core/turnrecorder.go`
   - new `CompletionRecord{TurnNumber, PromptN, CacheRead, CacheWrite,
     AgentRole, GoalID}` — one LLM API call.
   - `TurnRecorder.completions` + `RecordCompletion(role, goalID, u)` +
     `CompletionHistory()` (copy, like `TurnHistory`).
   - `RecordSubAgentTurn` logs a completion as well (it fires once per
     sub-agent `EventTokenStats` already).
2. `core/agentmanager_events.go::handleTokenStatsEvent` — after
   `RecordTokenStats`, call `RecordCompletion("main", am.currentGoalID(), …)`
   so each main-agent round lands as its own completion.
3. `core/agentmanager_accessors.go` + `core/context.go` — expose
   `CompletionHistory()` (nil-safe on `Context`), extend
   `core.SessionRecorder`.
4. `core/commands/stats_cache.go`
   - `cacheCompletionsFromHistory([]core.CompletionRecord) []cacheTurn`.
   - grouping: same (AgentRole, GoalID) key function applied to both series;
     the group struct carries `turns` and `completions`.
   - `writeCacheHitLast10` renders the last ≤10 **completions** of the group,
     oldest first / newest last, one row per API call with its own CH%.
     Columns: `Turn` + `Call` (1-based ordinal of the call within that turn in
     the group) + `CH %` — multi-call turns stay distinguishable.
5. Comment fix (accuracy only, no behavior): the stale
   `multiagent/foreground_orchestrator.go::forwardCacheStats` doc claim
   "fires once per turn with the turn's cumulative timings" — it fires per
   round for provider-usage streams; adjust wording.

No new setting, no YAML, no TUI changes.

## Test approach

- `core/turnrecorder_test.go`
  - `RecordCompletion` appends; `CompletionHistory` returns a copy (mutating
    the result must not affect the recorder).
  - `RecordSubAgentTurn` also appends a completion with the same role/goal.
- `core/agentmanager_events_test.go` (or adjacent): feed two
  `EventTokenStats` for one in-progress main turn → two completion records,
  both role `main`, turn number = in-progress turn, per-call usage preserved.
- `core/commands/stats_cache_test.go` (table-driven)
  - a 3-call main turn + sub-agent turns: last-completions table shows one row
    per call, own CH%, call ordinals `Tn #k`, newest last;
  - 15 completions → exactly 10 rows, the LAST 10 (newest kept);
  - empty completion log → "No cache activity recorded yet.";
  - turn-keyed sections unchanged (`assertCacheViewSkeleton` still passes with
    fixtures that now also carry completions).
- Update `fakeSessionRecorder` to implement the new interface method.

## Validation steps

1. `go vet ./...`
2. `staticcheck ./...`
3. `gocognit -over 15 .`
4. `gocyclo -over 12 .`
5. `go test -count=1 -race -cover ./...`
   (each run separately per bugs.md guideline; pre-existing warnings only
   acceptable when unrelated and noted)
6. Manual/visual: `/stats:cache` in a session with a multi-round turn shows
   `#k` call ordinals and >1 row for that turn — validated through the unit
   fixtures + markdown-pipeline render test already covering the view.

## Acceptance

- Last-completions window = last 10 API completions, newest last, per-call CH%.
- Turn-level tables keep aggregating per turn.
- All quality gates pass; bug entry archived to `docs/archive/`.
