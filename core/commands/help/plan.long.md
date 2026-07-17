<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /plan[:subcommand][:key=value]...

The /plan command manages structured work plans. Plans are persisted under
.goa/plans/ and are event-sourced for crash recovery.

## Subcommands

| Subcommand | Description |
|------------|-------------|
| `/plan` | Interactive action list (new / review / approve / ...) |
| `/plan:new:objective=...` | Create a new plan with the given objective |
| `/plan:review:id=` | Open the plan pager for annotation |
| `/plan:approve:id=` | Approve and start execution |
| `/plan:status:id=` | Open the plan-status overlay |
| `/plan:replan:id=` | Re-enter planning (pause execution at item boundary) |
| `/plan:list` | List all plans |
| `/plan:delete:id=...,confirm=true` | Delete a plan |

## ID Resolution

IDs are resolved by friendly name first, then internal ID. Run /plan:list to
see available plans with their friendly names.

## Examples

  /plan:new:objective=Refactor the auth module
  /plan:review:id=happy.hare
  /plan:approve:id=plan-abc123
  /plan:delete:id=happy.hare,confirm=true
