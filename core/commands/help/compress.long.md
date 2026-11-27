<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /compress[:strategy][:force]
Alias: /compact[:strategy][:force]

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

Idle summarize (context_compression.idle_summarize) runs the same explicit
summarize path once after a completed turn has remained idle. By default it
waits 240 seconds and requires at least 10% projected context occupancy. A
new exchange cancels and resets the countdown, including an in-flight request;
completion does not rearm until another exchange stops. Set delay_seconds: 0
to disable it. These settings live under /config → Compression → Advanced.
Per-model values live under context_compression.per_model.<model-id>.idle_summarize
and are editable via Advanced → Per-model overrides. Omitted fields inherit the
global values; explicit delay_seconds: 0 disables only that model.

Examples:
  /compress                 Compress with the configured strategy (forced)
  /compress:micro           Force micro compaction
  /compress:summarize       Force summarization
  /compact:summarize        Alias for the same explicit summarize path
  /compress:tool_elision    Force tool_elision
