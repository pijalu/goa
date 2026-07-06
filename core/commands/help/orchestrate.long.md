<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /orchestrate[:subcommand][:key=value,...]

Run and manage multi-agent orchestrations with hub, fanout, or pipeline topology.
Each run gets a memorable friendly name like happy.hare.

Actions:
  /orchestrate                      Open the interactive action menu (new / resume / delete / list).
  /orchestrate:new                 Start a new run; prompts for objective if missing.
  /orchestrate:new:objective=<text>
  /orchestrate:new:topology=<hub|fanout|pipeline>,objective=<text>
  /orchestrate:new:name=<alias>,objective=<text>
  /orchestrate:list                Interactive filterable list of all runs.
  /orchestrate:resume:id=<run-id>  Resume an unfinished run.
  /orchestrate:delete:id=<run-id>  Delete a run (requires confirmation).
  /orchestrate:delete:id=*         Delete all runs (requires confirmation).
  /orchestrate:steer:id=<agent-id|all|orchestrator>,message=<text>

Configuration:
  /config                          Open the settings menu; choose Orchestrator to edit
                                   roles, pool limits, default topology, and retention.

Steering:
  While the Orchestrator Summary tab is visible, the main input line shows
  "steer all:" and submitted text is broadcast to all live agents. You can
  still type explicit /orchestrate:steer commands for directed steering.

Examples:
  /orchestrate:new:topology=fanout,objective=Refactor auth layer
  /orchestrate:delete:id=happy.hare,confirm=true
  /orchestrate:steer:id=coder-1,message=use bcrypt

Aliases: /orch
