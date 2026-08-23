# Bug fix report: text color/background regression during streaming

Date: 2026-08-23 · Branch: feature/ratatui · Status: CLOSED

## Symptom (bugs.md `# To fix`)

With the latest changes there were text color/background regressions while
content streams:

- the same bubble changed color across frames while streaming/scrolling;
- during a tool call the block's background was not fully redrawn (BLUE not
  set across the block); tool-call text could lose its background entirely;
- identical rows did not keep one color: rows scrolled off-screen rendered
  GREY while on-screen rows rendered WHITE.

Evidence (`../term.log`): assistant ANSWER lines carried the chrome/muted
foreground `SGR 38;2;139;148;158` (footer-chrome RGB), tool blocks painted
`38;2;139;148;158;48;2;33;38;45` (grey-on-navy) instead of their intended
fg/bg pair.

## Root cause

The compositor's dirty-row emitter gained column-range partial updates
(`CUP(row,col)` + segment instead of erase + full-row rewrite). A partial
segment leaves every OTHER cell of the row untouched, so what those cells
render depends on the terminal's carried SGR state at that point of the wire
stream. Whenever a partial segment did not close its style (or began relying
on state opened in an earlier frame), untouched cells — and rows painted
after them — inherited the leaked attributes: chrome grey foregrounds bled
into content rows and block backgrounds were only partially set. Full-row
emission never exposed this because it rewrote the entire row under one
explicit style.

## Fix plan (as executed)

1. **Guarded partial emission** (`tui/compositor_rowdiff.go`):
   `planPartialRow` picks a column-range update ONLY when a stable prefix or
   suffix exists AND both rows are emittable (`rowEmittable`: no tabs/C0
   controls, no OSC, and either no escapes at all or a terminating reset),
   so SGR/hyperlink state cannot leak across frames via untouched cells.
   `planTailRow` / `planHeadRow` align the cut boundary so the untouched
   suffix renders under IDENTICAL SGR state in both frames; otherwise the
   emitter falls back to the legacy full-row clear+rewrite
   (`emitRowUpdate`).
2. **Both repaint paths route through it**: transcript rows and pinned
   chrome rows (`tui/compositor_scroll.go`) use `emitRowUpdate`, so partial
   and full-row emission behave identically everywhere.
3. **Emit-time SGR coalescing** (`internal/ansi/coalesce.go`,
   `tui/compositor.go` `writeFrame`): every frame buffer passes through an
   SGR run coalescer tracking the attribute state the terminal holds across
   the contiguous byte stream, eliding duplicate runs and reset+re-open
   pairs without changing what the terminal renders. State re-synchronizes
   on Restore().
4. **SGR-58 underline-color plumbing** (`internal/ansi/state.go`): ANSI
   state models ISO 8613-6 underline color (58/59) and exposes `EqualSGR`
   so emission decisions compare full SGR state.

## Test approach & validation (all passing)

- `tui/compositor_filmstrip_regression_test.go` — filmstrip visual-equality
  gate: drives the compositor through streaming-turn, tab-switch and resize
  scenes; replays EVERY frame into a shared `TermEmulator`; asserts
  cell-for-cell equality against the canvas-derived screen after every
  frame (`TestFilmstrip_StreamingTurnVisualEquality`,
  `TestFilmstrip_TabSwitchVisualEquality`,
  `TestFilmstrip_ChromeResizeVisualEquality`).
- `internal/app/stream_color_regression_test.go` — production event path
  (uiScenario) asserted on per-cell emulator styles; pins that assistant
  content never wears the chrome grey `#8b949e`, markdown tables keep one
  stable style, and tool-call blocks keep their background while streaming
  (`TestUI_StreamBubbleKeepsOneForeground`,
  `TestUI_MarkdownTableKeepsStableStyle`,
  `TestUI_ToolCallBlockKeepsBackgroundWhileStreaming`).
- `tui/jitter_regression_test.go` — diff frames must leave unchanged rows
  CELL-identical through the emulator (stronger than the erase-byte proxy);
  split into `emuForClampedScene` / `assertDiffFrameScreenIntegrity`
  helpers to stay within the gocyclo budget.
- `tui/compositor_partial_bytes_test.go`,
  `tui/compositor_rowdiff_integration_test.go`,
  `tui/compositor_rowdiff_partial_test.go` — unit coverage of the planner
  boundaries and byte shapes.
- `tui/compositor_sgr_coalesce_test.go` — coalescing reduces bytes and stays
  screen-equivalent.
- Quality gates (run separately): `go vet ./...` clean; `staticcheck ./...`
  clean except pre-existing SA1019 in core/commands/model_test.go (unrelated,
  noted); `gocognit -over 15 .` and `gocyclo -over 12 .` match the pre-change
  baseline exactly; `go test -count=1 -race -cover ./...` green.

## Closure

Streaming keeps one stable fg/bg per bubble across frames, tool-call blocks
paint their full background, and content color no longer changes when rows
scroll off-screen. Closed 2026-08-23.
