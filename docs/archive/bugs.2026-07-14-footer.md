<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Tracking Archive — 2026-07-14 (TUI empty line at bottom)

Resolved on 2026-07-14. Observed failure was reproduced before editing
(workflow step 1), localized to the smallest region (step 3), and verified
against the original failing behavior (step 6). Code-quality gates
(guideline #6) were run separately and introduced no new violations.

---

## TUI issues: 1 empty line at bottom

**Observed:** The TUI always showed a permanently-empty line at the very
bottom of the terminal that was never filled. The footer's two real status
lines (workdir/mode, model) were followed by a blank row.

**Root cause (localized):** `Footer.Render` appended `renderOrchStatsLines`,
which returned a single blank spacer line
(`[]string{strings.Repeat(" ", width)}`) whenever no orchestration stats were
active. That spacer was the permanently-empty bottom row. It was added in
commit `b9e48a6` ("keep footer height constant, preventing compositor
height-change full redraws"). That rationale was **invalid**: the chat
viewport is the layout's `HeightAllocated` fill and absorbs any chrome-height
change, so the total canvas height stays `== terminal height` regardless of
footer height — hence no height-change full redraw ever occurs. The blank
spacer was solving a non-existent problem while wasting a terminal row.

**Premise verified empirically (before the fix):** a footer height toggle
(idle 3-line ↔ orchestration N-line) kept the canvas at `== termH` and emitted
no `\x1b[2J` / `\x1b[3J` full redraw — proving the spacer was unnecessary.

**Fix (root cause, not symptom):** `renderOrchStatsLines` now returns `nil`
when no stats are active, so the idle footer is exactly its two chrome lines.
The chat viewport fill absorbs the one-row difference when orchestration
starts/stops, so there is still no full redraw.

**Test approach + validation:**
- `tui/footer_bottom_line_test.go` — `TestFooterNoEmptyLineAtBottom` (idle
  footer emits no blank line; composed canvas height == termH; bottom row
  carries footer model content) and `TestFooterHeightToggle_NoFullRedraw`
  (orchestration on/off emits no `\x1b[2J`/`\x1b[3J`; canvas height constant).
- `tui/footer_orchestration_test.go` — updated
  `TestFooter_IdleIsTwoLinesNoSpacer` (was asserting the buggy 3-line spacer
  behavior) to assert the idle footer is exactly 2 non-blank lines.
- End-to-end against the live binary: drove the real `goa` build via PTY with
  `GOA_DEBUG_TERMINAL` and replayed the initial render through the emulator →
  the bottommost non-blank row is the footer model line
  (`(lmstudio) google/gemma-4-e4b • high`); no blank row below it.

**Code quality (guideline #6, run separately):**
- `go vet ./...` clean.
- `staticcheck ./tui/`: only a pre-existing unrelated warning
  (`bytePosForCol` unused, present in HEAD).
- `gocognit -over 15` / `gocyclo -over 12`: no new violations.
- `go test -count=1 -race ./tui/ ./internal/app/`: green. (Note: the
  `core/orchestrator` package can time out under full-suite `-race` load due
  to pre-existing agent-pool goroutine contention; it passes in isolation both
  with and without this change, confirming the timeout is unrelated.)
