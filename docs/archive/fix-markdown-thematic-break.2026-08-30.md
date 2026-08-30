<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Fix plan — Markdown `---` horizontal rule (bugs.md "Markdown rendering")

**Status: CLOSED — NOT APPLICABLE (support already present). Closed 2026-08-30.**

## Report

Markdown rendering should support a lone `---` line to draw a horizontal rule.

## Investigation (verification, no code change)

All markdown surfaces in the TUI route through `MDStreamRenderer`
(`tui/markdown.go`), which has an explicit thematic-break case:

- `tui/markdown_block.go` `isThematicBreak()` — accepts `-`, `*`, `_` runs
  (≥3 chars, spaces/tabs allowed) per CommonMark.
- `tui/markdown.go` `Render()` case `isThematicBreak(trimmed)` → renders a
  full-width faint `─` rule (`ansi.Faint + strings.Repeat(ansi.BoxHorizontal,
  width) + ansi.Reset`).
- Routing: `tui/chat_viewport_markdown.go` `hasMDThematicBreak()` makes text
  containing `---` classify as markdown (not preformatted).
- Incremental streaming path (`tui/markdown_incremental.go`) delegates to the
  same renderer, so streamed `---` renders identically.
- Existing regression tests: `TestMDStreamRenderer_ThematicBreak`,
  `TestIsThematicBreak` (`tui/markdown_test.go`) — pass.

## Validation performed (actual render output verified)

Reproduced via a throwaway test rendering through `MDStreamRenderer` (80 cols):

- `"before\n\n---\n\nafter"` → `before`, `\x1b[2m────…────\x1b[0m` (80 `─`),
  `after`.
- Also verified: `---` alone, after a heading, mid-paragraph (`para\n---\nmore`
  breaks the paragraph correctly), `***`, and between lists.

Output matches the expected horizontal rule. The reported problem could not be
reproduced on `main`; entry closed as not applicable.
