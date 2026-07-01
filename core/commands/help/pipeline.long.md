<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /pipeline:command[:args]

Commands:
  list                  List available pipelines
  run:<name>[:input]    Run a pipeline (input is colon-separated)
  status                Show current pipeline status

Examples:
  /pipeline:list
  /pipeline:run:implement-feature:Add user authentication
  /pipeline:status
