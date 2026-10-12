<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B16 — Provider Quotas showed *that* a window would run out, not *when*

Closed 2026-10-05. Archived from `bugs.md`.

## Reported

Requested 2026-10-05: "/quota's Provider Quotas should show the time of run-out as
part of the over-budget state: exhausted at XX:XX".

## Root cause

The plugin already computed the projection but threw away the most useful half of
it. `projectedRatio` (`plugins/bundled/provider-quota/plugin.js`) scales
`used/limit` by the elapsed fraction of the window (from `resetsAt` + `periodMs`),
and that ratio drove both the "At reset" column (`atResetPct`) and the Status
words (`windowStatus` → `over budget` when > 1). Knowing *that* the window will
not survive to its reset — but printing only a percentage — leaves the user to
subtract a countdown from a rate. The footer segment carries percentages and
colours only, so it cannot answer either.

## Fix

* **One projection, two renderings.** `runOutAtMs(lim)` derives the exhaustion
  instant from the *same* ratio: at the current pace the window would end with
  `projected` used instead of `limit`, so the budget is consumed after the
  matching fraction of the window — `windowStart + period/projected`. Deriving it
  from the ratio (rather than a second rate calculation) is what keeps the words
  and the time next to them from disagreeing.
* **The Status cell names it**, in the user's local time, to the minute:
  `over budget — exhausted at 14:32`. `format.clock(ms)` builds `HH:MM` from the
  Date fields rather than `toLocaleTimeString`, so the fixed-width table renders
  the same shape in every locale.
* **Degrades rather than invents.** With no `resetsAt`/`periodMs`, a zero elapsed
  span, or a window already past its reset, `runOutAtMs` returns null and the cell
  keeps today's `over budget`. Windows inside their budget are untouched
  (`plenty of room`, `close to limit`).
* **Machine-readable too.** `/quota:json` carries `exhaustedAt` (epoch ms, or
  null) on every limit via `limitsWithRunOut`, so a script does not parse the
  sentence.

## Validation

`plugins/quota_runout_test.go` drives the shipped plugin (loaded from the source
tree by the existing quota harness, same file the bundle embeds) with a seeded
cache and a pinned clock (`Date.now`), so the assertion is arithmetic, not timing:

* 90% used 4 h into a 5 h window → runs out 4 h 26 m 40 s into the window, i.e.
  26 m 40 s from now: the row must read `over budget — exhausted at 09:26` *and*
  show the `+1h` countdown to the reset;
* the same row's "At reset" must read `113%` (= 0.9 / (4/5) rounded), so the two
  columns are provably one projection;
* 40% used stays `plenty of room`, 70% stays `close to limit`, and neither grows a
  clock;
* an over-budget window with no timing info keeps the bare `over budget`;
* `runOutAtMs` returns null for an unbounded window and for a pace that ends
  exactly at the limit, and `limitsWithRunOut` carries the instant in `/quota:json`
  next to the limit's own fields;
* `format.clock` is `HH:MM`, 24 h, local (morning/midnight/evening).

RED, measured by restoring the old `over budget`-only cell:

```
--- FAIL: TestQuotaRunOut_OverBudgetNamesTheTime
    window row = "| Z.ai (pro) | Session (5h) | ███████░ 90% | 113% | +1h | over budget |",
    want it to contain "over budget — exhausted at 09:26"
--- FAIL: TestQuotaRunOut_AtResetMatchesTheSameProjection
    window row = "… | over budget |", want the run-out time 09:26
```

Restored: the whole `plugins` package passes (3 consecutive runs), as does the
app-level `/quota` filmstrip test (`go test ./internal/app/ -run Quota`) that
renders the plugin through the real command registry, and
`go test -count=1 -race -cover ./...` exits 0 (88 packages ok).

**A test bug found while writing these tests.** The first fixtures spelled the
window as `int64(4*time.Hour)` — nanoseconds, not milliseconds — describing a
41-day window whose *ratio* was still right, so the projection "passed" while the
row read `+41666d 16h`. The tests now convert through an explicit `ms()` helper
and assert the `+1h` countdown, which is what makes the scenario checkable at all.

## Residual risk

* The instant is a projection from the *mean* pace since the window opened: a
  burst at the end of a window moves it, by design (the same assumption
  `projectedRatio` always made for the colour and the words).
* Minute precision is deliberate; a window that will run out inside the current
  minute shows the minute it crosses, not seconds.
* Windows whose pace is *invisible* (no `periodMs`, e.g. accumulated cost rows)
  cannot be projected and keep the words-only cell.
