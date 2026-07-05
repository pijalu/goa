<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Goa Architecture

## Overview

Goa is a terminal-native AI coding agent built around the **Agent SDK** in [`internal/agentic/`](../internal/agentic/). It follows an **event-driven architecture** with clear separation between the TUI layer (presentation), the core engine (application logic), and the agent SDK (LLM interaction).

## System Layers

```
┌─────────────────────────────────────────────────────────────────┐
│                   TUI Layer (ANSI-native)                       │
│  TUI engine → Component tree → [Header] [ChatViewport]          │
│                                 [StatusMsg] [Editor] [Footer]   │
│                                 [Selector] [Completion popup]   │
│  Differential rendering, viewport scrolling, focus routing      │
└──────────────────────────────┬──────────────────────────────────┘
                               │ agentic OutputEvent / keyboard
┌──────────────────────────────▼──────────────────────────────────┐
│                      Core Engine                                │
│  CommandRouter    → Route /commands to registered handlers      │
│  AgentManager     → Manage agent lifecycle, forward events      │
│  ExecutionCtrl    → yolo/confirm/review state machine           │
│  LoopDetector     → 5 heuristics to detect agent loops          │
│  SessionStore     → Persist/restore sessions as JSONL           │
│  DocEngine        → ?/?? suffix → help/documentation            │
│  ConfigLoader     → Cascade: embed→home→project→local→env→flags │
│  Responsibility: Application logic, state management            │
└──────┬─────────────────────────────┬────────────────────────────┘
       │                                                          │
┌──────▼──────────┐        ┌─────────▼────────────────────────────┐
│  Agent SDK      │        │  Tool System                         │
│  (internal/     │        │  read  │ edit  │ write               │
│   agentic/)     │        │  search     │ bash       │ ssh_bash  │
│  Agent.Run()    │        │  bg_exec    │ memento    │ goa_cmd   │
│  Session.Stream()│       │  registry   │ gitutil    │ pathutil  │
│  OutputObserver │        │  documentable interface              │
│  SkillRunner    │        │  Responsibility: Interface to FS/OS  │
│  AgentBus       │        │                                      │
│  Responsibility:│        │                                      │
│  LLM orchestration│      └──────────────────────────────────────┘
└──────┬──────────                                                ┘
                                                                  │
┌──────▼──────────────────────────────────────────────────────────┐
│  Provider Layer                                                 │
│  OpenAIProvider → any OpenAI-compatible endpoint                │
│  (llama.cpp, LM Studio, Ollama, OpenAI API)                     │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│  Supporting Systems                                             │
│  MemoryStore    → .goa/memory/*.md persistent memory            │
│  DreamEngine    → consolidate memories into .goa/memory.dream/  │
│  ModeRegistry  → Built-in + custom modes from prompts/mode/     │
│  ProviderManager→ Active provider/model, model listing          │
│  SkillRegistry  → Discover and load SKILL.md files              │
│  PluginLoader   → JS plugin runtime (Goja)                      │
│  WorktreeMgr    → Git worktree isolation                        │
│  SessionStore   → JSONL session persistence                     │
└─────────────────────────────────────────────────────────────────┘
```

## Data Flow

```
User Input (TUI / CLI)
                      │
    ▼
┌─────────────────────┐
│  CommandRouter      │
│  (/cmd, /cmd?,      │
│   /cmd??)           │
└──────┬──────────────┘
                      │
       ▼
┌─────────────────────┐
│  AgentManager       │
│  • Creates agent    │
│  • Feeds input      │
│  • Collects events  │
└──────┬──────────────┘
                      │
       ▼
┌─────────────────────┐
│  internal/agentic   │
│  .Agent             │
│  • Sends to LLM     │
│  • Streams response │
│  • Executes tools   │
└──────┬──────────────┘
                      │
       ▼
┌─────────────────────┐
│  Tool Execution     │
│  • Validate input   │
│  • Execute tool     │
│  • Return result    │
│  • Forward event    │
└──────┬──────────────┘
                      │
       ▼
  ┌──────────┐
  │ Loop back │  ← if LLM calls another tool
  └──────────         ┘
                      │
       ▼
   Agent responds (no tools)
                      │
       ▼
┌─────────────────────┐
│  TUI Panes update   │
│  • Chat shows msg   │
│  • Thinking shows   │
│    reasoning stream │
│  • Tool pane logs   │
│  • Token bar updates│
└─────────────────────┘
```

## Module Dependency Graph

```
cmd/goa/ (CLI entry)   main.go (Goa entry)
    │                       │
    └──────┬────────────────┘
           ▼
    ┌──────────────┐
    │   config/    │ ←── yaml.v3
    └──────┬───────┘
           ▼
    ┌──────────────┐
    │  internal/   │  (shared types, enums, errors, worktree)
    └──────┬───────┘
           │
    ┌──────▼───────┐
    │    core/     │ ←─── config/, internal/
    │  commands/   │
    └──────┬───────┘
           │
    ┌──────▼───────┐  ┌────────────┐  ┌──────────────┐
    │   tools/     │  │  memory/   │  │  prompts/mode/  │
    └──────┬───────┘  └────────────┘  └──────┬───────┘
           │                                  │
    ┌──────▼───────┐                 ┌────────▼───────┐
    │  multiagent/ │                 │   provider/    │
    └──────┬───────┘                 └────────┬───────┘
           │                                  │
    ┌──────▼───────┐                 ┌────────▼───────┐
    │   skills/    │                 │   plugins/     │
    └──────┬───────┘                 └────────────────┘
           │
    ┌──────▼───────┐
    │    tui/      │
    └──────────────┘
```

## Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Commands self-register via `init()`** | Zero-config command registration; each command file is self-contained |
| **Config cascade: embedded → home → project → local → env → flags** | Clear precedence; deep-merge for maps, last-write-wins for scalars |
| **Git worktree isolation** | Complete filesystem sandbox; discardable via `git worktree remove` |
| **Tool errors: `[tool error: type]\n<detail>\nHint: <action>`** | Structured format optimized for LLM parsing and recovery |
| **Multi-agent via AgentBus + Go channels** | Lightweight inter-agent communication without shared mutable state |
| **Skills as SKILL.md files** | Plain markdown with YAML frontmatter — human-readable, version-controllable |
| **JS plugins via Goja** | Pure Go JS runtime; no CGO; agents can create plugins dynamically |

## Event Types (agentic SDK)

Events emitted by the agent and consumed by the TUI:

| Event | Description | TUI Consumer |
|-------|-------------|--------------|
| `EventStateChange` | Agent transitioned to new output state | StatusBar, ThinkingPane |
| `EventContent` | Text content from LLM or tool result | ChatPane, LogPane |
| `EventToolCall` | LLM requested a tool execution | ToolPane, ConfirmModal |
| `EventToolResult` | Tool execution completed | ToolPane, LogPane |
| `EventEnd` | Conversation turn ended | ChatPane (flush) |
| `EventTokenStats` | Token generation statistics | TokenBar |
| `EventProgress` | Prompt processing progress | TokenBar |

## Multi-Agent Communication

Agents communicate via the `AgentBus` — a Go channel-based message router:

```
┌──────────┐    CommMessage     ┌──────────┐
│ Planner  │──────────────────►│  Coder    │
│ Agent    │◄──────────────────│  Agent    │
└──────────┘    CommMessage     └──────────┘
     │                                     │
     │       CommConnector                 │
     │   (auto-feeds inbox to              │
     │    agent.Run())                     │
     └───────────────────────────────      ┘
```

Two orchestration patterns:
- **Pair**: Planner → Coder → Planner (decompose → implement → verify)
- **Reviewer**: Coder → Reviewer → Coder (implement → review → revise, up to N cycles)

See [docs/AGENTIC-SDK.md](AGENTIC-SDK.md) for detailed SDK integration docs.
See [docs/PLUGINS.md](PLUGINS.md) for the JS extension system and plugin development guide.
See [docs/SKILLS.md](SKILLS.md) for the skill system and how to create new skills.
