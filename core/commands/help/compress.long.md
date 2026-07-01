<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /compress[:strategy][:force]

Trigger context compression on the active agent session.
Reduces token usage by summarizing older conversation turns
or eliding tool results, depending on the strategy.

A manual invocation always forces compression, bypassing the
configured usage threshold so the command does something
visible even when the context is nearly empty.

The optional strategy argument overrides the configured one
for this invocation. Available strategies:

  tool_elision  replace old tool args/results with placeholders
  selective     drop oldest messages, keep system + recent turns
  summarize     ask the LLM to summarize older turns
  hybrid        tool_elision then selective then summarize
  micro         truncate old tool result bodies (default)

The "force" keyword (or "--force") disables any remaining
per-strategy thresholds.

Compression strategy, trigger threshold, and max-tokens can be
configured permanently via /config → Compression.

Examples:
  /compress                 Compress with the configured strategy (forced)
  /compress:micro           Force micro compaction
  /compress:summarize       Force summarization
  /compress:tool_elision    Force tool_elision
