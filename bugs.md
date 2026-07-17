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

# Open bugs

## Commit skill
There seems to be a commit skill but it fails:
```


 ✗ read embedded:/commit-msg/SKILL.md
 Error: [read error: file_not_found]
 File not found: embedded:/commit-msg/SKILL.md
 Hint: Check the file path and try again. Use search to find the correct path.
 Took 0.60s


 ✓ $ cd /Users/muaddib/dev/goa && git diff --stat && git diff specs/plan-mode.md | head -60
 ```


# Closed Bugs

## Tool panic on write/edit (typed-nil LSP manager) — FIXED (2026-07-16)
Writing or editing a `.go` file intermittently failed with `Error: tool panic: runtime error: invalid memory address or nil pointer dereference` (recovered by `internal/agentic/tool_scheduler.go`). Observed live in two sessions on 2026-07-16 (incl. pid 57108) when the agent wrote `internal/app/*.go`.

Root cause — the Go **typed-nil interface trap**. `internal/app/bootstrap.go` `newLSPManager` returns a `*lsp.Manager` and returns a **nil pointer** when gopls fails to start (timeout/binary/env). That nil is stored into the tool's `LSPManager LSPDocumentManager` field — an **interface**. A nil concrete pointer inside a non-nil interface makes the `if t.LSPManager == nil` guards in `tools/writefile.go` (`lspDiagnostics`) and `tools/editfile.go` (`notifyLSP`) evaluate `false`, so the guard is bypassed and the first `.go` write/edit calls `OpenDocument`/`DidChange` on a nil `*Manager`, which dereferences `m.started` → panic.

Stack (reproduced):
```
lsp.(*Manager).OpenDocument              internal/lsp/manager.go:76   (m == nil)
tools.(*WriteFileTool).lspDiagnostics    tools/writefile.go:165
tools.(*WriteFileTool).Execute           tools/writefile.go:147
```

Fix: nil-receiver guards on the three `LSPDocumentManager` methods in `internal/lsp/manager.go` (`OpenDocument`, `DidChange`, `DiagnosticsFor`), so a typed-nil manager is a clean no-op ("lsp manager: not started") instead of a panic. Fixing at the receiver covers both `writefile` and `editfile` (and any future caller) without per-callsite checks.

Panic diagnostics hardening (per bugs.md guideline: panics must carry enough info to debug when not reproducible on demand): `internal/agentic/tool_scheduler.go` `runTask` now includes the tool name plus a trimmed stack (`panicStackBrief`) in the recovered error — tool results land in the session event log / crash exports where panics are debugged from. `tui/tool_execution.go` `updateBox` recover now logs `debug.Stack()` too (previously only `%v`).

Tests: `internal/app/write_panic_repro_test.go` (`TestWritePanicRepro` — `typed_nil_lsp` failed with the exact production stack before the fix; nil-interface, live-LSP, non-Go, and nil-worktree controls never panicked); `internal/agentic/tool_scheduler_test.go` (`TestToolScheduler_Panic_ReturnsError` now asserts tool name + stack frames in the error).

Residual risk: the *environmental* trigger (gopls failing to start in-session despite the binary existing) is not addressed — but the tool must never panic regardless, which is now guaranteed. Longer-term hardening: `newLSPManager` could return a nil *interface* on failure (eliminating the typed-nil at the source) — deferred, it changes the bootstrap signature.

## 100% CPU / TUI stuck during long write (O(n^2) tool-arg streaming) — FIXED (2026-07-16)
A goa session (pid 55211) pegged one core at 100% for 20+ minutes showing `Calling write...` while the UI lagged minutes behind. The write itself had **succeeded** (file on disk at 21:53; `nettop` showed zero traffic) — the process was grinding purely local render work. The same lag pattern occurs in other sessions with any large streamed tool args (write content, bash heredocs): the common factor is args size × delta count, not the specific tool.

Root cause — O(n²) in the tool-arg streaming path. Providers (kimi/openai) emit one SSE delta **per token**, each carrying the **full accumulated** `ToolInput`. For every delta the single engine command-loop goroutine does O(content) work on the whole accumulated args:
- `json.Unmarshal` attempt on the full (incomplete) args string — always fails mid-stream (`tui/tool_execution.go` `updatePartialArgs`).
- `partialStringFieldRe.FindAllStringSubmatch` over the full args (`updatePartialArgs`).
- `strconv.Unquote` per matched field per delta (~0.5 GB alloc for a 30 KB write).
- per-frame grapheme-width re-measure (`internal/ansi.Width` → `rivo/uniseg`, ~42% CPU cum) and ANSI re-strip (`regexp.ReplaceAllString`, ~0.7 GB alloc) over the whole viewport; header mascot re-render every frame (~14% CPU).

The body memoization (`buildBody`) only guards the body *string*; the parse/regex/width/strip work around it is unguarded. Measured: ~1.7 ms CPU + ~393 KB garbage **per 4-byte delta**; per-delta cost grows linearly with content (1.3→2.3 ms over 30 KB), so at ~36 tok/s the engine falls behind and never recovers.

Evidence: lldb on pid 55211 caught the hot thread in `regexp.FindAllStringSubmatch` ← `updatePartialArgs` ← `SetArgsPartial` ← `tooltracker.onCallDelta` ← `TUI.commandLoop`. CPU profile of the replay shows `madvise` 13% (heap churn), `uniseg` ~20%, `regexp` ~15%, `memmove` 7%. Memory profile: ~2 GB allocated to stream 30 KB.

Repro (currently RED, gating the fix): `internal/app/write_stream_lag_repro_test.go` — replays the real captured 30 KB write (`GOA_WRITE_LAG_ARGS=$(cat /tmp/write_args.json)`) through the full app pipeline. `TestWriteStreamingLagRepro/delta=4B` FAILS at 439,960 ns/content-byte (budget 80,000); `delta=64B` passes, proving the failure scales with delta count. `BenchmarkWriteStreamingFlood`: 393 KB/op, 1851 allocs/op.

Fix (verified): made the per-delta args path incremental and removed the per-delta full-document re-parse, in `tui/tool_execution.go`:
- `updatePartialArgs` no longer runs `partialStringFieldRe.FindAllStringSubmatch` over the whole accumulated args per delta. It uses a hand-rolled incremental scanner (`scanPartialField`/`consumePartialField`, no regexp) that consumes each field once and, for the single still-open tail field, decodes only the newly arrived bytes (`appendOpenValue` + `partialVDone`/`partialValue`), keeping per-delta work O(new bytes).
- The "is it complete yet" `json.Unmarshal` attempt is now gated by `couldBeCompleteJSON` (ends with `}`), so the O(n) parse-that-always-fails no longer runs on every mid-stream delta.
- `tools/writefile_renderer.go` `renderContent` no longer `strings.Split`s the whole content to show a 5-line preview: the collapsed path uses `splitFirstLines` (head only) and counts remaining lines with `strings.Count` + `countTrailingEmptyLines` (no []string materialization).

Verified results (repro `internal/app/write_stream_lag_repro_test.go`, real captured 30 KB write, `GOA_WRITE_LAG_ARGS`):
- engine-loop work: **439,960 → 4,799 ns/content-byte** (~92x).
- whole-stream wall time: **13.2 s → 0.14 s** for 30 KB @ 4 B deltas (7,717 deltas).
- per-delta cost: was 1.30→2.27 ms growing linearly with args size (the O(n^2) signature); now 0.02→0.03 ms, essentially flat.

Tests: `TestWriteStreamingLagRepro` (now GREEN; asserts the algorithmic shape — tail per-delta cost ≤ 4x head — plus a 300,000 ns/B absolute backstop, so it is robust under `-race`), `BenchmarkWriteStreamingFlood`, `tui/partial_args_correctness_test.go` (`TestUpdatePartialArgs_IncrementalMatchesReference` proves the incremental scanner matches the original regex extraction byte-for-byte at every streamed prefix, incl. quotes/backslashes/multi-field), `TestUpdatePartialArgs_PartialPrefixMatchesReference`. Gate green: `go vet`, `go test -race ./tui ./tools`, `go test -race ./internal/app`, `gocognit -over 15`, `gocyclo -over 12` (two flagged functions are pre-existing and unrelated).


## Skill issue — FIXED (2026-07-16)
The agent tried to use a skill but failed: `run_skill` was advertised in `<available_skills>` even when the tool was not registered (inline execution mode, the default).
Root cause: `internal/app/prompt.go` (`availableSkillsSection`), `skills/prompter.go` (`escapeSkills` always emitted `tool="run_skill"`), `prompts/available_skills.md` (hardcoded header), vs. registration gating in `internal/app/subsystems.go` (`registerSkillRunnerIfNeeded`, sub-agent mode only).
Fix: `<available_skills>` rendering is now mode-aware. When `run_skill` is not registered, action skills are listed with their `/skill:run:<name>` invocation and the header no longer mentions `run_skill`.
Tests: `skills/prompter_test.go` (`TestRenderAvailableSkills_NoRunSkillTool`), `internal/app/prompt_test.go` (`TestAvailableSkillsSection_InlineModeNoRunSkill`, `TestAvailableSkillsSection_SubAgentModeRunSkill`).

## tool list — FIXED (2026-07-16)
`agent`, `agent_swarm`, and `goa` tool calls were not part of the tool enable/disable list in `/config`.
Root cause: `tools/registry.go` (`ConfigurableTools`) and `config/config.go` (`ToolEnabledConfig`/`fieldPtr`) lacked the three tools; registration was unconditional (`internal/app/subsystems.go`).
Fix: added `Agent`/`AgentSwarm`/`Goa` flags (opt-out, default `true` in embedded `config/configs/default.yaml`), `ConfigurableTools` entries, registration gating in `registerSubAgentTools` and the goa tool registration, and runtime re-enable cases in `makeToolFactory` (`internal/app/commandcontext.go`, backed by new `subsystems.go`/`swarmState`/`taskBus` fields on `subsystems`).
Tests: `config/config_test.go` (`TestAgentToolsConfigurable`, `TestAgentToolsYAMLRoundTrip`, `TestAgentToolsDefaultEnabled`), `internal/app/subsystems_test.go` (`TestRegisterSubAgentTools_GatedByConfig`, `TestRegisterSubAgentTools_IndependentToggles`).

## Tool call start a review but no output of work done — FIXED (2026-07-16)
A stream canceled while an `agent` tool call's arguments were still streaming rendered a widget implying the sub-agent had started ("Review render loop + compositor perf (coder)", "Took 8.2s") although the tool never executed and no result existed.
Evidence: `.goa/exports/goa-export-20260716-153640.zip` — session/events.jsonl shows ~40 tool-call input deltas then `Error: context canceled`, with no `tool_call` event ever recorded.
History pairing was already safe (`handleStreamFailure` → `resetStreamRoundState` + `undoLastAssistantMessage` in `internal/agentic/`), so no unpaired `tool_use` reaches the provider. The remaining defect was UI truthfulness.
Fix: cancellation finalization now distinguishes "canceled before execution" (arguments never completed → "(canceled before execution — the tool never ran)") from "interrupted while running" in `internal/app/stats.go` (`failPendingTools`) and `internal/tooltracker/tracker.go` (`failIfInflight`).
Tests: `internal/app/bugs_md_streaming_validation_test.go` (`TestBugs_CanceledMidToolCall_LabeledNeverRan`, `TestBugs_CanceledRunningTool_LabeledInterrupted`).

(historical items — see `docs/archive/`)
