<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Goals

Goa supports **autonomous goals**: long-running, multi-turn tasks that the agent pursues on your behalf. Goals have a lifecycle, optional budgets, and can be chained into a queue.

## Starting a goal

Use the `/goal` command (colon nomenclature, like other Goa commands):

```text
/goal                          # interactive: prompts for the objective
/goal:new:fix the failing checkout tests
/goal:new:implement OAuth2 login with unit tests
```

A goal needs a verifiable end state. If your request is vague, the model will
ask for a completion criterion before creating the goal.

Each goal gets a **friendly alias** (e.g. `happy.fox`) shown in status and
the queue manager. If a goal is already active, creating a new one asks
whether it should be **first** (replaces the active goal) or **last** (queued).

## Goal lifecycle

| Status   | Meaning                                            |
|----------|----------------------------------------------------|
| active   | The agent is working autonomously on the goal.     |
| paused   | Goal is parked; resume with `/goal resume`.        |
| blocked  | Goal hit a blocker; resume with `/goal resume`.    |
| complete | Goal finished; the record is cleared automatically.|

Control commands:

```text
/goal:status   # show current goal
/goal:pause    # pause active goal
/goal:resume   # resume paused/blocked goal
/goal:cancel   # discard current goal
/goal:replace <objective>   # ask confirmation then abandon current goal
```

## Budgets

The model can set hard limits with the `SetGoalBudget` tool:

```text
stop after 20 turns
use no more than 500k tokens
finish within 30 minutes
```

Supported units: `turns`, `tokens`, `milliseconds`, `seconds`, `minutes`, `hours`.

## Queued goals

Queue follow-up goals that start automatically when the current one completes:

```text
/goal:next refactor the auth module
/goal:next update documentation
/goal:next                    # interactive: prompts for the objective
/goal:manage                  # open the queue manager
/goal:reorder:1B,2C,3A        # reorder by letter mapping
```

Queued goals auto-promote when the active goal is marked complete.

## Autonomy and starting goals

In non-yolo autonomy modes, starting a goal shows a permission dialog. You can switch to auto/yolo mode or proceed in the current mode.

## Events in the TUI

- A bordered **Goal** panel appears while a goal is active.
- Lifecycle transitions render as low-profile markers.
- Completion renders a summary card with stats.
- Goal status appears in the footer status bar.
