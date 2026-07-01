<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /mode[:coder|:planner|:reviewer|:<custom>]

Without arguments, opens an interactive mode picker listing all available
modes. With a mode name, switches directly to that mode.

Modes are defined under prompts/mode/<name>/definition.md in the prompt
tree. Embedded defaults (coder, planner, reviewer) can be overridden or
extended by creating the same path under .goa/prompts/mode/ in your
project or home directory.

Examples:
  /mode          Show mode picker
  /mode:coder    Switch to coder mode
  /mode:planner  Switch to planner mode
