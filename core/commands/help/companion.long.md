<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /companion[:on|:off|:agent|:framework]

Toggle the companion minor mode.
Agent-driven mode (default): the LLM can call request_review and delegate_to
  tools on its own initiative.
Framework-driven mode: the companion sub-agent reviews every main agent turn.

Examples:
  /companion           Show current status
  /companion:on        Enable companion mode (agent-driven)
  /companion:agent     Enable agent-driven mode
  /companion:framework Enable framework-driven (companion reviews every turn)
  /companion:off       Disable companion mode
