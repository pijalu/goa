# B17 — web UI: visual assessment and the block-plane redesign

Date: 2026-10-05 · Branch: `feature/webui`

## Reported

The web UI was "unusable": the logo/mascot area broken with a blinking
element, typed input not appearing inside the input line, text blinking, and
the bottom status bar missing. The reviewer also redefined the approach:
the integration should move toward the compositor and use the web platform
(browser-owned redrawing, HTML block rendering, browser-native history)
instead of simulating a VT terminal.

## Assessment

Headless Chromium against `goa server` at 1280×800, 700×900 and 1280×300,
driving keystrokes, a real streamed answer, scrolling, resizing and the
config selector:

- At the commits of 2026-10-05 (after 32c395af) the cell pipeline renders
  correctly: mascot and logo intact, keystrokes echo into the input line,
  the band stays pinned, history scrolls, resize re-renders. The reported
  symptoms did **not** reproduce at HEAD.
- The repo-root binary was built 2026-10-04 10:44 — *before* 32c395af
  (B5/B7/B8/B9/B10: pinned live band, colour parity, startup input gate).
  Re-running that stale binary shows exactly the class of breakage
  reported. Rebuilding removes it.
- The structural critique stands regardless: every webui bug closed to date
  (B2, B3, B4, B5, B7, B8, B9, B10) lives at the same seam — a browser
  re-deriving UI semantics (transcript wipes, DECTCEM caret, colour
  inheritance, geometry resets) from an escape-byte stream built for a
  glass TTY. The design also cannot meet the v2 goals: resize re-renders
  and re-ships the whole transcript, history is a simulated bounded row
  list, layout is fixed-cell.

## Fix: the block plane (specs/webui.md §22)

One engine, one scene, three consumers. `buildScene` now exports the
conversation as semantic blocks (`tui.SceneBlock`: id, kind, markdown/plain
text, tool metadata, header art) straight from the Model the TUI already
maintains — no second renderer, nothing re-parsed from bytes.

- `tui`: `SceneBlock` + `BlockExporter` (ChatViewport) + `SceneObserver`
  (optional Terminal interface notified by `renderOneFrame`); `Layer` grew
  a `CapturesInput` flag; `ToolStatus.String` added.
- `internal/webui`: `Plane` (cells = zero value, blocks = production), a
  `BlockTracker` diffing Scene snapshots into id-keyed wire ops
  (`set` / append-`set` with a `from` offset / `reset` + journal for
  attach), a compact SGR→runs converter for art lines, and band-only
  publishing: the transcript cells and the scrollback never leave the
  process in the blocks plane. Non-capturing overlays (autocomplete popup)
  extend the shipped band; input-capturing ones (config selector, confirms)
  flip the frame to full-cell shipping until they close. A resize answered
  by an identical screen still ships (band-geometry change detection).
- `assets`: the page renders blocks as HTML flow content — markdown
  (headings, lists, fenced code, tables, quotes, inline styles, safe
  links), tool cards, collapsible thinking — with the theme palette, while
  the editor band stays a cell footer. No framework, no CDN, no build step.
  Asset URLs are now content-versioned (`?v=<hash>`) because the old
  fixed URLs under `Cache-Control: immutable` pinned a year-old app.js in
  any browser that had visited once.
- `--server-cells` serves the v1 plane unchanged; the plane is server-wide
  because transcript cells only exist when the server ships them.

## Validation

- `go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12` clean on
  touched packages; `go test -count=1 -race -cover ./...` green
  (webui 91.2 %, tui 76.1 %).
- New tests: tui block export (kinds/order/tool meta/header art/overlay
  flags), webui BlockTracker (set/append/rewrite/meta/reset/journal),
  StyledLinesToRuns (SGR, 256-colour, OSC-8, incomplete escapes),
  band-only publish + overlay transitions + attach frames + codec
  round-trip; every pre-existing webui test passes untouched (the cells
  plane is byte-identical).
- Browser: real Chromium runs of the whole flow — attach, typed echo,
  streamed markdown answer, scroll, 700 px reflow (native, no transcript
  re-ship), autocomplete popup extending the band, `/config` overlay
  fallback + Escape restore, `--server-cells` regression.
- `e2e/w2_webui_blocks.sh` (new): 8 checks in a real browser, all PASS.
  `e2e/w1_webui_browser.sh` now pins `--server-cells` — it asserts the
  v1 plane's page mechanics.

## Residual risks

- The page's markdown renderer covers the subset goa emits; exotic
  constructs fall back to paragraphs (text is never lost, only styling).
- Tool-block output ships as plain text in v2.0; styled runs are an
  upgrade path behind the same wire op.
- The no-JS `/text` + plain mirrors stay grid-based by design.

## Follow-up (same day) — four refinements from live use

1. **System panels render markdown.** The TUI's goa panel runs command
   output through the markdown renderer unless the text looks preformatted
   (`isPreformatted`/`looksLikeMarkdown`, tui/chat_viewport_markdown.go);
   the page now mirrors both heuristics, so `/quota` shows rendered
   headings/tables instead of raw source.
2. **The autocomplete-popup remnant.** `overflow:hidden` clips at the
   PADDING box, so the band's 8px top padding showed the bottom sliver of
   the stale popup rows the overlay left in the grid model ("content still
   visible behind the conversation"). The band window has no top padding
   now, and the caret inset compensates per mode.
3. **Tool blocks collapse like the TUI widget.** The server already ships
   the widget's live expanded state; the card adds a collapsed-output
   preview (first lines, TUI-preview style), hides it when open, and caps
   the open body at a scrollable 24 rows.
4. **Ctrl/Cmd+D detaches the tab.** It used to deliver an EOF byte —
   ending the session *and* stopping the server for every viewer. It now
   closes this tab's socket, invites the browser to close the window, and
   never reaches the engine; the server is owned by the console Ctrl+C.

Pinned by `e2e/w2_webui_blocks.sh` (11 checks: quota_md, popup_remnant,
ctrl_d_detach added; all PASS in a real Chrome) and a live tool-call pass
(collapsed by default, preview hidden when open).
