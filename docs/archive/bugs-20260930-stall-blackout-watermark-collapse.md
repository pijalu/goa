<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug: stall blackout — the TUI blanks the conversation and only a resize recovers it

Date: 2026-09-30 · Status: FIXED — implemented, tested, gates clean.

## Reported

> When thinking stall, the TUI can be blacked out with "reconnecting" type of
> message but content is gone — need a resize to redraw.
>
> Why is the TUI redrawn to an empty message history with only the bottom part of
> the screen, without any left over of the conversation blocks?

Evidence: diagnostic bundle
`/Users/muaddib/dev/creaves.project/.goa/exports/goa-export-20260930-101703.zip`
(session `1790748040_v0aoy249`, provider `zai`, model `glm-5.3`). Its
`diagnostics/trace.json` ends with request 21 `"pending": true` — a stream open
at export time, never finalized — and flags requests 6 and 8 as
`context canceled`. That is the stall: the provider went quiet, the retry path
engaged, and the screen went black while the reconnect spinner kept spinning.

## Root cause

The compositor clamps the viewport top to the scrollback watermark
(`Compositor.windowTop`, `tui/compositor_frame.go`) so a row already committed to
terminal scrollback is never repainted — repainting it would duplicate the row
on screen. That clamp is ground truth about the TERMINAL, and while the canvas
only ever grows it is equally true of the canvas.

A stream-retry retraction breaks the second half. `handleStreamFailure`
(`internal/agentic/agent_stream_retry.go`) resets the round, and the TUI retracts
the partial bubble
(`handleUserOrSystemContent` → `RemoveLastMessageOfType` on the `stream_retry`
notification, `internal/app/stats_stream.go`). The canvas can collapse to fewer
rows than the watermark already committed. Then:

```
vt        = scrollTop        (clamped)         = 112
contentEnd = len(canvas)-chromeH               = 1
```

`vt >= contentEnd`, so the window contains **no transcript row at all**:

- `drawWindow`'s paint loop `for i := vt; i < transcriptEnd` never executes
  (112 → 1);
- its stale-row clear loop starts at screen row `transcriptEnd-vt+1` = **-110**,
  which the terminal clamps to row 1 and then uses to erase the entire
  transcript region.

Result: a black screen with only the pinned chrome band (status line, editor)
still drawn — the "only the bottom part of the screen" in the report — while
`Reconnecting (attempt N/M)...` spins in the status line. Nothing recovers it:
`scrollTop` is only ever lowered by `Clear()` or a width change, so no later
frame can re-anchor the window. A resize works only because it changes the
geometry (or triggers the scrollback reset).

The same collapse is reachable without any retry: `clearChat`
(`internal/app/events_control.go`, used by session restore and `/fork`) empties
the viewport but never called `ClearTranscript`, unlike `handleNewSession`.
Measured in the app harness: **5 canvas rows against `scrollTop` 115** after
`clearChat`.

## Fix

Two halves, because the defect has an app-side trigger and a compositor-side
consequence.

1. **Compositor — a settled collapse must not stay black**
   (`tui/compositor_blackout.go`, wired in `handleMidTranscriptEdit`).
   `windowBlackedOut` detects `vt >= contentEnd`. The frame that is still *in
   flux* is painted exactly as before — a transient shrink must still clear the
   rows it deleted, or removed messages linger on screen
   (`TestRenderGolden_ShrinkClearsStaleRows`) — and is recorded via
   `armBlackout`. When the **same** collapsed canvas comes back
   (`blackoutSettled`), the collapse is final, and `recoverBlackout` re-syncs the
   scrollback with one full reset: wipe, re-emit the surviving transcript,
   watermark re-anchored. The reset is safe here precisely because it wipes
   first, so nothing is duplicated.

2. **App — `clearChat` resets the transcript** like `handleNewSession` does, so
   the fresh canvas renders as a first frame instead of relying on the
   compositor to recover.

Discriminating transient from settled is the crux. Honoring the watermark
(repaint nothing) and avoiding the blackout are not in conflict: honoring it
means never repainting a *committed* row, and a transient shrink genuinely must
erase the rows it deleted. Only a collapse that persists is recovered.

## Tests

`tui/compositor_shrink_blackout_test.go`

- `TestCompositor_ShrinkBelowWatermark_Blackout` — the reported blackout: a
  settled collapse on a deep-watermark conversation must not leave the
  transcript region blank, and the reconnect notification must be visible.
- `TestCompositor_ShrinkToTail_RendersSurvivingRows` — a tail shorter than one
  window still renders its surviving rows.
- `TestCompositor_TransientShrinkStillClearsDeletedRows` — the other half of the
  contract: a one-frame shrink is still painted, so deleted rows are erased and
  the recovery does not fire early (no scrollback wipe on the collapse frame, at
  most one deferred resync on regrow).

`internal/app/stall_blackout_test.go`

- `TestClearChatResetsTranscript` — the app trigger: `clearChat` must drop the
  watermark and render the fresh canvas without resurrecting the old one.

Pre-existing invariants that had to keep passing (they pin the opposite side of
the same trade-off, and both previously passed *vacuously* — a black screen
satisfies "the mascot is not visible"):

- `TestCompositor_ShrinkBelowWatermarkDoesNotRedrawScrollbackRows`
- `TestCompositor_RegrowAfterShrinkDoesNotReEmitOffScreenContent`
- `TestRenderGolden_ShrinkClearsStaleRows`

## RED-before-fix evidence

Each fix reverted independently, confirming the tests actually catch the defect:

- Compositor fix reverted → `TestCompositor_ShrinkBelowWatermark_Blackout` and
  `TestCompositor_ShrinkToTail_RendersSurvivingRows` FAIL with
  `BLACKOUT: transcript region is entirely blank after the shrink (chrome band
  only)`.
- `clearChat` fix reverted → `TestClearChatResetsTranscript` FAILs with
  `clearChat left scrollback watermark at 115`.

An earlier end-to-end stall test through the real event path was written and
then **discarded as unproven**: it passed with the fix disabled, because in the
app harness the canvas layer stays tall (`AddSystemMessage` keeps the viewport
layer high) and existing reset guards recover the collapse. It is not included
rather than kept as decoration. The blackout itself is proven at the compositor
level, where the geometry is exact.

## Validation

- `go build ./...`, `go vet ./...` — clean.
- `staticcheck ./tui/... ./internal/app/...` — clean.
- `gocognit -over 15`, `gocyclo -over 12` on the changed files — clean.
- `go test -count=1 -timeout 600s ./...` — all packages pass.
- `go test -count=1 -race -timeout 900s ./...` — all packages pass, no races.

## Residual risk

`recoverBlackout` costs one scrollback wipe plus a full re-emit, but only once
per settled collapse — never per frame. A collapse that alternates between two
distinct short canvases (rather than repeating one) stays in the transient
regime and will keep painting an empty window until it settles; that did not
occur in the reported scenario, where a stall holds one canvas for the whole
retry. Closing it would mean keying the recovery on elapsed frames rather than
canvas identity, which is a larger behavioral change than this fix warrants.
