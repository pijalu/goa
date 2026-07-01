<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# PTY reproduction tooling for goa's TUI

`scripts/repro/` contains a small, dependency-free Python toolkit for driving
goa inside a pseudo-terminal and capturing the **exact bytes** goa writes to
the terminal. This exists because the bugs under investigation are
terminal-level (Kitty/`modifyOtherKeys` negotiation, auto-wrap / DECAWM,
synchronized output / CSI 2026, scrollback corruption, overlay compositing
that depends on the real cursor row). Unit tests cannot see these; only a real
PTY can.

> No Python dependencies beyond the standard library (`pty`, `select`). Works
> on macOS and Linux.

## Files

| File | Purpose |
| --- | --- |
| `setup_env.py` | Creates a throwaway `HOME` with a minimal `~/.goa/config.yaml` (no provider — enough to start the TUI for UI-only flows) and, with `--with-review-repo`, a tiny git repo containing a reviewable diff. |
| `drive_goa.py` | Forks goa in a PTY, drives keyboard input for a named scenario, and dumps the captured bytes after every step. |

## Quick start

```sh
# 1. build goa
go build -o ./goa ./cmd/goa

# 2. create an isolated HOME + a small git repo to review
eval "$(python3 scripts/repro/setup_env.py --root /tmp/goa-repro --with-review-repo)"

# 3. drive the review scenario, capture to ./repro-out
python3 scripts/repro/drive_goa.py \
    --goa ./goa --repo "$REPO" --home "$HOME" --out ./repro-out

# 4. inspect
ls repro-out/
cat repro-out/06_delete_prompt.txt        # human-readable screen (ESC -> \e)
grep -a $'\x1b\[<u' repro-out/NN_exit     # check for raw reset sequences
```

The same `HOME`/`REPO` pair can be reused across runs; `--out` is what
changes between captures.

## Output format

For each step the driver writes two files:

* `NN_<name>` — raw bytes exactly as goa emitted them (grep this for escape
  sequences).
* `NN_<name>.txt` — human-readable view where `ESC` → `\e`, `CR` → `<CR>`,
  `LF` → `<LF>\n`. Read this to understand the on-screen layout.

## Scenarios

Select with `--scenario NAME`:

* `review` (default) — opens `/review`, scrolls, adds a comment, triggers the
  delete confirm, opens/closes the base selector, closes the pager, and quits.
  Captures startup, each interaction, and the exit reset sequence.
* `exit` — minimal: just startup + `/quit`, to inspect the shutdown/reset
  bytes and confirm no bytes leak to the parent shell.

### Adding a scenario

Implement `scenario_<name>(ctx: DriveCtx)` in `drive_goa.py` using the helpers:

* `ctx.send(s)` — write bytes/str to goa's input.
* `ctx.wait(secs)` — sleep.
* `ctx.step(name, timeout=0.8)` — drain output for `timeout` seconds and dump
  it as step `name`.

Register it in the `SCENARIOS` dict. Keep timings generous: goa rate-limits
renders to ~60 fps and the Kitty protocol negotiation needs ~150 ms+ to
settle.

## Interpreting captures

Useful things to look for:

* **Screen layout** — read the `.txt` files top to bottom; `<LF>` marks row
  boundaries. A line split where none is expected (e.g. a label wrapping in
  the middle of a word) usually means an embedded newline in rendered text or
  a width/tab mismatch.
* **Reset on exit** — `NN_exit` should contain `\e[<u` (Kitty off),
  `\e[?2004l` (bracketed paste off), `\e[>4;0m` (modifyOtherKeys off),
  `\e[?25h` (show cursor), `\e[?7h` (auto-wrap on), and `\e[!p` (soft reset).
  Missing any of these is a corruption source.
* **Where a prompt appears** — the row number in the leading `\e[ROW;1H` tells
  you which screen row goa wrote the line to. For "the prompt must appear on
  the main input line" requirements, check that the row matches the input
  editor, not the overlay body.
