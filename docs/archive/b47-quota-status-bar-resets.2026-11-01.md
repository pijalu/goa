# B47 — quota status bar shows the Coding-Plan reset count

Closed: 2026-11-01

## Report

The z.ai quota footer segment appended `+N↻` after the window percentages
("[48%|9%] +3↻") via `withCodingPlanResets`
(plugins/bundled/provider-quota/plugin.js), spending scarce status-bar
columns on reset bookkeeping that belongs to `/quota:resets`. The
`/quota:resets` command and the `/quota` table row ("3 available
(2 × 5h · 1 × week)") already carry the actionable view.

## Resolution

- `withCodingPlanResets` is removed entirely; the segment builder returns
  `colorizedSegment(entry)` directly with a comment noting the B47 removal
  (status bar shows quota windows only; reset availability stays on
  /quota:resets). The throttled-suppression and zero-count paths die with
  the function — no suffix means nothing to suppress.

## Validation

- RED (observed): flipped
  `TestQuotaZaiCodingPlan_SegmentShowsResetCount`
  (plugins/quota_zai_coding_plan_test.go) to assert the segment never
  carries `↻`; before the fix it failed with the live suffix in the output
  ("[41%] +3↻"). The zero-available half of the test (already must-not-contain)
  is kept, as is the no-credentials quiet-status-bar test.
- GREEN: `go test -count=1 -timeout 120s ./plugins/` — ok (9.2s).
- Documentation sweep: no `+N↻`/`↻` mentions left in docs/*.md; the
  remaining matches are the must-not-contain assertions, the B47 comment in
  plugin.js, and closed-history records (bugs.md entry, b40 archive).
- Full race suite green after B48 (see its archive); gates run separately.

## Scope note

Footer-only: `/quota:resets`, `/quota:reset[:week]`, `/quota:reset:claim`,
and the `/quota` table row are unchanged.
