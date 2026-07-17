# TUI Rework — feature/tui-rework

Autonomous fix branch for rendering/streaming issues found via session-log analysis.

## Issues (root-caused)

- **P0** Frozen tool widgets / no live progress — tool args don't stream (0 `IsDelta` tool_calls in session log); bash stdout not live; footer shows only wall-clock "elapsed". Fix: universal live-progress footer (`elapsed · KB · lines`) + bash stdout streaming + provider arg-delta wiring/fallback.
- **P1** Ctrl+O doesn't expand mid-stream — global toggle re-derived per delta (`effectiveExpanded`); affordance hardcoded "to expand". Fix: per-widget expand wins while streaming; sticky override; honest label.
- **P2** Steering message not editable — `steeringPending.HandleInput{}` no-op; verify `OnEditSteering` wiring + hotkey. Fix: editable bubble + hotkey.
- **P3** Search `context_lines` dead param; want `max_lines`. Fix: wire context_lines; add max_lines; tests.
- **P4** History leak: chrome (input/status/footer/bubbles) scrolls into scrollback when tool output overflows screen. Repro: `tui/pinned_chrome_scrollback_test.go` (RED). Fix: pin chrome out of scroll region in the render/diff path.

## Progress log

| Item | Status | Commit | Notes |
|------|--------|--------|-------|
| Branch setup | done | — | baseline green except P4 RED test |
| P4 pinned chrome | done | — | compositor rewrite: watermark + DECSTBM region; full suite green incl. race; complexity in budget |
| P0 live progress | pending | — | |
| P1 expand mid-stream | pending | — | |
| P2 steering edit | pending | — | |
| P3 search lines | pending | — | |

## P4 solution (committed)

Rewrote the compositor around an explicit-scrollback model instead of the old
accreted six-strategy scroll dispatch:

- **`scrollTop` watermark** — transcript rows emitted into terminal scrollback
  exactly once, in order, monotonically advancing, and clamped to the chrome
  band start so chrome can never leak (structural, not heuristic).
- **One scroll mechanism** (`emitScrollbackAdvance`) with two sub-cases:
  first-frame top-down write (blank screen) and steady-state bottom
  scroll-then-write. The old first/large/bare/deleted/shrink/resize strategy
  dispatch is gone.
- **`prevWindowFull` discriminator** — a previous window counts as "full" only
  if every transcript region row is non-blank; partial windows (blank padding
  from bottom-aligned short content) take the full-range re-emit path. This was
  the subtle case behind the last content-loss bug.
- **DECSTBM scroll region** `[1, height-chromeH]` confines native terminal
  scroll to the transcript, so pinned chrome (status/editor/footer/bubbles)
  never moves and never enters scrollback.
- Chrome classification is role-based: the single `HeightAllocated` child is
  the scrollable transcript; every child after it is pinned chrome
  (`Scene.ChromeHeight` computed in `buildScene`).

Net −208 lines in compositor.go. Both test emulators (`TermEmulator`,
`screenEmulator`) gained DECSTBM support.

## Key lessons

- Compositor virtual-buffer invariant: scrolled-off transcript rows MUST stay in the buffer so differential scroll can emit them to native scrollback. Do NOT cap/shift the canvas in `compose` (broke 15 tests). Pin chrome in the **render/diff path** instead.
- Reference check: `../pi` renders one flat buffer and lets chrome scroll into history (accepts the leak class); `../opencode` uses OpenTUI, a cell-buffer compositor (GPU-style, internal scroll, no native scrollback). Goa's DECSTBM-region approach pins chrome without OpenTUI's full cell-buffer model.
