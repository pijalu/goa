<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# TUI (Terminal User Interface)

Goa's TUI is an ANSI-native terminal UI engine with differential rendering,
viewport management, and a component tree. It wraps the `agentic` SDK for LLM
interaction and provides keyboard-driven chat, markdown rendering, scrollable
history, and a status bar with animated spinner.

## Architecture

Goa's TUI is a **compositor MVC**. Concerns are strictly separated:

- **Model** — the screen is described as a list of protocol-free **Layers**
  (`Name`, `Rect` position/size, `Z` order, styled `Content`), plus the input
  cursor. The conversation state lives in a `Conversation` of `MessageData`.
- **Compositor** (`tui/compositor.go`) — the **single owner of terminal
  protocol**. It composes Layers onto a canvas, diffs against the previous
  frame, and emits every escape sequence (CSI 2026 synchronized output, cursor
  movement, line clears, scrollback scrolling, hardware-cursor positioning).
  Nothing else in the codebase constructs output escape codes.
- **View** — components declare position/size/content; `ChatViewport` is a
  thin view over the `Conversation` Model.
- **FocusStack** (`tui/focus.go`) — the single authority for who receives
  input; overlays push/pop.
- **AgentView** — `TUI.AgentFrame()` / `TUI.VisibleText()` produce a
  structured, ANSI-free view of the screen so AI tooling can "see" the TUI
  without parsing escape codes. Agent and terminal always agree (both consume
  the same `Scene`).

```
Controller (input keys + agent events) ──► Model (Conversation + Scene)
                                                  │
                           ┌──────────────────────┼──────────────────────┐
                           ▼                      ▼                      ▼
                     Compositor             AgentView                tests
                  (terminal bytes)       (plain text for AI)
```

```
┌──────────────────────────────────────────────────────────────┐
│  Header: goa coding agent v0.1                               │
│  Ctrl+C/D exit  |  / commands  |  Tab complete  |  ↑↓ history│
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  ChatViewport (scrollable message history)                   │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │ • Connected to LM Studio (qwen3.5-9b)                   │ │
│  │                                                         │ │
│  │ User: hello                                             │ │
│  │ ┌─────────────────────────────────────────────────────┐ │ │
│  │ │ Assistant response...                               │ │ │
│  │ └─────────────────────────────────────────────────────┘ │ │
│  │                                                         │ │
│  │ ◉ bash ls -la                                           │ │
│  │   ← [bash: ls -la]\nExit: 0\n...                       │  │
│  └─────────────────────────────────────────────────────────┘ │
│                                                              │
├──────────────────────────────────────────────────────────────┤
│  StatusBar: ⠋ thinking                                       │
├──────────────────────────────────────────────────────────────┤
│  Input area (editor, grows up to 12 lines max)               │
├──────────────────────────────────────────────────────────────┤
│  Footer: ~/dev/goa (⎇ main)              coder │ YOLO        │
│          ↑120 ↓80  200/4096 tok           qwen3.5-9b         │
└──────────────────────────────────────────────────────────────┘
```

## Key Components

### TUI Engine (`tui/tui.go`)

The TUI orchestrates components into a `Scene` and delegates output to the
`Compositor`. It manages:
- **Component tree**: children rendered into stacked base `Layer`s
- **`buildScene`**: renders components + overlays into protocol-free Layers and
  extracts the explicit `Scene.Cursor`
- **FocusStack**: keyboard input delivered to the top of the focus stack
- **Overlay stack**: modal overlays become positioned overlay `Layer`s

```go
engine := tui.NewTUI(terminal)   // engine owns a Compositor bound to terminal
engine.AddChild(header)
engine.AddChild(chatViewport)
engine.AddChild(editor)
engine.SetFocus(editor)
engine.Start()
// engine.AgentFrame() / engine.VisibleText() → ANSI-free view for AI tooling
```

### Terminal (`tui/terminal.go`)

`ProcessTerminal` implements raw-mode I/O with:
- Kitty keyboard protocol (flag 1: disambiguate) for Ctrl+Enter support
- Bracketed paste mode
- Stdin buffering with CSI-u sequence handling
- SIGWINCH handling for terminal resize

### Component Tree

Components implement the `Component` interface:

```go
type Component interface {
    Render(width int) []string
    HandleInput(data string)
    Invalidate()
}
```

Standard components:
| Component | File | Description |
|-----------|------|-------------|
| `Header` | `tui/header.go` | App name, version, keybinding hints |
| `ChatViewport` | `tui/chat_viewport.go` | Scrollable message history with typed messages |
| `Editor` | `tui/editor.go` | Multi-line text input with undo, kill-ring, autocomplete |
| `Footer` | `tui/footer.go` | Status bar with workdir, git branch, model, stats |
| `StatusMsg` | `tui/status.go` | Ephemeral status line with animated spinner |
| `Separator` | `tui/box.go` | Horizontal line across the terminal width |
| `Selector` | `tui/selector.go` | Interactive item picker overlay |
| `SelectList` | `tui/select_list.go` | Scrollable list for completion popups |

### Input Handling

The `ProcessTerminal.readLoop` goroutine reads raw bytes from stdin,
decodes them through `StdinBuffer` (which handles partial escape sequences,
Kitty CSI-u, bracketed paste), and forwards decoded key strings to
`TUI.handleKey`. The key is routed to the focused component's `HandleInput`.

### Rendering Pipeline

1. `renderNow()` acquires the TUI mutex
2. `renderTree()` calls each component's `Render(width)`, concatenating lines
3. Overlays are composited on top of the base content
4. `clipToViewport()` slices visible portion for terminal height
5. `diffRender()` compares against previous frame, writes changed lines
   using ANSI positioning (`\x1b[N;1H\x1b[K...`)
6. `extractCursorPos()` finds the hardware cursor marker (`\x1b_pi:c\x07`)
   emitted by the focused component
7. `positionCursor()` places the terminal cursor for IME support

### Scrolling

- **PageUp/PageDown**: Scroll chat by full terminal height
- **Mouse wheel** (via Ghostty alternate scroll): Generates Up/Down keys
  → Editor scrolls chat by 1 line when cursor reaches buffer boundary
- **ChatViewport** has independent `scrollTop` offset for scrolling
  through message history

### Markdown Rendering

Markdown in assistant messages is rendered by `MDStreamRenderer`
(`tui/markdown.go`) which handles:

- **Headings** (`#` through `######`) — colored and bold
- **Fenced code blocks** with syntax highlighting for Go, Python, Bash, JSON, YAML
- **Inline code** — monospace with background
- **Bold** (`**text**`), **Italic** (`*text*`), ~~Strikethrough~~ (`~~text~~`)
- **Links** (`[text](url)`)
- **Blockquotes** (`>`) — dim with indent marker
- **Unordered/ordered lists** — `•` / `1.` markers
- **Tables** — box-drawing characters (┌─┬─┐, │, ├─┼─┤, └─┴─┘)
  with natural width calculation, cell wrapping, and proportional shrinking
- **Thematic breaks** (`---`, `***`, `___`)

### Goa Text Panel

All goa-originated text (slash-command output such as `/help`, `/docs`,
`/hotkeys`, `/goal:status`) is rendered inside a bordered **goa panel**
(`renderGoaPanel` in `tui/chat_viewport_components.go`). The panel:

- draws an ASCII box (`╭─┄╮` / `╰─┄╯` / `│`) using the `goa_panel_border`
  theme token,
- fills the inner lines with the `goa_panel_bg` dark background so the text
  is visually distinct from the terminal/chat background,
- keeps markdown/preformatted rendering intact inside the box.

This is the global convention for goa-authored output across the tool.

**Information ordering** — when a command both needs user input and shows
context, the input title is presented first (on the editor top border) and the
supporting goa text is shown as a panel immediately after, avoiding redundant
or ambiguous bubbles.

### Autocomplete

The Editor supports inline autocomplete via a `Completer` interface.
Tab triggers completion; Enter accepts and submits. Down/Up cycle
through candidates when the popup is active.

## Event Flow

```
Agent SDK → OutputEvent → AgentManager.OnEvent → TUI events channel
  → handleAgentOutputEvent → chat.AddMessage / statusMsg.Show / footer updates
  → renderNow()
```

## Dependencies

The TUI has no external dependencies beyond:
- `golang.org/x/term` — raw mode terminal I/O
- Internal `ansi` package — ANSI escape sequence helpers, width calculation,
  text wrapping
