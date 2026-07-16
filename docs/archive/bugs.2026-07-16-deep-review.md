<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Archive — 2026-07-16 (Deep Review)

Archived after a deep review of `bugs.md` against the guideline. All three open
items were reproduced as event sequences, localized, and validated end-to-end
with filmstrip tests; the previously-"closed" items were also moved here. One
new residual defect was found during validation and fixed.

## How each open item was handled

Every open bug was driven through the **full production app event handler**
(`App.handleAgentOutputEvent`) via the `uiScenario` filmstrip harness, asserting
on structured, ANSI-free UI state — never on escape bytes and never against a
live model (per the `tui-test` skill). The validation tests live in
`internal/app/bugs_md_streaming_validation_test.go`.

---

## Open — edit/write do not show streaming

**Original:** Long write/edit are not shown streaming; only final results are
shown. They should stream progress (write: head of the file being written +
line stats; edit: a live diffstat of lines deleted/inserted).

**Status:** Already fixed by prior commits, validated end-to-end this session.

**Reproduction / localization trace:**
- `provider.EventToolCallDelta` accumulates args and emits `EventToolCall`
  with `IsDelta=true`, `ToolInput` = the *accumulated* args so far
  (`internal/agentic/agent_streaming.go:handleToolCallPartial` /
  `handleToolCallDeltaByIndex`).
- `internal/tooltracker/tracker.go:onCallDelta` routes each delta to the
  canonical widget via `tc.SetArgsPartial(ev.ToolInput)`.
- `tui/tool_execution.go:updatePartialArgs` parses incomplete JSON with
  `partialStringFieldRe` (no value terminator), so the *currently-streaming*
  field (unterminated) is captured into `tc.args`.
- `WriteFileRenderer`/`EditFileRenderer` implement `tuirender.StreamingRenderer`;
  `NewToolExecution` initialises `isPartial=true`, so `buildBody` invokes
  `RenderPartial` on every delta.

**Validation tests (new, `internal/app/bugs_md_streaming_validation_test.go`):**
- `TestBugs_WriteStreamingShowsContentLive` — replays 3 growing write deltas
  (unterminated `content`) and asserts each increment is visible live, plus the
  truncation hint once content exceeds the preview window.
- `TestBugs_EditStreamingShowsDiffstatLive` — replays `old_string` then
  `new_string` deltas and asserts the live `-X lines` / `+Y lines` diffstat
  appears and updates before the result.

**Result:** both PASS. Streaming content reaches the screen live for write and
edit.

**Relevant prior commits:** `6771fb9` (generic streaming preview),
`cd7031b` (ground-up tool streaming redesign), `42c670f` (edit streaming +
read silent config).

---

## Open — disconnection/stop of work, no error/notification

**Original:** Multiple disconnects/stop of work requiring a forced new message
to restart, with no error/notification.

**Status:** Already fixed by prior commits, validated end-to-end this session.

**Localization trace:**
- `core/agentmanager.go:executeRunner` emits `EventEnd` carrying either
  `Text=<err>` (connection/loop errors) or `Metadata["cancelled"]="true"`
  (Escape/Ctrl+C). `recoverTurnPanic` likewise surfaces panics as `EventEnd`
  with `Text`.
- `internal/app/stats.go:handleSessionEnd` shows `friendlyConnectionHint(Text)`
  for errors and `"Generation stopped by user."` for cancellations.
- `internal/agentic/agent_streaming.go` adds an event-level stall watchdog
  (`646b8d5`) that converts "provider alive but no real events" into a
  retryable error (surfaced), closing the keep-alive-only silent-hang gap.

**Validation test (new):**
- `TestBugs_DisconnectSurfacesNotification` — asserts a cancelled stream shows
  a visible "stopped by user" notification, and a `connection refused` error
  shows a visible friendly hint. Never a silent stop.

**Result:** PASS.

**Relevant prior commits:** `4c895f6` (escape debounce + stream ctx check →
spurious `context.Canceled` is no longer misclassified; cancellations surface
correctly), `646b8d5` (event-level stall watchdog).

**Note:** a truly silent stop would require a provider to close the connection
*gracefully* (nil error) with no content and no tool calls; that path now ends
through `finishStreamTurn` with an empty `EventEnd`, indistinguishable from a
normal empty turn. No reproducible evidence of this residual was found; the
known silent-stop causes (spurious cancel, no-event stall) are covered.

---

## Open — Write stuck; ctrl-c/esc not working

**Original:** The last write/edit operation gets stuck (elapsed timer running)
while the agent is still producing output; Ctrl-C/Esc did not work, making it
impossible to exit.

**Status:** Already fixed by prior commits, validated end-to-end this session.
One residual found and fixed (see next section).

**Localization trace:**
- The "stuck widget" was orphaned widgets: a provider streams a tool call with
  an *empty* `ToolCallID`, then ships the completed call/result with the real
  id; the old reconciliation created a second widget and left the first in
  `Pending` forever (elapsed timer → ∞). `internal/tooltracker` is the sole
  widget creator and guarantees one widget per logical call via late-id
  adoption (`adoptStreamingNoID`).
- The "can't exit" half was spurious/lost Escape key presses from split CSI
  sequences. `tui/terminal.go` debounces a bare `0x1b` (`escapeDebounceTimeout`
  + fallback `time.AfterFunc`) so Escape is reliably registered.
- `Ctrl+C` clears the editor or exits (`tui/tui.go:handleCtrlC`); Escape
  interrupts the agent (`handleEscape`).
- `failPendingTools` (stats.go) is the safety net at `EventEnd`: any widget
  still `Pending`/`Running` is marked interrupted (✗), so no widget can spin
  forever.

**Validation tests (new):**
- `TestBugs_NoStuckWriteWidget_LateIDAdoption` — streams a write with empty id,
  then delivers the completed call + result with a real id; asserts exactly
  one widget exists and it resolves to success (no orphan).
- `TestBugs_NoStuckWriteWidget_StrandedMarkedInterrupted` — a write that never
  receives a result is marked interrupted (✗) at `EventEnd`, not stranded.

**Result:** both PASS (after the residual fix below).

**Relevant prior commits:** `cd7031b` (tooltracker / orphan fix),
`4c895f6` (escape debounce).

---

## New defect found during validation — interrupted write widget showed empty body

**Found while validating "Write stuck".** When a write is interrupted mid-run,
`failPendingTools` sets the widget output to `"(interrupted)"` and status to
`ToolError`. The `✗` icon rendered correctly, but the body was **empty**: the
user saw a failed write with no explanation. The edit renderer did not have
this problem (it falls back to raw output when no diff hunk is present).

**Root cause:** `WriteFileRenderer.RenderResult` returned `""` whenever
`extractWriteContent` found no fenced code block. Non-fenced output (the
`(interrupted)` sentinel, error strings) was silently dropped.

**Fix (`tools/writefile_renderer.go`):** when no fenced content is present,
surface the raw output verbatim (so the body is never silently empty for a
terminal/error result); empty output (mid-stream, pre-result) still renders
empty.

**Tests:**
- `tools/writefile_renderer_test.go:TestWriteFileRenderer_RenderResult_NonFencedOutputShown`
  — asserts `(interrupted)` and `Error: ...` outputs are shown verbatim, and
  empty output stays empty.
- `internal/app/bugs_md_streaming_validation_test.go:TestBugs_NoStuckWriteWidget_StrandedMarkedInterrupted`
  — end-to-end assertion that the interrupted widget body contains
  "(interrupted)".

---

## Previously-closed items (moved out of bugs.md)

### Spontaneous generation cancellation (`context.Canceled` with no user action)

Fixed by `4c895f6`: Escape debounce in `ProcessTerminal.readLoop` + restored
`ctx.Err()` check in `consumeStream` (reads via `stream.SeqCtx`).

### Scroll/history issue with tool call (double-separator artefacts)

Root cause identified (editor borders pushed into scrollback as the chat
viewport grows). Fix deferred — requires editor rendering refactor to an
overlay model. *Carried forward as a known item, not regressed this session.*

### Edit tool streaming progress → fixed by `42c670f`

`EditFileRenderer.RenderPartial` shows a compact diffstat. Confirmed live by
`TestBugs_EditStreamingShowsDiffstatLive`.

### Read tool silent by default → fixed by `42c670f`

New config `tui.tools.show_read` (default `false`); read widgets stay collapsed
even in "full" view unless toggled.

---

## Validation gate (guideline #6, each run separately)

- `go vet ./...` — clean.
- `staticcheck ./...` — only pre-existing, unrelated warnings
  (`applyToolResultToWidget`, `parseTwoArgs`, test helper `ptr`); none in
  changed files.
- `gocognit -over 15 .` / `gocyclo -over 12 .` — only pre-existing violations
  (`shortcuts.go`, `core/commands/model.go`, `tui/selector.go`, `tui.go`);
  `writefile_renderer.go` is 3/3 (well under budget).
- `go test -count=1 -race ./...` — all packages pass.
