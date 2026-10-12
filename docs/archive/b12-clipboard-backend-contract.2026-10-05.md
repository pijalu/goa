<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B12 — Clipboard backend contract diverges from the reference agent (pi)

Closed 2026-10-05. Archived from `bugs.md`.

## Reported

Found by reading `internal/clipboard_image.go` beside `/Users/muaddib/dev/pi`
(`packages/coding-agent/src/utils/clipboard-image.ts`, `utils/clipboard.ts`,
`test/clipboard-image.test.ts`). Four divergences on the linux/WSL path — and the
reason they could exist: nothing on that path was ever executed by a test.

1. **Fall-through after an answer.** `readClipboardImageBytes` returned one
   `bool` for both "the backend answered and there is no image" and "the backend
   could not answer", so a Wayland `wl-paste` that answered *no image* still fell
   through to `xclip` — which on a Wayland session reads the XWayland/X11
   selection, i.e. goa could paste an image the Wayland clipboard does not hold.
   pi distinguishes `undefined` (backend failed) from `null` (no image) exactly
   for this: *"An empty Wayland clipboard must not fall through to stale X11
   clipboard contents"* (`clipboard-image.ts:89-90, 213-221`).
2. **Blind type probes.** When the type-list query failed, goa requested
   `image/png` anyway (`:170` wl-paste, `:188` xclip). pi treats a failed
   `--list-types`/`TARGETS` as "backend unavailable" and never probes an
   unadvertised type (`#9786`); asking an unrelated X11 owner to convert a type
   can hang it, and every blind probe can only end in the 1 s `clipboardTimeout`
   — on the input loop.
3. **WSL not detected.** `powershell.exe` ran as the last linux step
   unconditionally, and nothing read `/proc/version` or
   `WSL_DISTRO_NAME`/`WSLENV`. pi has `isWSL()` (`utils/wsl.ts`) and treats WSL as
   Wayland-like, with the Windows clipboard as the last resort — the only place a
   Win+Shift+S screenshot lands.
4. **Dispatch not injectable.** Backend selection was a `runtime.GOOS` switch, so
   on any one host only that host's branch could execute. pi takes
   `{platform, env}` as arguments and tests every platform on one host.

## Root cause

A single boolean cannot carry a three-valued contract, and a `runtime.GOOS`
switch cannot be driven from a test. Both were load-bearing: the first makes the
stale-X11 fall-through possible, the second is why nobody noticed.

## Fix

* **A three-state backend result.** `clipResult` is `clipUnavailable` (helper
  missing, or it failed/timed out) / `clipAbsent` (the backend answered: no image
  here) / `clipFound`. `absent` ends its stage of the walk; `unavailable` moves to
  the next backend — never to a blind probe.
* **Session-gated order, as pi's.** Linux/WSL: `wl-paste` first when the session
  is Wayland *or* WSL, then `xclip` (X11), then PowerShell — PowerShell only on
  WSL, and again even after an `absent` answer when no image has been found yet,
  because that is where a Windows screenshot lives. Termux reads no images at all
  (no display tools exist there), so nothing is probed.
* **A platform seam** (`clipboardGOOS`, `clipboardToolAvailable`,
  `clipboardProcVersion`) so every backend of every platform runs in tests on any
  host, and `isWSL`/`isWaylandSession` decide the session from env and
  `/proc/version`.
* **No unadvertised probes**: a failed `--list-types`/`TARGETS` is a backend that
  could not answer, and an advertised-but-unserved type is a backend failure too.

## Validation

New `internal/clipboard_dispatch_test.go` drives the whole dispatch with the
platform seam and a recording fake runner, asserting *which backends were
consulted* and in what order: Wayland-stops-before-X11, Wayland→X11 when
`wl-paste` is missing, X11-no-probe-when-TARGETS-fails, X11-no-probe-of-
unadvertised-types, X11 reads the advertised image, WSL→PowerShell (both after an
absent Linux answer and after Linux failures), `/proc/version` WSL detection,
plain-Linux-is-not-WSL, Windows→PowerShell only, Termux no probe, darwin→osascript
only, and a platform with no backend staying silent.

RED, measured by patching the old behaviour back in (each claim separately):

```
# old single-bool contract (absent folded into unavailable)
--- FAIL: TestPlatformDispatch_WaylandNoImageStopsBeforeX11
    backends consulted = [wl-paste xclip], want only wl-paste: a Wayland answer must end the chain
--- FAIL: TestPlatformDispatch_WSLTriesPowerShellAfterAnAbsentLinuxAnswer
    backends consulted = [wl-paste xclip powershell.exe], want wl-paste → PowerShell on WSL

# old blind probes
--- FAIL: TestPlatformDispatch_X11DoesNotProbeWhenTargetsFails
    backends consulted = [xclip xclip], want one xclip TARGETS probe and no image request
--- FAIL: TestPlatformDispatch_WSLReachesPowerShellWhenLinuxToolsFail
    backends consulted = [wl-paste wl-paste xclip xclip powershell.exe], want wl-paste → xclip → PowerShell

# no WSL detection
--- FAIL: TestPlatformDispatch_WSLUsesWindowsClipboard
    ReadClipboardImageBytes = ("",false), want the Windows clipboard image
--- FAIL: TestPlatformDispatch_WSLDetectedFromProcVersion
    a Microsoft kernel string in /proc/version must read as WSL
```

Restored: `go test -count=1 -race -cover ./...` passes, and the real-terminal
check `e2e/clipimg.sh` still reports

```
[PASS] Ctrl+V pasted the clipboard image: input line = …/goa-image-3987286049.png (8x8, file on disk)
```

## Residual risk

* Live validation covers the host's own platform (macOS) end to end; the
  Linux/WSL/Windows paths are validated by injected-platform tests that pin each
  backend's command shape and order, not by a real Wayland/WSL/Windows box.
  Docker was not available on this machine (`docker info` fails), and a container
  would still need a compositor (Wayland) or Xvfb to be a real check.
* `readClipboardTextBytes` keeps pi's order (session backend first, then X11
  tools): a text read has no stale-owner hazard, unlike an image read.
