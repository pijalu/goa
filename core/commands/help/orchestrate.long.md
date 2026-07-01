<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /orchestrate:<task1>, <task2>, ...

Run a sequence of tasks through planner → coder → tester → reviewer.
The orchestration agent coordinates each task through all phases.

Examples:
  /orchestrate:Build auth system, Add rate limiting, Write tests
  /orchestrate:run:Refactor database layer

Steering:
  /skip    Skip current task
  /retry   Restart current task
  <msg>    Send steering message to the current agent

Aliases: /orch
