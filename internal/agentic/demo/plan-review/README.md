<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Plan-Review Demo: Multi-Agent Communication

This demo shows two agents — a **planner** and a **reviewer** — collaborating through the `AgentBus` to create and refine a plan. The planner drafts a plan, sends it to the reviewer, receives feedback, revises, and stops once approved.

## What it demonstrates

- **Inter-agent communication** via `send_message` tool over an `AgentBus`
- **Different system prompts** per agent (planner vs. reviewer roles)
- **Channel-based coordination** — deterministic completion without magic timeouts
- **Auto-receive** via `CommConnector` — messages flow into agents automatically

## Running the demo

### Live LLM (default)

```bash
go run demo/plan-review/main.go
```

Requires a local LLM at `http://localhost:1234/v1/chat/completions` (configurable).

### Mock mode (deterministic, no LLM)

```bash
go run demo/plan-review/main.go -mock
```

Uses scripted mock providers for instant, reproducible output.

## Architecture

```
┌─────────────┐     send_message      ┌─────────────┐
│   Planner   │ ────────────────────▶ │  Reviewer   │
│             │ ◀──────────────────── │             │
└─────────────┘     send_message      └─────────────┘
       ▲                                      ▲
       │      AgentBus (Go channels)          │
       └──────────────────────────────────────┘
```

### Key components

| Component | Purpose |
|-----------|---------|
| `AgentBus` | Routes `CommMessage` between registered agents |
| `SendMessageTool` | Tool that agents call to send messages |
| `CommConnector` | Auto-feeds incoming bus messages into `agent.Run()` |
| `coordinator` | Channel-based turn tracker — knows when conversation is done |

### Coordinator: deterministic completion

The demo uses a **work-tracking coordinator** instead of magic timeouts:

```go
// Tracks outstanding turns:
//   count = 1 initially (planner.Run)
//   +1 on every send_message (future turn created)
//   -1 on every EventEnd (turn completed)
// When count == 0, conversation is done.
coord := newCoordinator()
planner.AddObserver(coord)
reviewer.AddObserver(coord)

// Block until all turns are accounted for
coord.Wait()
```

## Conversation flow

```
USER → PLANNER: "Create a user onboarding flow..."

[PLANNER] → send_message: "DRAFT PLAN: 1. Sign-up..."
[REVIEWER] ← receives plan
[REVIEWER] → send_message: "FEEDBACK: Add error handling..."
[PLANNER] ← receives feedback
[PLANNER] → send_message: "REVISED PLAN: ..."
[REVIEWER] ← receives revised plan
[REVIEWER] → send_message: "APPROVED."
[PLANNER] ← receives approval → stops
```

## Code walkthrough

### 1. Create the bus and register agents

```go
bus := agentic.NewAgentBus()
plannerInbox, _ := bus.Register("planner")
reviewerInbox, _ := bus.Register("reviewer")
```

### 2. Create send tools

```go
plannerSend := &agentic.SendMessageTool{Bus: bus, FromName: "planner"}
reviewerSend := &agentic.SendMessageTool{Bus: bus, FromName: "reviewer"}
```

### 3. Create agents with tools

```go
planner := agentic.NewAgent(agentic.Config{
    Provider:     plannerProvider,
    SystemPrompt: "You are the planner...",
    Tools:        []agentic.Tool{plannerSend},
})

reviewer := agentic.NewAgent(agentic.Config{
    Provider:     reviewerProvider,
    SystemPrompt: "You are the reviewer...",
    Tools:        []agentic.Tool{reviewerSend},
})
```

### 4. Wire auto-receive connectors

```go
plannerConn := agentic.NewCommConnector(planner, plannerInbox)
reviewerConn := agentic.NewCommConnector(reviewer, reviewerInbox)
```

### 5. Kick off and wait

```go
planner.Run(ctx, "Create a user onboarding flow...")
coord.Wait() // blocks until conversation is done
```
