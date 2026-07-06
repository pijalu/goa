# Complexity Cleanup Plan

Goal: Bring every function under the project gates:
- gocognit cognitive complexity ≤ 15
- gocyclo cyclomatic complexity ≤ 12

## Refactoring Strategy (SOLID)

For each function:
- Extract pure helpers for single-responsibility steps.
- Replace nested `if/switch/for` chains with early-return helpers.
- Keep helpers under the same limits so we do not just shift complexity.
- Prefer table-driven assertions in tests.
- Do not change behavior; only structure.

## Micro-Steps

### Step 1 — Restore build
- [x] Restore `lmstudioReachable` helper deleted during test refactor.
- [x] Run `go test ./internal/app/...` to verify build.

### Step 2 — agentic/events_to_history.go
- [x] Extract `historyBuilder` type with `handleEvent`, `handleContent`, `handleToolCall`, `handleToolResult`, `ensureAccum`, `flush` helpers.
- [x] Run `go test ./internal/agentic/...`.

### Step 3 — tui/markdown_inline.go
- [x] Extract `writeCommand`, `findLatexMathClose`, `nextDollarAfter` helpers from `translateLaTeXMathContent`/`translateLatexMath`.
- [x] Run `go test ./tui/...`.

### Step 4 — tui/markdown_test.go
- [x] Move `TestRenderInline_LaTeXMathInParagraph` to `tui/markdown_test_latex.go`.
- [x] Extract `checkGePercent`, `checkMixedLatexEntity`, `checkJustPercentEscape` helpers.
- [x] Run `go test ./tui/...`.

### Step 5 — agentic/agentic_test.go
- [x] Extract `assertSingleWordRepeatNotDetected` and `assertMultiWordRepeatDetected` from `TestStreamLoopIntegration`.
- [x] Extract `assertWindowRange` from `TestStreamLoopWindowRange`.
- [x] Run `go test ./internal/agentic/...`.

### Step 6 — config/wizard_render.go
- [x] Extract `applyEndpointResult`, `advanceEndpointResult`, `endpointNeedsKey`, `endpointKeyState`, `endpointTestState` helpers from `startEndpointInput`.
- [x] Run `go test ./config/...`.

### Step 7 — tools/readfile_test.go
- [x] Extract `assertDirectoryListing` helper and simplify `TestReadDirectory`.
- [x] Run `go test ./tools/...`.

### Step 8 — core/agentmanager_test.go
- [x] Extract `setupBlockingAgentManager`, `waitForProviderStart`, `waitForAgentManagerRunning`, `waitForEndEvent` helpers from `TestAgentManager_Interrupt_CancelsRunningTurn`.
- [x] Run `go test ./core/...`.

### Step 9 — tui/tui.go
- [x] Extract `findCursorInLayers` and `findCursorInLayer` helpers from `extractCursorMarker`.
- [x] Run `go test ./tui/...`.

### Step 10 — cmd/webbuild/main.go
- [x] Replace `curatedBlurb` big switch with `curatedBlurbs` map lookup.
- [x] Extract `collectDocSet`, `processDocs`, `stemFromPath` helpers from `run`.
- [x] Build `cmd/webbuild`.

### Step 11 — internal/agentic/agent_streaming.go
- [x] Extract `buildProviderHistory`, `buildSystemPrompt`, `mergeGoalProgress` helpers from `Agent.buildProviderContext`.
- [x] Run `go test ./internal/agentic/...`.

### Step 12 — config/config.go
- [x] Replace `ToolEnabledConfig.ApplyTo` if-chain with table-driven field loop.
- [x] Run `go test ./config/...`.

### Step 13 — Final validation
- [x] `gocognit -over 15 .` exits 0.
- [x] `gocyclo -over 12 .` exits 0.
- [x] `go vet ./...` clean.
- [x] `go test -count=1 -race -cover ./...` passes.
- [x] `go build -o goa ./cmd/goa` succeeds.
- [x] Interactive-shell smoke test: TUI launched and accepted input.

## Success Criteria
- `gocognit -over 15 .` exits 0.
- `gocyclo -over 12 .` exits 0.
- All tests pass.
- `go vet ./...` clean.
- Binary builds.
