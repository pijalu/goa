<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Keyboard Shortcuts

Goa provides keyboard shortcuts for navigation, editing, and application actions.
The table below shows the default keybindings. Customize them in your theme config.

---

## Navigation

| Key | Action |
|-----|--------|
| `↑` / `Ctrl+P` | Move cursor up / browse history up |
| `↓` / `Ctrl+N` | Move cursor down / browse history down |
| `←` / `Ctrl+B` | Move cursor left |
| `→` / `Ctrl+F` | Move cursor right |
| `Alt+←` / `Alt+B` | Move cursor by word left |
| `Alt+→` / `Alt+F` | Move cursor by word right |
| `Ctrl+A` / `Home` | Move to start of line |
| `Ctrl+E` / `End` | Move to end of line |
| `PgUp` | Scroll chat viewport up by one page |
| `PgDn` | Scroll chat viewport down by one page |

## Editing

| Key | Action |
|-----|--------|
| `Enter` | Send message / submit input |
| `Alt+Enter` / `Ctrl+Enter` | Insert a newline |
| `Tab` | Accept completion / path completion |
| `Ctrl+W` / `Alt+Backspace` | Delete word backwards |
| `Alt+D` | Delete word forwards |
| `Ctrl+U` | Delete to start of line |
| `Ctrl+K` | Delete to end of line |
| `Ctrl+Y` | Paste most-recently deleted text (yank) |
| `Alt+Y` | Cycle through deleted text after pasting |
| `Ctrl+Z` | Undo |

## Application

| Key | Action |
|-----|--------|
| `Ctrl+G` | Toggle the goal status bubble |
| `Ctrl+T` | Cycle thinking/reasoning level |
| `Alt+M` | Cycle major mode (coder → planner → reviewer) |
| `Alt+O` | Open the interactive mode selector |
| `Ctrl+Shift+M` | Cycle autonomy level (yolo → solo → confirm → review) |
| `Ctrl+L` | Open the model selector |
| `Ctrl+P` | Show the assembled system prompt |
| `Ctrl+X` | Switch orchestrator tab (Conversation ↔ Stats) |
| `Ctrl+Tab` | Toggle thinking block visibility |
| `Ctrl+O` | Toggle all tool output (Summary/Full) |
| `Ctrl+Shift+Z` | Delete the last chat message |
| `Ctrl+C` | Cancel input request / quit when empty |
| `Esc` | Cancel completion / close selection |
| `/` | Slash commands |
| `!` | Run a bash command |

## Modals & Overlays

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate items in a selector |
| `Enter` | Confirm selection |
| `Esc` | Dismiss modal / close overlay |
| `Tab` | Next field or option |
| `Shift+Tab` | Previous field or option |

## Review Pager

When the interactive review pager is open (`/review`):

| Key | Action |
|-----|--------|
| `↑` / `k` | Scroll one line up |
| `↓` / `j` | Scroll one line down |
| `PgUp` | Scroll one page up |
| `PgDn` | Scroll one page down |
| `c` | Add a comment on the current line |
| `e` | Edit the comment on the current line |
| `d` | Delete the comment on the current line |
| `b` | Change the base commit |
| `s` | Submit the review to the agent |
| `x` | Export the review to a Markdown file |
| `q` / `Esc` | Close the pager |

> **Note:** Keybindings may vary by terminal emulator. Some keys (like
> `Ctrl+Enter`) require Kitty keyboard protocol support.
> Use `/hotkeys` inside Goa to see your active keybindings.
