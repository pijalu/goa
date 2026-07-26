<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Manage goals as a todo-like list: one active goal the runtime pursues autonomously across
turns, plus a queue of upcoming goals that start automatically when the active one ends.
Add goals freely — you rarely replace.

Choose the operation with the `action` field:

- `create` — add a goal. By default this ADDS to the list: if no goal is active the new goal
  starts active; if one is already active the new goal is QUEUED (never an implicit replace).
  Pass `objective` for one goal or `objectives` (array) to add several at once — the first
  starts active if none is active, the rest queue. Optional: `completionCriterion` (applies
  to every objective in the call, including queued ones), `freshContext` (run this goal on a
  clean context), and `replace: true` to instead discard the current goal and start this one
  (single objective only — use sparingly, only when you truly mean to abandon the active
  goal). Only create a goal when the user explicitly asks you to work autonomously toward an
  outcome, or a host goal-intake prompt asks you to. Do NOT create goals for greetings,
  ordinary questions, or vague requests that lack a verifiable end state; ask for the
  missing completion criterion first. Optional: `verifyCommand` — a shell command the
  runtime executes after you confirm completion (exit 0 = pass); set it whenever the
  done-condition is machine-checkable (tests, build, lint, health check). Keep it passing
  as you work: after confirmed completion it runs, and a non-zero exit rejects the
  completion with the output tail as evidence. Repeated verification failures auto-block
  the goal for user review. Optional: `priority: "front"` — insert the goal at the FRONT of
  the queue (promoted next) instead of appending. Use it to push an execution goal ahead of
  the goal it unblocks (see `blocked` below).
- `list` — show the active goal and the queued goals (id, name, objective, status).
- `cancel` — remove a queued goal. Requires `goalId` (the queued goal's ID or friendly name).
- `reorder` — move a queued goal. Requires `goalId` and `direction` (`up` | `down`).
- `update` — set the goal's lifecycle status (this is how you resume, end, or yield a goal).
  Requires `status`, one of:
  - `active` — resume a paused or blocked goal when the user asks you to work on it (any
    phrasing of "continue/proceed/go on" counts — resume immediately, same turn).
  - `complete` — the objective is satisfied and any stated validation has passed. The goal
    ends. ENFORCED: when a completion criterion is recorded, your first `complete` call is
    intercepted by a verification challenge that restates the criterion; self-audit, then
    call `complete` again with `reason` citing the concrete evidence (commands run, outputs
    observed, tests passing). Without a criterion, completion is immediate. When a
    `verifyCommand` is recorded, machine verification runs after your confirmed completion
    and a failure rejects it (fix the failure, then complete again); an independent judge
    may also audit your evidence semantically.
  - `blocked` — an external condition or required user input prevents progress, or the
    objective cannot be completed as stated. Requires BOTH `reason` (the concrete blocker)
    and `expectation` (exactly what input or change unblocks it). The goal stops but can be
    resumed later; when the user's reply supplies the expectation, resume with `active`.
  - `paused` — set the goal aside. Requires `reason` justifying why the goal must yield.
    NEVER pause just to ask the user whether to continue — that defeats goal mode. If you
    can make any progress, keep working instead; if you need input, use `blocked`.
  Only call `complete` when all required work is done and there is no useful next action —
  not after only a plan, summary, first pass, or partial result.
- `get` — read the current goal: objective, criterion, status, budgets (turns/tokens/time
  and how much remains), todo list, and the recorded terminal reason/expectation when
  paused or blocked. Returns `{ "goal": null }` when there is no current goal.
- `set_budget` — set a hard budget limit. Requires `value` (positive number) and `unit`, one
  of `turns`, `tokens`, `milliseconds`, `seconds`, `minutes`, `hours`. Use only when the
  user clearly gives a runtime limit (e.g. "stop after 20 turns", "no more than 500k
  tokens", "finish within 30 minutes"); do not invent limits. Convert compound times to one
  unit ("2 hours and 3 minutes" → `value: 123, unit: "minutes"`). If the requested budget is
  not reasonable, do not set it; tell the user.
- `add_todo` — add a task to the goal's managed todo list. Requires `todoTitle`. For a
  multi-step goal, decompose it into ordered todo items up front so the goal self-tracks;
  the list is surfaced back to you each turn.
- `update_todo` — set a todo item's status. Requires `todoId` and `todoStatus`
  (`pending` | `in_progress` | `done`). Mark items done as you complete them.

If a goal is active and you do not call `update`, the goal keeps running: after your turn
ends you will be prompted to continue. A stall watchdog tracks measurable progress (todo
transitions, workspace changes) across continuation turns: turns with no measurable progress
trigger a challenge, and continued stalling auto-blocks the goal for user direction. Keep
each turn producing visible progress — or end the goal explicitly.
