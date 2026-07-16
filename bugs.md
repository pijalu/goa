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

(none — all items closed; see Closed Bugs for fix details)

# Closed Bugs

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
