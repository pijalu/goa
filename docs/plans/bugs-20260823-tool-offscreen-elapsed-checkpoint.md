# CHECKPOINT — Bug 2: off-screen running tool widget freezes elapsed (HANDOVER)

Date: 2026-08-23 · Branch: `feature/ratatui` · Status: **code complete +
unit/integration-validated; ALL quality gates green; PTY visual validation
PASS; archived and ready to commit**

Completion evidence: fixed the mock filler vocabulary, reran the real PTY
filmstrip, archived bug §2, and verified all gates. Commit the implementation
and archive as one changeset. Do NOT redesign — the implementation is tested
and in-budget.

## 1. Objective (bugs.md §2)

A live tool block that moves fully off-screen must keep its status current
(either row updates reach scrollback or the running widget is pinned visible
until completion); the final row must always end with the true final
duration, never a stale intermediate `elapsed`.

## 2. Design as implemented (both halves of the "either/or")

Terminals cannot rewrite committed scrollback rows, and a per-tick full
scrollback reset is the CPU storm the compositor explicitly rejects
(`scrollOffUnstable` treats the in-place tick as benign). Therefore:

**A. Pinned live strip (running-status currency).** `ToolLiveStrip` — a 0/1-row
pinned chrome component (same pattern as goal bubble / agent tabs) directly
under the transcript, wired in `internal/app/tui.go::assembleEngine`. While
the OLDEST running tool is fully committed to scrollback
(`ChatViewport.OffscreenRunningTool()`), it renders that widget's
`LiveStatusLine()` — header identity + elapsed RECOMPUTED AT CALL TIME +
progress stats — refreshed by the render-loop live ticker that already runs
while tools execute (B002). Zero rows otherwise; empties at completion.

**B. Boundary scrollback resyncs (final truth).** One-time full scrollback
wipe+re-emit (`scrollbackDirty` → `drawWindowResetScrollback`) requested at
exactly two boundaries via `Compositor.RequestScrollbackResync()`:
1. a still-running widget changes while fully off-screen (its update could
   never be painted there) — once per episode (`sync.Map` guard),
2. completion while off-screen (guard re-armed by the non-terminal→terminal
   transition) so scrollback's final row for the block reads the true
   `Took Xs`, never a stale `elapsed`.

Mid-run staleness of the HISTORICAL rows between the boundaries is inherent
(scrollback is immutable); the live status itself stays current via the
strip, and the final row is always rewritten with the truth. The app's
completion echo (`echoScrolledOffToolResult`) is kept — it attributes the
completion in the live tail.

## 3. Files (ALL UNCOMMITTED in working tree)

| File | Change |
|---|---|
| `tui/compositor.go` | NEW `RequestScrollbackResync()` (sets `scrollbackDirty` under lock) |
| `tui/chat_viewport_offscreen.go` | NEW — `SetScrollbackResyncRequest(fn)`, `maybeResyncOffscreenTool(tc)` (once-per-episode guard), `rearmOffscreenResync(tc)` |
| `tui/chat_viewport.go` | fields `scrollbackResync func()` + `offscreenResynced sync.Map`; import `sync` |
| `tui/chat_viewport_messages.go` | widget hooks: `onInvalidate` calls `maybeResyncOffscreenTool`; `onStatusChange` re-arms + requests at terminal transition (B002 counter logic preserved) |
| `tui/tool_live_strip.go` | NEW — `ToolLiveStrip` + `LiveStatusLine()` on the widget + `OffscreenRunningTool()` on the viewport |
| `tui/tui.go` | `TUI.Start` wires `cv.SetScrollbackResyncRequest(t.compositor.RequestScrollbackResync)` before loops run |
| `internal/app/tui.go` | `assembleEngine` adds `tui.NewToolLiveStrip(chat)` right under the chat viewport |
| `tui/tool_offscreen_resync_test.go` | NEW tests (see §4) |
| `tui/tool_live_strip_test.go` | NEW tests (see §4) |
| `docs/plans/bugs-20260823-tool-offscreen-elapsed.md` | full fix plan (updated with testing learnings) |

## 4. Tests added (all passing)

`tui/tool_offscreen_resync_test.go`:
- `TestCompositor_RequestScrollbackResync` — request → NEXT frame wipes
  scrollback exactly once (`\x1b[3J` count 1), follow-up frames none.
- `TestChatViewport_OffscreenResyncBoundaries` — running update while
  off-screen → 1 resync (not per-output: storm guard); completion → +1;
  terminal→terminal → 0 more; pure scroll-off (no widget change) → 0.
- `TestChatViewport_OnscreenToolNeverResyncs` — negative control.
- `TestOffscreenToolCompletionRewritesScrollback` — E2E via fakeTerminal +
  screenEmulator: after off-screen completion, scrollback+screen contain
  `Took 42.0s` (true final) and the tool identity.

`tui/tool_live_strip_test.go`:
- `TestToolLiveStrip_RendersOnlyForOffscreenRunningTool` — 0 rows on-screen;
  1 row (identity + elapsed) off-screen; clock-advance changes the line
  (proves per-call-time recompute); empties after completion.
- `TestToolLiveStrip_VisibleInChrome` — composed screen VISIBLE band contains
  `elapsed 9.` + `sleep 900` while the widget is off-screen.

## 5. Validation already done (rerunnable)

- `go test ./tui/ ./internal/app/ -count=1` — ok
- `go vet ./...` — clean
- `staticcheck ./...` — only the pre-existing unrelated SA1019
  (`core/commands/model_test.go:198`, verified pre-existing via stash)
- `gocognit -over 15 .` / `gocyclo -over 12 .` — no findings in changed
  files (pre-existing unrelated: `e2e/perfdrive/main.go`, one 13-cyclo in
  `goal_list_stream_perf_test.go`, `model_test` complexity — untouched)
- `go test -count=1 -race -cover ./...` — EXIT 0 (all packages)
## 6. REMAINING WORK for the finishing agent

**6a. PTY harness (root cause found and fixed).** The first PTY run FAILED with
`STRIP_LINES=0 TOOK_LINES=0` because the stub's filler repeated a cycling phrase
and goa's stream loop detector correctly aborted the stream before tool calls.
The filler was changed to lexically unique vocabulary, the binary rebuilt, and
the real filmstrip rerun successfully:

`STRIP_LINES=1`, `TOOK_LINES=1`, `Took 12.0s`, `VALIDATION PASS`.

**6b. Archive and tracking: DONE.** Created
`docs/archive/bugs-20260823-tool-offscreen-elapsed.md` and removed §2 from
`bugs.md`.

**6c. Commit** (single commit, all files in §3 + archive + bugs.md +
plan/checkpoint): suggested message —
`fix(tui): off-screen running tool keeps live status; final duration rewrites scrollback`
(body: pinned ToolLiveStrip + one-time boundary scrollback resyncs; tests;
PTY evidence; bug archived).

## 7. Gotchas learned (do not re-trip)

- The scrollback watermark publishes AFTER the frame that scrolls content
  off → the strip/resync activate on the FOLLOWING frame (production's live
  ticker supplies it; tests call `engine.RenderNow()` twice).
- `SetStatus(ToolRunning)` RESETS `startTime` (execution-clock semantics) —
  backdate `startTime` AFTER setting Running in fixtures.
- A widget-growth frame takes the DEFERRED path (`deferScrollbackSync`);
  the actual full reset lands on the next SETTLED frame (MutationGen
  unchanged) — tests render once more after the completion frame.
- PTY size matters: use ≤30 rows so the tool stack actually scrolls off.
- bugs.md §3/§4 remain queued (retry fibonacci/config, render CPU batching).