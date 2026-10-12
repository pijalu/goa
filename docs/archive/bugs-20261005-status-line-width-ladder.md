# Status line clipped: no width ladder, model name cut mid-word

**Status:** fixed & validated (2026-10-05). Fix commit `2a997e4d` (tracker id B11).

## Report

**Observed.** With a live session the line-2 status bar overflows the terminal
and its right edge is clipped by the screen:

```
↑291.6K ↓130.4K 72.5 tok/s CH:99.0%▸99.4% TC:190 $0.6259 28.1%/1.0M                   (opencode-go) deepseek-v4.1-fla
```

The model name arrives cut mid-word with no ellipsis, and the composed line is
wider than the terminal, so the final column is lost to the screen edge as well.
The information that gets sacrificed is the best-shaped information: the model
identity — the thing the line exists to name.

**Expected.** Graceful degradation: low-value fields drop as the width shrinks,
the model name is ellipsized rather than cut, and the context/quota figure
survives to the end. Minimal form:

```
28.1%/1.0M  deepseek-v4.1-flash • xhigh • [41%]
```

Drop order, low → high priority (user-specified):

| # | field | example |
|---|-------|---------|
| 1 | amount | `$0.6259` |
| 2 | provider | `(opencode-go)` |
| 3 | up/down counters | `↑291.6K ↓130.4K` |
| 4 | tool count | `TC:190` |
| 5 | token speed | `72.5 tok/s` |
| 6 | last CH | `▸99.4%` |
| 7 | avg CH | `CH:99.0%` |
| 8 | ellipsize the model name | `deepseek-v4.…` (thinking badge drops here too) |
| 9 | quota / context | `28.1%/1.0M` — never dropped |

## Root cause

- `tui/footer_render.go` `buildLeftSide` returned `f.data.Stats` verbatim: the
  stats side had no width awareness, so it could consume the whole terminal.
- The only ladder (`compactRightSide`) shortened the *right* side alone and its
  last resort was `truncateToWidth(right2, targetW, "")` — an **empty** ellipsis,
  i.e. a hard cut, producing `deepseek-v4.1-fla`.
- The model's budget was clamped **upward**: `availW := width - leftW - minPad;
  if availW < 30 { availW = 30 }`. The display was built with the provider
  prefix and thinking badge even when almost nothing was left, and the later
  compaction paid for the over-claim by cutting the model name instead of the
  droppable fields (visible in the web UI sweep: at 768px the provider was
  dropped at a fixed threshold while the line still fitted comfortably).
- `renderTwoCol` forced `pad = 1` whenever the two sides already overflowed, so
  the assembled line was wider than `width` and the screen clipped the tail.

Reproduced in the web UI (`agent-browser`, `goa server`, kimi-code/k3-256k,
one real turn) at 480px: `↑2.2K ↓84 5.8 tok/s CH:75.2%▸97.3% TC:1 1.7%/262.1K k3`
and at 400px: `↑2.2K ↓84 5.8 tok/s CH:75.2%▸97.3% TC:1 1.7%/` — the quota figure
itself cut mid-value. A pixel map of the status band showed the ink running to
the viewport edge and stopping dead.

## Fix

One ladder for the whole line, driven by tiered segments:

- `tui.FooterSegment{Tier, Text, Glue}` plus the exported tier constants
  (`FooterTierAmount` … `FooterTierQuota`). `Glue` keeps fields that read as one
  cell attached (`CH:99.0%▸99.4%`) while remaining independently droppable.
- `Footer.fitStatusLine` applies the drop table in the order above, then
  ellipsizes the model name, then — only as a last resort on a very narrow
  terminal — truncates the model side; `renderTwoCol` can no longer emit a line
  wider than the terminal.
- `internal/app` emits the stats as tiered segments (`buildFooterStatSegments`,
  split into `buildTokenSegments` / `buildCacheSegments`) while
  `formatFooterStats` keeps its exact byte output for headless and orchestration
  consumers.
- The provider prefix and thinking badge are no longer gated by independent
  `availWidth` thresholds: the ladder is the single authority on what fits.
  `compactRightSide` was removed; its strip steps are now the ladder's tier-8
  actions.
- `FooterData.StatsSegments` is optional: a caller that only sets `Stats`
  behaves exactly as before (single never-dropped segment).

## Validation

- `TestStatusLineNeverExceedsWidth` — every width 12..200 produces a line that
  fits it (the invariant that was violated).
- `TestStatusLineDropOrderIsTheLadder` — survival-width monotonicity: each
  higher-priority field survives to a strictly narrower terminal than the field
  below it (`$0.6259` → `(opencode-go)` → `↑291.6K` → `TC:190` → `72.5 tok/s` →
  `▸99.4%` → `CH:99.0%`).
- `TestStatusLineKeepsModelNameAndQuota` / `TestStatusLineQuotaOutlivesEveryStat`
  — the model identity and the quota figure survive every width, the name marked
  as shortened rather than cut.
- Live web-UI re-validation after the fix (same session shape, same widths):
  480px → `CH:83.8%▸97.1% 1.7%/262.1K k3-256k • xhigh • [12%|44%]`;
  400px → `1.7%/262.1K k3-256k • xhigh • [12%|44%]`. Quota and identity intact,
  stats dropped in order.
- Gates: `go vet ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`,
  `staticcheck ./...` (only the two pre-existing `plugins/` findings, unrelated),
  `go test -count=1 -race ./...` green.

## Residual

- The ladder ranks the *stats* fields; plugin-contributed segments, the
  companion label, the thinking badge, the companion cycle count and the
  activity word are all tier-8 decorations, removed just before the model name
  is shortened. Their relative order among themselves is unchanged from the
  pre-B11 strip sequence.
- A terminal narrower than the model identity plus the quota figure still has to
  hard-truncate (nothing droppable remains); the line stays inside the terminal
  and keeps the ellipsis marker.
