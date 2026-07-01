<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /skill[:run[:<name>[:args...]]|:show[:<name>]]

Manage and execute skills.

Commands:
  (no args)              List all available skills
  run:<name>[:args...]   Execute a skill (args are colon-separated)
  show:<name>            Show detailed information about a skill

Examples:
  /skill                    List available skills
  /skill:run:refactor:src/main.go
  /skill:show:test-gen

Aliases: /sk
