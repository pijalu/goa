# Bug Fix Plan — CM status segment shows token count

## Bug

The status bar CM part renders the exact missed-token damage:

```
CM:1·71,424
```

Expected: only the number of cases:

```
CM:1
```

## Root Cause

`internal/app/stats_footer.go` `formatCacheMissPart(full, partial,
missedTokens)` appends a dim `·N` suffix with the thousands-grouped missed
token count (lines 228–230). That suffix is what the bug wants gone. The
per-kind split (`CM:2|1`) stays — it IS the "number of cases".

The token damage figure remains available where it belongs: the per-turn
`/stats:cache` breakdown (`core/commands/stats_cache.go` renders
`CM: Full N (Xt) / Partial M (Yt)`) and the persisted session-summary JSON
(`cm_tokens`).

## Fix

1. `formatCacheMissPart`: drop the `missedTokens` parameter and the dim
   `·N` suffix. Update the doc comment.
2. `formatCacheMissPartIfAny`: stop forwarding `s.CacheMissedTokens`.
3. `groupThousands` becomes unused → remove it (it exists only for the CM
   token figure).
4. Tests:
   - `TestFormatCacheMissPart` — update golden strings (no `·N` suffix);
     drop the now-irrelevant "no token damage omits the dim suffix" case
     (folds into the bare-label case).
   - `TestBuildFooterStatParts_CacheMiss` — assert `CM:2|1` and explicitly
     assert NO `·` follows the counts.
   - `TestGroupThousands` — removed with the helper.
   - Token-damage *counting* tests (`tokenCacheMissedTokens` accumulators,
     JSON keys) are untouched — the data still flows to /stats:cache and
     session exports.

## Validation

1. Unit tests above pass.
2. `go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12`,
   `go test -count=1 -race -cover ./internal/app/...` — clean.

## Execution log

Status: **verified closed**.

### Changes

- `internal/app/stats_footer.go` — `formatCacheMissPart` drops the
  `missedTokens` parameter and the dim `·N` suffix; `groupThousands` removed
  (existed only for that suffix). The footer now renders counts only:
  `CM:1`, `CM:2|1`. Token damage remains in `/stats:cache`
  (`CM: Full N (Xt) / Partial M (Yt)`) and the session export (`cm_tokens`).

### Tests

- `TestFormatCacheMissPart` — golden ANSI updated (no `·N`), table now
  2-param; the "token damage" cases folded into the plain per-kind cases.
- `TestBuildFooterStatParts_CacheMiss` — asserts `CM:2|1` and explicitly
  rejects a `45,213` / `CM:2|1·` leak.
- `TestGroupThousands` — removed with the helper.
- Token-damage accounting tests (accumulators, `cm_tokens` JSON keys)
  untouched and passing — the data still flows to /stats:cache and exports.

### Terminal validation (guideline #5)

Rendered segment print-out: `"\x1b[38;2;248;81;73mCM:1\x1b[0m"` (red
`CM:1`) and red `CM:2` + orange `|1` — no token suffix.

### Quality gates (each run separately)

1. `go vet ./internal/app/...` — clean
2. `staticcheck ./internal/app/...` — clean
3. `gocognit -over 15 ./internal/app/` — clean
4. `gocyclo -over 12 ./internal/app/` — 2 pre-existing warnings in unrelated
   test files (`stream_capture_test.go`, `lsp_runtime_toggle_test.go`)
5. `go test -count=1 -race -cover ./internal/app/...` — pass (56.6%)
