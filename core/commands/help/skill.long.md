<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /skill[:run[:<name>[:args...]]|:show[:<name>]|:enable[:<name>]|:disable[:<name>]]

Manage and execute skills.

Commands:
  (no args)              List all available skills
  run:<name>[:args...]   Execute a skill (args are colon-separated)
  show:<name>            Show detailed information about a skill
  enable:<name>          Enable a disabled skill (saved to config)
  disable:<name>         Disable an enabled skill (saved to config)

Examples:
  /skill                    List available skills
  /skill:run:refactor:src/main.go
  /skill:show:test-gen
  /skill:disable:telegram   Disable the telegram skill (embedded → global)
  /skill:enable:telegram    Re-enable it

Gold rules for persistence:
  - Embedded skills are global: toggles are saved to the home config.
  - All other loaded skills are per-project: toggles are saved to the project
    config (.goa/config.yaml).

Aliases: /sk
