<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /stats[:session|:turn-number|usage-args]

Show LLM usage statistics. By default shows the global per-project/provider/
model summary from the persistent usage store (like /usage). Use :session for
the current session's per-turn breakdown, or a turn number for that turn's
detailed tree.

Examples:
  /stats          Global usage summary (per project/provider/model)
  /stats:session  Current session per-turn overview
  /stats:3        Detailed breakdown for session turn 3
  /stats:7d       Global usage, last 7 days
  /stats:cost     Global cost breakdown

Aliases: /tokens, /tok
