<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Spawn a swarm of sub-agents to process multiple items in parallel.

Parameters:
  task            Overall swarm task description
  items           List of items to process
  subagent_type   coder (default), explore, or plan
  prompt_template Optional template with {{item}} placeholder
