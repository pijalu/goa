<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Goa User Guide

> **Terminal-native AI coding agent with multi-agent collaboration, workflow automation, and full LLM transparency.**

Welcome to the Goa user guide. This document covers core usage, the three most
powerful multi-agent features (Workflows, Orchestrator, Companion), and
configuration references.

If you're new to Goa, start with the [Quick Start](#0-quick-start) section
below, then explore the features that match your workflow.

## Table of Contents

0. [Quick Start](#0-quick-start)
1. [Workflows — Multi-Stage Pipelines](#1-workflows--multi-stage-pipelines)
2. [Orchestrator — Multi-Agent Topologies](#2-orchestrator--multi-agent-topologies)
3. [Companion — Sub-Agent Code Review](#3-companion--sub-agent-code-review)
4. [Feature Comparison](#4-feature-comparison)
5. [Configuration Reference](#5-configuration-reference)

---

## 0. Quick Start

### First Run

```bash
# Build from source
make build

# Start Goa — first run launches the setup wizard
./goa
```

The setup wizard walks you through:
1. Configuring an LLM provider (endpoint, model, API key)
2. Selecting an agent profile (coder, planner, reviewer)
3. Choosing an execution mode (yolo, confirm, review)

After setup, you'll see the main TUI screen with a chat viewport, status bar,
and input line at the bottom.

### Basic Usage

Type your request at the prompt and press `Enter`. Goa sends your message to
the configured LLM, which streams its response, thinking blocks, and tool calls
into the chat viewport.

**Example conversation:**
```
> read the file cmd/goa/main.go and explain it
```

The agent will:
1. Read the file using the `read` tool
2. Analyze the code
3. Return a formatted explanation

### Key Concepts

| Concept | Description |
|---------|-------------|
| **Slash Commands** | Type `/` to access commands (help, mode, model, skills, etc.) |
| **Execution Mode** | Controls tool approval: `yolo` (auto), `confirm` (pause before tools), `review` (queue writes) |
| **Agent Mode** | Your role: `coder` (default), `planner`, `reviewer` |
| **Tools** | Agent capabilities: read, write, edit, search, bash, and more |
| **Skills** | Reusable prompt templates for specialized tasks |

### Common First Commands

```
/help                  → List all commands and setup tips
/mode:coder            → Switch to coder mode
/model gpt-4o          → Switch models
/session:new           → Start a fresh session
```

### Getting Help

- `/help` — list all commands
- `/help <command>` — detailed help for a specific command
- `/docs` — list all embedded documentation
- `/docs:TOPIC` — read a specific document (e.g., `/docs:TOOLS`)
- `/hotkeys` — show keyboard shortcuts
- `/cmd?` — short help for any command
- `/cmd??` — long help for any command

---

## 1. Workflows — Multi-Stage Pipelines

Workflows let you define **multi-stage, multi-agent pipelines** where different
agent roles (planner, coder, reviewer) execute sequentially, each building on
the previous stage's output.

```
Workflow "Implement Feature"
  │
  ├── Stage 1: Plan     → planner agent
  ├── Stage 2: Implement → coder agent
  └── Stage 3: Review   → reviewer agent
```

### When to Use Workflows

- **Structured multi-step tasks** — you need a plan before you code, a review
  after you code.
- **Role separation** — different LLM models for different roles (e.g., a
  powerful planner model, a fast coder model).
- **Reproducible pipelines** — you run the same workflow repeatedly with
  different inputs.

### Built-in Workflows

| Workflow | Stages | Description |
|----------|--------|-------------|
| `implement-feature` | Plan → Implement → Review | Full feature implementation pipeline |
| `review-changes` | Review | Quick review of uncommitted changes |

### Running a Workflow

Use the `/workflows` command:

```
/workflows:list                        → List all available workflows
/workflows:show implement-feature      → Show detailed info about a workflow
/workflows:run:implement-feature       → Run with interactive input prompt
/workflows:implement-feature           → Shorthand (same as above)
/workflows:run:implement-feature "Add OAuth login"  → Run with direct input
/workflows:cancel                      → Cancel a running workflow
```

The `:run:` colon syntax enables tab completion:

```
/workflows:⭾    → Tab completes to /workflows:run:
/workflows:run:⭾ → Tab completes workflow names
```

### How Workflows Work

#### Agent Team Model

When a workflow starts, Goa creates a pool of agents — one for each role
defined in the workflow stages. They are registered on a shared **AgentBus**
that allows inter-agent messaging.

1. **All agents are pre-created** in a pool
2. They register on a shared **AgentBus** — each agent can message any other
3. Only the **current stage agent** is actively running at any one time
4. When the active agent calls `workflows_next`, the orchestrator:
   - Marks the current stage as complete
   - Starts the next stage agent with accumulated context
5. Agents use `send_message` / `receive_message` tools to communicate

#### Tool Availability Per Role

| Tool | Planner | Coder | Reviewer |
|------|---------|-------|----------|
| `send_message` | ✅ | ✅ | ✅ |
| `receive_message` | ✅ | ✅ | ✅ |
| `workflows_next` | ✅ | ✅ | ✅ |
| `read` | ❌ | ✅ | ✅ |
| `edit` | ❌ | ✅ | ❌ |
| `bash` | ❌ | ✅ | ❌ |
| `write` | ❌ | ✅ | ❌ |

#### Stage Flow Example

```
User: /workflows:run:implement-feature "Build a chat UI"

      [system] Starting workflow: Implement Feature

Stage 1 (planner):
  - Reads the user request
  - May ask one clarification question at a time:
      Summary: <what's understood>
      Question: <one question>
      Options: <possible answers>
  - Creates a detailed implementation plan
  - Calls workflows_next

Stage 2 (coder):
  - Reads the plan from conversation history
  - Can message the planner for clarification via send_message
  - Implements using tools (write, edit, bash, etc.)
  - Calls workflows_next

Stage 3 (reviewer):
  - Reviews the implementation
  - Can request fixes from coder via send_message
  - Calls workflows_next → workflow complete
```

### Creating Custom Workflows

Workflows are defined in the `workflows/` directory at the project root (or
`~/.goa/workflows/` for user-level custom workflows).

#### Directory Structure

```
workflows/
  implement-feature/
    definition.yaml      # Workflow stages configuration
    plan.md              # Planner stage prompt
    implement.md         # Coder stage prompt
    review.md            # Reviewer stage prompt
  my-custom-workflow/
    definition.yaml
    stage1.md
    stage2.md
```

#### `definition.yaml` Format

```yaml
id: my-custom-workflow
name: My Custom Workflow
description: Automate a custom multi-step process
stages:
  - id: stage1
    name: Stage One
    agent: planner
    prompt: stage1.md         # Relative to workflow directory

  - id: stage2
    name: Stage Two
    agent: coder
    prompt: stage2.md

  - id: stage3
    name: Stage Three
    agent: reviewer
    prompt: prompts://my-shared-prompt  # From prompt registry
```

#### Prompt Resolution

Prompts are resolved in this order:
1. **Relative file path** — resolved against the workflow directory
2. **`prompts://` URI** — resolved from the shared prompt registry
3. **Inline text** — returned as-is

Relative paths take precedence over `prompts://` URIs, allowing workflows to
override shared prompts.

### PipelineRun Lifecycle

| Method | Description |
|--------|-------------|
| `NewPipelineRun(pipeline)` | Create a new run with all stages pending |
| `NextStage()` | Advance to next stage, mark previous as complete |
| `CompleteStage(id)` | Mark a specific stage as completed |
| `Cancel()` | Cancel the run |
| `StatusSnapshot()` | Thread-safe snapshot of current state |

### Tips

- **Use different models for different roles.** Configure a powerful model for
  the planner and a fast one for the coder in your config.
- **Keep stage prompts focused.** Each stage prompt should describe only what
  that agent needs to do, not re-explain the whole workflow.
- **Use `send_message` for clarification.** The coder can ask the planner
  questions mid-implementation without waiting for the next workflow run.

---

## 2. Orchestrator — Multi-Agent Topologies

The orchestrator runs **multi-agent orchestration with per-run topology
selection**: you choose how agents collaborate (hub, fanout, or pipeline)
for each run. It sits above the workflow system and provides:

- **Topology selection** per run — hub, fanout, or pipeline
- **Bounded agent pool** with per-model and total concurrency caps
- **Live TUI observability** with Conversation and Stats tabs
- **Steering** — inject guidance into running agents
- **Goal binding** — attach objective tracking with budget enforcement
- **Event sourcing** — every run is fully resumable

### When to Use the Orchestrator

- **Complex research tasks** — use hub topology to delegate sub-questions to
  specialist agents.
- **Parallel analysis** — use fanout to run multiple agents simultaneously on
  independent aspects of a problem.
- **Sequenced delegation** — use pipeline for stages that depend on each
  other's output.
- **Long-running tasks** — runs are persisted and resumable even after a
  crash.
- **Goal-bound work** — attach a budget and completion criteria to the run.

### Topologies

#### Hub Topology

The orchestrator agent acts as a **hub**: it delegates sub-tasks to specialist
agents and synthesizes their answers. The hub decides who does what.

```
                      ┌──────────────┐
         ┌─────────── │ Orchestrator │ ───────────┐
         │            └──────────────┘            │
         ▼                                        ▼
  ┌──────────────┐                        ┌──────────────┐
  │  Specialist   │                        │  Specialist   │
  │  (coder)      │                        │  (planner)    │
  └──────────────┘                        └──────────────┘
```

**Use when:** you want a model to decide how to decompose and delegate work.
Best for open-ended research or complex tasks with unknown structure.

#### Fanout Topology

Every configured role runs **one turn in parallel** against the objective.
Fastest topology for independent specialists.

```
         ┌──────────────────┐
         │    Objective      │
         └────────┬─────────┘
                  │
     ┌────────────┼────────────┐
     ▼            ▼            ▼
┌────────┐ ┌────────┐ ┌────────┐
│ Agent 1 │ │ Agent 2 │ │ Agent 3 │
│ (coder) │ │(planner)│ │(reviewer)│
└────────┘ └────────┘ └────────┘
```

**Use when:** you have independent aspects of a problem that can be explored
simultaneously. Fastest completion time.

#### Pipeline Topology

Roles run **sequentially**; each agent's output is carried forward as context
to the next stage. Same concept as workflows but configurable per-run.

```
   Agent 1    →    Agent 2    →    Agent 3
  (planner)       (coder)        (reviewer)
```

**Use when:** stages have strict dependencies — a review must happen after
implementation, which must happen after planning.

### Configuration

Configure orchestrator roles, pool limits, and defaults in your config:

```yaml
orchestrator:
  roles:
    orchestrator:
      model: gpt-4o                    # The planner/delegator
    coder:
      model: claude-sonnet-4-20250514
      provider: anthropic              # Optional per-role provider
      allowed_tools: [bash, edit]      # Optional tool allowlist
    planner:
      model: gpt-4o
    reviewer:
      model: claude-sonnet-4-20250514
  pool:
    max_total_agents: 4                # Total concurrent agents
    max_agents_per_model:
      gpt-4o: 2                        # Per-model concurrency cap
  defaults:
    topology: hub                      # hub | fanout | pipeline
```

### Command Reference

```
/orchestrate new hub "Research X and summarize"     → New hub run
/orchestrate new fanout "Analyze from all angles"   → New fanout run
/orchestrate new pipeline "Build step by step"      → New pipeline run
/orchestrate new fanout goal "Implement X"           → Goal-bound run
/orchestrate list                                     → List all runs
/orchestrate resume <run-id>                          → Resume a persisted run
/orchestrate steer all "consider edge cases"          → Broadcast to all agents
/orchestrate steer orchestrator "stay focused"        → Steer orchestrator only
/orchestrate steer coder-1 "optimize for readability" → Steer specific agent
```

Shorthand aliases:

```
/orch new hub "Research X"
/orch list
/orch resume run-abc123
```

### Live TUI Tabs

While an orchestrator run is active, a persistent **tab bar** appears above
the input line with two tabs (navigate with `Ctrl+x` or click on the tab number):

#### Conversation Tab (default)

Shows the orchestrator and every specialist agent streaming their thinking,
content, and tool calls into the main chat viewport as agent-labeled blocks.
Parallel agents each get their own distinct widget.

```
┌─ Conversation ──┬─ Stats ───────────────────────────────────┐
│                                                               │
│  ▸ orchestrator [gpt-4o]: Let me break this down...           │
│                                                               │
│  ▸ coder-1 [claude-sonnet]: I'll implement the auth module... │
│     ┌──────────────────────────────────────────────────┐      │
│     │ ◉ bash npm install passport                      │      │
│     │   ← Exit: 0                                      │      │
│     └──────────────────────────────────────────────────┘      │
│                                                               │
│  ▸ planner-1 [gpt-4o]: The architecture should follow...      │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

#### Stats Tab

Shows the live agent table with real-time metrics:

```
┌─ Conversation ──┬─ Stats ───────────────────────────────────┐
│                                                               │
│  Role         │ Model              │ Turns │ Tokens  │ Status │
│  ───────────────────────────────────────────────────────────  │
│  orchestrator │ gpt-4o             │  3    │ 1,234   │ ▶      │
│  coder-1      │ claude-sonnet      │  2    │ 892     │ ▶      │
│  planner-1    │ gpt-4o             │  1    │ 456     │ ✓      │
│  reviewer-1   │ claude-sonnet      │  0    │ 0       │ ⏳     │
│                                                               │
│  Aggregate: 6 turns · 2,582 tokens · 78% cache hit            │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

### Steering

Steering lets you inject guidance into running agents without waiting for a
turn to finish.

| Tab | Steering target |
|-----|----------------|
| **Conversation** | Most recently started agent (e.g., the currently delegated specialist) |
| **Stats** | Broadcast to all live agents + orchestrator |

```
/orchestrate steer all "double-check error handling"
/orchestrate steer coder-1 "use functional options pattern"
```

Or type directly in the input prompt shown in the footer when an orchestrator
run is active.

### Goal Binding

Add `goal <objective>` to bind a run to a goal:

```
/orchestrate new fanout goal "Refactor auth module" \
  "Analyze current auth" "Design new auth" "Implement changes"
```

The run:
- Accrues aggregate token usage across all agents to the goal budget
- On budget exhaustion → run aborts, goal marked **blocked**
- On success → goal marked **complete**

### Caps & Backpressure

The bounded agent pool enforces two limits:

| Limit | Description |
|-------|-------------|
| `max_total_agents` | Maximum concurrent live agents across all models |
| `max_agents_per_model` | Maximum concurrent agents per specific model |

When a cap is saturated, **Acquire blocks** (FIFO, context-cancellable) until
an agent releases. Caps release on all exit paths (success, crash, context
cancel).

### Event Sourcing & Resumability

Every orchestrator run is fully event-sourced:

```
.goa/orchestrator/<run-id>/
  events.jsonl       → Full event log (NDJSON)
```

Event types: `RunStarted`, `AgentStarted`, `AgentMessage`, `AgentThinking`,
`AgentToolCall`, `AgentToolResult`, `AgentStats`, `AgentFinished`,
`RunFinished`.

To resume a run:

```
/orchestrate resume <run-id>
goa --orchestrate <run-id>          # Headless resume
```

On resume, Goa replays all events to rebuild agent state, stats, and steering
queues, then resumes from the last non-terminal event. Crashed mid-flight
agents are marked `Crashed` and re-acquired.

### Headless Mode

Run orchestrations without the TUI:

```bash
goa --orchestrate run-abc123 --yes --max-turns 20
goa --orchestrate run-abc123 --prompt "continue the analysis"
```

Useful for CI/CD pipelines, overnight batch processing, or server deployments.

---

## 3. Companion — Sub-Agent Code Review

The companion is a **dedicated sub-agent** that provides code review and
critique. It can operate in two modes:

| Mode | Trigger | Description |
|------|---------|-------------|
| **Agent-driven** (default) | LLM calls `request_review` / `delegate_to` tools | The main agent decides when to ask for a review |
| **Framework-driven** | Automatic after every turn | The companion reviews every main-agent output |

### When to Use Companion Mode

- **Code review automation** — catch issues before they reach production.
- **Teaching and mentoring** — the companion acts as a senior reviewer.
- **Quality gate** — enforce coding standards and best practices.
- **Agent-driven delegation** — let the LLM decide when it needs a second
  opinion.

### Agent-Driven Mode

In the default **agent-driven** mode, the main agent (coder, planner) can
request reviews on its own initiative using two tools:

#### `request_review`

The agent requests a code review of its current output from the companion:

```
The agent will send its work to the companion and receive feedback.
The companion reviews the code, suggests improvements, and reports issues.
```

The agent decides when a review is needed — e.g., after completing a complex
function, before making a commit, or when uncertain about design decisions.

#### `delegate_to`

The agent delegates a sub-task to a specific role:

```
/delegate_to coder "Write unit tests for the auth module"
/delegate_to companion "Review the error handling approach"
/delegate_to planner "Design the database schema"
```

Arguments:
- `agent`: `"coder"`, `"companion"`, or `"planner"`
- `task`: description of the task to delegate

The delegated agent runs independently and returns its result. The main agent
incorporates the result into its ongoing work.

### Framework-Driven Mode

In **framework-driven** mode, the companion automatically reviews every main
agent turn. No LLM initiative needed.

```
User prompt → Main agent → [output] → Companion reviews → Feedback
```

The companion reviews:
1. Code quality
2. Error handling
3. Security concerns
4. Performance implications
5. Adherence to the original requirements

The companion's feedback is shown in the chat viewport, labeled as a
**companion cycle**:

```
  ┌─ Companion · Cycle 1 ─────────────────────────────────────────┐
  │                                                                │
  │ Review findings:                                               │
  │ • Missing error handling in database connection                │
  │ • Consider using context.WithTimeout for HTTP calls            │
  │ • LGTM: test coverage meets 80% threshold                     │
  │                                                                │
  │ Suggestions:                                                   │
  │ 1. Wrap os.ReadFile errors with additional context             │
  │ 2. Extract magic string "localhost:8080" to a constant         │
  │                                                                │
  └────────────────────────────────────────────────────────────────┘
```

### Command Reference

```
/companion              → Show current companion status
/companion:on           → Enable companion (agent-driven mode)
/companion:agent        → Enable agent-driven mode (default)
/companion:framework    → Enable framework-driven mode
/companion:off          → Disable companion entirely
```

Bare `/companion` shows the current mode:

```
Companion mode: enabled (agent-driven)
Companion mode: enabled (framework-driven)
Companion mode: disabled
```

### Configuration

Configure the companion model in your config:

```yaml
agent:
  companion:
    model: gpt-4o-mini              # Use a smaller model for reviews
    provider: openai                # Optional per-agent provider
```

If not configured separately, the companion reuses the main agent's model.

### Autocomplete

The companion subcommand supports tab completion:

```
/companion:⭾            → Shows available options
/companion:agent⭾       → Completes to /companion:agent
```

Completions adapt to current state:
- When off: `on`, `agent`, `framework`
- When agent-driven: `off`, `framework`
- When framework-driven: `off`, `agent`

### Companion States

| Status | Meaning |
|--------|---------|
| **disabled** | No companion agent. Main agent works independently |
| **agent-driven** | Companion available via tools. LLM decides when to invoke |
| **framework-driven** | Companion runs after every main-agent turn automatically |

### Use Cases

| Scenario | Recommended Mode | Why |
|----------|-----------------|-----|
| Solo coding | Agent-driven | Let the LLM decide when to ask for review |
| PR preparation | Agent-driven | Request review before submitting changes |
| Teaching/mentoring | Framework-driven | Get automatic feedback on every change |
| Codebase onboarding | Framework-driven | Companion catches project-specific patterns |
| Critical security code | Framework-driven | Every change is reviewed for vulnerabilities |
| Exploratory coding | Disabled | Uninterrupted flow during prototyping |

### Example Workflows with Companion

#### Agent-Driven Review Flow

```
1. User: "Implement a REST API for user management"
2. Agent implements the API
3. Agent: "Let me request a review of this implementation"
4. Agent calls request_review → companion reviews
5. Companion returns: "Add input validation, use DTOs"
6. Agent applies suggestions
7. Agent outputs final code
```

#### Framework-Driven Review Flow

```
1. User: "Add error handling to the logger"
2. Main agent writes error handling code
3. → Automatic companion review triggers
4. Companion: "Consider structured error types instead of strings"
5. Main agent can apply suggestions in the next turn
```

#### Delegation Flow

```
1. User: "Build a user authentication system"
2. Planner agent delegates:
   - "/delegate_to planner Design the auth database schema"
   - "/delegate_to coder Implement the registration endpoint"
   - "/delegate_to companion Review security of the token handling"
3. Each sub-agent runs, produces output
4. Planner synthesizes results into a final plan
```

---

## 4. Feature Comparison

| Aspect | Workflows | Orchestrator | Companion |
|--------|-----------|-------------|-----------|
| **Purpose** | Structured multi-stage pipelines | Flexible multi-agent topologies | Code review sub-agent |
| **Agents** | Fixed by workflow definition | Per-run role selection | Single companion agent |
| **Execution** | Sequential stages | Hub/fanout/pipeline | Post-turn or on-demand |
| **Control** | Pre-defined pipeline | Per-run topology choice | Toggle on/off, two modes |
| **Persistence** | Live only | Event-sourced, resumable | Live only |
| **Goal binding** | — | ✅ | — |
| **Steering** | — | ✅ (live, per-agent) | — |
| **Custom prompts** | Per-stage prompt files | Config-defined roles | Companion system prompt |
| **When to use** | Reproducible pipelines | Complex/exploratory tasks | Quality assurance |

---

## 5. Configuration Reference

### Workflows Configuration

Workflows are configured through their `definition.yaml` files. See
[WORKFLOWS.md](WORKFLOWS.md) for full details.

### Orchestrator Configuration

```yaml
orchestrator:
  roles:
    orchestrator:
      model: <model-id>
      provider: <provider-id>      # optional
      allowed_tools: [<names>]     # optional tool allowlist
    <role-name>:
      model: <model-id>
      provider: <provider-id>
      allowed_tools: [<names>]
  pool:
    max_total_agents: <int>
    max_agents_per_model:
      <model-id>: <int>
  defaults:
    topology: hub | fanout | pipeline
```

### Companion Configuration

```yaml
agent:
  companion:
    model: <model-id>
    provider: <provider-id>
```

### Environment Variables

| Variable | Feature | Description |
|----------|---------|-------------|
| `GOA_ORCHESTRATOR_ROLES_*` | Orchestrator | Override role model assignments |
| `GOA_ORCHESTRATOR_POOL_MAX_TOTAL_AGENTS` | Orchestrator | Override total agent cap |
| `GOA_AGENT_COMPANION_MODEL` | Companion | Override companion model |

---

## 6. Provider Quota — Usage Tracking

The bundled **provider-quota** plugin tracks usage/quota for configured
providers and shows a compact quota readout in the status bar.

### Status Bar

The footer shows the **active provider's** quota after the model name,
bracketed and color-coded by projected window-end usage:

```
(kimi-code) k3 • high • [5h:7% / wk:21%]
```

Color: green = comfortably in budget · orange = close to the limit · red =
projected to run out · white = still pending. Auth problems show as
`[∇ auth]`; a provider with no quota API falls back to local session tokens.

### Commands

| Command | Description |
|---------|-------------|
| `/quota` | Full session + per-provider quota breakdown (markdown tables) |
| `/quota:refresh` | Force-refresh all provider quotas |
| `/quota:json` | Machine-readable JSON output |
| `/quota:auth-status` | Show per-provider auth state |
| `/quota:login:<provider>` | OAuth device login (OAuth providers only) |
| `/quota:logout:<provider>` | Clear stored OAuth tokens |
| `/quota:<provider>` | Force-refresh one provider |

`Ctrl+Shift+Q` force-refreshes quota data without typing a command.

### Authentication

Quota auth is **separate** from model auth. API-key providers (Anthropic,
OpenAI, Z.ai, Kimi, MiniMax, OpenRouter) reuse the same key as inference — no
extra step. OAuth providers (none bundled by default) would need a one-time
device-code login via `/quota:login:<provider>`; the token is stored
(`~/.goa/plugins/provider-quota/storage.json`) and refreshed automatically.
Run `/quota:auth-status` to see provider auth state.

### Supported providers

Anthropic · OpenAI · Z.ai · Kimi · MiniMax · OpenRouter, plus a local
(inferred) fallback that counts session tokens.

OpenCode was removed: its real quota percentages (rolling/weekly/monthly)
are served only by the web app's cookie-session RPCs, which the OAuth
device flow cannot access programmatically — the console spend analytics
under-reported actual usage, making the bars misleading.

### Disabling

```yaml
plugins:
  bundled:
    provider-quota: false
```

---

## See Also

- [WORKFLOWS.md](WORKFLOWS.md) — Workflow system reference
- [ORCHESTRATOR.md](ORCHESTRATOR.md) — Orchestrator technical reference
- [COMMANDS.md](COMMANDS.md) — All command reference
- [CONFIGURATION.md](CONFIGURATION.md) — Full configuration reference
- [TOOLS.md](TOOLS.md) — Tool system reference
- [TUI.md](TUI.md) — TUI layout and keybindings
- [PLUGINS.md](PLUGINS.md) — JS plugin system and bridge API
- [AGENTIC-SDK.md](AGENTIC-SDK.md) — Agent SDK internals
