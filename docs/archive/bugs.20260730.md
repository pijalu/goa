# Bugs fixed 2026-07-30 — TUI streaming stutter (scrollback watermark)


# Active Bugs

TUI code review context (2026-07-28): investigated user-reported streaming
stutter — identical streamed assistant sentences appearing 2-4x in the
terminal (scrollback), plus a transient duplicate line at the
history/screen border. Reviewed `tui/compositor.go` (watermark, scroll
emission, diff repaint), `tui/chat_viewport.go` (incremental frame cache),
`tui/chat_viewport_components.go` (message widgets), and
`internal/app/agent_streams.go` / `internal/app/stats.go` (stream wiring).
Findings below; areas reviewed and found sound: `updateLastEntry` lineOffset
guard, `unchangedRow` screen-position mapping, `repaintWindow` lastScrollCount
skip window, DECSTBM region bookkeeping, deferred auto-wrap handling (full
-width padded rows are always preceded by absolute CUP / cleared with 2K).

## Bug 1 — HIGH — Scrollback duplication + lost rows when a mid-stream shrink dips the window below the watermark and the canvas regrows across it in one frame

**Observed failure (user report):** during streaming, the same assistant
sentence is painted multiple times in the terminal (examples: "Let me check
how the transpiler uses r and res assignments." x3+, "Let me test some key
Tier 1 packages to see our progress." x4). Copies persist in scrollback.

**Observed failure (repro):**
`go test ./tui/ -run TestCompositor_DipRegrowAcrossWatermark_NoCorruption -v`
→ `transcript row "row-29" DUPLICATED within scrollback (2 times)` and
`transcript row "row-32" LOST from the terminal`.

**Root cause / localization (tui/compositor.go):**
1. `windowTop` (lines 849-855) deliberately lets the natural viewport top dip
   below the scrollback watermark when `canvasLen > height` (the "partial
   shrink" regime sanctioned by
   `TestCompositor_OneRowShrinkNoBlankBottom`). Dips happen mid-stream
   whenever the canvas shrinks: a tool widget finalizing
   (`patchRunningToolWidgets` / `updateEntryInCache`), a thinking block
   collapsing, a companion section `SetDone`, or a stream retry retracting
   the partial bubble (`internal/app/stats.go:202`
   `RemoveLastMessageOfType`).
2. While dipped, no scroll emission runs (watermark never moves backward) —
   fine. But when the canvas then regrows PAST the watermark within ONE
   frame (a large streamed delta), `advanceScrollback` (lines 922-928)
   computes `from = max(c.vt, c.scrollTop) = scrollTop` while the PHYSICAL
   screen top still shows canvas row `c.vt < scrollTop`.
3. `emitSteadyScroll`'s full-window branch (lines 1056-1071) assumes the
   physical top IS `from`: its line-feed scrolls push the physical top rows
   `[c.vt, c.vt+n)` into scrollback — rows ALREADY emitted there
   (DUPLICATES) — while the watermark advances to `to`, skipping rows
   `[scrollTop+n, to)` that never physically scrolled (LOST rows). The
   doc comment on `emitScrollbackAdvance` ("The screen currently shows rows
   [from, from+H)") is false in the dip regime.

**Fix plan:** in `emitSteadyScroll`, route the scroll through the existing
shift-safe fallback `emitTopDownScroll` whenever `c.vt < c.scrollTop`.
Top-down re-emit writes canvas rows `[from, to+windowH)` from the region top;
rows `[from, to)` scroll into scrollback with current text, exactly once,
independent of physical row identity. One-condition change; covers both
callers (`advanceScrollback` in renderDiff, `appendOverflow` in drawWindow —
overlay frames during a dip hit the same desync). Update the two doc
comments to state the dip-regime assumption.

**Test approach:** `tui/compositor_watermark_dip_test.go`
`TestCompositor_DipRegrowAcrossWatermark_NoCorruption` (RED before fix):
stream 40 rows, collapse 3 rows inside the window (dip), regrow 4 rows in
one frame; replay all bytes through TermEmulator; assert every transcript
row appears exactly once across scrollback+screen, none lost, none
duplicated. All existing scroll/stutter/ghosting regression tests must stay
green (`go test ./tui/`).

**Validation steps:** repro test GREEN; `go test -count=1 -race ./tui/`;
interactive shell run of `go run ./cmd/goa` streaming a long answer while a
tool runs — scroll back and confirm no duplicated/lost lines.

## Bug 2 — HIGH — Reset storm: a ticking tool widget crossing the scroll-off region wipes and re-emits the whole transcript per tick

**Observed failure (repro):**
`go test ./tui/ -run TestCompositor_TickingWidgetInScrollOffRegion_NoResetStorm -v`
→ `scrollback wiped 2 times during steady scrolling with a ticking widget`.

**Root cause / localization (tui/compositor.go):** `scrollOffUnstable`
(lines 894-917) treats ANY non-blank→different-non-blank change inside the
scroll-off region `[from, to)` as a malignant mid-transcript edit, routing
the frame to `drawWindowResetScrollback` — a `\x1b[3J` scrollback wipe plus
re-emit of the ENTIRE transcript. A running tool widget's elapsed-time
ticker (`patchRunningToolWidgets` → `updateEntryInCache`, same row position,
same height) is a benign same-position text edit: the physical row scrolling
off carries the right identity (one tick stale), which is fine for
scrollback. Cost of the false positive: O(transcript) bytes + full repaint
per tick while scrolling (perf/flicker); on terminals that ignore `\x1b[3J`
(some multiplexers) the re-emit APPENDS the whole transcript to scrollback
again — whole-history duplication.

**Fix plan:** refine `scrollOffUnstable` to fire only on POSITION-shifting
edits: when an in-region difference is found, lazily build a content→index
map of the current canvas transcript (excluding the chrome band, keyed on
stripped+trimmed content); a changed row whose previous content exists at a
DIFFERENT current index is a shift (malignant — keep the reset path); a
changed row whose previous content exists nowhere was edited in place
(benign). Blank↔content transitions remain benign. Extract the map builder
as a helper to stay within the complexity budget.

**Test approach:** same file,
`TestCompositor_TickingWidgetInScrollOffRegion_NoResetStorm` (RED before
fix): scroll a 30-row transcript one row/frame while a 3-row widget at a
fixed position ticks its text each frame; assert zero `\x1b[3J` writes and
exactly-once row accounting. True-shift coverage stays with the existing
quota-stream/mid-transcript tests (`compositor_quota_stream_repro_test.go`).

**Validation steps:** repro test GREEN; `go test -count=1 -race ./tui/`;
`TestCompositor_QuotaStreamRepro` still passes; interactive run with a
long-running bash tool while text streams — no per-tick flicker/rewrite.

## Bug 3 — MED — Transient duplicate line at the scrollback/screen border during streaming dips (known trade-off, documented)

**Observed failure (user report):** "on the border of history and main
screen there is sometimes a duplicate line during streaming; it disappears
quickly — off-by-one between main screen and history."

**Localization:** `windowTop` (tui/compositor.go lines 849-855): during a
partial shrink (canvas still taller than the terminal), the window anchors
at the natural `vt < scrollTop`, so `repaintWindow` re-paints the
`scrollTop - vt` rows below the watermark that are already in scrollback.
The overlap is VISIBLE as the same line at the bottom of history and the
top of the screen until regrowth re-anchors the window (a few frames).

**Resolution:** this overlap is a deliberate product trade-off sanctioned by
`TestCompositor_OneRowShrinkNoBlankBottom` (the alternative — clamping the
window to the watermark — leaves an orphaned blank row at the screen
bottom, the "screen shrank one line" regression). Bug 1's fix removes the
CORRUPTION half of this regime (the overlap can no longer become permanent
scrollback duplication); the transient visual overlap itself is accepted
and documented here. No code change. If it proves too visible in practice,
the follow-up is reducing spurious mid-stream canvas shrinks (e.g.
batching tool-widget finalization with the next streamed delta), not
compositor geometry.
## Resolution

- **Bug 1 — FIXED.** emitSteadyScroll now routes through the shift-safe emitTopDownScroll whenever the window is dipped below the scrollback watermark (c.vt < scrollTop). Regression: tui/compositor_watermark_dip_test.go TestCompositor_DipRegrowAcrossWatermark_NoCorruption. Validated on a real pty (script capture + TermEmulator replay): pre-fix control produced 4 duplicated + 4 lost scrollback rows; fixed build: 60/60 marker rows exactly once.
- **Bug 2 — FIXED.** scrollOffUnstable now only fires on POSITION-shifting edits (content→index map, lazily built); same-position text edits (live widget tickers) no longer wipe and re-emit the whole transcript per tick. Regression: TestCompositor_TickingWidgetInScrollOffRegion_NoResetStorm.
- **Bug 3 — CLOSED (documented, no code change).** Transient border overlap during partial-shrink dips remains a sanctioned trade-off (TestCompositor_OneRowShrinkNoBlankBottom); Bug 1's fix ensures it can no longer become permanent corruption. Revisit only as a product decision (blank-bottom vs overlap).

Gates: go vet clean; staticcheck only pre-existing U1000 in unrelated files; gocognit/gocyclo no new entries; go test -count=1 -race -cover ./... all green.
