<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Schedule tools

The schedule tools manage persistent session reminders backed by a durable job
store (`.goa/schedule/jobs.json` in the project directory). A created schedule
survives restarts; when a job's target passes, its prompt is delivered as a
user message at the start of the next turn and the job is dispatched exactly
once (one-shots are removed, fixed-rate jobs advance to their next occurrence).

## schedule_create

Supply a non-empty `prompt` and exactly one selector:

- `after_seconds` — positive integer delay before a one-shot reminder fires.
- `at` — strict offset RFC 3339 instant (e.g. `2026-06-01T10:00:00Z`) or a
  local date/time object `{"date": "2026-06-01", "time": "10:00:00", "time_zone": "Europe/Paris"}`.
  Must be in the future.
- `every_seconds` — fixed-rate recurrence interval of at least 300 seconds.
  Recurring reminders stay creation-aligned, skip missed occurrences, and
  deliver one latest occurrence per overdue rule.

## schedule_list

Lists every active reminder in creation order, with its exact id, UTC target,
`scheduled` or `overdue` state, and `session-local` delivery mode.

## schedule_delete

Removes one active reminder by the exact id from `schedule_create`/`schedule_list`.
Unknown ids return `deleted: false` with code `schedule_not_found`.

Delivery is session-local: a due reminder fires on the next turn once its
target has passed.
