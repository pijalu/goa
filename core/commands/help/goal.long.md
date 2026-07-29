<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /goal[:subcommand[:<text>]]

  /goal                       Create a new goal interactively (prompts for objective)
  /goal:new                   Create a new goal interactively
  /goal:new:<text>            Create a goal with the given objective
  /goal:new:fresh:<text>      Create a goal on a clean context (no prior conversation)
  /goal:new:reuse:<text>      Create a goal reusing the current conversation
  /goal:next                  Queue a goal interactively (appended last)
  /goal:next:<text>           Queue a goal to run after the current one
  /goal:next:fresh|reuse:<text> Queue with an explicit context mode
  /goal:replace               Replace the current goal interactively (asks objective)
  /goal:replace:<text>        Replace the current goal with a new one (asks confirmation)
  /goal:manage                Open the queued-goals manager
  /goal:reorder:<map>         Reorder queue with letter mapping (e.g. 1B,2C,3A)
  /goal:status                Show current goal status
  /goal:current               Show the current goal in full: objective, criterion, verify command, todos
  /goal:list                  List active + queued goals in order, with full objectives (markdown)
  /goal:pause                 Pause the active goal
  /goal:resume                Resume a paused or blocked goal
  /goal:cancel                Discard the current goal

Each goal gets a friendly alias (e.g. "happy.fox") shown in status and the
queue manager. Creating a goal while one is already active asks whether the
new goal should be first (replaces the active goal) or last (queued).

Context mode: new goals default to a CLEAN context (objective + handoff only;
the prior conversation is preserved in the transcript but not sent to the
agent). Override per goal with :fresh / :reuse, or change the default in
/config → Goals → "Fresh context for new goals" (goals.fresh_context).
