<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /stats[:session|:verbose|:cache|:turn-number|usage-args]

Show LLM usage statistics. By default shows the global per-project/provider/
model summary from the persistent usage store (like /usage). Use :session for
the current session's per-turn breakdown, :verbose for every known project each
split by provider and model, :cache for the per-completion cache hit-rate
evolution chart plus a table of cache drops (before/after rate), or a turn
number for that turn's detailed tree.

Examples:
  /stats          Global usage summary (per project/provider/model)
  /stats:session  Current session per-turn overview
  /stats:verbose  All projects, each split by provider and model
  /stats:cache    Cache hit-rate evolution + drop table (this project)
  /stats:cache all  Cache evolution across all projects
  /stats:3        Detailed breakdown for session turn 3
  /stats:7d       Global usage, last 7 days
  /stats:cost     Global cost breakdown

Aliases: /tokens, /tok
