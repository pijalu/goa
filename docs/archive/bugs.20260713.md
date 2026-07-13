<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Archived Bugs — 2026-07-13

All items below were tracked in `bugs.md` and closed on this date.

## 1. Tool elapsed time

**Report**
The tool elapsed time should only be shown during the tool call - after, only "took" should be shown (and it must be correctly calculated).

Currently:
```
Took 0.0s
Took elapsed 0.05s
```

Should be:
```
Took 0.05s
```

This should only be shown if the tool took more than 0.01s to complete.

**Fix**
- `tui/tool_execution.go`: rewrote `renderDuration()` to store the full display line ("elapsed X.XXs" or "Took X.XXs") in `tc.box.duration` and made `build()` use it directly. Added a 0.01s minimum threshold so instantaneous tools don't show noisy duration lines.
- `tools/bash_renderer.go`: changed sub-second duration formatting from `%.1fs` to `%.2fs` and hid durations ≤ 0.01s; renamed partial label from "Elapsed" to "elapsed".
- Updated `tui/tool_execution_test.go` and `tools/bash_renderer_test.go` to assert the new behavior.

**Validation**
- `go test ./tui -run TestToolExecution` passes.
- `go test ./tools -run TestBashRenderer` passes.
- No "Took elapsed" strings appear in the rendered output.

## 2. Spinner disappearing

**Report**
On some occasions, the spinner disappears during conversation and does not appear again.

**Fix**
- `internal/app/stats.go`: in `handleStateChange()`, reset the `StatusMsg` session-ended guard whenever the agent transitions to an active state (thinking/content/tool_call/tool_result). This prevents a mid-turn `EventEnd` from permanently silencing the spinner while the turn is still alive.
- Added `TestUIScenario_SpinnerSurvivesMidTurnEventEnd` in `internal/app/ui_scenario_regression_test.go` to reproduce and guard the exact mid-turn EventEnd → state_change sequence seen in the export log.

**Validation**
- New regression test passes.
- Existing `TestUIScenario_SpinnerSurvivesToolCallTurn` still passes.
- `go test ./internal/app` passes.

## 3. Tool call streaming

**Report**
Slow/long tool calls should be streaming to the TUI, even if not finished. Partial output is ok; a write when the file is not yet known should use a placeholder like "...".

Make sure tool calls are shown as soon as they start, not only after they finish, using filmstrip testing.

**Fix**
- `internal/tuirender/renderer.go`: added `Args map[string]any` to `RenderContext` so renderers can access streaming arguments.
- `tui/tool_execution.go`: `SetArgsPartial()` now best-effort extracts string fields from incomplete JSON into `tc.args`; `updateBox()` renders the partial argument body while the tool is still streaming.
- `tools/writefile_renderer.go`: `RenderCall()` now shows a minimal "write path ..." header during streaming; `RenderResult()` renders the partial `content` from args when the final output is not yet available. Split the function to keep cognitive complexity below the project threshold.
- Added tests in `tui/tool_execution_test.go` and `tools/writefile_renderer_test.go`, plus `TestUIScenario_ToolWidgetVisibleFromStart` in `internal/app/ui_scenario_regression_test.go`.

**Validation**
- Filmstrip test confirms the tool widget is visible from `EventToolCall` and the result content is visible after `EventToolResult`.
- Write streaming test confirms partial content appears in the body before the tool result arrives.

## 4. Build error on Windows (GitHub)

**Report**
Cross-compiling the Windows amd64 binary failed with:
```
# github.com/pijalu/goa/internal/background
Error: internal/background/process_windows.go:39:32: undefined: windows.STILL_ACTIVE
```

**Fix**
- `internal/background/process_windows.go`: replaced `windows.STILL_ACTIVE` with the documented Windows constant value `259` (local `const stillActive = 259`).

**Validation**
- `GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o /tmp/goa-windows-amd64.exe ./cmd/goa/` succeeds.
- `GOOS=windows GOARCH=amd64 go vet ./internal/background` passes.
- `go test ./internal/background` passes.

## Code-quality summary

Ran separately:
- `go vet ./...` — clean.
- `staticcheck ./...` — one pre-existing unused function (`tui/editor_render.go:646: bytePosForCol`), unrelated to these changes.
- `gocognit -over 15 .` — clean.
- `gocyclo -over 12 .` — clean.
- `go test -count=1 -race -cover -timeout 30s ./tui ./tools ./internal/app ./internal/background` — all pass.
- `go test -timeout 30s ./...` — all pass.
