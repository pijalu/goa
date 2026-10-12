<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B13 — Read side is asymmetric with the copy side: xsel and Termux cannot be read

Closed 2026-10-05. Archived from `bugs.md`.

## Reported

goa could *write* the clipboard through more backends than it could read it back
from:

* write side — `nativeCopyCommand` (`internal/clipboard.go:79-95`): `pbcopy`,
  `wl-copy`, `xclip`, `xsel`, `termux-clipboard-set`, `clip`;
* read side (`internal/clipboard_image.go`) — `pbpaste`, `Get-Clipboard`,
  `wl-paste`, `xclip` only.

So on an X11 box with only `xsel`, or on Termux, goa set the clipboard and could
not read it back: a terminal paste of that clipboard inserted nothing. pi reads
`xsel --clipboard --output` and `termux-clipboard-get`, gated on `DISPLAY` /
`WAYLAND_DISPLAY` / `TERMUX_VERSION` (`clipboard.ts:54-66`).

## Root cause

The read side was built platform-first (`runtime.GOOS` switch plus a two-tool
Linux list) while the write side was built tool-first. Neither list was derived
from the other, so they drifted.

## Fix

Text reads now walk the same tools the write side uses, in session order:
Termux (`termux-clipboard-get`) when `TERMUX_VERSION` is set, then `wl-paste
--type text --no-newline` on a Wayland session, then `xclip
-selection clipboard -out` and `xsel --clipboard --output` when `DISPLAY` is set
(`linuxTextCommands`). No display and no Termux means no text backend is tried at
all, instead of spawning two tools that cannot answer. Images stay on `xclip`
(xsel has no image support — pi agrees), and the file-path flavour follows the
same session gating, so a Wayland session is not answered from the X11 selection.

## Validation

`TestPlatformDispatch_TextReadsThroughCopySideBackends` is a table over darwin
(`pbpaste`), Windows (`Get-Clipboard -Raw`), X11 with `xclip`, X11 with *only*
`xsel`, Wayland (`wl-paste`), Termux (`termux-clipboard-get`) and a headless
session (no backend consulted, no text), asserting both the text read and the
exact list of tools consulted.
`TestPlatformDispatch_WaylandTextKeepsItsOrder` pins the order
`wl-paste → xclip → xsel` when the session backend cannot answer.

RED, measured by restoring the old reader (unconditional `wl-paste` then `xclip`,
no `xsel`, no Termux, no gating):

```
--- FAIL: TestPlatformDispatch_TextReadsThroughCopySideBackends/x11_xclip
    call 0 = "wl-paste", want "xclip"
--- FAIL: TestPlatformDispatch_TextReadsThroughCopySideBackends/x11_xsel_only
    backends consulted = [wl-paste], want [xclip xsel]
--- FAIL: TestPlatformDispatch_TextReadsThroughCopySideBackends/termux
    call 0 = "wl-paste", want "termux-clipboard-get"
--- FAIL: TestPlatformDispatch_TextReadsThroughCopySideBackends/no_display
    backends consulted = [wl-paste], want []
--- FAIL: TestPlatformDispatch_WaylandTextKeepsItsOrder
    backends consulted = [wl-paste xclip], want [wl-paste xclip xsel]
```

Restored: all pass.

## Residual risk

* `xsel` covers text only; a box with xsel and no xclip still pastes no image
  (xsel has no image target support at all).
* Termux reads text through `termux-clipboard-get`; images are not attempted
  there, matching pi and the Android clipboard's lack of an image flavour.
