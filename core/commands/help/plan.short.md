<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /plan[:subcommand][:key=value]...

Manage structured work plans: create, review, approve, execute, and delete.

Subcommands:
  new      Create a new plan. Requires: objective=...
  review   Open the plan pager for annotation.
  approve  Approve the plan and start execution.
  status   Open the plan-status overlay.
  replan   Re-enter the planning phase.
  list     List all plans.
  delete   Delete a plan. Requires: id=..., confirm=true

Run /plan? for short help, /plan?? for long help.
