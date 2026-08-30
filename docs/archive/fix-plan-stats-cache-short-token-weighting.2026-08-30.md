# Fix plan — `/stats:cache:short` + token-size weighting of all stats percentages

**Reported:** 2026-08-30 (bugs.md) · **Status:** in progress

## Bug 1 — footer CH global fold under-weights OpenAI-style rounds

### Root cause

`foldCacheHitGlobalLocked` (`internal/app/stats_tokens.go`) weights each round
by `read+write`, falling back to `promptN` when that is 0. The correct weight
is the round's `CacheHitPct` denominator:

- `write > 0` (Anthropic-style): `read+write` — already correct.
- `write == 0` (OpenAI-style): `read+prompt` — today a round with `read > 0`
  weights by `read` only, dropping the uncached prompt the rate was computed
  over. A bust round (read 137, uncached 2663) weighted 137 instead of 2800,
  inflating the footer's session-wide CH to 72.6% where the honest
  token-weighted rate was 41.88% (live e2e observation, 2026-08-30).

### Fix

Weight = `write>0 ? read+write : read+prompt`. Update the function comment.
Pinned cases stay identical: `(0,300,100)`→w=400, `(10000,0,0)`→w=10000,
`(0,5000,0)`→w=5000 (fallback), 33.3% combined example unchanged. Only
`write==0 && read>0 && prompt>0` rounds change weight.

### Audit of the other percentage surfaces (no change needed)

All sum raw counters first, then apply `CacheHitPct` once over the sums —
within a single provider style that IS exact token weighting:

- `core/commands/usage.go` (project/session/verbose header lines)
- `core/commands/transparency.go` `writeSummaryStats`
- `tui/orchestrator/content.go` orchestrator stats footer
- `internal/app/stats.go` `cacheHitTrendFromTotals`
- `core/commands/stats_cache.go` `writeSessionTotalLine` (the honest 41.88%)

Per-call rates (`cacheTurnRate`, footer `lastCacheHit.observe`) keep pinned
per-call semantics. Cross-provider mixing in one session is out of scope
(one provider per session; `CacheHitPct` branch semantics are test-pinned).

## Bug 2 — no `/stats:cache:short`

### Root cause

`StatsCommand.CompleteArgs` / `Run` (`core/commands/transparency.go`) only
route `cache`. No short variant exists.

### Fix

- Route `cache:short` (and `:cache:short`) → `runCacheStatsShort`.
- Completion entry `cache:short` — "session-wide cache totals only".
- `showCacheStatsShort` (`core/commands/stats_cache.go`): same empty-history
  gate and data extraction as `showCacheStats`, then ONE combined
  `cacheGroup{turns, completions}` (all groups) rendered through
  `writeCacheGlobalStatistics` ONLY — exactly one session-wide
  `## Global statistics` block: no group `#` headers, no last-10 /
  per-turn / misses / drops tables. The combined group's authoritative
  series is the per-call log when present, else turns (same rule as
  per-group view), so the total and missed-tokens headline always agree
  with the full report's per-group blocks.

## Test approach (RED first)

1. `internal/app` — `TestFoldCacheHitGlobal_OpenAIDenominatorWeight`:
   feed `(prompt=0, read=300, write=100)` then `(10000, 500, 0)`.
   Expect global ≈ **7.34%** (= Σread/Σdenom = 800/10900); the old code
   yields 35.98%. Existing pinned tests
   (`TestCacheHitTrend_WeightedGlobal`, `TestFooterCH_GlobalWeightedFirst`)
   must stay green unchanged.
2. `core/commands` — short-view tests:
   - multi-group fixture (main + companion, two goals) → exactly one
     `## Global statistics`, one `Session total:` line, and NONE of
     `# `, `## Last 10 exchanges`, `## Cache usage per turn`,
     `## Cache misses`, `## Cache drops`;
   - token-weighted combined total: `(0,300,0)` + `(10000,500,0)` →
     **7.41%** over 2 LLM calls;
   - empty history → "No turn history available. Send a message first.";
   - no-cache-traffic → "No prompt-cache activity" line, no "Session total:";
   - `CompleteArgs("")` proposes `cache:short`; `Run(ctx, ["cache:short"])`
     renders the short view.
3. Keep every session-1 regression test green
   (`core/commands/stats_cache_test.go`).

## Validation

1. Gates, each run separately: `go vet ./...`, `staticcheck ./...`,
   `gocognit -over 15 .`, `gocyclo -over 12 .`,
   `go test -count=1 -race -cover ./...`.
   Pre-existing warning acceptable only if unrelated and noted:
   `TestRetryConfigSetters core/commands/config_test.go:66:1` (gocyclo).
2. Live TUI (ptydrive + e2e mockllm, MOCK_BUST_EVERY=4, FILLER_KB=2):
   - `/stats:cache:short` prints exactly the Global statistics block —
     "Session total: 41.88% cache hit (token-weighted over 4 LLM calls)",
     "Missed cache tokens: 1,763 across 1 exchange(s) (0 full, 1 partial)",
     nothing else;
   - footer CH global tracks the honest rate (≈41.9%), not 72.6%.

## Results

**Fixed & validated 2026-08-30.**

- `foldCacheHitGlobalLocked` now weights every round by its CacheHitPct
  denominator (`write>0 ? read+write : read+prompt`).
  `TestFoldCacheHitGlobal_OpenAIDenominatorWeight`: RED 35.98%/w900 →
  GREEN 7.34%/w10900; all previously pinned fold cases unchanged.
- `/stats:cache:short` routed through `runCacheStatsView` +
  `cacheShortRequested` (the router splits user input on every colon, so
  "/stats:cache:short" arrives as args ["cache" "short"] — the live first
  attempt rendered the full view and exposed this; now unit-pinned in
  `TestCacheStatsShort_Routing` together with the router contract).
- `writeCacheGlobalStatistics` now labels the legacy turn-series fallback
  "(token-weighted over N turns)" instead of the mislabeled "LLM calls".
- Gates each run separately: `go vet` clean; `staticcheck` clean;
  `gocognit -over 15` clean; `gocyclo -over 12` only the pre-existing,
  unrelated `TestRetryConfigSetters core/commands/config_test.go:66:1`;
  `go test -count=1 -race -cover ./...` all green.
- Live TUI (ptydrive + e2e mockllm): `/stats:cache:short` printed exactly
  one Global statistics block — "Session total: 60.90% cache hit
  (token-weighted over 4 LLM calls)" + "Missed cache tokens: 3,283 across
  1 exchange(s) (0 full, 1 partial)" — and nothing else; the footer's CH
  global settled at 60.9%, matching the report total. In a second session
  both views agreed (63.97% over 4 LLM calls, footer CH 64.0%): short view,
  full view, and footer now all report the same honest token-weighted rate.
