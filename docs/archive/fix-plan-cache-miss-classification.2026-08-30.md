# Fix plan — Cache-miss classification: context resets are not full cache misses

**Reported:** 2026-08-30 (bugs.md) · **Status:** DONE (2026-08-30)

## Problem

Intentional context resets — starting a new (fresh-context) goal, running
/summarize (compaction) — legitimately invalidate the provider cache prefix.
They bring context gain (a summarized, smaller conversation) and are not a
cost, yet they were reported as faults:

- The round immediately after a summarize compaction that reads 0 cache
  tokens incremented the footer's **full** miss counter
  (`applyTokenTimingsLocked`, `internal/app/stats_tokens.go`): only
  `EventContextReset` (fresh-context goal begin) re-armed the detector;
  `EventCompact` did not.
- The /stats:cache report's miss scan (`scanMisses`/`missScan.Full`,
  `core/commands/stats_cache.go`) had NO reset signal at all: a 0-read
  round after cache activity was always a FULL miss, so the same
  post-summarize / post-goal round showed up as a fault row in the misses
  table and the Global statistics headline.
- Session records carried no marker of an intentional reset
  (`CompletionRecord`/`TurnRecord`), so the report could not exempt those
  rounds even conceptually.

## Target semantics (final, per user correction)

The **"full" miss category becomes "unexpected"**. Exemption set is
**intentional resets ONLY** — a summarize/new-goal is not a cost because it
brings important context gain:

- **Exempt (re-arm, never counted):**
  - `EventContextReset` — fresh-context goal begin.
  - `EventCompact` with strategy `summarize` ONLY.
- **Costs (busts after them count as unexpected):** every other
  compaction — micro, tool_elision, selective, hybrid, remote_compact,
  fresh_window, and the text fallbacks (overflow, truncation,
  tool_result_pruning, hard fallback, empty/unknown strategy). Cache-miss
  after any compaction outside of summarize is a real cost.
- A miss counts as **unexpected** when the prefix was still valid and the
  provider failed to serve it (zero-read round on an established,
  un-reset prefix — TTL expiry, eviction, non-summarize compaction bust).
  Micro-compaction busts therefore count as unexpected.
- **Partial** misses (drop beyond tolerance with an intact partial hit)
  keep today's semantics.

History note: an intermediate implementation exempted all "non-micro"
strategies; the user corrected this — only summarize is free, everything
else is a cost. The shipped code implements the summarize-only rule.

## Changes

### 1. Live footer (`internal/app`)

- `handleAgentStatsEvent`: on `EventCompact`, after `recordCompact`, call
  `resetCacheBustBaseline()` when the strategy IS summarize
  (`isSummarizeCompaction`). Every other strategy leaves the detector
  armed, so its bust is counted.
- Rename accumulator and stat field to match the new semantics:
  `tokenCacheFullMisses` → `tokenCacheUnexpectedMisses`,
  `sessionStats.CacheMissesFull`/`cm_full` →
  `CacheMissesUnexpected`/`cm_unexpected` (schema pinned by test, key is
  internal-only). Footer `CM:X|Y` label stays (compact); the first count
  now means unexpected. `[stats]` stream line updated to the renamed
  fields (same format).

### 2. Reset marker in session records (`core`)

- `TurnRecorder.MarkContextReset()`: latches a pending flag, consumed by
  the next `RecordCompletion`/`FinalizeTurn` so the first record of the
  new conversation carries `ContextReset: true` (sub-agent records do not
  consume it).
- `AgentManager.handleTypedEvent`: `EventContextReset` → always mark;
  `EventCompact` → mark only when `summarizeCompactionEvent` (structured
  `CompactionInfo.Strategy` wins over `ev.Text`; equals `summarize`).
- `CompletionRecord.ContextReset` / `TurnRecord.ContextReset` bool fields.

### 3. Report scan (`core/commands/stats_cache.go`)

- `cacheTurn` gains `Reset bool`; both series mappers pass it through.
- `scanMisses`: an entry with `Reset` set starts a NEW conversation — no
  miss classified for it and `prev`/`established` restart from zero, so
  later rounds compare inside the new conversation only.
- Terminology: `missScan.Full()` → `Unexpected()`, `cacheMissTurn.full` →
  `unexpected`, `missTotals.Full` → `Unexpected`, miss-table kind and the
  headline render "unexpected"/"partial". Legacy sessions without the
  marker behave exactly as today for non-reset rounds.

### 4. Tests

- `internal/app/stats_cm_test.go`: renamed to the unexpected accumulator;
  `TestHandleTokenStats_CacheMissCompactionExemption` — summarize
  zero-read → 0|0; summarize collapse → no partial; summarize keeps prior
  miss/session totals; micro bust → 1 unexpected; selective bust → 1
  unexpected; truncation → partial; TTL bust after summarize
  re-establishment → 1 unexpected.
- `core/turnrecorder_test.go`: `TestTurnRecorder_ContextResetMarker`
  (latch + consume by completion/finalize); `TestSummarizeCompactionEvent`
  (summarize true; micro/tool_elision/selective/truncation/fresh_window
  false; payload Strategy wins over Text).
- `core/commands/stats_cache_test.go`: `TestScanMissesResetBoundary`
  (reset-marked entry breaks the chain — no miss, next conversation
  classified fresh; non-summarize bust still unexpected);
  `TestWriteCacheMissListUnexpectedKind` renders "unexpected" and "No
  cache misses detected." across a reset boundary.

### 5. Validation results

1. `go test -count=1 -race -cover ./...` — zero failures (core 78.3%,
   core/commands 63.8%, internal/app 60.7%; all packages ok).
2. Gates, each run separately: `go vet ./...` clean; `staticcheck ./...`
   clean; `gocognit -over 15 .` clean (nothing over 15);
   `gocyclo -over 12 .` reports only the pre-existing, unrelated
   `TestRetryConfigSetters core/commands/config_test.go:66:1` (13) —
   explicitly allowed per bugs.md guidelines.
3. Live TUI validation via ptydrive + e2e mockllm (mock gained a
   backward-compatible `MOCK_ZERO_AT` knob: chosen request ordinals report
   `cached_tokens=0`, plus ordinal/cached request logging). One session,
   sends: hello, again, /compress:summarize, postsum, rewarm,
   /compress:micro, postmicro, /goal:new …, /stats:cache — with zeros
   landing on postsum (r4), postmicro (r6), goal turn 1 (r7):
   - post-summarize zero read (r4): NOT counted — per-turn `CM 0-0`
     despite `CH 0.0%` (boundary re-arm);
   - post-micro zero read (r6): counted — per-turn `CM 1-0`, miss row
     `| T5 | unexpected | 100.0% | 2,660 |` (= prior round's read);
   - fresh-context goal turn zero read (r7): NOT counted — new session
     segment renders "No cache misses detected.";
   - headline: "Missed cache tokens: 2,660 across 1 exchange(s) (1
     unexpected, 0 partial)"; footer settled at `CM:1` (partial hidden
     at 0). The summarize sub-call itself is not a tracked turn and did
     not disturb counters.
4. Plan archived to `docs/archive/`, bugs.md reduced to guideline-only,
   work committed.
