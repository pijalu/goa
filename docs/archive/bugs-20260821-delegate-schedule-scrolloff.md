<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Closed bugs — delegate_to silent failure, schedule deferral, scrollback corruption (2026-08-21)

All three items from `bugs.md` `# TODO` — fixed, regression-tested, and
committed on `feature/team`. Fix plan (with test approach + validation steps
and the deviations found during execution): `docs/plans/bugs-20260821-fix-plan.md`
— archived alongside as `docs/archive/fix-plan-20260821-delegate-schedule-scrolloff.md`.

## 1. `delegate_to` reports success without clear UI feedback — FIXED

**Observed:** `delegate_to` reported success, but nothing visibly happened in
the UI. It was unclear whether a sub-agent was spawned or what work was in
progress.

**Log:** `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260820-232441.zip`

**Root cause (confirmed from the log):** `session/agents/coder.jsonl` shows
the coder sub-agent's first request failing with
`Error: 400 - {"detail":"Unsupported parameter: max_output_tokens"}`.
Mechanism: `AgentPool.assembleConfig` force-set `opts.MaxTokens = 4096` when
the model config was `0`/`<2048`; the codex responses transport then emitted
`max_output_tokens`, which the ChatGPT Codex **subscription** backend rejects.
The main agent was unaffected (`BuildStreamOptions` leaves `MaxTokens = 0`,
field omitted) — only the pool's floor made it non-zero, so only the
sub-agent failed, and it failed **silently** (the error path only closed the
stream; no chat entry).

**Fix (commit `1e4270c`):**
- Removed the `MaxTokens = 4096` floor in `AgentPool.assembleConfig` —
  sub-agents are normal agents and inherit the main agent's stream options
  verbatim (spec G6).
- `buildResponsesBody` omits `max_output_tokens` when `flavor == "codex"`
  (defense in depth; non-codex flavors keep it).
- New `ForegroundOrchestrator.EmitMessage` (Kind `"message"`) used on the
  `DelegateTool.runDelegatedAgent` error path — the failure lands as a
  visible, agent-attributed chat entry via the InterAgent path (TUI) and
  `CompanionChunk` (headless). A plain `Emit` would have been swallowed
  (Kind `"content"` with `To: "main"` matches no forwarder case).
- New `tools.DelegateRenderer` for `delegate_to`/`request_review` bubbles
  (names target agent + task; renders the async ack as a one-line status),
  registered in `tui/register_renderers.go`.

**Regression tests:** pool config keeps `MaxTokens` verbatim (0 stays 0);
codex body omits `max_output_tokens` with `MaxTokens > 0` while non-codex
keeps it; DelegateRenderer call/result rendering; failed `Run` emits
Kind `"message"` `From=role` `To=main`; filmstrip
`TestDelegateFailure_MockLLM_Filmstrip` shows the `coder — delegation failed`
entry in the actual terminal frame. (Plus `272ae49`: streaming-validation
coverage for the two newly registered renderers.)

## 2. Schedule tools moved to `tool_search` (deferred loading) — DONE

**Observed:** `schedule_create/delete/list` were part of the always-loaded
tool set, occupying context on every turn despite being rarely used.

**Fix (commit `481962e`):** `Deferred()` markers for the three tools in
`tools/deferred.go` (deliberately reversing the 2026-08-17 NOT-A-BUG
decision); `TestScheduleToolsAreEager` inverted into
`TestScheduleToolsAreDeferred` — asserts the tools are withheld from the eager
schema block, advertised in the tool_search catalog, loadable via
`LoadDeferred` (the `select:` path), and served after loading. Registration
stays unconditional; only schema placement changed.

## 3. Out-of-screen tool call results corrupt the terminal UI — FIXED

**Observed:** after a long tool output (1,300+ lines of `go test`) scrolled
past the viewport, expanding the out-of-screen result corrupted the TUI
layout.

**Root cause:** rows committed to terminal scrollback (below the compositor's
scrollback watermark) can never be repainted. Expanding such a widget —
per-widget Enter/Ctrl+O (`setExpandedExplicit`) or global Ctrl+O
(`ToggleAllToolsView` → `invalidateAllToolWidgets`) — rebuilt it with a
different line count, shifting every later entry's repaint geometry so the
compositor's diffs no longer matched the physical screen.

**Fix (commit `9c3364b`):** expansion of a scrolled-off widget is a no-op +
a one-line flash notice. New optional `tui.ScrollbackGuard` interface
(`IsScrolledOff` + `FlashNotice`, implemented by `ChatViewport`, kept separate
from `ToolViewPolicy` per ISP); `setExpandedExplicit` consults it before
recording the override; `invalidateAllToolWidgets` skips scrolled-off widgets
and emits one summary flash. `SetExpanded` is only reachable via
`setExpandedExplicit`, so all expansion mutations are guarded.

**Regression tests (verified to fail without the guards):** per-widget toggle
on a scrolled-off widget leaves state and rendered rows untouched + flashes;
global toggle skips scrolled-off widgets (byte-identical rows) + summary
flash; `TestCompositor_ScrolledOffToolTogglePreservesScrollback` replays the
raw byte stream through the screen emulator — every previously committed
scrollback row stays byte-identical after both toggles, the visible layout
keeps its shape, and the notice is on screen.
