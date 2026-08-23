# Archived — /stats:cache drops table lacks the cached-but-lost token delta

Closed: 2026-08-23 · Plan: `docs/plans/bugs-20260823-cache-drops-lost-tokens.md`

## Original report (bugs.md §5, user request)

**Observed:** the `# Cache drops` table showed turn, before/after rates, and
the fall in percentage points — but not how many tokens were cached and then
lost at the drop.

**Expected:** each drop row carries the token delta — tokens that were cached
but were lost (`prev cache-read − current cache-read`, full bust = the entire
previous cached prefix) — formatted consistently with the misses table.

## Fix

- `cacheDrop` gained `LostTokens`; `detectCacheDrops` fills it via the new
  `lostCachedTokens(prev, cur)` helper: `max(0, prev.CacheRead −
  cur.CacheRead)` over the same consecutive active turns the rates come from.
- `writeCacheDrops` renders a fifth column `Lost tokens` using the same
  `groupThousands` formatting as the misses table's `Tokens recomputed`.

Semantics: a full bust (read → 0) shows the entire previous cached prefix —
the same figure the misses table's full-miss row shows; a write-driven rate
fall with an intact/grown read shows 0 (nothing cached was lost).

## Tests

- `TestDetectCacheDrops_LostTokens` (table-driven): partial shed 400→250 =
  150; full bust = 400; write-driven fall with intact read = 0.
- `TestDetectCacheDropsSession`: full-bust sub-test also asserts LostTokens.
- `TestWriteCacheDrops`: pins the `Lost tokens` header and the grouped
  `8,000` figure.
- `TestStatsCommand_CacheView` skeleton updated to the 5-column drops row.

## Validation

- `go vet ./...` clean; `staticcheck ./...` no new findings (one pre-existing
  unrelated SA1019 in untouched `core/commands/model_test.go:198`).
- `gocognit -over 15 .` / `gocyclo -over 12 .`: no warnings in changed files
  (LostTokens cases split into their own table-driven test to stay in budget).
- `go test -count=1 -race -cover ./...` — exit 0.
- PTY filmstrip (goa TUI vs `e2e/mockllm`, `/stats:cache`): section renders,
  empty case shows "No cache drops detected." (mock reports no cache tokens;
  populated rows pinned by unit tests).
