<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B15 — Session logs were kept forever: prune after 7 days by default, behind a config flag

Closed 2026-10-05. Archived from `bugs.md`.

## Reported

Requested 2026-10-05: session logs should be removed after 7 days (measured from
the last update) **by default**, and that window should be a configuration flag.

## Root cause

`core.SessionStore` wrote `<project>/.goa/sessions/<timestamp>_<name>.jsonl` with
no retention at all: the only ways a file left the directory were the explicit
`DeleteSession` and `SaveCurrent` discarding its own empty session. A project's
`.goa/sessions` therefore grew for the lifetime of the project. The codebase
already had the pattern for this — `ImageStoreLifetime` (30 days) plus
`PruneImages`, called opportunistically at startup — and three retention configs
(`orchestrator.retention`, `goals.retention`, `plan.retention`, all `enabled` +
`days`, all defaulting on with 7 days).

## Fix

* **Config**: new `sessions.retention` section (`config/config_sessions.go`),
  with the embedded default `enabled: true, days: 7` in
  `config/configs/default.yaml`. `SessionsConfig.SessionWindow()` resolves it:
  unset keeps the default, `enabled: false` keeps everything, `days: 0` keeps
  everything, `days: N` keeps N days.
* **Tri-state on purpose.** Both fields are pointers (`*bool`, `*int`) and
  `mergeSessions` merges them field by field, so an explicit `false`/`0` from a
  higher layer always wins. The three older retention structs use a
  `Days != 0 || Enabled` replace rule, which — with a default that is ON — makes
  `enabled: false` from a higher layer a **no-op**; a flag the user cannot switch
  off is not the flag this entry asked for. (The older three keep their behaviour
  here; see Residual risk.)
* **Prune**: `SessionStore.PruneSessions(maxAge)` removes `*.jsonl` files whose
  last write is older than the window (best-effort, `maxAge <= 0` = keep
  everything), and never the session this process has open: the writer holds that
  file open for the whole run, so its mtime stands still while a long turn is in
  flight and age alone would eventually look abandoned.
* **Wiring**: `subsystems.startSessionCleanup()` runs the sweep once at startup
  and hourly (`internal/app/session_cleanup.go`), the same shape as
  `startOrchestratorCleanup`, gated on the configured window.
* **Docs**: `docs/CONFIGURATION.md` documents the section next to the other
  retentions.

## Validation

* `config/config_sessions_test.go` — the default is 7 days; the full tri-state
  table (unset / enabled true / enabled false / days 0 / days 3 / days 30 /
  explicit off over an explicit window); the merge semantics; and
  `TestSessionsRetentionCascade`, which loads the **real cascade** (embedded
  defaults + a project layer) instead of merging structs by hand, asserting the
  7-day default, `enabled: false` switching it off, and `days: 30` widening it.
* `core/sessionstore_prune_test.go` — window selection either side of the cutoff,
  `0`/negative = keep everything, the open session spared even when back-dated 90
  days, and non-session entries + a missing directory left alone.
* `internal/app/session_cleanup_test.go` — the startup sweep through the real
  subsystems path with the real config type: an 8-day-old log goes, a 6-day-old
  log stays, and nothing is pruned with retention disabled.

RED, measured by short-circuiting `PruneSessions` to `return 0`:

```
--- FAIL: TestPruneSessions_RemovesOnlyExpiredLogs
    PruneSessions removed 0 logs, want the two outside the window
--- FAIL: TestPruneSessions_SparesTheOpenSession
    PruneSessions removed 0 logs, want just the other one
--- FAIL: TestRunSessionLogCleanup_UsesTheConfiguredWindow
    expired session log survived the default sweep
```

Restored: all pass.

## Residual risk

* **No `/config` menu entry yet.** `orchestrator`, `goals` and `plan` retention
  can be edited from `/config`; sessions retention is currently file/env/flag
  only. Adding it is a menu-parity task (`core/commands/config_orchestrator.go`
  already has a generic `promptRetentionDays` editor), not a behaviour gap.
* **The three older retention flags cannot be disabled from a higher layer**
  (`Days != 0 || Enabled` replace rule with an ON default). B15 deliberately does
  not reuse that rule; the older three would need the same tri-state treatment to
  make `enabled: false` effective.
* Retention is a wall-clock window, not a size cap: a week of very large
  sessions is kept in full. A size cap would be a separate knob.
