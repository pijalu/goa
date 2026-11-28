<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: tool-call backgrounds have default-terminal gaps with CRLF output

Closed 2026-11-28. Moved from `bugs.md` (entry: "Tool-call backgrounds have
default-terminal gaps, especially with Windows output").

## Observed and expected behavior

Tool-call rows could contain patches of the terminal's default background,
especially with Windows-produced output. Every cell through the right edge must
instead keep the tool's pending, success, or error background, without losing
visible output.

## Reproduction and root cause

`TestToolExecution_CRLFOutputKeepsUniformBackground` passed `alpha\r\nbeta\r\n`
through a real `ToolExecutionComponent`, replayed each rendered row into the
per-cell `TermEmulator`, and failed before the fix:

- the `alpha` row had default background in columns 34–39 of a 40-column row;
- the `beta` row had default background in columns 35–39;
- both rows' text was erased by the subsequently emitted padding.

The renderers split output on LF but preserve the preceding CR. `ansi.Width`
correctly assigns CR no display columns, while `padToWidthStyled` emitted that
CR unchanged. A real terminal therefore returned to column 1 before receiving
the right-padding spaces. The padding overwrote the beginning of the row and
stopped before the right edge, leaving the tail at its previous/default
background.

## Fix

`tui/utils.go`: `padToWidthStyled` now removes carriage returns at the logical
row boundary before width calculation, tab expansion, and background padding.
This is shared by all background-styled tool rows and also protects rows with
the default background. Tool execution results themselves are unchanged.

## Regression coverage and terminal validation

- `TestToolExecution_CRLFOutputKeepsUniformBackground` covers direct component
  rendering, visible text, and every cell's success background. It reproduced
  the bug before the one-line rendering fix and passes afterward.
- `TestUI_ToolWidgetCRLFOutputKeepsStatusBg` drives bash `EventToolCall` and
  `EventToolResult` events through the production app/filmstrip harness. It
  replays the actual compositor writes into `TermEmulator`, finds the tool
  widget, verifies `alpha` and `beta` remain visible, and checks every widget
  cell for the success background.
- Focused race-enabled component and production-event tests pass. No live
  model, server, browser, or throwaway terminal process was started.

## Build and quality validation

All commands were run separately where required:

- `go build ./...` — passed.
- `GOOS=windows GOARCH=amd64 go build ./...` — passed.
- `go vet ./...` — passed.
- `staticcheck ./...` — passed.
- `gocyclo -over 12 .` — passed.
- `gocognit -over 15 .` — reported only eight pre-existing findings in
  untouched files (`provider/manager_auth_startplan_test.go`,
  `tools/mcp_binary_test.go`, `internal/app/tool_factory_coverage_test.go`,
  `core/commands/orchestrate.go`, `config/tools_default_mirror_test.go`,
  `core/agentmanager_turns.go`,
  `internal/agentic/provider/tool_projection.go`, and
  `internal/app/subsystems_goal.go`); no modified file was reported.
- `go test -count=1 -race -cover -timeout 180s ./...` — passed across the full
  repository. TUI coverage was 78.5%; `internal/ansi` was 83.0%; `tools` was
  84.5%.

Validation used the terminal cell emulator and a Windows cross-build, not an
interactive session on a physical Windows host.
