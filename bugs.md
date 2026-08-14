# Bug and feature Tracking

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

# To fix

(none)

## Queued goals (from the goal queue — not started)
1. **jade.bison — Session status totals view:** the session status should end with a clear TOTAL block showing, for each key element (input / cached-input / output), the number of tokens used, plus tool calls, compressions, and cache misses — rendered as a table/markdown after the per-round details, giving a clear summary view of the whole session.
2. **happy.owl — Commit all changes and push.**

Execution order when resumed: jade.bison, then happy.owl (commit+push LAST, covering everything).

# Archive

## Unexpected cache miss after destructive "ceiling" compaction (export goa-export-20260814-153156) — FIXED
Review of the export bundle + all recent sessions for unexpected provider prefix-cache misses.

**Findings (traced):**
- Export logs one detected miss: `prev_cache_read 82496 → 0` at 2026-08-14T15:22:08 (zai/glm-5.3, session 1786713126_2wql4s85). The captured bust+preceding requests (seq 24/25, 6s apart) have IDENTICAL message prefixes (msgs 0–51 byte-equal, tools/model/thinking equal) — so that pair alone does not explain the miss by content. Remaining suspects for the pair: provider-side cache write timing (the 82k prefix was read but not re-established in time), or a byte-level serialization difference invisible in the re-marshaled capture.
- The BIGGER, reproducible cache-buster: the session later crossed 95% usage and the reactive enforcer ran a DESTRUCTIVE ceiling cut — `⚡ Context compacted (ceiling): 96% → 44% · 120 messages dropped · ~107889 tokens freed` — which wholesale rewrites the history prefix. Every subsequent request then re-reads the full input at full price (cache bust by design of a front-cut). The user config has `context_compression.enabled: false`, yet the ceiling cut fired: the reactive `enforceContextCeiling` ignores the Enabled toggle and the destructive drop is the ONLY thing that acts at 95% under default config (proactive hard tier requires `hard_percent > 0`, which is 0 by default).

**User-confirmed intended contract (now enforced):**
- Default cache compression: ALL algorithms disabled EXCEPT **summarize at 95%** (the hard layer default).
- When compression is disabled (`enabled: false`): soft/trigger layers off, but the hard 95% default STILL triggers summarize.
- The only "hybrid" case, at 95%: if summarize cannot be executed (LLM error due to context overflow), a micro pre-compression is applied, then summarize retried. Micro is a fallback ONLY — never a first pass.
- The destructive ceiling message-drop is a LAST RESORT only (summarize cannot run at all / still overflows after the micro fallback).
- All of this remains **customizable per model** (`context_compression.per_model`): per-model thresholds (incl. hard_percent) and strategies (incl. hard strategy), zero fields inheriting the defaults.

**Root cause (traced):**
1. `resolveThresholds` (internal/agentic/compression_thresholds.go): default `hardStrategy` = `hybrid` (elision → selective → summarize), not `summarize`.
2. `proactiveTierLocked`: hard tier required `rt.hard > 0`; with the default `hard_percent: 0` the tier never fired, so at 95% only the reactive ceiling drop (label "ceiling") acted — the exact observed banner.
3. `Compact` (agent_compression.go): the summarize-overflow micro fallback was gated on `MicroCompaction.Enabled` — but per the contract it must run whenever summarize itself overflows, regardless of the micro opt-in.
4. `buildCompressionConfig` (core/agentmanager_lifecycle.go) zeroed ALL thresholds on `enabled: false`, intending to disable everything; under the new semantics hard=0 must mean "default 95 on" so only soft/trigger are disabled.
5. (Found live during the PTY repro; crash.log "ceiling cannot be enforced … 4750") `maybeCompress`: the legacy micro branch gated on `ContextStats().UsagePercent`, whose denominator is the runtime window (display stat), not the effective window (`context_compression.max_tokens`) — under a configured max_tokens the legacy gate fired below the real hard threshold and masked the tierHard path.

**Fix:**
1. `resolveThresholds`: default `hardStrategy = summarize`; negative `HardPercent` preserved = explicit disable; zero → default 95 ON.
2. `proactiveTierLocked`: hard tier gate is `rt.hardEnabled() && usage >= effectiveHard()`.
3. `Compact`: summarize-overflow micro fallback is now UNCONDITIONAL (no longer gated on `MicroCompaction.Enabled`).
4. `compaction.go` `microFallbackConfig()`: field-wise defaults for a zero micro config.
5. `maybeCompress` (agent_compression.go): computes `computeContextStatsForMax(maxTokens)` under `a.mu` so the legacy micro branch uses the effective window.
6. Comments only: `core/agentmanager_lifecycle.go`, `config/config.go`, `config/configs/default.yaml` (documented "0 = default 95", negative = disable).

**Tests (RED → GREEN, 10× flake-check):**
- `internal/agentic/compression_default_summarize_test.go`: `TestResolveThresholds_DefaultHardIsSummarizeAt95`, `TestProactiveTier_HardFiresAtDefault95`, `TestMaybeCompress_DefaultHardCeilingRunsSummarizeNotCeiling`, `TestPreparePath_CeilingOnlyWhenSummarizeCannotRun`, `TestCompact_SummarizeOverflowAppliesMicroUnconditionally` (fail-once overflow provider), plus `LegacyMicroBranchUsesEffectiveWindow` and `LegacyMicroBranchStillSelfManagesBelowCeiling` (resized user/asst 2200 chars each, window 1000).
- `internal/agentic/compression_thresholds_test.go`: hard −1 stays disabled; zero-thresholds hard ON at 95; wantHard → summarize.
- `internal/agentic/compact_micro_optional_test.go`: overflow fallback via `registerOverflowProvider(name, 1)`; `PersistentOverflowPropagatesError` (failures=2).
- `core/agentmanager_lifecycle_test.go`: `TestBuildCompressionConfig_PerModelHardOverridesDefaults` (per-model hard_percent/strategies override; zero fields inherit).

**Validation:**
- Mock-model e2e (no remote, mocked LLM): new in-repo deterministic OpenAI-compatible mock server `e2e/mockllm/server.py` (+ `start_mock_llm`/`stop_mock_llm` helpers in `e2e/lib.sh`, documented in `e2e/README.md`). Seeded-PRNG filler (~30 KB, loop-detector-safe: repeated lorem and even numbered lines trip goa's stream-loop guardrail — the server now varies word choices deterministically; also threaded + safe on system-less requests). PTY run against mock with `context_compression.max_tokens: 6500`, two "hi" turns: session JSONL contains exactly one compact event `strategy="summarize"` (237% → 72%, 3 messages dropped), ZERO `strategy="ceiling"` events, ZERO error events, no loop-detector notes.
- Gates (each run separately): `go vet ./...` clean; `go test -count=1 -race -cover ./...` all green; `staticcheck ./...` identical set to pre-change baseline (22 pre-existing, e.g. `compressHistory` unused); `gocognit -over 15` identical (73 pre-existing); `gocyclo -over 12` identical set (`proactiveTierLocked` 13, `overlayCompressionForModel` 14 — pre-existing, untouched).

## Cursor jumps out of input during tool status redraw — FIXED
Tool status changes trigger a cursor glitch: the cursor momentarily jumps out of the input box, then returns to the expected position on the next render.

**Root cause (traced):** `appendCursorSeq` mapped the cursor row linearly (`screenRow = cursorRow - vt + 1`), but the paint layout is two-phase: transcript rows are linear in `vt`, while the chrome band (editor) is PINNED to the screen bottom (`screenRow = windowH + (cursorRow - contentEnd) + 1`). The two mappings agree only when `vt` equals the natural bottom anchor (`canvasLen - height`). During a tool-widget collapse (canvas shrinks by `d` while the scrollback watermark is still high) `windowTop` clamps `vt` above the natural anchor, and the cursor landed `d` rows ABOVE the editor row — outside the input box — until the canvas regrew (next streaming chunk) snapped it back.

**Fix:** `appendCursorSeq` (`tui/compositor.go`) now maps through the same two-phase layout as the paint: extracted `cursorScreenRow(targetRow, totalLines, vtop, height)` computes `contentEnd = totalLines - chromeH`; cursor rows at/above `contentEnd` map to the pinned chrome band (`windowH + (row - contentEnd) + 1`), rows below map linearly. No Scene/layout changes — the paint split is purely by canvas row.

**Tests:** `TestCompositor_CursorStaysOnEditor` (`tui/cursor_clamp_repro_test.go`, RED first): scrolled canvas with chrome, shrink transcript so `vt` clamps, explicit `Scene.Cursor` on the editor row; replayed through `screenEmulator`; asserts the hardware cursor row equals the painted editor row, not the linear map. `TestCompositor_CursorStaysOnEditorAcrossShrinkAndRegrow` covers the stacked shrink+regrow sequence (tool collapse + queued user message).

**Validation:** PTY session (opencode-go/deepseek-v4-flash): `read` tool running→done collapse + streaming — editor/input stayed pinned at the screen bottom, no visible cursor detach. Gates: `go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12`, `go test -count=1 -race -cover ./...` all pass (pre-existing warnings unrelated to the change: `tui/render_trace.go` U1000 unused `sceneLayersTrace`, `renderLoop` gocognit 16, `scrollOffUnstable` gocyclo 13 — all untouched).

## Input cursor occasionally redrawn one line too high — FIXED
Cursor on the input box was sometimes redrawn one line above its true position (transient), worst after a `read` tool completes and a user message is queued right after (two stacked height changes).

**Root cause (traced):** same family as the cursor-jump bug. The frame where the tool widget collapses shrinks the canvas; `windowTop` clamps `vt` to the stale scrollback watermark (above the natural anchor); the two-phase repaint keeps the editor pinned at the screen bottom, but `appendCursorSeq` positioned the cursor with the linear mapping, placing it exactly `d` rows (the shrink delta) above the true editor row. A user message queued+sent regrows the canvas past the watermark on the next frame(s), re-aligning both mappings — the snap-back.

**Fix:** same `cursorScreenRow` two-phase mapping as above (single fix covers both bugs).

**Tests:** same regression tests, incl. the shrink+regrow sequence asserting the cursor row stays on the editor's painted row across consecutive renders.

**Validation:** same gates + PTY repro as the cursor-jump bug — all green.

## Blank screen + cursor on first line after `/new` — FIXED
After `/new`, the cursor landed on line 1 and the screen stayed blank until the input line.

**Root cause (traced):** the renderLoop requests a Scene snapshot from the commandLoop, then hands it to `compositor.Render`. `/new` (handleNewSession) runs `chat.Clear()` + `compositor.Clear()` on the commandLoop — which can land BETWEEN the snapshot and the Render. The stale pre-clear scene then consumed `clearRequested`, repainted the OLD canvas as a "first frame", and restored the stale `scrollTop`/`prevLines`. Every subsequent frame diffed against that stale baseline: `windowTop` clamped `vt` to the stale watermark far above the (now short) canvas, no transcript row was in range, and `appendCursorSeq` clamped the cursor to screen row 1 — blank window, cursor on line 1, chrome at the bottom.

**Fix:** clear-generation epoch on the Compositor. `Clear()` increments `clearGen` under its mutex; `Scene.ClearGen` is stamped at snapshot time (`buildSnapshot`/`renderNow` in `tui/tui.go` read `compositor.ClearGen()`); `Render` drops any scene whose generation is older than the current one (the wipe stays pending for the next, fresh frame). Stale snapshots racing a `/new` can no longer repaint the dead session.

**Tests:** `TestCompositor_RenderDropsStaleSceneAfterClear` (RED first): stale scene rendered after `Clear()` produces NO terminal writes and leaves the wipe pending; the next fresh frame wipes + paints. `TestTUI_ClearTranscriptNextFrameIsFresh`: full engine path — after `/new`-style Clear, the next frame contains no stale session rows and paints the fresh screen.

**Validation:** PTY session: `/new` from an active scrolled session (↑4.7K scrollback) — fresh header/banners/input painted immediately, no blank screen, no cursor at line 1. Gates all pass as noted above.

## Content not shown after `/new` until a resize — FIXED
After `/new`, no content was displayed at all until a window resize occurred, at which point everything appeared.

**Root cause (traced):** same stale-scene race as the blank-screen bug, without the visible cursor clamp: after the stale frame repainted the old canvas and restored the stale watermark, subsequent frames diffed against a baseline that no longer mapped onto the screen; nothing repainted the (short) fresh transcript because `windowTop` was clamped above it. A resize changed the width → `frameGeometryReset` → `drawWindowResetScrollback` reset `scrollTop`/`vt` to 0 and re-emitted everything — which is why a resize "fixed" it.

**Fix:** covered by the clear-generation epoch (+ its regression tests, which assert the frame after a racy stale delivery still paints fresh content with no resize).

**Validation:** same PTY `/new` repro (content visible without any resize) + gates as above — all green.

## Screen glitching — FIXED
The screen history can have double line at boundaries.

**Root cause:** When the chrome band shrinks (goal bubble clears), the canvas shortens by the chrome delta. `windowTop` used the natural bottom anchor (`canvasLen - height`) without clamping to the scrollback watermark, so `vt` dipped below `scrollTop`. Rows already emitted into terminal scrollback were repainted on screen — appearing twice: once in scrollback, once at the top of the visible window.

**Fix (2 parts):**
1. `windowTop` (`compositor.go`): Always clamp `vt >= scrollTop`. A scrolled-off row must never reappear on screen — the terminal offers no "unscroll", so repainting it would duplicate it.
2. `repaintWindow` / `drawWindow` (`compositor.go`): Two-phase repaint — transcript rows in screen rows [1, windowH], chrome rows in [windowH+1, height]. This keeps the chrome band pinned at the screen bottom even when `vt` is clamped above the natural anchor, preventing the "chrome in the middle with blank rows below" problem that the original partial clamp (commit 6921104) was working around.

**Tests:**
- `TestCompositor_ChromeShrinkNoDuplicate` (`tui/compositor_boundary_dup_repro_test.go`): reproduces the exact bug — chrome grows then shrinks while the transcript is scrolled, asserts no row appears twice across scrollback+screen.
- `TestCompositor_OneRowShrinkNoDuplicate` (`tui/compositor_partial_shrink_test.go`, replaces `TestCompositor_OneRowShrinkNoBlankBottom`): verifies a 1-row transcript shrink leaves a truthful blank row instead of duplicating a scrolled-off row.

**Validation:** `go vet`, `staticcheck`, `gocognit`, `gocyclo`, `go test -race -cover ./...` — all pass, no new warnings.

Log: /Users/muaddib/dev/creaves.project/.goa/exports/goa-export-20260813-204324.zip
