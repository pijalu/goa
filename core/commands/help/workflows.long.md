<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /workflows:command [args]

Commands:
  list                  List available workflows
  show <name>           Show workflow details
  run <name> [input]    Run a workflow

Examples:
  /workflows:list
  /workflows:show review
  /workflows:run:implement-feature "Add OAuth"
  /workflows:implement-feature "Add OAuth"   (shorthand)

Workflows are defined in workflows/<name>/definition.yaml.
