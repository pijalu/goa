<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Agent SDK Integration

Goa uses the **Agent SDK** located in [`internal/agentic/`](../internal/agentic/). The SDK was originally a standalone module and has been merged into the Goa repository, so it carries no external dependency.

## SDK Architecture

```
┌──────────────────────────────────────────────────────┐
│  Agent (orchestrator)                                │
│  • Manages conversation history                      │
│  • Creates sessions with LLM provider                │
│  • Executes tools when LLM requests them             │
│  • Emits events to OutputObservers                   │
└───────────────────────┬──────────────────────────────┘
                        │
          ┌─────────────┼──────────────┐
          ▼             ▼              ▼
   ┌──────────────┐ ┌──────────┐ ┌────────────────┐
   │ LLMProvider  │ │   Tool   │ │ OutputObserver │
   │  Interface   │ │ Interface│ │   Interface    │
   │   Stream()   │ │ Schema() │ │   OnEvent()    │
   │              │ │ Execute()│ │                │
   └──────────────┘ └──────────┘ └────────────────┘
```

### Agent (`internal/agentic.Agent`)

The core orchestrator. Goa instantiates one (or more for multi-agent) via `AgentManager`:

```go
agent := agentic.NewAgent(agentic.Config{
    Provider:     provider,
    SystemPrompt: prompt,
    Logger:       logger,
    Tools:        tools,     // []agentic.Tool from ToolRegistry
})
agent.AddObserver(observer)  // connects to TUI event bus
agent.Run(ctx, input)        // blocks until response
```

### LLMProvider Interface (`internal/agentic.LLMProvider`)

Abstraction for any LLM backend. Goa uses the built-in `OpenAIProvider`:

```go
provider := &agentic.OpenAIProvider{
    Endpoint: "http://localhost:1234/v1/chat/completions",
    Model:    "llama-3.2-1b-instruct",
    Client:   &http.Client{},
    Logger:   log,
}
```

Compatible with: OpenAI API, llama.cpp, LM Studio, Ollama (OpenAI-compatible mode).

### Tool Interface (`internal/agentic.Tool`)

Every tool in Goa's `tools/` package implements this interface:

```go
type Tool interface {
    Schema() agentic.ToolSchema
    Execute(input string) (string, error)
}

type ToolSchema struct {
    Name        string
    Description string
    Schema      map[string]interface{}  // JSON Schema
}
```

Goa uses its own `ToolRegistry` that wraps `agentic.ToolRegistry` with `Documentable` support for self-documenting tools.

### OutputObserver Interface (`internal/agentic.OutputObserver`)

Goa's TUI subscribes to agent events via observers:

```go
type OutputObserver interface {
    OnEvent(event OutputEvent)
}

type OutputEvent interface {
    Type() OutputEventType
    Message() Message
}
```

Goa's `AgentManager` bridges agent events to TUI panes (chat, thinking, tools, logs).

## SDK Features Used by Goa

| Feature | Goa Usage | Location |
|---------|-----------|----------|
| `Agent.Run()` | Core agent loop | `core/agentmanager.go` |
| `OpenAIProvider` | LLM provider | `provider/manager.go` → `CreateProvider()` |
| `Tool` interface | All 10+ tools | `tools/*.go` |
| `ToolRegistry` | Schema aggregation | `tools/registry.go` |
| `OutputObserver` | TUI event bridge | `core/agentmanager.go` → `tui/` |
| `SkillRunner` | Sub-agent skill execution | `skills/runner.go` |
| `AgentBus` | Multi-agent messaging | `multiagent/orchestrator.go` |
| `SendMessageTool` | Inter-agent communication | `multiagent/` |
| `CommConnector` | Auto-receive messages | `multiagent/` |
| `Helper.ConsoleObserver` | Terminal output | (debug mode) |
| `Helper.MessageLogObserver` | Structured logs | `core/sessionstore.go` |

## How Goa Creates and Uses an Agent

```
main.go
  │
  ├── config.NewCascadeLoader()     → load config
  ├── provider.NewProviderManager() → manage providers
  ├── tools.NewToolRegistry()       → register tools
  ├── core.NewAgentManager()        → manages agent lifecycle
  │     │
  │     ▼
  │   agentic.NewAgent(Config{...})
  │     │
  │     ├── add OutputObserver → events forwarded to TUI
  │     │
  │     └── agent.Run(ctx, input)  → blocks, streams events
  │
  └── tui.NewAppModel()            → event consumer
```

## SkillRunner Integration

Goa's `skills/runner.go` wraps [`internal/agentic/skillrunner`](../internal/agentic/skillrunner/):

```go
runner, _ := skillrunner.NewRunner(skillrunner.Config{
    Loader:   skillLoader,
    Provider: provider,
    WorkDir:  projectDir,
})
// Add runner as a tool so the LLM can invoke skills
toolRegistry.Register(runner)
```

Skills are SKILL.md files with YAML frontmatter. When invoked via `run_skill`, the runner creates a sub-agent with limited tools (read, edit, run_command, rest_api).

## Inter-Agent Communication (AgentBus)

Goa uses `internal/agentic.AgentBus` for multi-agent patterns:

```go
bus := agentic.NewAgentBus()
plannerInbox, _ := bus.Register("planner")
coderInbox, _ := bus.Register("coder")

plannerSend := &agentic.SendMessageTool{Bus: bus, FromName: "planner"}
plannerConn := agentic.NewCommConnector(plannerAgent, plannerInbox)

// Messages flow automatically
plannerAgent.Run(ctx, "Decompose and implement...")
```

Two Goa orchestrators consume this:
- `PairOrchestrator`: planner → coder → planner
- `ReviewerOrchestrator`: coder → reviewer → coder (up to N cycles)
