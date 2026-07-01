<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /reload

Reloads all skill directories and context files (AGENTS.md) from disk,
updates the skill registry, and rebuilds the system prompt.

Use after adding, modifying, or removing skills or plugins. No restart
is needed.

What gets reloaded:
  - Skills: re-scans all SKILL.md directories
  - Context: re-scans for AGENTS.md/CLAUDE.md files (ancestor walk)
  - Shortcuts: re-registers /<command> shortcuts for skills with
    "command:" frontmatter
  - Plugins: planned — JS plugin hot-reload is not yet implemented
