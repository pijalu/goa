<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# Fix plan: /config percentage list sorts single-digit options after double-digit ones

Bug: bugs.md — "/config percentage list sorts single-digit options after
double-digit ones (e.g. hard limit)".

## Root cause

`tui.NewSelector` sorts items alphabetically by Label (`sortSelectorItems`).
`ceilingPercentItems` (core/commands/config_compression.go) builds the
soft/trigger/hard ceiling picker with labels `"%d%%"`, so the single-digit
label "5%" sorts after "45%" and before "50%": the user sees
10%, 100%, 15%, … 45%, 5%, 50%, … instead of ascending order.

All four percent pickers (soft, trigger, hard, per-model via
`percentItemsWithInherit`) share `ceilingPercentItems`, so one fix covers all.

## Fix

The ceiling ladder is an inherently ordered list, so `ceilingPercentItems`
sets `PreserveOrder: true` on every row — the selector then keeps the
caller's ascending order (0 disabled, 5, 10, …, 95, 100) instead of sorting
alphabetically by Label. Labels stay natural ("5%", "10%", …, "100%");
`Value` is unchanged so config keys and the ✓ current-value marker are
unaffected. All four percent pickers (soft, trigger, hard, per-model via
`percentItemsWithInherit`) share `ceilingPercentItems`, so one fix covers all.

Deviation from the report's suggested mechanism (zero-padding "5%" → "05%"),
found while testing the padded variant: single-digit padding alone still
renders "05%, 10%, 100%, 15%" — no lexicographic padding of two-digit labels
can place "100%" after "95%" ('1' < '9'), and the report's own observed
sequence explicitly lists the 10%/100%/15% interleave as part of the defect,
while its Expected behavior demands a fully ascending list. PreserveOrder
achieves that end state for every entry; the micro-ratio picker (30..90,
uniform two-digit) needs no change.

## Test approach (RED first)

Filmstrip-style (assert the rendered picker order, guideline #5):

1. Feed `ceilingPercentItems(false)` through the REAL `tui.NewSelector` and
   assert the rendered rows read in ascending numeric order
   (0% disabled, 5%, 10%, …, 95%, 100%) with the 5% row adjacent to the top
   when it is the active value. RED today: fails with "5%" rendered between
   "45%" and "50%".
2. Assert every item carries `PreserveOrder` (the mechanism) and the built
   order is numerically ascending; assert `Value` stays unpadded so the
   persisted config value and ✓ marker are unaffected.

## Validation steps

- `go test ./core/commands/ -run 'CeilingPercent' -count=1 -race` green.
- Quality gates run separately (go vet, staticcheck, gocognit, gocyclo,
  go test -race -cover ./...).
- Commit with a descriptive message; archive the bug entry to docs/archive.
