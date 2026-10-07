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
│  Agent.Run()    │        │  bg_exec    │ memento    │ python     │
│  Session.Stream()│       │  goa_cmd   │ registry   │ gitutil    │
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

## The Virtual Terminal (`internal/webui/`)

`goa server` does not reimplement the UI. It swaps the process terminal for a
`webui.VirtualTerminal` — a `tui.Terminal` that keeps the emulator's **cell
grid** in memory instead of writing ANSI to a pty — and serves that grid over
HTTP. The TUI engine, the components, the focus stack, the command router and
the agent are literally the same objects the interactive build uses.

```
browser keydown ──▶ /key or /ws frame
        │              webui.EncodeKey ──▶ terminal bytes (the xterm/Kitty
        │                                       table lives server-side)
        ▼
VirtualTerminal (tui.Terminal: Start/Input/Resize/Size/Write/Clear/SetTitle)
        │  writes accumulate into a cell grid
        ▼
tui.TUI ──▶ Scene ──▶ Compositor ──▶ cells ──▶ webui.Frame ──▶ browser
```

Three consequences worth stating, because they are the whole design:

* **One rendering path.** Cells are produced by the same compositor that feeds
  a real terminal, so a browser cannot drift from the TUI: there is no second
  renderer to keep in sync.
* **One input path.** The page sends a normalized key *descriptor*
  (`key`, `ctrl`, `alt`, `shift`); `webui.EncodeKey` turns it into the bytes a
  terminal would have written. The engine then decodes the very same bytes it
  decodes in a terminal — `TestKeyEncoder_SpecTable` pins the byte table and the
  `internal/app` web-parity tests drive the real editor/overlays with them, so
  the two cannot drift.
* **The grid is the network boundary.** Frames are diffed against the previous
  one (only changed rows travel) and carry a sequence number, so a reconnect
  resumes by sequence instead of painting a delta across a hole.

Ownership is unchanged too: state mutation still goes through the engine's
command loop (`TUI.Apply`), which is why the web session is a plain
`subsystems` bundle with one field (`terminal`) replaced.

See [WEBUI.md](WEBUI.md) for transports, auth and the URL map;
[specs/webui.md](../specs/webui.md) for the full design.
## The Attached Terminal (`internal/attach/`)

`goa attach` is the mirror image of the web page: the terminal is the client.
It dials the same `/ws` endpoint (requesting the `cells` plane), feeds every
received frame through a renderer that emits truecolor SGR, OSC-8 links and
the server's cursor as the local caret, and pushes scrolled-off transcript
rows into the terminal's native scrollback. Input needs no decoding table of
its own: the client's terminal is in raw mode, and the bytes it produces are
forwarded verbatim — the engine decodes exactly what a local session would
read. Reconnects re-announce the last seen frame sequence; keystrokes typed
while offline are held (bounded) and replayed.

## The Multi-Project Supervisor (`internal/webui/supervisor/`)

`goa server --server-projects-root` hosts no session itself. Each project
directory is served by an ordinary child `goa server` process bound to a
private Unix socket inside a `0700` directory, authenticated with a per-child
random bearer token that only the supervisor's proxy presents. The parent
serves one hardened HTTP surface — the same `Guard` chain as the session
server — with a session index, a connect-by-path handshake (`POST /connect`,
validated absolute/existing/under-root before any child spawns), and a
reverse proxy keyed by session id, including ids a child has rotated away
from. One engine per project keeps config, plugins and conversation cache
identities exact by construction; idle children are reaped with their
transcripts left on disk.


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
| **The browser *is* the terminal (`goa server`)** | One engine, one renderer, one input decoder; the web UI is a transport, not a second UI |

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
