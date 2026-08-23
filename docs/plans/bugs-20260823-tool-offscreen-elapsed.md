# Fix plan — off-screen running tool widget freezes elapsed; final duration lost

Source: `bugs.md` §2 (2026-08-23).

## Problem

A running tool widget that scrolls fully above the viewport top stops
receiving repaints: the compositor clamps the window at the scrollback
watermark (`windowTop`), and a terminal cannot rewrite committed scrollback
rows. The per-frame elapsed tick (`ToolExecutionComponent.Render` →
`updateBox`) therefore lands on canvas rows the diff never emits — scrollback
keeps a stale `elapsed 123.5s` forever, and the completion's true `Took Xs`
never reaches it either. `scrollOffUnstable` deliberately treats the in-place
tick as benign (a per-tick full reset is the CPU storm the codebase already
rejected), and no one-time scrollback resync fires at the boundaries.

## Fix design (defer-and-sync at the two boundaries)

Physical constraint: per-tick scrollback rewriting is impossible without a
per-tick full reset (rejected: CPU storm). The sanctioned pattern in this
codebase is the ONE-TIME scrollback resync (`scrollbackDirty` →
`drawWindowResetScrollback` wipes `\x1b[3J` and re-emits the entire
transcript from the canvas — O(transcript) once). Apply it at exactly the two
boundaries the bug names:

1. **Running boundary** — a still-running widget transitions to fully
   off-screen (detected on its next invalidate, e.g. streamed progress):
   request one resync so scrollback reflects a fresh elapsed at that moment;
   the per-widget guard prevents repeat requests (no storm).
2. **Completion boundary** — a widget reaches a terminal status (✓/✗) while
   fully off-screen: re-arm the guard and request one resync so scrollback's
   final row for that block reads the true `Took Xs`, never a stale
   intermediate `elapsed`.

Mechanics:

- `Compositor.RequestScrollbackResync()` — sets `scrollbackDirty` under the
  compositor lock; the existing `handleMidTranscriptEdit` branch performs the
  single full reset on the next eligible frame (window changed or mutations
  settled), then clears the flag. NOTE (validated during testing): a frame
  that grows the widget above the window takes the DEFERRED path first
  (`deferScrollbackSync`); the actual reset lands on the following settled
  frame — the production render loop supplies it continuously.
- `ChatViewport` — `SetScrollbackResyncRequest(fn)` (wired in `TUI.Start` to
  the compositor method) + `maybeResyncOffscreenTool(tc)` helper with a
  `sync.Map` once-per-episode guard, called from the widget hooks the
  viewport already owns (`onInvalidate` closure; `onStatusChange` re-arms on
  the non-terminal→terminal transition). All on the command loop, same as
  the existing closure that iterates entries.
- The app's completion echo (`echoScrolledOffToolResult`) is kept: it
  attributes the completion in the live tail; the resync additionally makes
  the historical rows truthful.

### Pinned live strip (running-status currency)

Scrollback rows are immutable, so "keeps receiving row updates while running"
  is delivered the other way the report allows: the running widget is PINNED
  VISIBLE until completion. `ToolLiveStrip` is a 0/1-row pinned chrome
  component (the established goal-bubble/agent-tabs pattern) directly under
  the transcript: while the oldest running tool is fully off-screen it
  renders that widget's `LiveStatusLine()` — header identity + elapsed
  recomputed at call time + progress stats — on the live-ticker frames that
  already run while tools execute (B002). It renders zero rows otherwise and
  empties at completion (when the boundary resync rewrites history). Wired in
  `internal/app.assembleEngine`.

Mid-run staleness of the HISTORICAL rows between the two boundaries is
  inherent (scrollback is immutable); the live status itself stays current on
  screen via the strip, and the final row is always rewritten with the truth.

## Test approach

- Compositor unit: `RequestScrollbackResync` → next Render emits exactly ONE
  scrollback wipe (`\x1b[3J`); subsequent frames without requests emit none.
- ChatViewport unit (fake callback): running widget fully below the
  watermark → resync requested once (not per further output); terminal
  transition off-screen → requested again; on-screen widget → never.
- Live strip unit: 0 rows for on-screen/none; 1 row with call identity +
  fresh elapsed for off-screen running (clock-advance changes the line);
  empties at completion; end-to-end the line is VISIBLE in the pinned chrome
  band of the composed screen.
- Integration (fakeTerminal + screenEmulator, the repo's standard harness):
  baseline messages → running tool widget → pushed fully off-screen by later
  content → progress update triggers a reset whose scrollback re-contains
  the widget header; completion triggers another reset whose scrollback
  contains the final `Took` line. NB: the strip/resync activate one frame
  AFTER the scroll-off (the watermark publishes post-render), and
  `SetStatus(ToolRunning)` resets the execution clock — fixtures respect
  both.

## Validation steps

1. `go vet ./...`
2. `staticcheck ./...`
3. `gocognit -over 15 .`
4. `gocyclo -over 12 .`
5. `go test -count=1 -race -cover ./...`
6. PTY filmstrip: long-running bash tool via mockllm-free scenario — real
   TUI, tool scrolled off, verify final `Took` in captured stream.

## Acceptance

Off-screen running widget gets a scrollback refresh at the off-screen
boundary; its completion always rewrites scrollback with the true final
duration; no per-tick resets; gates pass; archived; committed.
