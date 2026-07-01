<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Workflows

Goa's workflow system enables **multi-stage, multi-agent collaboration** where different AI agents work together as a team to complete complex tasks.

## Overview

A workflow is a sequence of stages, each assigned to a different agent role (planner, coder, reviewer). Agents communicate via a shared **AgentBus** and advance through stages by calling the `workflows:next` tool.

```
Workflow "Implement Feature"
  │
  ├── Stage 1: Plan     → planner agent
  ├── Stage 2: Implement → coder agent
  └── Stage 3: Review   → reviewer agent
```

All agents are registered on the same AgentBus and can message each other at any time using the `send_message` tool.

## Command

### `/workflows:list`
Lists all available workflows with box-drawn formatting:
```
┌─ Implement Feature ─────────────────────────────────────────────┐
│  Plan, implement, and review a feature                          │
│  3 stages:  Plan → Implement → Review                           │
│  Run: /workflows:run:implement-feature                          │
└─────────────────────────────────────────────────────────────────┘
```

### `/workflows:show <name>`
Shows detailed information about a workflow, including stage prompts.

### `/workflows:run:<name> [input]`
Runs a workflow. Supports colon syntax for tab completion:
```
/workflows:run:implement-feature "Add OAuth login"
/workflows:implement-feature "Add OAuth login"    (shorthand)
```

## How Workflows Work

### Agent Team Model

1. All agents are pre-created in a pool when the workflow starts
2. They are registered on a shared AgentBus (each agent can message any other)
3. Only the **current stage agent** is actively running
4. When the active agent calls `workflows:next`, the orchestrator:
   - Marks the current stage as complete
   - Starts the next stage agent with accumulated context
5. Agents use `send_message`/`receive_message` tools to communicate

### Tools Available to Agents

| Tool | Planner | Coder | Reviewer |
|------|---------|-------|----------|
| `send_message` | ✅ | ✅ | ✅ |
| `receive_message` | ✅ | ✅ | ✅ |
| `workflows:next` | ✅ | ✅ | ✅ |
| `read` | ❌ | ✅ | ✅ |
| `edit` | ❌ | ✅ | ❌ |
| `bash` | ❌ | ✅ | ❌ |

### Stage Flow

```
User: /workflows:run:implement-feature "Build a fire animation"

      [system] Starting workflow: Implement Feature

Stage 1 (planner):
  - Reads the user request
  - Asks clarification questions ONE at a time:
    Summary: <what's understood>
    Question: <one question>
    Options: <possible answers>
  - Creates a detailed plan
  - Calls workflows:next

Stage 2 (coder):
  - Reads the plan from conversation history
  - Can message planner for clarification
  - Implements using tools (write, bash, etc.)
  - Calls workflows:next

Stage 3 (reviewer):
  - Reviews the implementation
  - Can request fixes from coder via send_message
  - Calls workflows:next → workflow complete
```

## Directory Structure

Workflows are defined in the `workflows/` directory at the project root:

```
workflows/
  implement-feature/
    definition.yaml      # Workflow stages configuration
    plan.md              # Planner stage prompt
    implement.md         # Coder stage prompt
    review.md            # Reviewer stage prompt
  review-changes/
    definition.yaml
    review.md
```

### `definition.yaml` Format

```yaml
id: implement-feature
name: Implement Feature
description: Plan, implement, and review a feature
stages:
  - id: plan
    name: Plan
    agent: planner
    prompt: plan.md           # Relative to workflow dir, or prompts:// URI

  - id: implement
    name: Implement
    agent: coder
    prompt: implement.md

  - id: review
    name: Review
    agent: reviewer
    prompt: review.md
```

### Prompt Resolution

Prompts are resolved in this order:
1. **Relative file path** — resolved against the workflow directory
2. **`prompts://` URI** — resolved from the shared prompt registry
3. **Inline text** — returned as-is

Relative paths take precedence over `prompts://` URIs, allowing workflows to override shared prompts.

## PipelineRun Lifecycle

| Method | Description |
|--------|-------------|
| `NewPipelineRun(pipeline)` | Create a new run with all stages pending |
| `NextStage()` | Advance to the next stage, mark previous as complete |
| `CompleteStage(id)` | Mark a specific stage as completed |
| `Cancel()` | Cancel the run |
| `StatusSnapshot()` | Thread-safe snapshot of current state |

## Built-in Workflows

| Workflow | Stages | Description |
|----------|--------|-------------|
| `implement-feature` | Plan → Implement → Review | Full feature implementation pipeline |
| `review-changes` | Review | Quick review of uncommitted changes |

## Creating Custom Workflows

1. Create a directory under `workflows/<name>/`
2. Add a `definition.yaml` file
3. Add prompt files for each stage
4. Run with `/workflows:run:<name>`

Workflows can also be loaded from `~/.goa/workflows/<name>/definition.yaml` for user-level custom workflows.
