<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /thinking [level]

Set the reasoning effort level for the current agent.
When called without arguments, shows the current level.

Levels:
  off     — No reasoning
  minimal — Very brief reasoning (~1k tokens)
  low     — Light reasoning (~2k tokens)
  medium  — Moderate reasoning (~8k tokens)
  high    — Deep reasoning (~16k tokens)
  xhigh   — Maximum reasoning (~32k tokens)

Examples:
  /thinking       Show current level
  /thinking:high  Set deep reasoning
