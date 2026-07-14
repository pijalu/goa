<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Tracking Archive — 2026-07-14 (TUI selection widget scrollback)

Resolved on 2026-07-14. Observed failure was reproduced before editing
(workflow step 1), localized to the smallest region (step 3), and verified
against the original failing behavior (step 6). Code-quality gates
(guideline #6) were run separately and introduced no new violations.

---

## TUI selection widget breaks scrolling/history redraw

**Observed:** Opening the command-selection widget (even a single `/`) pushed
base content (mascot/logo, header, info boxes, editor border) into the
terminal scrollback, and closing it never restored the view — each
`/`+backspace cycle leaked more content into scrollback. The terminal
recording (`/tmp/goa-term-scroll.log`, 80×29) captured the failure: a single
`/` emitted the compositor's first-scroll sequence (`\x1b[29;1H\x1b[1;1H` =
`emitViewportScroll` → `emitFirstScroll`), scrolling the top 9 mascot rows
into scrollback.

**Reproduction (data, not a live model):** the failure is fully reproducible
without a real terminal by replaying the compositor's byte stream through the
in-package terminal emulator — per the `tui-test` skill (never debug a UI bug
against a live model). Diagnostic confirmed: base canvas height 10 → 15 on
popup open (overlay=false), 5 scrollback lines accumulated (`KEEP_VISIBLE_TOP`
box + editor border + `/`).

**Root cause (localized):** `Editor.Render` *appended* the autocomplete popup
to its base render output (`appendCompletionLines`), so opening the popup
grew the editor's **base** layer beyond the terminal height. The Compositor
bottom-anchors the viewport and, on the first such overflow, runs
`emitFirstScroll` which pushes the displaced top rows into terminal
scrollback. Because terminals cannot "un-scroll", closing the popup (which
only shrinks the canvas) left that content stranded in scrollback forever.

**Fix (root cause, not symptom):** the popup is no longer part of the base
render; it is exposed via a new optional `PopupRenderer` interface
(`tui/component.go`) and composited as a `LayerOverlay`
(`tui/tui.go: buildBaseLayers` + `buildPopupOverlays`). `Editor.Render`
returns only the frame; `Editor.PopupLines` returns the popup. Because the
base canvas height never changes when a popup opens/closes, the viewport
never scrolls and scrollback is never touched. Placement prefers
directly-above-the-owner (conventional + overflow-safe for a bottom-anchored
editor), falls back to below, and is clamped to the visible viewport so the
overlay itself never grows the canvas past the terminal height.

**Test approach + validation:**
- Permanent regression: `tui/autocomplete_popup_scrollback_test.go` —
  `TestAutocompletePopupDoesNotPushScrollback` (controlled geometry, asserts
  base canvas unchanged + overlay present + scrollback empty across
  open/close, base content restored) and
  `TestAutocompletePopupScrollbackInvariant_RealTree` (full production
  component tree at the recorded 80×29, 3 open/close cycles → scrollback 0).
  The test was confirmed to FAIL before the fix (5 scrollback lines) and
  PASS after.
- End-to-end against the live binary: drove the real `goa` build through a
  PTY (`/`+backspace×3) with `GOA_DEBUG_TERMINAL`, captured the byte stream,
  and replayed it through the emulator → **0 scrollback lines**. Replaying
  the original buggy recording through the same emulator → **9 scrollback
  lines** (the mascot art), confirming the replay correctly detects the bug.

**Code quality (guideline #6, run separately):**
- `go vet ./...` clean.
- `staticcheck ./...`: only a pre-existing unrelated warning
  (`bytePosForCol` unused, present in HEAD).
- `gocognit -over 15` / `gocyclo -over 12`: no new violations from the
  change (remaining hits are pre-existing in `core/` and a prior session's
  test file).
- `go test -count=1 -race -cover ./...`: all packages pass.
