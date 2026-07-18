<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Tracking

## Guideline

1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

## Open Bugs

### B001 — Silent recovery exhaustion: tool-calling loop ends turn with no user feedback

**Severity**: HIGH
**Found**: 2026-07-18 (export `goa-export-20260718-084642`)
**Session**: `1784354684_cq1qycvv`
**Status**: FIXED in root cause (B003 below) + notification emission

**Symptoms**:
- Agent is stuck in a tool-calling loop (≥50 rounds in one turn).
- Recovery system sends up to 3 recovery hints asking the model to respond with text.
- Model keeps calling tools despite recovery hints.
- Recovery exhausts: `recovery stream exhausted all 3 rounds; ending turn`.
- **No user-visible error or message** — the UI goes silent and the user sees only "stopped working."

**Root cause** (two independent defects):

1. **B003 — `hasStalled()` always returns true** (below). The stall detector fires at every round 49 because `bufferedToolCalls` is always empty after `executeBufferedToolCalls` clears it. This means recovery ALWAYS triggers at round 50, even when the model is legitimately making diverse, non-repeated tool calls (read/search/bash with different args). **This was the primary reason the user's session in `goa-export-20260718-092152` was interrupted.** The model was making research progress with unique tools — not stalled at all.

2. **Silent exhaustion**: Even when recovery is warranted (genuine loop), `runRecoveryStream` returns `nil` instead of emitting a user-visible notification, so the user sees a silent stop.

**Fixes applied** (2026-07-18):
1. `internal/agentic/agent_streaming.go:189-192`: `hasStalled()` returns `!turnHadToolExecution` instead of `true` when `bufferedToolCalls` is empty. This prevents false-positive stall detection for models making progress.
2. `internal/agentic/agent_streaming.go:85-89`: Added hard cap of 250 total rounds to prevent runaway when a model calls 1 tool per round indefinitely.
3. `internal/agentic/agent_streaming.go:179-188`: `runRecoveryStream()` emits `[goa-system]` notification before returning nil on exhaustion.

**Test approach**:
- `TestAgent_ExecutesTool_Stream` validates the stall detection + hard cap (passes with 250-round cap).
- Add a test that simulates recovery exhaustion and verifies system notification emission.

**Validation steps**:
1. `go vet ./...` — must pass.
2. `go test -count=1 -race -cover ./...` — all must pass.
3. `gocognit -over 15 .` — no new violations.
4. `gocyclo -over 12 .` — no new violations.
5. `staticcheck ./...` — no new issues.

### B002 — TUI FPS inconsistent: elapsed time jumps irregularly during streaming

**Severity**: MEDIUM
**Found**: 2026-07-18 (investigation of streaming refresh rate)

**Symptoms**:
- Time-elapsed display in tool widgets jumps irregularly: e.g. `"0.1s"` → `"0.7s"` → `"2.1s"` (gaps of 0.6s and 1.4s).
- FPS is inconsistent: high during burst events, low (or zero) between events.
- Most noticeable during streaming when the user expects smooth progress updates.

**Root cause**:
The TUI rendering is purely **event-driven** — the `renderLoop` (`tui/tui.go:425`) only fires when `RequestRender()` is called. There is no periodic frame clock. The elapsed time in running tool widgets (`ToolExecutionComponent.Render`, `tui/tool_execution.go:754`) updates only when `Render()` is called, which depends entirely on external triggers:

1. **Spinner animation** (`tui/status.go:190`): The ONLY periodic render trigger. The spinner ticks at its configured interval (default ~100ms for "arc") and calls `tickFrame()` → `onFrameChange()` → `chat.InvalidateRunningToolWidgets()` → `patchRunningToolWidgets()` which calls `tc.updateBox()` to recompute the elapsed time.

2. **Streaming events**: Content deltas, tool progress, and state changes trigger `RequestRender()` via `applyCommand` (`tui/tui.go:417`). These are bursty — fast when chunks arrive, silent between chunks.

3. **Status clear/reset breaks the periodic trigger**: `statusMsg.Clear()` (`internal/app/stats.go:156`) is called on progress-clear events and between tool transitions. This **stops the spinner ticker**. The next `Show()` restarts it, but with a gap. During this gap, no periodic render trigger exists, so the elapsed time stalls.

4. **ChatViewport frame cache** (`tui/chat_viewport.go:267`): The viewport caches its rendered output. Between data updates, if the spinner hasn't ticked, the cache is returned and `ToolExecutionComponent.Render()` is never called — meaning the elapsed time is frozen until the next event.

The root architecture: **no independent periodic ticker for elapsed time or render loop** — everything cascades from the spinner animation.

**Fix plan**:
1. **Primary fix — Add a render ticker to the render loop**: Instead of blocking idle on `dirtyChan`, make the render loop tick at a configurable base rate (e.g. 10-15fps) when at least one running/pending tool widget exists. This gives a guaranteed minimum FPS for elapsed-time updates.
   - Alternative: Add a per-tool-widget elapsed-time timer that fires `Invalidate()` every ~100ms while `status == Running`, independent of the spinner.

2. **Secondary fix — Patch elapsed time on every render, not just on change**: Remove the `if tc.box.duration != elapsed { tc.updateBox() }` guard in `ToolExecutionComponent.Render()` so the box is always rebuilt with the current elapsed time. The difference from caching is negligible (a string comparison and one string alloc per frame).

3. **Tertiary fix — Decouple elapsed timer from spinner**: The spinner should not be the sole periodic render trigger. Consider adding a dedicated `liveRenderTicker` that the render loop checks periodically (e.g. every 50-100ms) regardless of events, to keep the display fresh during active turns.

**Test approach**:
- Write a test that drives the TUI with a running tool widget and verifies the elapsed time text updates within 200ms of each second boundary, without relying on streaming events to trigger renders.
- Use `fakeTerminal` to capture the compositor output at regular intervals and assert the elapsed string changes appropriately.
- Test with spinner disabled (`SetSpinner(Definition{})`) to verify elapsed time still updates independently.

**Affected files**:
- `tui/tui.go:425-448` — `renderLoop` (no periodic tick, purely event-driven)
- `tui/tool_execution.go:754-766` — `ToolExecutionComponent.Render` (elapsed time depends on render being called)
- `tui/chat_viewport.go:267-268` — frame cache shortcut (skips per-frame elapsed time update)
- `tui/chat_viewport.go:770-791` — `InvalidateRunningToolWidgets`/`patchRunningToolWidgets` (the only elapsed-time refresh path between events)
- `tui/status.go:100,190-218` — spinner `tickFrame` + `onFrameChange` (sole periodic render trigger)
- `internal/app/stats.go:156` — `statusMsg.Clear()` (breaks the periodic trigger chain)

**Validation steps**:
1. `go vet ./...` — must pass.
2. `go test -count=1 -race -cover ./...` — all must pass.
3. `gocognit -over 15 .` — no new violations.
4. `gocyclo -over 12 .` — no new violations.
5. `staticcheck ./...` — no new issues.

### B003 — `hasStalled()` always returns true (false-positive stall detection)

**Severity**: HIGH (root cause of B001)
**Found**: 2026-07-18 (export `goa-export-20260718-092152`)
**Session**: `1784358808_g59fm1bg`
**Status**: FIXED

**Symptoms**:
- Recovery ALWAYS triggers at round 49-50, even when the model is making diverse, non-repeated tool calls.
- Model doing legitimate multi-tool research (read different files, search different patterns, run different bash commands) gets interrupted.
- The per-turn round limit becomes a hard 50-round cap for any multi-step reasoning, defeating the purpose of the "extend horizon if making progress" fallback.

**Root cause**:
`internal/agentic/agent_streaming.go:189-192`:
```go
if len(a.bufferedToolCalls) == 0 {
    return true  // ← Always true after any round with tool executions
}
```
After `consumeStream` → `completeStreamTurn` → `executeBufferedToolCalls`, `bufferedToolCalls` is drained (set to nil). So `len()` is always 0 when `hasStalled()` runs, causing it to return `true` unconditionally. The `turnHadToolExecution` flag (which signals real tool execution) was ignored.

**Fix applied** (2026-07-18):
- `internal/agentic/agent_streaming.go:189-192`: Changed to `return !a.turnHadToolExecution` so the stall detector returns `false` when real tools were executed.
- Added hard cap of 250 rounds as a safety ceiling.

**Test approach**:
- `TestAgent_ExecutesTool_Stream` validates the 250-round cap.

**Validation steps**:
1. `go vet ./...` — must pass.
2. `go test -count=1 -race -cover ./...` — all must pass.
3. `gocognit -over 15 .` — no new violations.
4. `gocyclo -over 12 .` — no new violations.
5. `staticcheck ./...` — no new issues.

### B004 — Write tool duration line never shows byte/line progress during streaming

**Severity**: LOW (visual/UI polish)
**Found**: 2026-07-18 (review)
**Status**: Open — fix pending

**Symptoms**:
- During write tool streaming, the duration line shows only `"elapsed X.XXs"` without the `· Y KB · Z lines` suffix that bash/terminal tools show.
- After completion, the duration line shows `"Took X.XXs"` — still no byte/line statistics.
- The body-level stats (`appendWriteStats` in the widget body) are correct, showing `"writing N lines"` / `"N lines"`.

**Root cause**:
`tui/tool_execution.go:349-353`:
```go
func (tc *ToolExecutionComponent) progressSuffix() string {
    if tc.outputBytes == 0 { return "" }
    return " · " + formatByteSize(tc.outputBytes) + " · " + formatLineCount(tc.outputLines)
}
```
`outputBytes`/`outputLines` are only updated by `SetOutput()`, which is called for `EventToolResult` (completion) and `EventToolProgress` (ongoing output streaming). Write tools stream content through `EventToolCall` deltas → `SetArgsPartial`, which never touches `outputBytes/outputLines`. The progress suffix only shows for bash/terminal tools that emit `EventToolProgress`.

**Fix plan**:
1. **Primary fix**: In `SetArgsPartial`, when `tc.args["content"]` is present, update `outputBytes`/`outputLines` from the partial content's length. This makes the duration line show `elapsed X.XXs · Y KB · Z lines` during write streaming.
2. **Secondary fix** (lower priority): Update `outputBytes`/`outputLines` also from `tc.args["content"]` in the write renderer, so even the final "Took" line reflects file size.

**Affected files**:
- `tui/tool_execution.go:349-353` — `progressSuffix()` (returns `""` when `outputBytes == 0`)
- `tui/tool_execution.go:422-431` — `SetArgsPartial()` (needs to update `outputBytes/outputLines` from `tc.args["content"]`)

**Test approach**:
- Write a test that streams write args and checks that the duration line includes `· X bytes · Y lines` during streaming.
- Verify that `TestWriteFileRenderer_WriteStatsDuringPreparation` still passes (body stats unchanged).

**Validation steps**:
1. `go vet ./...` — must pass.
2. `go test -count=1 -race -cover ./...` — add new tests; all must pass.
3. `gocognit -over 15 .` — no new violations.
4. `gocyclo -over 12 .` — no new violations.
5. `staticcheck ./...` — no new issues.

### B005 — CPU usage during markdown streaming: repeated re-parse + highlight per delta

**Severity**: LOW (visual/UI polish — not user-visible latency but measurable CPU cost)
**Found**: 2026-07-18 (review during write stats investigation)
**Status**: Open — investigation needed before fix

**Symptoms**:
- During LLM text streaming (large markdown responses), CPU usage is elevated.
- Every content delta triggers a full or partial re-render of the markdown document.
- With the `IncrementalMDRenderer`, the **stable prefix is cached**, but the **unstable tail is re-rendered on every frame** — which includes re-splitting, re-parsing, and re-highlighting the last open markdown block.
- For responses with large code blocks, long paragraphs, or rapid small deltas (single-token-at-a-time providers), this re-render overhead adds up across hundreds of frames.

**Rendering pipeline per delta** (for the streaming text block):

1. `MDStreamRenderer.Render()` (`tui/markdown.go:39`) — splits full text into lines, then iterates through every line to classify and collect blocks (headings, fences, lists, paragraphs, tables). For the unstable tail, this runs every frame.

2. `IncrementalMDRenderer.Render()` (`tui/markdown_incremental.go:45`) — computes `lastStableBoundary()` which scans the full text to find a safe split point. O(n) per frame for the full document, even when only a few bytes changed.

3. `renderInline()` (`tui/markdown_inline.go`) — called per paragraph/heading/blockquote line. Scans character-by-character for bold `**`, italic `*`, code `` ` ``, links `[text](url)`, images, and entity replacements (`$pi$` → `π`). Each run does string concatenation via `strings.Builder` and ANSI escape wrapping.

4. `highlightLine()` (`tui/markdown_highlight.go`) — for fenced code blocks, applies tokenizer-based syntax highlighting per line. Tokenizers (Go, Python, Bash) scan character-by-character and emit ANSI escape sequences. For a 500-line code block arriving at 1 token per delta, each delta re-highlights the entire open fence tail.

5. `ansi.Wrap()` (`internal/ansi/wrap.go`) — word-wraps rendered text to terminal width, measuring each line's display width via uniseg grapheme analysis. Called for every paragraph, heading, and quoted line.

6. `ansi.Width()` — used throughout: wraps, truncation, padding calculations. Each call measures grapheme clusters, which is a non-trivial Unicode algorithm (combining characters, emoji sequences, zero-width joiners).

7. **Frame rebuild cascading** — `ChatViewport.rebuildFrame()` (`tui/chat_viewport.go`) iterates all entries and re-renders any that have changed (dirty). If only the last content block is dirty due to streaming (which is the common case), cache hits skip re-render for everything else. But each rebuild still does cache-key lookup per entry.

**Hot spots by estimated cost (relative to each other):**

| Operation | Cost | Notes |
|-----------|------|-------|
| `highlightLine` (tokenizer) | High | Character-by-character scan per code line |
| `renderInline` (pattern scan) | Medium-High | Scans for bold/italic/code/link patterns |
| `lastStableBoundary` | Medium | Scans full text for blank-line boundaries |
| `ansi.Width` / `ansi.Wrap` | Medium | Grapheme cluster measurement (uniseg) |
| `MDStreamRenderer.Render` (collect blocks) | Low-Medium | Line splitting + block classification |
| `ChatViewport.rebuildFrame` | Low | Cache lookup per entry; cheap when cache hits |

**Potential optimizations (listed in order of expected impact):**

1. **Boundary cache**: `lastStableBoundary()` scans the full text every frame to find the split point. Cache the last boundary position and only scan from there forward. If the text grew but no new blank line appeared, the boundary hasn't moved.

2. **Inline partial render**: When a paragraph is still being streamed (no blank line yet), `renderInline()` re-scans the entire paragraph text on every delta. Cache the rendered output of the paragraph prefix and only render the newly appended suffix, similar to how `IncrementalMDRenderer` handles the block level.

3. **Deferred highlighting**: For large fenced code blocks, defer syntax highlighting until the fence is closed. During streaming (while the opening ``` is open), render as plain monospace text without tokenizer highlighting. Apply highlighting in a single pass when the closing ``` arrives.

4. **Grapheme cache**: `ansi.Width()` calls uniseg for every call. Cache the width of recently-measured strings (LRU or simple last-value cache) to avoid repeated measurement of the same rendered substrings across multiple render passes.

5. **Render coalescing**: Content deltas arriving faster than the display frame rate (e.g., 1 token at 60 tok/s ≈ 16ms per delta, while TUI renders at ≤60fps ≈ ≥16.6ms per frame) cause redundant renders. Batch pending deltas into a single render pass instead of rendering every delta individually.

**Affected files**:
- `tui/markdown_incremental.go:45-60` — `IncrementalMDRenderer.Render` (boundary scan per frame)
- `tui/markdown.go:39-100` — `MDStreamRenderer.Render` (full block collection)
- `tui/markdown_inline.go` — `renderInline` (character-by-character pattern scan)
- `tui/markdown_highlight.go` — `highlightLine` (syntax highlighting tokenizer)
- `tui/markdown_block.go:231-278` — `renderFencedCode` (line-by-line highlight + pad)
- `internal/ansi/wrap.go` — `Wrap`/`Width` (grapheme measurement)
- `tui/chat_viewport.go:253-273` — `ChatViewport.Render` (frame rebuild dispatch)

**Benchmark approach**:
- Profile: `go test -bench BenchmarkMarkdownStream -benchmem ./tui/` with a large markdown document and 1-token deltas.
- Measure CPU per delta and total CPU per document.
- Compare before/after each optimization.

**Validation steps**:
1. Profile identified hot spots — no regression.
2. `go vet ./...` — must pass.
3. `go test -count=1 -race -cover ./...` — all must pass.
4. `gocognit -over 15 .` — no new violations.
5. `gocyclo -over 12 .` — no new violations.

 # Closed Bugs
 
 (all items closed — see `docs/archive/bugs.2026-07-17.md` and earlier archives)
