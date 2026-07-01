<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# ⟡ Goa — Terminal-native AI Coding Agent

Goa is a terminal-native AI coding agent with **full LLM transparency**, **multi-agent collaboration**, and an ANSI TUI engine (inspired by pi / OpenCode / kimi-code). It uses the merged **Agent SDK** in `internal/agentic/` to orchestrate conversations with any OpenAI-compatible LLM provider (OpenAI, llama.cpp, LM Studio, Ollama) and exposes a powerful tool system for interacting with your codebase.

> **Status:** Active development. See `bugs.md` for current issues.

## Quick Start

```bash
# Build from source
make build

# First run launches the interactive setup wizard
./goa
```

On first run, Goa walks you through:
1. Configuring an LLM provider (endpoint, model, API key)
2. Selecting an agent profile (coder, planner, reviewer)
3. Choosing an execution mode (yolo, confirm, review)

## Features

| Feature | Description |
|---------|-------------|
| **🧠 Multi-provider** | Connect to any OpenAI-compatible LLM endpoint |
| **🛠 Tool System** | Read, write, edit, search, bash, SSH, background exec, git utilities |
| **📝 Config Cascade** | Embedded defaults → home → project → local → env → CLI overrides |
| **👤 Agent Profiles** | Built-in profiles: coder, planner, reviewer — custom via `extends` |
| **🧩 Skills** | Reusable prompt templates — inline (system prompt injection) or sub-agent |
| **🔁 Multi-Agent** | Pair (planner → coder) and reviewer (coder → reviewer) collaboration |
| **📋 Workflows** | Multi-stage, multi-agent workflows with team collaboration and AgentBus |
| **🧪 Loop Detection** | 5 heuristics (identical calls, token budget, timeout, activity, error rate) |
| **💾 Session Persistence** | Full JSONL session history with `/save` and `/restore` |
| **📦 Diagnostic Export** | Self-contained ZIP bundle via `/export` with events, logs, config, and issue description |
| **🖥 Rich TUI** | Chat, thinking stream, tool ledger, log, token budget, side panel, modals |
| **🔌 JS Plugins** | Extend Goa with JavaScript plugins via Goja |
| **🔄 Execution Modes** | yolo (auto-approve), confirm (pause before each tool), review (queue edits) |
| **🔒 Git Worktree Isolation** | Sandboxed agent filesystem via `git worktree` |
| **🎨 Configurable Spinner** | Choose from 50+ spinner styles via `/config` or `tui.spinner` |
| **🔍 Priority Search** | Search results ordered by file type (source first) with match counts |
| **⌨️ History Persistence** | Input history saved across sessions |
| **🗑️ Model/Provider Mgmt** | Delete models/providers via `/config:remove` or backspace in pickers |
| **⚡ Direct Tool Execution** | Run tools directly via `/tools:<name>:<key>=<value>,...` |

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        Goa Process                              │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │                  TUI Layer (ANSI engine)                  │  │
│  │   Header │ ChatViewport │ StatusBar │ Editor │ Footer     │  │
│  │   Overlays │ Selector │ Markdown                          │  │
│  └──────────────────────┬────────────────────────────────────┘  │
│                         │ agentic.OutputEvent / raw key events  │
│  ┌──────────────────────▼────────────────────────────────────┐  │
│  │                    Goa Core Engine                        │  │
│  │   CommandRouter │ AgentManager │ ConfigLoader │ DocEngine │  │
│  │   LoopDetector │ ExecutionController │ SessionStore       │  │
│  └──────┬────────────────────────────┬───────────────────────┘  │
│         │                            │                          │
│  ┌──────▼───────────┐        ┌───────▼───────────────────────┐  │
│  │   Agent SDK      │        │   Tool Registry               │  │
│  │  Agent/Session   │        │  read_file │ edit_file ···    │  │
│  │  SkillRunner     │        │  bash │ ssh_bash │ bg_exec    │  │
│  │  AgentBus        │        │  memento │ search │ goa_cmd   │  │
│  └──────┬───────────┘        └───────────────────────────────┘  │
│         │                                                       │
│  ┌──────▼───────────────────────────────────────────────────┐   │
│  │                   Provider Layer                         │   │
│  │  OpenAIProvider (any compatible endpoint)                │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │                  Supporting Systems                       │  │
│  │  MemoryStore │ ProfileResolver │ ProviderManager          │  │
│  │  SkillRegistry │ Plugin Loader │ WorktreeManager          │  │
│  │  SessionStore                                             │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

## Dependencies

| Dependency | Purpose |
|------------|---------|
| Agent SDK (`internal/agentic/`) | Core AI agent SDK (merged, no external dependency) |
| ANSI TUI (`tui/`) | Custom TUI engine: Component/TUI/ProcessTerminal |
| [`golang.org/x/term`](https://pkg.go.dev/golang.org/x/term) | Terminal raw mode |
| [`cobra`](https://github.com/spf13/cobra) | CLI flag parsing |
| [`goja`](https://github.com/dop251/goja) | JavaScript plugin runtime |
| `gopkg.in/yaml.v3` | YAML parsing |

## Documentation

| Document | Description |
|----------|-------------|
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | Full system architecture |
| [SETUP.md](docs/SETUP.md) | Installation & setup guide |
| [CONFIGURATION.md](docs/CONFIGURATION.md) | Config cascade & all settings |
| [COMMANDS.md](docs/COMMANDS.md) | Command system reference |
| [TOOLS.md](docs/TOOLS.md) | Tool system reference |
| [SKILLS.md](docs/SKILLS.md) | Skills system reference |
| [PROFILES.md](docs/PROFILES.md) | Agent profiles & resolution |
| [TUI.md](docs/TUI.md) | TUI layout & usage |
| [AGENTIC-SDK.md](docs/AGENTIC-SDK.md) | How Goa wraps the agentic SDK |
| [WORKFLOWS.md](docs/WORKFLOWS.md) | Workflow system reference |
| [DEVELOPMENT.md](docs/DEVELOPMENT.md) | Development guide |
| [bugs.md](bugs.md) | Known issues and roadmap |
| [AGENTS.md](AGENTS.md) | AI agent coding guidelines |

## Project Structure

```
goa/
├── main.go                  # Entry point
├── cmd/goa/                 # Cobra CLI root
├── config/                  # Config struct, cascade loader, defaults, wizard
├── core/                    # Commands, registry, router, agent manager,
│   │                        # loop detector, execution controller, session store
│   └── commands/            # 30+ commands (auto-registered via init())
├── internal/                # Shared types, enums, errors, git worktree manager
├── tui/                     # ANSI TUI: engine, components, overlays, styles
├── tools/                   # Tool registry & implementations
├── memory/                  # Persistent memory store
├── profiles/                # Profile resolution & built-in definitions
├── provider/                # Provider management & model listing
├── skills/                  # Skill registry, runner, inline injection
├── multiagent/              # Pair & reviewer orchestrators, workflow engine
├── workflows/               # Workflow definitions (directory-per-workflow)
├── plugins/                 # JS plugin loader & Goja bridge
├── internal/agentic/        # Merged Agent SDK
├── chunks/                  # Milestone implementation briefs
├── docs/                    # Documentation
├── Makefile                 # Build, test, lint, cross-compile
└── bugs.md                  # Known issues and roadmap
```

## Development

```bash
# Install prerequisites
go install github.com/uudashr/gocognit/cmd/gocognit@latest
go install github.com/fzipp/gocyclo/cmd/gocyclo@latest

# Build & test
make build
make test
make lint

# Run Goa
make run
```

See [DEVELOPMENT.md](docs/DEVELOPMENT.md) for detailed development guidelines.

## License

GNU GPLv3 — see the [LICENSE](LICENSE) file for details.
