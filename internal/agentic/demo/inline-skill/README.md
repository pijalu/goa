<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Inline Skill Execution Demo

Demonstrates the `inline` skill execution mode with automatic context compression.

## What This Shows

**Inline mode** (`SkillExecutionMode: "inline"`) makes skills execute within the parent LLM session instead of spawning isolated sub-agents. This is faster and uses fewer provider connections, but requires context compression to manage history growth.

**Context compression** automatically shrinks the conversation history when it exceeds a configured threshold, keeping the conversation within the LLM's context window.

## Running

Requires a local LLM at `http://localhost:1234/v1/chat/completions` (e.g., LM Studio, Ollama, llama.cpp).

```bash
cd demo/inline-skill
go run main.go
```

Optional environment variables:
- `LLM_ENDPOINT` — override the default endpoint
- `LLM_MODEL` — specify the model name

## What to Watch For

1. **Skill execution is inline**: The `learn_skill` tool returns the skill's markdown instructions. The LLM follows them and calls `read_file` in the same session.

2. **Context compression triggers**: Watch the console for `[CONTEXT]` markers showing current usage. If the conversation grows large enough, `[COMPRESSION]` markers appear as `tool_elision` or `selective` compression runs.

3. **Conversation continues after compression**: The LLM retains context from the compressed summary and answers follow-up questions correctly.

## Demo Flow

1. Creates `sample.go` with intentional code issues
2. Calls `learn_skill(code-reviewer)` with `focus=bugs` — inline mode returns instructions, LLM reads the file
3. Asks a follow-up question about the review
4. Calls `learn_skill(code-reviewer)` again with `focus=style` — generates more history
5. Prints final context stats
