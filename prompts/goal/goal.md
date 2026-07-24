<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Manage the current goal: create one, update its lifecycle status, read it, or set a hard
budget. A goal is a durable objective the runtime pursues autonomously across turns.

Choose the operation with the `action` field:

- `create` — start a new goal. Requires `objective`. Optional: `completionCriterion`,
  `replace` (replace an existing goal instead of failing). Only create a goal when the user
  explicitly asks you to work autonomously toward an outcome, or a host goal-intake prompt
  asks you to. Do NOT create goals for greetings, ordinary questions, or vague requests that
  lack a verifiable end state; ask for the missing completion criterion first.
- `update` — set the goal's lifecycle status (this is how you resume, end, or yield a goal).
  Requires `status`, one of:
  - `active` — resume a paused or blocked goal when the user explicitly asks you to work on it.
  - `complete` — the objective is satisfied and any stated validation has passed. The goal ends.
  - `blocked` — an external condition or required user input prevents progress, or the
    objective cannot be completed as stated. The goal stops but can be resumed later.
  - `paused` — set the goal aside for now; it can be resumed later.
  Only call `complete` when all required work is done and there is no useful next action — not
  after only a plan, summary, first pass, or partial result.
- `get` — read the current goal: objective, criterion, status, budgets (turns/tokens/time and
  how much remains), latest self-report and evaluator verdict. Returns `{ "goal": null }`
  when there is no current goal.
- `set_budget` — set a hard budget limit. Requires `value` (positive number) and `unit`, one of
  `turns`, `tokens`, `milliseconds`, `seconds`, `minutes`, `hours`. Use only when the user
  clearly gives a runtime limit (e.g. "stop after 20 turns", "no more than 500k tokens",
  "finish within 30 minutes"); do not invent limits. Convert compound times to one unit
  ("2 hours and 3 minutes" → `value: 123, unit: "minutes"`). If the requested budget is not
  reasonable, do not set it; tell the user.

If a goal is active and you do not call `update`, the goal keeps running: after your turn ends
you will be prompted to continue.
