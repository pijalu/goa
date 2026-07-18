# TUI Rework — feature/tui-rework

Autonomous fix branch for rendering/streaming/tooling issues found via session-log
analysis. Work is sequential: each P lands as its own commit with a regression test.

> **Status: ALL DONE.** P0–P4 committed. Run the full gate before merging:
> `go vet ./... && go test -count=1 -race ./tui/ ./tools/ ./internal/agentic/ ./internal/app/`

## Commits on this branch (newest first)

| Commit | Item | What |
|--------|------|------|
| `1c5b39b` | P3 | search: wire context_lines through; add max_lines cap |
| `7ba5ddc` | P0 | agentic: per-round tool-call delta counter logged at stream done |
| `b4a5d4c` | P0 | tui: live-progress footer (bytes+lines) while tool runs |
| `b4da356` | P2 | test: fix stale steering-footer assertion (found red at handoff) |
| `8269910` | P2 | steering edit discoverable (Alt+E hint + keybinding + hotkeys help) |
| `3c92af0` | P1 | per-widget expand/collapse wins over global policy while streaming |
| `bfa61b2` | P4 | compositor rewrite: explicit-scrollback watermark + DECSTBM region; chrome pinned |
| `84d9584` | P4 | RED regression test `tui/pinned_chrome_scrollback_test.go` + this tracker |

## P0 solution (committed `b4a5d4c` + `7ba5ddc`)

**Symptom:** while a tool runs, its widget body was empty and the footer showed only
wall-clock "elapsed Ns" — no bytes/lines growth. Bash output appeared only at exit.

**Findings from the trace:**
- Bash stdout streaming already worked end-to-end (`progressWriter` →
  `EventToolProgress` → `tracker.OnProgress` → `SetOutput`); tests
  `TestToolProgress_ShowsPartialOutputWhileRunning` / `TestToolStreamingRepro_*` pass.
- Arg streaming exists for OpenAI (`EventToolCallDelta` + Partial snapshot) and
  Anthropic (`input_json_delta` + ContentIndex). For providers emitting neither,
  no widget can exist before the call arrives — nothing to show "preparing…" in.
  The visible "frozen" symptom was the missing growth indicator, not missing widgets.

**What landed:**
1. **Live-progress footer** (`tui/tool_execution.go`): `ToolExecutionComponent`
   tracks `outputBytes`/`outputLines` (updated in `SetOutput`, which serves both
   partial progress and final result). While Pending/Running the duration line
   renders `elapsed 12.3s · 1.2 KB · 84 lines`; suffix omitted with no output yet
   (fast tools stay clean) and dropped on completion (plain `Took Xs`).
   Helpers: `progressSuffix`, `formatByteSize`, `formatLineCount`.
   Test: `TestToolExecution_LiveProgressFooter` (+ formatter unit tests).
2. **Provider delta diagnostic** (`internal/agentic`): `toolCallDeltasThisRound`
   counted in `handleStreamToolCallDelta`, reset per round, logged at
   `handleStreamDone` as `stream round done: tool_call deltas=N`. A zero count
   in session logs proves the active provider/model can't stream args, settling
   the "verify provider" question with data instead of code reading.

## P3 solution (committed `1c5b39b`)

**Bug:** `context_lines` was advertised in the schema and parsed into
`searchParams.ContextLines` but NEVER used — `searchWithPattern`/`formatResults`
never received it. The old test only asserted non-empty output (passed vacuously).
Models learned "search gives no context" and fell back to bash grep
(321 bash-grep vs 100 search calls across 15 sessions).

**Fix:**
1. `searchResult` gained `Before`/`After`; `searchFile` captures ±N lines per
   match via the `matchContext` helper (kept gocognit under budget), threaded
   through `searchFiles` → `searchWithPattern` → `Execute`/`searchPipePattern`.
2. Rendered grep-style in `formatFileContentLines`: context rows `  N- content`
   vs match rows `  N: content` (shared `sanitizeSearchLine` helper).
3. Default `context_lines` changed 1 → 0 (opt-in; plain searches stay compact).
4. New **`max_lines`** param (schema + params + doc): `truncateOutputLines` caps
   output content lines like `| head -N`, emitting a
   `… (K lines truncated by max_lines)` marker after the header.
5. Tests: `TestSearchTool_Execute_WithContextLines` now asserts context rows
   actually appear; `TestSearchTool_Execute_ContextLinesDefaultOff`;
   `TestSearchTool_Execute_MaxLinesTruncates`; `TestTruncateOutputLines` table test.
   Documented in `tools/search.long.md`.

Not done (nice-to-have, low value): `invert_match`, `count_only`, `files_only`
modes (60 bash greps used `-v`; 4 `-c`; 1 `-l`).

## P4 solution (committed `bfa61b2`)

Rewrote the compositor around an explicit-scrollback model instead of the old
accreted six-strategy scroll dispatch:

- **`scrollTop` watermark** — transcript rows emitted into terminal scrollback
  exactly once, in order, monotonically advancing, clamped to the chrome band
  start so chrome can never leak (structural, not heuristic).
- **One scroll mechanism** (`emitScrollbackAdvance`) with two sub-cases:
  first-frame top-down write (blank screen) and steady-state bottom
  scroll-then-write. The first/large/bare/deleted/shrink/resize strategy
  dispatch is gone.
- **`prevWindowFull` discriminator** — a previous window counts as "full" only
  if every transcript region row is non-blank; partial windows (blank padding
  from bottom-aligned short content) take the full-range re-emit path. This was
  the subtle case behind the last content-loss bug (the `start` frame).
- **DECSTBM scroll region** `[1, height-chromeH]` confines native terminal
  scroll to the transcript, so pinned chrome (status/editor/footer/bubbles)
  never moves and never enters scrollback.
- Chrome classification is role-based: the single `HeightAllocated` child is the
  scrollable transcript; every child after it is pinned chrome
  (`Scene.ChromeHeight` computed in `buildScene`, `tui/tui.go`).

Net −208 lines in compositor.go. Both test emulators (`TermEmulator`,
`screenEmulator`) gained DECSTBM support. Refactored into small helpers to stay
within gocognit 15 / gocyclo 12 (`transcriptWindow`, `emitFirstFrameScroll`,
`emitSteadyScroll`, `steadyWriteRange`, `prevWindowFull`, `scrollTarget`,
`advanceScrollback`, `repaintWindow`).

## P1 solution (committed `3c92af0`)

`ToolExecutionComponent.expandedSet` flag: an explicit per-widget toggle
(Enter/Ctrl+O on a focused block, `HandleInput` → `setExpandedExplicit`) wins
over the global view policy in BOTH directions and persists across streaming
re-renders. `effectiveExpanded()` checks `expandedSet` first
(`tui/tool_execution.go`). The global toggle-all clears overrides via
`ClearExplicitExpand()` in `ChatViewport.invalidateAllToolWidgets`
(`tui/chat_viewport.go`) so Ctrl+O-all still flips everything uniformly.

## P2 solution (committed `8269910`)

Steering recall already worked: Alt+E → `App.handleEditSteering`
(`internal/app/submithandler.go:166`) flushes `SteeringQueue` back into the
input line and clears the bubble/footer. The gap was discoverability:
- bubble footer now shows "Alt+E to edit" (`tui/chat_viewport_components.go`);
- `KbEditSteering` registered (`tui/keybindings.go`);
- listed in `/hotkeys` help (`core/commands/hotkeys.go`).

## Key lessons / gotchas

- **Don't restructure `compose`'s canvas layout** (capping/shifting rows broke
  15 tests). The virtual-buffer invariant: scrolled-off transcript rows must
  stay addressable so the scroll path can emit them to native scrollback.
- **The `tui/` test suite cancellation:** `go test ./tui/ -count=1` occasionally
  gets canceled in this environment; build+vet are reliable. Re-run if canceled.
- **Reference implementations:** `../pi` renders one flat buffer and lets chrome
  scroll into history (accepts the leak). `../opencode` uses OpenTUI (Zig-backed
  cell-buffer compositor, internal scroll, no native scrollback). Goa's
  DECSTBM-region approach pins chrome without adopting a full cell-buffer model.
- **Test emulators** (`tui/term_emulator.go`, `tui/streaming_scroll_test.go`)
  both model DECSTBM scroll regions; replay tests through `emu.Scrollback()` to
  assert what lands in history.
- **Gate before committing:** `go vet ./tui/`, `go test -count=1 -race ./tui/`,
  `gocognit -over 15 ./tui/`, `gocyclo -over 12 ./tui/` (must be empty output).
