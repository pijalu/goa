# Investigation — Mouse wheel scroll-up jumps to the beginning of the terminal when scrolled too fast

Date: 2026-08-15 (goal `sturdy.lark`)
Status: **Investigation complete — root cause is in Ghostty's native-scroll delta math, not in Goa's rendering.**
Terminal under test: **Ghostty 1.3.1** (stable) on macOS (`TERM_PROGRAM=ghostty`).

## Symptom

> "Scroll up with the mouse wheel will usually jump directly to the beginning of the terminal if too fast."

In a Goa session the user scrolls up with the mouse wheel (or trackpad) to re-read
history; when the wheel is spun fast the viewport snaps all the way to the top of
the session instead of moving a few lines.

## Conclusion (TL;DR)

1. Goa renders in the **main screen buffer** (no alternate screen, no mouse
   reporting) and pushes the whole transcript into the **terminal's native
   scrollback** by design. The mouse wheel scrolls that scrollback; Goa never
   sees wheel events.
2. Ghostty maps a wheel event to a scroll of
   `yoff_max × cell_height × mouse-scroll-multiplier` rows
   (`src/Surface.zig` `scrollCallback`). On macOS the OS **ramps `yoff` up with
   wheel speed** ("macos tries to simulate precision scrolling with non precision
   events by ramping up the magnitude of the offsets as it detects faster
   scrolling"), and the default discrete multiplier is **3**.
3. So a fast wheel spin produces a delta of dozens of rows **per event**, events
   arrive many times per second, and Ghostty applies the delta **instantly with
   no smoothing/clamping**. With a session scrollback of a few hundred to a few
   thousand lines, a fast flick reaches the top → *"jump directly to the
   beginning of the terminal"*.
4. **This is not a Goa rendering bug.** Goa's byte stream during streaming is
   clean (verified below); nothing Goa emits clears the scrollback or resets the
   viewport. The same jump happens in any full-screen app in the main buffer
   (shell, `top`, `watch`, etc.) with a large scrollback on Ghostty.
5. Ghostty itself already fixed this class of bug for **mouse-reporting mode**
   (issue #10004: discrete wheel events were inflated 3×3=9) — but the fix
   applies only when the *app* enables mouse reporting, and it **did not change
   the native viewport-scroll path** that Goa (and every non-mouse-reporting
   app) uses. The viewport path still applies the macOS ramp × the multiplier.

## How the scrollback works in Goa (design)

- `tui/terminal.go` `Start` — raw mode, `\x1b[?7l` (DECAWM off), bracketed
  paste, Kitty keyboard query. **No `\x1b[?1000h/1002h/1003h/1006h` — no mouse
  reporting.** (verified: no mouse-enable sequence anywhere in `tui/`/`internal/`).
- `tui/compositor.go` — explicit-scrollback model: rows that scroll off the top
  are emitted into the terminal's native scrollback exactly once via `\n`
  line-feeds inside a DECSTBM region `[1, height-chromeH]`, inside one
  CSI-2026 synchronized frame. Frames repaint the visible window with absolute
  CUP.
- `tui/tui.go:679-682` — the TUI routes all input to the focused component;
  **there are no mouse event handlers; scrolling is done via the terminal's
  native scrollbar.** (`docs/TUI.md:146-152` still describes an old
  wheel→arrow-key/ChatViewport.scrollTop design that no longer matches the code;
  that doc is stale.)
- `docs/TUI-REWORK-PROGRESS.md` P4 — this native-scrollback approach is
  deliberate (the alternative, e.g. opencode's OpenTUI cell-buffer + internal
  scroll, was considered and rejected).

### Verified byte stream (scratch compositor test)

Two consecutive steady-streaming frames (12-row screen, 2-row chrome) emit:

```
frame N:   \x1b[?2026h \x1b[1;10r \x1b[10;1H \n \r\x1b[2K[status] \n \r\x1b[2K[input ] \n \r\x1b[2Kl12 ... \n \r\x1b[2Kl15 \x1b[r \x1b[1;1H... \x1b[?2026l
frame N+1: \x1b[?2026h \x1b[1;10r \x1b[10;1H \n \r\x1b[2Kl16 ... \x1b[r \x1b[1;1H... \x1b[?2026l
```

The `\n` at the region bottom scrolls the region and appends rows to the
terminal scrollback; the CUP repaints target the active area. **Nothing here
clears scrollback or moves the viewport**; the scrollback grows monotonically
(the intended design).

## Ghostty-side mechanism (source-verified, v1.3.1 and main)

Config defaults (no `~/.config/ghostty/config` present on this machine, so
defaults apply):

- `mouse-scroll-multiplier` = `precision:1, discrete:3` (`src/config/Config.zig:996`).
- `scroll-to-bottom` = `keystroke, no-output` (`src/config/Config.zig:942`) —
  new output does **not** yank the viewport to the bottom.
- `scrollback-limit-bytes` = 50 MB, `scrollback-limit-lines` = unlimited
  (`src/config/Config.zig:1390,1408`) — the session scrollback is effectively
  the whole transcript.

Wheel path (`src/Surface.zig` `scrollCallback`, v1.3.1 lines 3376-3546):

1. Discrete wheel: `yoff_adjusted = yoff_max × cell_size × multiplier.discrete`
   with `yoff_max = max(yoff, 1)` (line 3410-3415). macOS ramps `yoff` with
   speed, so a fast spin gives `yoff` of several units.
2. Precision (trackpad): `yoff_adjusted = yoff × multiplier.precision` (pixels).
3. `delta = trunc(poff / cell_size)` rows (line ~3425-3436).
4. No mouse reporting active (Goa) → `scrollViewport(.{ .delta = delta })`
   (line 3544).
5. `scrollViewport(.delta)` → `Screen.scroll(.delta_row)` →
   `PageList.scroll(.delta_row)` (`src/terminal/Screen.zig:1591`,
   `src/terminal/PageList.zig:3231`). Scrolling up past the top clamps to
   `.top` (`upOverflow` overflow → `viewport = .top`).

Numeric example (cell height ≈ 17 px):
- Slow single notch: `yoff=0.1 → max=1 → 1×17×3 = 51 px → 3 rows` (the
  intended "3 lines per notch").
- Fast spin (macOS ramps `yoff` to, say, 5-10 per event): `5..10 × 17 × 3 =
  255..510 px → 15..30 rows` **per event**, events at 60-100 Hz → 100-500 rows
  in a fraction of a second. On a session with a few hundred lines of
  scrollback, that is the top.
- Trackpad flick (precision): large momentum pixel deltas × 1, no smoothing →
  same outcome.

Ghostty applies the delta **instantly** (no scroll animation/smoothing — there
is no smooth-scroll option in the config), which is why a large delta looks
like a *jump* rather than a fast scroll. Terminal.app / iTerm2 interpolate
wheel scrolling; Ghostty snaps.

Ghostty issue #10004 ("fix(mouse): avoid inflated wheel events in mouse
reporting mode", closed/merged after 1.3.1) is the same class of bug:
discrete wheel input was being inflated (OS tick magnitude × discrete
multiplier ⇒ 3×3=9). The fix only changed the **mouse-reporting** branch
(`if (self.isMouseReporting())`), ignoring the raw tick magnitude there. The
**native viewport path** — the one Goa uses — was left with the inflated delta.
It is reasonable to report this as a Ghostty bug: the viewport path should
either ignore the macOS ramp when the discrete multiplier is applied, or clamp
per-event deltas.

## What is NOT the cause

- **CSI 2026 (synchronized output):** frames are tiny and short-lived; the
  sync only defers the draw, it does not change scroll deltas. (Ghostty #12685
  is a rendering-corruption issue with DEC 2026, unrelated.)
- **DECSTBM region:** row erases inside the region only move pins within the
  region; the scrolled-up viewport pin is in the scrollback above it.
- **Viewport pin sliding:** Ghostty keeps a scrolled-up viewport pinned to its
  content when new output appends (verified in `PageList.grow`); pins only
  reset on scrollback **pruning** (50 MB byte limit — not hit in normal Goa
  sessions) or resize.
- **`scroll-to-bottom`:** default `no-output`; output does not yank to bottom.
- **Goa clearing scrollback:** `\x1b[3J` only on startup, `/new`
  (`Compositor.Clear`), or width change (`drawWindowResetScrollback`), never in
  steady streaming.

## Recommendations

### User-side (immediate mitigation)

- Set `mouse-scroll-multiplier = 1` (or lower) in `~/.config/ghostty/config`:
  reduces the discrete jump 3× (slow notch = 1 line instead of 3). The macOS
  ramp still applies, so a very fast spin can still jump far, but it is
  dramatically less aggressive.
- Slow down wheel scrolling when reading history (or use the trackpad's less
  aggressive momentum).

### Upstream (root cause)

- File a Ghostty issue: apply the #10004-style handling to the **native
  viewport** scroll path (`Surface.scrollCallback` → `scrollViewport(.delta)`):
  ignore the OS-ramped tick magnitude when the discrete multiplier is applied
  (or clamp the per-event delta), and/or add smooth/accelerated scrolling like
  Terminal.app/iTerm2.

### Goa-side (optional, larger)

- If the native-scrollback UX remains unacceptable, the only Goa-side fix is to
  **enable mouse reporting (SGR 1006) and implement an internal scroll view of
  the transcript** (the opencode/OpenTUI model). That is a significant design
  change deliberately avoided in the compositor rewrite (P4); it is not needed
  to fix this bug, since the root cause is Ghostty-side.

## Files read (evidence)

| File | Lines | What it shows |
|------|-------|---------------|
| `tui/terminal.go` | 110-179 | Start: raw mode, DECAWM off, bracketed paste, Kitty query; **no mouse tracking** |
| `tui/tui.go` | 679-682 | No scroll/mouse handlers; native scrollback by design |
| `tui/compositor.go` | 303-327, 1113-1373 | Explicit-scrollback watermark; `\n` scroll into terminal scrollback; DECSTBM region |
| `docs/TUI-REWORK-PROGRESS.md` | 76-138 | P4 native-scrollback design rationale; opencode comparison |
| `docs/TUI.md` | 146-152 | **Stale** old wheel→arrow-key/ChatViewport.scrollTop doc |
| Ghostty `src/config/Config.zig` | 928-996, 1390-1408 | `scroll-to-bottom` default; `mouse-scroll-multiplier` defaults; scrollback limits |
| Ghostty `src/Surface.zig` (v1.3.1) | 3376-3546 | Wheel delta math: `yoff_max×cell×3`, instant `scrollViewport(.delta)`; #10004 branch only under `isMouseReporting` |
| Ghostty `src/terminal/Screen.zig` | 1562-1614 | `scrollViewport` → `Screen.scroll` |
| Ghostty `src/terminal/PageList.zig` | 3231-3440, 3940-4100 | `delta_row` scroll; `.top` clamp; pins stay fixed on grow/prune |
| Ghostty PR #10004 | diff | Mouse-reporting wheel inflation fix — viewport path unchanged |
