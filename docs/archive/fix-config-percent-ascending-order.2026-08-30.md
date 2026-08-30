<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: /config percentage list sorts single-digit options after double-digit ones

Closed 2026-08-30. Moved from `bugs.md`. Fixed by commit `5d13134`
("fix(config): ceiling percent pickers render in ascending order —
PreserveOrder").

Observed: in /config, the soft/trigger/hard ceiling pickers ordered
alphabetically by label — 10%, 100%, 15%, …, 35%, 40%, 45%, 5%, 50%, …,
95%; the hard-limit list showed 35%, 40%, 45%, then 5%, then 50%.
Expected: the list reads in ascending order (05%, 10%, 15%, …, 95%)
instead of interleaving 5% between 45% and 50%.

## Root cause

`tui.NewSelector` sorts items alphabetically by Label
(`sortSelectorItems`). `ceilingPercentItems` produced labels "5%" …
"100%", so the single-digit entry sorted after the two-digit ones, and
"100%" sorted between "10%" and "15%" ("100%" < "15%" lexicographically).

The report suggested zero-padding ("5%" → "05%"). Testing that variant
showed it fixes the 5% interleave but still renders "05%, 10%, 100%,
15%": no padding of two-digit labels can rank "100%" after "95%"
('1' < '9'), and the report's own observed sequence lists the
10%/100%/15% interleave as part of the defect while its Expected
behavior demands a fully ascending list.

## Fix

The ceiling ladder is an inherently ordered list, so
`ceilingPercentItems` sets `PreserveOrder: true` on every row — the
selector keeps the caller's ascending order (0 disabled, 5, 10, …, 95,
100). Labels stay natural ("5%", …, "100%"); values are untouched (they
are the persisted config keys and the ✓ marker matches on them). All
four percent pickers (soft, trigger, hard, per-model via
`percentItemsWithInherit`) share the builder, so one fix covers all.

## Validation

- RED first: `TestCeilingPercentPickerRendersAscending` drove the REAL
  `tui.NewSelector` + `Render` and captured the live bug — rows
  "30% 35% 40% 45% ›✓5% 50% 55% 60%" (filmstrip, guideline #5). After
  the fix the same render reads "0% (disabled), 5%, 10%, …" ascending.
- `TestCeilingPercentItemsPreserveAscendingOrder` pins the mechanism
  (every row PreserveOrder) and the built ladder order;
  `TestCeilingPercentItemsValuesUnpadded` pins the config-value side.
- Full gates green: `go vet`, `staticcheck`, `gocognit -over 15`,
  `gocyclo -over 12` (one pre-existing unrelated warning:
  TestRetryConfigSetters), `go test -count=1 -race -cover ./...` (87
  packages ok).
