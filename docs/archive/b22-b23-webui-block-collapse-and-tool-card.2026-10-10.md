<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B22 / B23 — web blocks: collapse state ignored, tool cards unlike the TUI

Closed 2026-10-10. Archived from `bugs.md`.

## Reported

* **B22**: "thinking block should be expanded by default", and "for the collapse:
  follow the rules: if a collapse is open (or closed) subsequent item should follow
  the same logic" (answered: **per kind** — a thinking toggle affects later thinking
  blocks, a tool toggle later tool cards).
* **B23**: "tool calls will show a lot of content *then* be collapsed — the tool call
  should follow the TUI: some details are visible (type, file, action, couple line
  preview)" (answered: header + preview while running, header + preview when done,
  never the whole output unless opened).

## Root causes

**B22 — the collapse state was never consulted.** `renderBlock` read the previous
open state as

```js
var wasOpen = b.body && b.body.open;      // null on a block's FIRST render
```

and `collapsible(wasOpen, metaOpen)` treats anything that is not `undefined` as the
state to restore. `null` is not `undefined`, so every new collapsible was created
with `det.open = null` — closed — and the `meta.expanded` the server ships (the
TUI's own state: `newThinkingBlock` starts expanded, a tool card follows
`tui.tools.view`) was dead code. Every thinking block and tool card had to be
clicked open.

Two smaller defects on the same surface:

* The indicator selectors were `.block.thinking[open] summary::before` and
  `.block.tool[open] summary::before` — the `open` attribute lives on the
  `<details>`, never on the block wrapper, so the glyph never flipped. Verified in a
  real browser: `::before` content is now `"▾ "` open and `"▸ "` closed.
* There was no way for a reader's choice to reach later blocks at all.

**B23 — the preview was the wrong lines and the wrong count.** The page previewed
`headLines(b.text, 4)`: the *first* 4 lines, hard-coded. The TUI's collapsed body
shows the **tail** with a "… N earlier lines" notice (`tools/bash_renderer.go`
`RenderResult` slices `lines[len(lines)-maxLines:]`) and uses the view policy's
count (`tui.tools.preview_lines`, default 10). The page could not know that count:
it is view policy, so it has to be exported.

## Fix

* `wasOpen` is `b.body ? b.body.open : undefined`, so `meta.expanded` applies on the
  first render and the reader's live state wins afterwards.
* `collapsePref[kind]` records the reader's choice from a `toggle` listener attached
  *after* the initial state is set (a programmatic `open` fires `toggle` too), and
  `collapsible(kind, wasOpen, metaOpen)` consults it for new blocks of that kind.
* `tailLines(text, n)` + `previewLinesFor(meta)` replace `headLines`: the tail, with
  the server's `meta.preview` and the TUI's default (10) as fallback.
* The server exports it: `applyViewMeta` adds `"preview"` to a tool block's meta from
  `ToolExecutionComponent.effectivePreviewLines()`, a new method that also replaces
  the inline policy lookup in `updateBox` (one source for the number both renderers
  use).

## Tests

* `TestClientJS_ThinkingBlockHonoursServerExpanded` — expanded=1 renders open,
  expanded=0 renders closed.
* `TestClientJS_CollapsePreferenceIsStickyPerKind` — after a reader closes a thinking
  block, the next thinking block is closed and the next tool card is not.
* `TestClientJS_ToolCardPreviewsTheTailWithThePolicyLineCount` — `meta.preview=2` on a
  5-line output gives "… 3 earlier lines" + the last two lines, with the whole output
  in the open body.
* `TestClientJS_ToolCardPreviewFallsBackToTheTUIDefault` — no `meta.preview` → the
  TUI default of 10 lines.
* `TestExportBlocks_ToolMeta` (tui) — the exported meta carries the policy's count.
* Harness: `newClientHarnessIn(t, "blocks")` boots the real `app.js` in the blocks
  plane (the stub gained `window.__setBodyPlane`), and `blocksFrameDoc` builds
  blocks-plane frames.

RED before the fix (production change stashed, tests kept): all four client tests
failed with exactly the reported symptoms — "a thinking block the server ships
expanded was rendered collapsed", preview `"l1\nl2\nl3\nl4\n… +1 lines"` where the
tail was expected, and 4 preview lines where the policy said 10.

## Validation

* `go test -count=1 -race ./internal/webui/ ./tui/` — ok.
* Real browser: a `<details>` inside `.block.thinking` reports `::before` `"▾ "` open
  and `"▸ "` closed (the fixed selectors), and the blocks plane still boots clean
  (6 blocks, no page errors, caret placed, status live).

## Residual risk

The sticky preference is per page load (a reload forgets it) and per kind, as
answered. A block whose kind changes on re-render (none do today) would carry its old
element's state through `wasOpen`.
