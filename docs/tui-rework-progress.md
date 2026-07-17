# TUI Rework — feature/tui-rework

Autonomous fix branch for rendering/streaming/tooling issues found via session-log
analysis. Work is sequential: each P lands as its own commit with a regression test.

> **Handoff note (for the next agent):** P4, P1, P2 are DONE and committed.
> Remaining: P0 and P3. Everything below has the design, file:line references,
> and gotchas needed to finish them. Run `go test ./tui/ -count=1` to confirm the
> branch is green before starting.

## Commits on this branch (newest first)

| Commit | Item | What |
|--------|------|------|
| `8269910` | P2 | steering edit discoverable (Alt+E hint + keybinding + hotkeys help) |
| `3c92af0` | P1 | per-widget expand/collapse wins over global policy while streaming |
| `bfa61b2` | P4 | compositor rewrite: explicit-scrollback watermark + DECSTBM region; chrome pinned |
| `84d9584` | P4 | RED regression test `tui/pinned_chrome_scrollback_test.go` + this tracker |

## Remaining work

### P0 — Frozen tool widgets / no live progress (HIGHEST value, needs provider check)

**Symptom:** while a tool runs, its widget body is empty and the footer shows only
wall-clock "elapsed Ns" — no bytes/lines growth. Bash output appears only at exit.

**Root cause (traced in session log):** tool args are NOT streamed. In
`.goa/sessions/*.jsonl`, every `tool_call` event appears ONCE fully-formed (no
`IsDelta` tool_call events), so the widget is only created at completion. The
streaming machinery EXISTS end-to-end but is never fed:
- `internal/agentic/agent_streaming.go:689 handleToolCallDeltaByIndex` emits
  `OutputEvent{Type: EventToolCall, IsDelta: true, ToolInput: accumulated}`.
- `internal/app/stats.go:547 handleToolCall` → `toolTracker().OnCall(ev)`.
- `internal/tooltracker/tracker.go:146 onCallDelta` → `tc.SetArgsPartial(...)`.
- `tui/tool_execution.go:363 SetArgsPartial` → `updatePartialArgs` (incremental
  JSON decoder, verified robust across chunk sizes/escapes in tests).

**Fix plan:**
1. **First, verify whether the active provider emits `EventToolCallDelta`.**
   `agent_streaming.go:303 handleStreamToolCallDelta` handles both OpenAI-style
   (Partial snapshot) and Anthropic-style (Delta+ContentIndex). Add a per-turn
   counter of tool-call deltas, logged at turn end, to confirm whether the
   provider streams args at all. If the provider can't stream args, go to (2).
2. **Fallback "preparing …" indicator** while args aren't streaming: the widget
   shows the assistant-text generation that precedes the call as a byte count.
3. **Bash stdout/stderr streaming**: `tools/bgexec.go` / `tools/pty_exec.go` pipe
   output already; forward a throttled `EventToolProgress` with accumulated bytes
   → `tracker.OnProgress` → `tc.SetOutput(partial)` so the body fills live.
4. **Universal live-progress footer** for running tools: `elapsed 12.3s · 1.2 KB ·
   84 lines` (bytes for bash, lines for write/edit). Add counters to
   `ToolExecutionComponent`, update on each delta/progress event, render next to
   the duration (`tool_execution.go renderDuration` ~line 310).
   Tests: fake provider emitting deltas → assert body+footer grow; bash stdout
   appears before exit.

### P3 — Search tool: `context_lines` bug + `max_lines`

**Bug:** `context_lines` is advertised in the schema and parsed into
`searchParams.ContextLines` (`tools/search.go:100`, defaulted at :117) but NEVER
used — `searchWithPattern`/`formatResults` don't receive it. The existing test
`TestSearchTool_Execute_WithContextLines` (`tools/search_test.go:169`) only
asserts non-empty output, so it passes vacuously. Models learn "search gives no
context; grep -A5 does" and fall back to bash (measured: 321 bash-grep vs 100
search calls across 15 sessions).

**Fix:**
1. Wire `ContextLines` through: capture ±N lines around each match in
   `searchFile`/`searchFiles`, extend `formatResults`/`formatFileContentLines`
   to print them (marked distinctly from match lines). Strengthen the test to
   assert context lines actually appear.
2. Add **`max_lines`** param (line-oriented cap, matches the `| head -N` habit
   — user explicitly preferred lines over bytes). Truncate output with a
   "K truncated" marker. Document in `tools/search.long.md`.
3. (nice-to-have) `invert_match`, `count_only`, `files_only` modes (60 bash
   greps used `-v`; 4 used `-c`; 1 `-l`).

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
