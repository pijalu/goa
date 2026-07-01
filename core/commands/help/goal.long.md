<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /goal[:subcommand[:<text>]]

  /goal                       Create a new goal interactively (prompts for objective)
  /goal:new                   Create a new goal interactively
  /goal:new:<text>            Create a goal with the given objective
  /goal:next                  Queue a goal interactively (appended last)
  /goal:next:<text>           Queue a goal to run after the current one
  /goal:replace               Replace the current goal interactively (asks objective)
  /goal:replace:<text>        Replace the current goal with a new one (asks confirmation)
  /goal:manage                Open the queued-goals manager
  /goal:reorder:<map>         Reorder queue with letter mapping (e.g. 1B,2C,3A)
  /goal:status                Show current goal status
  /goal:pause                 Pause the active goal
  /goal:resume                Resume a paused or blocked goal
  /goal:cancel                Discard the current goal

Each goal gets a friendly alias (e.g. "happy.fox") shown in status and the
queue manager. Creating a goal while one is already active asks whether the
new goal should be first (replaces the active goal) or last (queued).
