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
| `Ctrl+V` | Paste from the system clipboard: files copied in a file manager, then an image, then text |

`Ctrl+V` is the only way to bring an **image** in: a terminal's own paste
chord (`Cmd+V`) can only deliver the clipboard's text flavours, so a screenshot
never reaches goa and pasting it appears to do nothing. `Ctrl+V` reads the OS
clipboard directly, stores the image in the durable image store
(`~/.cache/goa/images`, `~/Library/Caches/goa/images` on macOS) and inserts the
stored path into the input line, where the submit path turns it into an
attachment — the same store the web UI's `/upload` uses.

## Application

| Key | Action |
|-----|--------|
| `Ctrl+G` | Toggle the goal status bubble |
| `Tab` | Next multi-agent tab (yields to a visible completion popup) |
| `Shift+Tab` | Previous multi-agent tab |
| `Alt+]` / `Alt+[` | Next / previous multi-agent tab (aliases) |
| `Alt+1`…`Alt+9` | Jump to multi-agent tab N |
| `Alt+T` | Cycle thinking/reasoning level |
| `Alt+M` | Cycle major mode (coder → planner → reviewer) |
| `Alt+O` | Open the interactive mode selector |
| `Ctrl+Shift+M` | Cycle autonomy level (yolo → solo → confirm → review) |
| `Ctrl+L` | Open the model selector |
| `Ctrl+P` | Show the assembled system prompt |
| `Ctrl+X` | Switch orchestrator tab (Conversation ↔ Stats) |
| `Ctrl+T` | Toggle thinking block visibility |
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

> **Single-file review:** `/review:file:<path>` opens the same pager for one
> text file (no git required). It shares every key above except `b`
> (change base), and `Ctrl+C` also closes it.

> **Note:** Keybindings may vary by terminal emulator. Some keys (like
> `Ctrl+Enter`) require Kitty keyboard protocol support.
> Use `/hotkeys` inside Goa to see your active keybindings.

---

## Browser keys (`goa server`)

The tables above are the whole story: over the web UI **the same bindings
apply**, because the page does not implement shortcuts of its own. A keypress
is reported to the server (`key`, `ctrl`, `alt`, `shift`), and the server's
encoder turns it into the byte sequence a terminal would have written — so
`Ctrl+G`, `Alt+1`, `Ctrl+L`, `PgUp`, `F5` and friends mean in the page exactly
what they mean in a terminal. Click the page first to give it focus.

What does differ, and is worth knowing:

| Difference | Why |
|------------|-----|
| `Cmd`/`Ctrl+Cmd` chords are left to the browser | Reload, devtools and tab switching stay available; the page never swallows them |
| `Alt` alone may be claimed by the browser menu on some platforms | Use `Esc` to dismiss an overlay if the browser eats `Alt` |
| Text inserted programmatically (`Input.insertText`, some IME/assistive paths) fires no `keydown` | Those keystrokes have no descriptor to encode; type or paste normally — paste goes through `/input` verbatim |
| No Kitty keyboard protocol | Chords are encoded in the legacy xterm form plus Kitty CSI-u where legacy has no parameter, which covers every binding Goa has |

Read-only servers (`--server-read-only`) accept every view and reject every
keystroke, so viewers can navigate and scroll but cannot drive the session.

See [WEBUI.md](WEBUI.md) for the full transport and security model.
