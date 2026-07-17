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
| P4 pinned chrome | in-progress | — | repro test RED; render-path pin (compose untouched) |
| P0 live progress | pending | — | |
| P1 expand mid-stream | pending | — | |
| P2 steering edit | pending | — | |
| P3 search lines | pending | — | |

## Key lessons

- Compositor virtual-buffer invariant: scrolled-off transcript rows MUST stay in the buffer so differential scroll can emit them to native scrollback. Do NOT cap/shift the canvas in `compose` (broke 15 tests). Pin chrome in the **render/diff path** instead.
