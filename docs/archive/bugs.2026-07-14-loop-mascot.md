<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Fixed Bugs — 2026-07-14

## 1. Generation stopped incorrectly after a tool result (read after edit treated as a loop)

**Observed failure:** After the agent sent a `read` tool result back to the model, the turn stopped silently. The diagnostic export showed five reads of `tools/python_renderer.go` interleaved with edits; the 5th read triggered a loop guardrail that cancelled the turn, and the UI displayed the generic "Generation stopped by user." message.

**Root cause:** The `core/LoopDetector` default `LoopInterrupt` threshold was 5. Reading the same file after editing it (a non-consecutive repeat) accumulated to 5, so the `core/AgentManager` treated it as a tool-call loop and cancelled the context. The `executeRunner` path only distinguished between "connection error" and "user cancelled", so the UI showed the generic cancellation message.

**Fix:**
- `core/loopdetector.go`: raised `DefaultLoopDetectorConfig` to `LoopWarning: 7`, `LoopInterrupt: 10` so that reading a file after an edit does not trigger a false loop before 10 non-subsequent repeats.
- `core/agentmanager.go`: added `loopStopReason` and a `setLoopStopReason` helper; `handleLoopWarning` records the reason when `LoopCritical` or `LoopInterrupt` fires; `executeRunner` now emits an `EventEnd` with a clear `[goa-system] Agent stopped: ...` message and does not set the `cancelled` metadata when the loop detector stopped the turn.

**Validation:**
- `core/loopdetector_test.go`: updated thresholds and existing interrupt/warning tests.
- `core/agentmanager_test.go` `TestAgentManager_LoopStopReason_EmitsClearEventEnd`: verifies that loop-induced cancellation produces a clear `EventEnd` without the generic `cancelled` flag.
- `internal/app/export_filmstrip_test.go` `TestExportReplay_LoopDetector_NoFalsePositive`: replays the tool-call sequence from the diagnostic export and asserts it does not hit `LoopInterrupt` before 10 repeats.

**Quality checks passed:** `go vet ./...`, `go test -count=1 -race -cover ./...`, `gocognit -over 15 .` and `gocyclo -over 12 .` report only pre-existing, unrelated violations.

## 2. TUI mascot/logo flash during tool calls

**Observed failure:** Reported flash of the mascot/logo header in the visible area during tool calls (especially `edit`) when the tool widget grew or shrank.

**Root cause / validation:** The export was used to drive the app-level filmstrip harness. A compressed but representative replay of the export events was added that asserts the mascot/logo header never reappears in visible filmstrip frames after it has scrolled off. This acts as a regression guard for the reported tool-call-driven growth/shrink sequence.

**Fix:** No production code change was required; the filmstrip test validates the scenario stays clean.

**Validation:**
- `internal/app/export_filmstrip_test.go` `TestExportReplay_Filmstrip_NoMascotRedraw`: loads the diagnostic export, compresses streaming content deltas while preserving tool calls/results and state changes, replays them through the production app/TUI stack, and asserts no mascot/header redraw in the captured filmstrip.

**Quality checks passed:** same as above.
