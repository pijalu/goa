<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /exchange[:turn-number]

Dump the complete LLM exchange for a turn, including stats, user input,
thinking blocks, tool calls/results, and assistant responses.
If no turn number is given, shows the last turn.

Examples:
  /exchange       Show last turn
  /exchange:3     Show turn 3
