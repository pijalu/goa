# B-LSPhdr — lsp tool call header hides the operation and the searched term

Status: FIXED (2026-09-04)

## Observed

In the TUI, an `lsp` tool call renders its header as only
`✓ lsp cmd/goa/main.go` regardless of the operation. A `workspaceSymbol`
search for `AskUserQuestionTool`, a `hover` at line 11, and a `rename` all
look identical; the queried term / position / new name are never shown
(export `.goa/exports/goa-export-20260904-080913.zip`, session.md line 1272).
The result body carries the details; the call header does not.

## Root cause

`lsp` has no dedicated renderer, so the header falls back to
`FormatToolArgs` (`tui/tool_execution_status.go`), whose default branch
returns the first non-empty of `[path, command, name, pattern, id]` — for
lsp args `{"op":…,"path":…,"query":…}` it picks `path` and drops `op`,
`query`, `line`, `character`, `newName`.

## Expected

Header shows the operation and its target, e.g.
`✓ lsp workspaceSymbol "AskUserQuestionTool"`,
`✓ lsp definition cmd/goa/main.go:12:4` (1-indexed, matching result
`path:line:col`), `✓ lsp symbols cmd/goa/main.go`,
`✓ lsp rename cmd/goa/main.go:12:4 → NewName`.

## Fix (executed per plan)

1. Added `case "lsp"` to `FormatToolArgs` in `tui/tool_execution_status.go`
   delegating to a new `formatLSPArgs` helper: `op` + target, where target
   is the `query` (quoted) for `workspaceSymbol` (path is only a routing
   anchor there), else `path[:line:char]` bumped to 1-indexed via the new
   `bumpIndex` helper, with `→ newName` appended for `rename`. Graceful
   degradation when fields are missing (falls back to `lsp <path>` or bare
   `lsp`).
2. Tests: table-driven `TestFormatToolArgs_LSP` in
   `tui/tool_execution_test.go` covering symbols, workspaceSymbol (the
   reported case), position ops with 0-indexed → 1-indexed bump, rename with
   newName, missing/empty fields, and malformed JSON. Verified RED against
   the pre-fix code (old output: bare path), GREEN after.
3. Terminal-output validation (guideline 5, filmstrip harness):
   `internal/app/lsp_header_filmstrip_test.go`
   (`TestLSPToolCallHeaderShowsOperationAndTerm`) replays the exact
   `symbols` + `workspaceSymbol` events from the export through the
   production component tree on a fake terminal and asserts the rendered
   ANSI-stripped screen shows `lsp symbols cmd/goa/main.go` and
   `lsp workspaceSymbol "AskUserQuestionTool"`. Verified FAIL on pre-fix
   code, PASS after.
4. Quality gates (each run separately, all clean):
   `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
   `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...`
   (87 packages ok; tui coverage 75.6%, internal/app 60.7%).
