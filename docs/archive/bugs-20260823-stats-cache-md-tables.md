# bugs-20260823-stats-cache-md-tables — Stats cache view renders barcharts instead of a clear table

**Reported:** 2026-08-23 · **Fixed:** 2026-08-23 · **Commit:** feat(stats): render /stats:cache as Markdown tables (drop barcharts)

## Symptom

`/stats:cache` drew the cache-hit evolution as block barcharts — a vertical
█ chart for the last ≤10 completions and a 20-column horizontal █ bar per
turn — plus an ASCII-aligned drops table. The bars were hard to read
precisely, wasted vertical space, and bypassed the app's own MD table
rendering.

## Root cause

The view was written as hand-drawn ANSI art before the on-screen markdown
pipeline existed. Command output that looks like markdown routes through
`systemMessage → MDStreamRenderer`, which renders `|---|` tables as
box-drawn tables; the block-bar lines just degraded into noisy paragraphs.

## Fix plan (as planned in bugs.md before executing)

1. Rewrite `writeCacheHitLast10` to emit an MD table (`| Turn | CH % |`),
   newest last; drop the vertical chart.
2. Rewrite `writeCacheAvgPerTurn` rows as MD table rows
   (`| Turn | Tokens kT | CM | CH % |`); keep the exact numeric columns.
3. Convert `writeCacheMissList`
   (`| Turn | Kind | % of prefix | Tokens recomputed |`) and
   `writeCacheDrops` (`| Turn | Before | After | Δ |`) to MD tables.
4. Remove the chart helpers (`writeCacheChart`, `cacheBarHeights`,
   `writeCacheChartRow`, `cacheRowGutter`, `writeCacheRowCells`,
   `writeCacheChartBaseline`, `writeCacheChartLabels`, `latestCacheRates`,
   `cacheBarColor`, `cacheLevelColors`, `cacheChartRows`,
   `cacheChartCellW`, `cacheChartGutter`) and the unused band-color scheme.
5. Keep output markdown-looking so it routes through MDStreamRenderer.

Test approach: table-driven unit tests per section asserting no █ remains,
each section carries a valid `|---|` skeleton and the exact expected data
rows; one integration test feeding the full output through
`tui.NewMDStreamRenderer` asserting box-drawn table rows appear.

Validation steps: run new tests + package suite (+race); quality gates
separately; commit; archive.

## Validation

- Rewritten `core/commands/stats_cache_test.go`: all section tests pass,
  including `TestWriteCacheMDOutput_RendersThroughMarkdownPipeline`
  (rendered output contains box-drawing glyphs `│`, zero bar blocks).
- `go test ./core/commands -count=1` and `-race` — pass.
- Gates at baseline: go vet clean; staticcheck only the known unrelated
  SA1019; gocognit/gocyclo findings identical to the pre-existing set
  (the rewritten view test was split below the complexity budget).

## Notes

- Session-total line stays a heading+text line (no tabular data).
- Numeric semantics are unchanged: same rates, kT volumes, cumulative CM
  counters, drop detection thresholds, and thousands grouping.
