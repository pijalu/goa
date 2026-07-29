<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Manage an in-session todo list for tracking tasks and progress.

Todos are lightweight checklist items — NOT goals. A todo has no lifecycle,
turns, budget, or completion criterion; use it to break the current task into
visible steps. Use goals for autonomous multi-turn work.

Parameters:
  action       add | update | complete | remove | list | clear
  id           Todo ID (for update/complete/remove)
  description  Todo text (for add)
  status       pending | in_progress | done (for update)

Goal linkage:
  - No goal active: items live in the session list (conversation scratchpad).
  - Goal active: the list is LINKED to the goal — blank at goal start, items
    added now belong to the goal (the framework surfaces them each turn and
    the stall watchdog counts their transitions as progress), and they are
    contained by it: when the goal ends, its todos end with it and the
    session list resurfaces unchanged. remove/clear are refused while
    goal-linked (mark items done instead). A goal completed with open todos
    reminds you of them so unfinished work is not silently dropped.
  - Enabled via tools.enabled.todo_list (default on).
