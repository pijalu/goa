<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug/Feature Tracking — Archive 2026-07-26

## ✅ BUG: Thinking (reasoning) tokens do not count as "content" for the forced-answer nudge — model gets interrupted mid-task despite actively reasoning

**Observed:** In session `1785054839_zg874a2v`, the model's own thinking tokens reveal:

> "the instruction says to produce the final answer from information already gathered or state what's missing. This is a host control note, so I must comply: I need to wrap up this turn with an answer."

The forced-answer nudge fires because the consecutive tool-round counter reaches 15 — but the model was **actively reasoning** in every one of those rounds. Thinking tokens (State=1) flow into `thinkingBuf`, NOT `contentBuf`. The guardrail (`trackToolCallingRound` → `consecutiveToolRounds`) only checks `contentBuf.Len() > 0`, so rounds that produce thinking + tool calls + no visible text are counted as "tool-only" and increment the streak.

**Result:** The model is told to "produce the final answer now" while it is demonstrably making progress (reasoning → tool calls → reasoning → tool calls). The user must type "continue" to resume. The model itself recognises the conflict:

> "The user asked to continue until completion, but the host control note requires me to produce a final answer now."

**Fix applied:** `trackToolCallingRound` in `internal/agentic/agent_streaming.go` now checks both `contentBuf.Len() > 0` AND `thinkingBuf.Len() > 0` when deciding whether to reset the consecutive tool-round counter. If the model emitted reasoning tokens in a round, that round is not "idle" and resets the streak.

**Tests:**
- `TestTrackToolCallingRound_ThinkingResetsStreak` — thinking tokens reset the streak
- `TestTrackToolCallingRound_NudgeNotFiredWithThinking` — nudge does NOT fire when thinking is present
- `TestTrackToolCallingRound_NudgeFiredWithoutThinking` — nudge DOES fire for truly silent rounds
- `TestConsecutiveToolRounds_NudgeFiresOncePerTurn` — regression: existing once-per-turn behavior preserved

## ✅ BUG/UX: Host control notes injected to the model are invisible to the user — no bubble/notification

**Observed:** The forced-answer nudge (#1), recovery hint (#2), and premature-stop auto-continue (#4) are all injected as ephemeral system messages:

```
[goa-system] Internal control note (never show or mention to the user): ...
```

These messages carry a deliberate instruction to the model ("do not reference this note"), but they are **completely invisible to the user**. When the model stops mid-task, the user sees no explanation — only a turn ending with a summary/checkpoint. The user has to infer what happened and type "continue" blindly.

In the session log, the model's thinking reveals it is reacting to these notes, but the user sees none of this.

**Fix applied:** `InjectEphemeralSystemMessage` in `internal/agentic/agent.go` now emits an `EventProgress` with user-friendly text ("System guardrail: model told to wrap up or adjust behavior.") when the injected message starts with `[goa-system]`. The event is transient (not part of conversation history) and shows in the TUI status area.

**Tests:**
- `TestInjectEphemeralSystemMessage_EmitsProgressEvent` — control note emits progress event
- `TestInjectEphemeralSystemMessage_NoProgressEventForNonControl` — non-control messages do NOT emit progress
- `TestEphemeralSystemMessage_NotEmittedAsContentEvent` — regression: no EventContent for control notes
- `TestUIScenario_GuardrailNotificationVisible` — TUI filmstrip: guardrail text visible in status area, no `[goa-system]` in chat viewport

## ✅ BUG/FEATURE: `MaxConsecutiveToolRounds` is not configurable via `/config` — hardcoded default of 15 cannot be changed or persisted

**Observed:** The forced-answer nudge fires after 15 consecutive tool-only rounds (the default of `MaxConsecutiveToolRounds` in `internal/agentic/agent_streaming.go:208-216`). This threshold is hardcoded:
- `internal/agentic/agent.go` `Config.MaxConsecutiveToolRounds` field exists
- `effectiveMaxConsecutiveToolRounds()` defaults to 15 when the config value is 0
- But there is no `/config` command to set it, no config-file key to store it, and no persistence mechanism

**Result:** Users who work with thinking-heavy models (DeepSeek, Qwen, Claude with extended thinking) hit the nudge repeatedly but cannot raise or disable the threshold without modifying source code and recompiling.

**Fix applied:**
1. Config file key: `execution.max_consecutive_tool_rounds` (integer, default 15, 0=disabled) in `config/config.go` and `config/configs/default.yaml`
2. Env var: `GOA_EXECUTION_MAX_CONSECUTIVE_TOOL_ROUNDS` (auto-discovered by cascade loader)
3. CLI flag: `--max-consecutive-tool-rounds` in `internal/app/bootstrap.go`
4. Runtime command: `/config:set execution.max_consecutive_tool_rounds=N` (persists via `core/commands/config_cli.go`)
5. Wired into agent config at session start in `core/agentmanager_lifecycle.go`
6. Tab-completion in `core/commands/config_completion.go`
7. Also fixed pre-existing bug: `MaxStreamRounds` and `MaxConsecutiveToolRounds` were missing from `mergeExecution` in `config/config_merge.go`, so YAML config values were silently ignored

**Tests:**
- `TestCascadeYAML_MaxConsecutiveToolRounds` — YAML config loads correctly
- `TestCascadeEnvNested_MaxConsecutiveToolRounds` — env var loads correctly
- `TestCascadeCLIOverride_MaxConsecutiveToolRounds` — CLI flag loads correctly
- `TestMaxConsecutiveToolRounds_ZeroDisables` — 0 disables the guardrail
- `TestMaxConsecutiveToolRounds_CustomThreshold` — custom threshold fires at configured value
- `TestEffectiveMaxConsecutiveToolRounds_DefaultRaised` — regression: default is 15
