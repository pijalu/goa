# FIX — Average cache: token-weighted session average (footer CH 1st value)

> **Closed**: 2026-08-21 · commit `c85975b` (feature/multi-agent)
> **Source**: bugs.md report "Average cache"

## Report
The average cache should be calculated/weighted by the number of tokens cached — so a 10k token miss (0%) and a CH 5k token hit (100%) not show 50% but 33%.

```
newLevel = (currentLevel × currentTokens + latestLevel × latestTokens)
           / (currentTokens + latestTokens)
```

This global rate should show as 1st value in CH percentage (replace the last X)

## Fix plan (executed)
Root cause: the footer's first CH value is an unweighted mean of the last ≤10
per-round rates (`CacheHitTrend.window` / `AvgPct`) — every round counts the
same regardless of how many tokens went through the cache pipeline.
Interpretation note: the footer renders `CH:<avg>%▸<last>%`; the global
token-weighted rate becomes the **1st** value; the most-recent-round rate stays
as the 2nd (the "last") value.

Changes:
1. `internal/app/stats.go`: drop `window`/`AvgPct`/`AvgPrevPct`;
   `CacheHitTrend` gains `GlobalPct`, `GlobalPrevPct`, `GlobalHasPrev`.
2. `internal/app/stats_tokens.go`: running weighted level on App
   (`cacheHitGlobalLevel`/`cacheHitGlobalWeight`). Per round with prompt-side
   volume: rate = metrics.CacheHitPct(read, write, prompt);
   weight w = CacheRead+CacheWrite, falling back to PromptN when both are 0
   (goa normalizes PromptN to exclude cached tokens — computePromptN in the
   OpenAI completions parser — so an uncached miss still carries its full
   prompt weight; this makes the report's example exact);
   newLevel = (level·W + rate·w)/(W+w); W += w.
3. `formatLastCacheHitPart`: renders CH:<GlobalPct>%▸<Pct>%, each element
   colored by its own evolution (>=1pt grow bold green / >=5pt drop red).
4. `cacheHitTrendFromTotals` (orchestrator rows/headless): totals-derived rate
   already is token-weighted — feeds both GlobalPct and Pct.
5. `clearStats` resets the new accumulators.

## Test approach & validation results
- `TestCacheHitTrend_WeightedGlobal` — fold math incl. the report example:
  miss 10k → 0% (w=10000), full hit 5k → 100% (w=5000) ⇒ 33.3% (not 50%). PASS
- `TestCacheHitTrend_WeightedGlobal_NoOps` / `_Lifecycle` — zero-volume rounds
  skipped; clearStats resets; level surfaces in footer stats. PASS
- `TestFooterCH_GlobalWeightedFirst` — terminal-output validation through the
  real event path (`handleAgentOutputEvent`) reading rendered footer widget
  data: asserts `CH:33.3%▸100.0%` and rejects the old count-average 50%. PASS
- Updated color-contract suites (`TestFormatCacheHitPart_*`,
  `TestFormatFooterStats_*`) to the global/last element contract. PASS
- Gates (run separately): `go vet ./...` OK · `staticcheck ./...` OK ·
  `gocognit -over 15 .` only pre-existing warnings in untouched files
  (core/agentmanager_compression_test.go, core/commands/goal_command_manage_reorder_test.go,
  config/config_test.go — noted per guideline exception) · `gocyclo -over 12 .`
  no hits in changed files · `go test -count=1 -race -cover ./...` all green.
