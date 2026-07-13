<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Tracking Archive — 2026-07-13 (TUI stability + tooling review)

All items below were resolved on 2026-07-13. Each entry states the observed
failure, the localized root cause, the fix, and the test approach + validation
steps used to close it.

Observed failures were reproduced before editing (workflow step 1), localized
to the smallest region (step 3), and verified against the original failing
command/behavior (step 6). Code-quality gates (guideline #6) were run
separately and introduced no new violations.

---

## 1. Duplicate time on tool calls

**Observed:** bash tool calls rendered two `Took 1.2s` lines.
```
✓ $ go test ./tools/ -run "Search" ...
ok     github.com/pijalu/goa/tools     0.507s
Took 1.2s
Took 1.2s
```

**Root cause:** Two independent timing renderers. `tools/bash_renderer.go`
parsed the `Duration:` footer from the tool output and rendered its own
`Took`/`elapsed` line in the body, *and* the generic
`ToolExecutionComponent` (`tui/tool_execution.go`, `renderDuration`) rendered
a second `Took` line from wall-clock elapsed time.

**Fix:** Made the generic widget duration line the single source of truth.
`BashRenderer.RenderResult` no longer emits a timing line; it still strips the
`Duration:` footer from the body (so it never leaks as raw text). Removed the
now-unused `formatBashDuration` and `parseBashDurationLine` value parsing.

**Tests/validation:**
- `tools/bash_renderer_test.go` — updated to assert the renderer emits NO
  `Took`/`elapsed` and strips the `Duration:` footer.
- `tui/tool_execution_test.go::TestToolExecution_BashRenderer_NoDuplicateTook`
  — asserts the fully rendered bash widget contains exactly one `Took` line.
- Reproduction (built ad-hoc) confirmed exactly one `Took` after the fix.

---

## 2. Search tool not used (agent prefers bash+grep)

**Observed:** the agent reached for `bash`+grep instead of the dedicated
`search` tool for codebase search.

**Root cause:** `SearchTool.Schema().Description` was a terse
`"Search files for a pattern."` that did not signal it should be preferred
over grep, and `BashTool`'s description did not steer away from grep for
searching.

**Fix:**
- `tools/search.go` — rewrote the schema description to state it is the
  *preferred* code/text search tool (parallel, auto-excludes noise dirs,
  structured ranked output), referencing grep/rg so a model wiring bash+grep
  maps that intent here; enriched the per-parameter descriptions and docs
  (`tools/search.short.md`, `tools/search.long.md`).
- `tools/bash.go` + `tools/bash.long.md` — added a "Tool selection" note
  telling the agent to prefer `search` for codebase search and reserve bash
  for features search cannot express.

**Tests/validation:** `tools/search_test.go` adds
`TestSearchTool_Schema_SteersAwayFromGrep` and
`TestBashTool_Schema_SteersToSearchForCodebaseSearch` locking in the guidance
keywords. `go build ./...` clean.

---

## 3. Screen redraw flashes the mascot/logo

**Observed:** "sometimes a redraw of the screen will suddenly show the
mascot/logo … even if such content should be way off-screen."

**Root cause:** `Compositor.applyFrameTracking` reset `firstScrollDone =
len(canvas) > height` on every full/resize frame. After the chat shrank to fit
the screen (e.g. a thinking block collapsed), the flag went false; the next
regrow re-entered `emitFirstScroll`, which re-writes the *whole* canvas from
row 0 — flashing the off-screen header mascot back onto the visible screen and
duplicating scrollback content. Reproduced at the compositor level
(`fakeTerminal` + `screenEmulator`): the regrow frame emitted the mascot.

**Fix:** Made `firstScrollDone` *sticky* in `applyFrameTracking` — once a
session has scrolled (scrollback populated), it never resets, so
`emitFirstScroll` can never re-fire and re-emit off-screen content.

**Tests/validation:**
`tui/compositor_mascot_redraw_test.go::TestCompositor_RegrowAfterShrinkDoesNotReEmitOffScreenContent`
— fails before the fix (regrow re-emits `MASCOT`), passes after.

---

## 4. Scroll-back loses content after long edit/write

**Observed:** long edit/write operations left terminal scrollback with missing
content; old content not retained.

**Root cause (two parts):**

(a) **Test-helper bug** — `screenEmulator` (`tui/streaming_scroll_test.go`)
did not flush pending text *before* a cursor-moving/clearing CSI, so text
written immediately before a CUP landed on the destination row, producing a
false "multiple lines stacked on one row" signal. Fixed `handleCSI` to flush
before `H/f/A/B/G/J/K`. (This is what made the symptom look worse than it was
and masked the real defect.)

(b) **`emitLargeScroll` was fundamentally broken.** The path taken when a
single frame grows the canvas by more than the viewport height wrote each gap
line at the bottom row then a trailing `\n`. A trailing `\n` at the bottom
row scrolls the *top* row (not the just-written line) into scrollback, so gap
lines stacked on screen and were then overwritten by the new viewport —
losing every gap line. Reproduced at the compositor level: a 25-line/frame
growth lost 162/201 lines.

**Fix:** Rewrote `emitLargeScroll` to scroll *then* write (`\n` first, which
pushes the top into scrollback and opens a blank bottom row; then fill it),
iterating over the newly-added region through the new viewport. This makes
scrollback receive the real content.

**Note on residual scope:** the realistic tool-arg streaming case (small
per-frame growth → bare-newline path) is fully correct
(`TestCompositor_StreamingWriteWidgetPreservesScrollback`); pure large appends
are fully correct (`TestCompositor_LargeAppendPreservesAllScrollback`). For
in-place widget growth that exceeds the viewport in one frame (a whole file
arriving in a single delta — rare), the visible screen stays correct and only
some mid-canvas history is not recoverable in scrollback; this is documented
in the `emitViewportScroll` comment.

**Tests/validation:**
- `tui/compositor_scrollback_growth_test.go` —
  `TestCompositor_LargeAppendPreservesAllScrollback` (0 loss) and
  `TestCompositor_StreamingWriteWidgetPreservesScrollback` (baseline + streamed
  lines retained, latest line visible).

---

## 5. CI/CD flaky: TestBGExec_ReadOutput_LongLinePreserved

**Observed:** intermittent `200KiB line was not preserved (len out=192693)`.

**Root cause:** `background.Manager.waitForExit` called `cmd.Wait()` *before*
the pipe-draining goroutines finished. Per `exec.Cmd` docs, `Wait` closes the
stdout/stderr pipes, so reading after it can return a partial result and drop
whatever was still buffered in the kernel pipe — the truncation. Reproduced
reliably with a new stress test (`TestManager_PreservesLargeOutputOnExit`,
~2.5 MiB across 40 lines) that failed 3+/20 runs before the fix and 0/50 after.

**Fix:** Drained both pipes fully (`proc.wg.Wait()`) *before* `cmd.Wait()` in
`waitForExit`, so all buffered output is captured before Wait closes the
pipes.

**Tests/validation:**
- `internal/background/manager_test.go::TestManager_PreservesLargeOutputOnExit`
  — catches the race without the fix, passes with it (verified by reverting).
- Original `tools/w2_fixes_test.go::TestBGExec_ReadOutput_LongLinePreserved`
  passes 50× under `-race`.

---

## 6. Tool streaming ("nothing happens" syndrome)

**Observed:** slow/long tool operations showed no output until completion.

**Root cause:** tool execution output was only delivered on the final
`EventToolResult`; nothing streamed during execution. (Tool *call-argument*
streaming was already implemented end-to-end; this was execution *output*.)

**Fix (Open/Closed via context — no Tool-interface change):**
- `internal/agentic/progress.go` — `ProgressFunc` + `WithProgress` /
  `ProgressFromContext` context helpers.
- `internal/agentic/observer.go` — new transient `EventToolProgress` event
  type.
- `internal/agentic/agent_tools.go` — `executeToolWithResult` injects a
  progress emitter into the context that emits `EventToolProgress`.
- `tools/bash.go` — `progressWriter` wraps stdout, debouncing snapshots
  (`bashProgressInterval`) and flushing on completion; the final output buffer
  is unchanged.
- `internal/app/stats.go` — `handleToolProgress` renders partial output into
  the running widget (`SetOutput` + `SetPartial(true)`) without completing it.
- `tui/tool_execution.go` — added the `IsPartial()` getter.

**Tests/validation:**
- `tools/bash_progress_test.go` —
  `TestBashTool_StreamsProgressDuringExecution` (mid-run snapshot contains the
  first line) and `TestProgressWriter_DebouncesAndFlushes` (debounce + final
  flush + buffer integrity).
- `internal/app/tool_progress_test.go::TestToolProgress_ShowsPartialOutputWhileRunning`
  — filmstrip: `EventToolProgress` shows partial output while the widget stays
  Running/partial; `EventToolResult` then resolves it.

---

## Verification gates (all green, no new violations)

- `go vet ./...` — clean.
- `staticcheck ./...` — only the pre-existing `tui/editor_render.go:646
  bytePosForCol unused` warning (untouched file).
- `gocyclo -over 12 .` — clean.
- `gocognit -over 15 .` — only pre-existing `core/docengine.go` entries
  (untouched file).
- `go test -count=1 -race -timeout 300s ./...` — all packages pass.
- Built `cmd/goa` and smoke-tested the live TUI render (mascot/header/footer
  stable, splash + connect + input render correctly).
