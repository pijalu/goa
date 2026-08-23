# Fix plan — /stats:cache drops table lacks the cached-but-lost token delta

Source: `bugs.md` §5 (user request, 2026-08-23).

## Problem

The `# Cache drops` table shows turn, before/after rates, and the fall in
percentage points — but not how many **tokens** were cached and then lost at
the drop; the reader must cross-check the misses table to size the damage.

## Fix design

- `core/commands/stats_cache.go`:
  - `cacheDrop` gains `LostTokens int`.
  - `detectCacheDrops` computes it per drop via a new
    `lostCachedTokens(prev, cur)` helper: `max(0, prev.CacheRead −
    cur.CacheRead)` on the same consecutive active turns the rates come from.
    A full bust (read → 0) loses the entire previous cached prefix — the same
    figure the misses table's full-miss shows; a write-driven rate fall with
    an intact/grown read loses nothing (0).
  - `writeCacheDrops` renders a new `Lost tokens` column with the same
    `groupThousands` formatting as the misses table's `Tokens recomputed`.

No other sections change; per-agent/goal grouping untouched.

## Test approach (table-driven, existing file)

- `TestDetectCacheDropsSession` new sub-tests:
  - full bust → LostTokens = entire previous read prefix;
  - partial shed (read 400 → 250 with rate fall) → 150;
  - write-driven rate fall with intact read → 0.
- `TestWriteCacheDrops` pins the new column header `Lost tokens` and the
  grouped figure `8,000`.
- `TestStatsCommand_CacheView` skeleton updated to the 5-column drops row
  (`| T3 | 80.0% | 0.0% | 80.0 | 400 |`).

## Validation steps

1. `go vet ./...`
2. `staticcheck ./...`
3. `gocognit -over 15 .`
4. `gocyclo -over 12 .`
5. `go test -count=1 -race -cover ./...`
6. PTY filmstrip: real TUI against `e2e/mockllm`, `/stats:cache` — the drops
   table header must render `Lost tokens` in the box-drawn table.

## Acceptance

Drops rows show the cached-but-lost token delta consistent with the misses
table; gates pass; bug archived; committed.
