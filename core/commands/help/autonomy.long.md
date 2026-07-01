<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /autonomy[:yolo|:solo|:confirm|:review]

Without arguments, shows the current autonomy level.
With an autonomy level, changes the autonomy for the current mode and
persists it to the project configuration.

Autonomy levels:
  yolo    execute all tool calls automatically
  solo    auto-run tools constrained to the codebase
  confirm pause before each tool call
  review  queue writes for batch approval at turn end

Examples:
  /autonomy         Show current autonomy level
  /autonomy:review  Switch to review autonomy for the current mode
