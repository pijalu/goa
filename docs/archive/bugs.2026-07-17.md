<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Archive — 2026-07-17

Archived after fixing in the 2026-07-17 session (two rounds: initial close-out
plus a review-fix round against `main` that corrected two root causes and added
two items). See `TODO.md` "Review-fix plan (2026-07-17)" for the full tracker.

## Stuck in "Sending request..." — FIXED

goa got stuck on "Sending request..." with no error or timeout indication.
Sending another message un-stuck the session.

**Root cause (corrected in review-fix round):** three independent defects
stacked; the original close-out credited only the first and mis-scoped the
second.

1. **Unbounded connect phase.** When the provider config had no `timeout`
   field, no HTTP timeout was applied at all, so a request to an unresponsive
   server (accepts the connection, never sends a response header) hung
   forever — the spinner sat on "Sending request..." indefinitely. The first
   fix (`provider/manager.go` default 5-minute timeout) was itself too broad:
   it applied a whole-request deadline that killed long *healthy* streams
   mid-body (slow local models like LM Studio can stream for hours).

2. **Progress-clear was a no-op.** `finishProcessing()` — the guaranteed
   cleanup path on every turn exit — did emit `EventProgress{Text: ""}`, but
   `handleProgressEvent` ignored empty-text progress events, so the clear
   never reached the spinner. The clear only happened on the specific error
   branches of `handleStreamFailure`, leaving all other exit paths stuck.

3. **Missing `EventEnd` on runner error.** `executeRunner` returned runner
   errors up the call stack without always guaranteeing a terminal `EventEnd`
   reached the UI, so the turn was never marked complete on some failure
   paths.

**Fix:**

- `internal/agentic/provider/transport/http.go`: `clientWithHeaderTimeout`
  clones the transport and sets `ResponseHeaderTimeout` (dial → TLS → request
  send → first response header) while keeping `http.Client.Timeout` at zero so
  body reads stay unbounded. `TransportRequest.Timeout` now bounds only the
  connection phase; a stalled body is caught by the provider runtime's
  idle-timeout reader, not a wall clock. Default remains 5 minutes when the
  provider config omits `timeout` (`provider/manager.go`).
- `internal/app/stats.go`: `handleProgressEvent` now treats empty-text
  progress as the clear signal — `statusMsg.Clear()` + render — so the
  `finishProcessing` cleanup emission always reaches the spinner on every exit
  path (success, error, cancellation).
- `core/agentmanager.go`: `executeRunner` always emits a terminal `EventEnd`
  (with the loop-stop reason, a cancellation marker, or the error text) via
  both agent and internal channels with backpressure so it is never dropped
  under load.

**Also fixed (error visibility):** the retry message in `handleStreamFailure`
was `transient`, so it vanished at turn end and the user never saw that an
error occurred even when a retry succeeded. Retry messages are now
non-transient, and exhausted retries surface a clear non-transient system
message with full error details.

**Tests:** `TestHTTPTransportHeaderTimeoutFiresOnHang` (unresponsive server
aborts at the connect-phase timeout), `TestHTTPTransportHeaderTimeoutAllowsSlowStream`
(a stream slower than the timeout still completes — regression for the
over-broad whole-request deadline), `TestBugs_ProgressClearClearsStatus`
(empty progress text clears the spinner).

## Tool and chat history artefacts — FIXED

Tool calls in history appeared to show terminal-rendering artefacts mixed into
conversation output.

**Root cause:** not a rendering bug. The tool-execution component properly
strips ANSI and renders results in themed boxes; the perceived artefacts were
the terminal footer/status bar appearing in full-frame filmstrip captures.
Tool-result content is properly isolated from terminal framing.

**Validation:** `TestBugs_ToolCallNoTerminalArtefacts` verifies that write
tool results do not contain raw terminal prompt patterns (`~/dev/goa`,
`tok/s`, `coding-posture`) in the visible conversation text.

## Steering view preview — FIXED

The pending-steering view rendered all queued lines with no line count or
truncation, so a long multi-line steering message flooded the viewport.

**Fix:** `tui/chat_viewport_components.go` `steeringPending.Render()` caps the
preview at 5 wrapped lines and adds a footer with the total line count
("N line(s) to send (M hidden)"). A `countLines` helper computes the wrapped
count accurately, and leading blank lines are skipped in the preview.

**Tests:** `TestSteeringPending_Render_LeadingBlanksSkipped`, steering line
count/preview filmstrip tests.

## Steering edit-before-send — FIXED

There was no way to revise a queued steering message before it was injected;
the only option was to let it send as-is.

**Fix:** Alt+E recalls pending steering back into the editor, flushes the
steering queue, and clears the steering bubble and footer indicator so the
message can be edited and re-sent.

**Tests:** `TestHandleEditSteering_*`.

## Write tool stats — FIXED (corrected in review-fix round)

Write stats showed "writing N lines" during streaming, but after completion
the widget reported the preview line count instead of the total lines written.

**Root cause (corrected):** `buildWritePreview` fences only the first 10 lines
of the content in the tool result. Post-completion the renderer counted lines
from that fenced preview, so the "total" was the preview size, not the file
size.

**Fix:** `tools/writefile_renderer.go` `resolveContent` now prefers the
retained (complete) tool arguments as the authoritative full content once the
call completes successfully; the tool result wins on error so the
"(interrupted)"/error sentinel is shown, and remains the fallback when args
are unavailable (e.g. restored sessions). `internal/app` also normalizes
`OutputEvent.ToolResult` → `Text` in `handleToolResult` so the renderer sees
consistent content.

**Tests:** `TestWriteFileRenderer_CompletedWriteShowsTotalLines`,
`TestBugs_WriteToolStatsShowsTotal`.

## Sessions restore listing — FIXED

The session picker (`/session` list) showed conversation-less sessions
(sessions containing only system/stats/progress events, or abandoned before
the first reply) which restored to a blank transcript, and listed sessions in
an unstable order.

**Fix:** `core/sessionstore.go` `ListSessions` filters sessions with no
user/assistant conversation content (`scanSessionFile` reports
`hasConversation`) and sorts newest-first with a name tiebreak for a stable
order when several sessions share a mod-time (names embed a timestamp, so the
tiebreak stays chronological). Empty session files are kept on disk, just
hidden from pickers/listings.

**Tests:** `TestSessionListSessions_FiltersEmptySessions`,
`TestSessionListSessions_NewestFirst`.

---

(historical items from 2026-07-16 and earlier — see `docs/archive/bugs.2026-07-16.md`)
