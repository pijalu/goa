# Retry settings menu: grouped values and menu/prompt mismatch

**Date:** 2026-11-07
**Area:** `core/commands/config_menu_retry.go`

## Observed

`/config` → Retry settings rendered rows that carried several settings at once:

```
Maximum retries                       15 retries, 90 (warn at 80) stall retry
Auto-retry after provider silence     90 (warn at 80)
```

and the two stall rows disagreed with their own prompts: the retry row displayed
`90` (the event stall = 3/4 of the 2m byte budget) while the prompt prefilled
`120` (the configured `execution.activity_timeout`). The provider rows were worse:
their prompt prefilled `2m (default)` — a string that cannot be parsed as a
duration, so accepting the prefill wrote an invalid value.

## Root cause

Two independent habits in one menu:

1. Rows described *derived* values (`EventStallTimeout(budget)`, `2/3 of window`)
   instead of the value the row's prompt edits, so displayed ≠ editable.
2. Descriptions borrowed `retrySettingsLabel` (a whole-category summary) for the
   `retries` row and folded the warn lead into the window row.

## Fix

- One row = one setting = the value that row's prompt edits.
  `retries` → the configured count; `stall_timeout` → the configured byte-idle
  budget (`configuredActivityTimeout`, plain seconds); `stall_warn` → the
  effective lead; provider rows → the configured duration (never a `(default)`
  marker).
- Row renamed `Auto-retry after provider silence` → `Provider silence budget
  before retry` (that is what the key holds; the agent retries at 3/4 of it).
- The derivation rules (3/4 retry trigger, 2/3 warn fallback, default durations)
  moved into the prompt titles, where they do not pollute the menu row.
- The aggregate summary stays only on the parent `/config` row:
  `retrySettingsLabel` → `"<n> retries, <budget>s silence budget"`.

New helpers: `retriesLabel`, `configuredRetries`, `configuredActivityTimeout`;
`activityTimeoutLabel` / `activityWarnAfterLabel` now return a single bare value.

## Regression coverage

- `TestRetrySettingsMenu_OneValuePerLine` — every sub-menu description carries a
  single value (no comma, no parenthetical summary) and equals its prompt
  prefill.
- Updated `TestRetrySettingsMenu_ShowsStallTiming`,
  `TestRetrySettingsMenu_DerivesWarningWhenUnset`,
  `TestRetrySettingsMenu_Labels`, `TestRetrySettingsRootLabel_ShowsStallRetry`.

## Validation

- Real terminal (pty, headless, sandboxed `HOME`) — `/config` → filter `retry`:

  ```
  Active provider retry cap  2m0s
  Active provider stream idle timeout  2m0s
  Maximum retries  15
  Provider silence budget before retry  120
  Stall warning after provider silence  80
  ```

- Prompt prefills verified in the same run: provider idle timeout → `2m0s`
  (valid duration, was `2m (default)`); silence budget → `120`, matching its row.
- `go vet ./...`, `staticcheck ./...`, `go test -count=1 -race ./...` all pass;
  `gocognit`/`gocyclo` show no new findings in the touched files.
