# Archived — `/stats:cache` "last completions" lists turns, not API completions

Closed: 2026-08-23 · Plan: `docs/plans/bugs-20260823-stats-cache-per-completion.md`

## Original report (bugs.md §1)

**Observed:** the `# Cache hit — last completions` table in `/stats:cache`
(core/commands/stats_cache.go, `cacheTurnsFromHistory`/
`writeCacheHitLast10`) showed one row per **turn** — a turn flattens every
LLM call of the turn (main agent + sub-agents/skills that hit the API) into a
single aggregated row keyed by turn number. On turns with several calls the
last-10 window covered far fewer than 10 actual completions, and the per-call
hit rates were lost in the aggregate.

**Expected:** the table lists the last **10 completions** — each individual
API call (completion), newest last — with its own cache hit %, not the last
10 turns. Turn-level aggregation may stay in the per-turn average table
below; only the "last completions" window must switch to per-call records.

## Root cause

The session had no per-call log. `TurnRecorder.RecordTokenStats` REPLACES the
in-progress turn's usage snapshot (last call wins), and `RecordSubAgentTurn`
appends one row per sub-agent stats callback — so the turn series
`writeCacheHitLast10` sliced was turn-keyed. The per-call data existed only
transiently as one `EventTokenStats` per streaming round.

## Fix

- `core/turnrecorder.go`: new session-scoped `CompletionRecord` log —
  `RecordCompletion(role, goalID, usage, turnNumber)` +
  `CompletionHistory()` (defensive copy). `RecordSubAgentTurn` logs a
  completion per sub-agent call.
- `core/agentmanager_events.go`: `handleTokenStatsEvent` records every
  main-agent `EventTokenStats` as a completion tagged role/goal; turn record
  keeps its flattened (last-call) snapshot contract unchanged.
- `core/context.go` + `core/agentmanager_accessors.go`:
  `SessionRecorder.CompletionHistory()` exposed, nil-safe on `Context`.
- `core/commands/stats_cache.go`: section 1 now renders the group's last ≤10
  completions, oldest first / newest last, columns `Turn | Call | CH %`
  (`#k` = call ordinal within the turn). Filter switched from cache-active to
  "actually called the LLM" so 0% bust calls stay visible in the per-call
  window (per-turn/session-total sections keep their original filters).
  Grouping key `cacheGroupKey` shared by both series; turn-keyed sections 2–5
  unchanged.
- `multiagent/foreground_orchestrator.go`: corrected the stale doc comment
  on `forwardCacheStats` (fires per streaming round, not once per turn).

## Tests

- `core/turnrecorder_test.go`: `RecordCompletion` per-call log + defensive
  copy; sub-agent turn also logged as a completion.
- `core/agentmanager_events_test.go`: two `EventTokenStats` in one turn →
  two completions (role main, turn 1); turn snapshot still last-call;
  numbering continues across turns.
- `core/commands/stats_cache_test.go`: per-call rows with own CH% and
  ordinals, newest last; 12 completions → exactly the last 10; per-agent/goal
  sectioning of completion rows; fixtures updated to the 3-column shape.

## Validation

- `go vet ./...` — clean.
- `staticcheck ./...` — one pre-existing SA1019 in untouched
  `core/commands/model_test.go:198` (noted, unrelated).
- `gocognit -over 15 .` / `gocyclo -over 12 .` — no warnings in changed
  files (test split applied to stay under budget; pre-existing unrelated
  warnings remain elsewhere).
- `go test -count=1 -race -cover ./...` — all packages pass.
- Interactive PTY (filmstrip): goa TUI driven against `e2e/mockllm`; two
  prompts then `/stats:cache` — raw terminal stream shows the box-drawn
  `Turn | Call | CH %` table with `T1 #1` / `T2 #1` rows and per-turn table
  intact (`/tmp/goa-statscache-raw.log`).
