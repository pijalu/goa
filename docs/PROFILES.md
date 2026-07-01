<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Agent Modes

Modes define the agent's **behavioral role** — system prompt, tool access, skills, autonomy defaults, and runtime guard rules. Built-in modes are compiled into the binary as embedded prompt files, and users can define custom modes by adding their own `definition.md` files. No Go code changes are required to add or modify a mode.

## Built-in Modes

### Coder

The default mode. Focused on implementing code changes using all available tools.

| Property | Value |
|----------|-------|
| **Body** | Default coding mode. Full codebase access. |
| **Default Autonomy** | `solo` (auto-run constrained to codebase) |
| **Definition** | `prompts/mode/coder/definition.md` |

### Planner

Decomposes work, produces plans, and analyzes requirements.

| Property | Value |
|----------|-------|
| **Body** | Planning and architecture mode. |
| **Default Autonomy** | `review` (queues writes for batch approval) |
| **Guard** | Writes/edits limited to `.goa/plan`, `.agents/plan`, or markdown files with `plan` in the name. Bash commands limited to plan directories. |
| **Definition** | `prompts/mode/planner/definition.md` |

### Reviewer

Reviews code changes, provides feedback, and validates quality.

| Property | Value |
|----------|-------|
| **Body** | Code review and quality mode. |
| **Default Autonomy** | `review` (queues writes for batch approval) |
| **Definition** | `prompts/mode/reviewer/definition.md` |

### Coding-Posture

A coding mode that injects an explicit risk-posture preamble before non-trivial work.

| Property | Value |
|----------|-------|
| **Body** | Coding Posture preamble with modes such as debug, fix, review, test-first, refactor, optimize, migrate, upgrade, integrate, spike, and unstuck. |
| **Default Autonomy** | `solo` (auto-run constrained to codebase) |
| **Definition** | `prompts/mode/coding-posture/definition.md` |

## Mode Resolution

Modes are loaded from `prompts/mode/<name>/definition.md` in the embedded prompt tree. User-defined modes in `.goa/prompts/mode/<name>/definition.md` override or extend the built-in defaults.

Resolution order:
1. User project dir: `.goa/prompts/mode/<name>/definition.md`
2. User home dir: `~/.goa/prompts/mode/<name>/definition.md`
3. Embedded default: `prompts/mode/<name>/definition.md`

## Mode Definition Format

Each mode is a Markdown file with YAML frontmatter:

```yaml
---
major: coder
name: Coder
description: Full coding mode (default, solo autonomy)
default_autonomy: solo
default_skills: []
allowed_tools: []      # empty means all tools allowed
blocked_paths: []      # empty means no path restrictions
temperature: 0.0       # 0 means use config default
max_tokens: 0          # 0 means use config default
guard:
  rules:
    - tools: [write, edit]
      expr: 'regexMatch(path, `\.goa/plan`) || regexMatch(path, `\.agents/plan`)'
      message: "Writes are restricted to plan directories."
---

Mode prompt body goes here...
```

The mode body is injected into the system prompt when the mode is active. Custom modes can restrict tool access and paths for sub-agents.

### Guard rules

The optional `guard` section enforces access-control rules at tool-call time. Each rule can match one or more tools and either:

- `allow`: a list of regex patterns; the tool passes if every referenced path matches at least one pattern
- `expr`: an [expr-lang](https://expr-lang.org/) expression that receives `tool` (string) and `path` (string) and must return a boolean

Helper functions available in `expr`:

| Function | Description |
|----------|-------------|
| `regexMatch(s, pattern)` | Returns whether `s` matches the Go regex `pattern` |
| `contains(s, substr)` | Substring check |
| `hasPrefix(s, prefix)` | Prefix check |
| `hasSuffix(s, suffix)` | Suffix check |
| `base(s)` | File basename |

A rule's `message` is shown to the model when the rule rejects a tool call, so it should clearly explain what paths are allowed.

## Switching Modes

At runtime, use the `/mode` command or the mode hotkeys:

| Hotkey | Action |
|--------|--------|
| `alt+m` | Cycle to the next major mode |
| `alt+o` | Open the interactive mode selector |
| `ctrl+shift+m` | Cycle autonomy level |

```
/mode               → Open interactive mode picker
/mode:coder         → Switch to coder
/mode:planner       → Switch to planner
/mode:my-custom     → Switch to custom mode
```

When a conversation is active, mode and thinking-level changes are announced immediately but only take effect at the **start of the next turn**, so the current turn is not interrupted.

## Custom Modes

Create custom modes in `.goa/prompts/mode/<name>/definition.md` with the same frontmatter structure. The mode will automatically appear in the `/mode` picker and `/config` settings menu.

```
.goa/prompts/mode/my-coder/definition.md:
---
major: my-coder
name: My Custom Coder
description: Coder that always writes tests first
default_autonomy: confirm
default_skills: [test-gen]
allowed_tools:
  - read
  - write
  - edit
  - bash
  - search
---

## Mandatory Rules
- Always write tests before implementation code
- Run tests after every change
- Maintain 80%+ coverage
```

## Mode vs Autonomy

Mode controls the role and system prompt. Autonomy (set via `/autonomy` or `ctrl+shift+m`) controls how tool calls are approved:

| Autonomy | Behavior |
|----------|----------|
| `yolo` | Execute all tool calls automatically |
| `solo` | Auto-run tools constrained to the codebase |
| `confirm` | Pause before each tool call |
| `review` | Queue writes for batch approval at turn end |

Use `/autonomy <level>` or `ctrl+shift+m` to change the autonomy for the current mode. The change persists to the project config via `mode.defaults.<modename>`.
