<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Read the current goal: its objective, completion criterion, status, budgets (turns, tokens,
time, and how much remains), the latest self-report, and the latest evaluator verdict.

Use `GetGoal` before deciding whether to continue working, report completion, report a blocker,
or respect a pause. It returns `{ "goal": null }` when there is no current goal.
