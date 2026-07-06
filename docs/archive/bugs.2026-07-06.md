<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Closed Bugs — 2026-07-06

Fixed by plan `docs/plans/orchestrator-conversation-rendering-fix.md`.

## Orchestate
The orchestrate agent does not seems to do much - I would expect the orchestrate to analyse the work and *then* send work to the coder(s) for implementation - review the output

**Fix:** Strengthened hub orchestrator prompt to require analyze → delegate → summary; made orchestration thinking, tool calls, and per-agent work visible in the chat viewport via agent-labeled streaming blocks.

## Incorrect rendering of conversation
In orchestrator mode, the rendering does not make much sense:
Conversation *must* be rendered as other chat (thinking/tool execution/...)

In case of parallel work - each widget should be update accordingly based - so 2 agents thinking should appear in distinct thinking blocks being streamed.
The position should match start-position of the streaming - the widget should stay at the bottom until it's complete.

The current rendering is incorrect:
```
[coder] `)
[coder]  is
...
```

**Fix:** Conversation now renders in the normal chat viewport using the existing widget primitives (`thinkingBlock`, `agentMessage`, `ToolExecutionComponent`). Each agent has its own stream state with in-place-updating blocks. Two concurrent agents produce two distinct in-place thinking blocks that stay at their start position. Chat is not suppressed during orchestration; Ctrl+x toggles Conversation ↔ Stats.
