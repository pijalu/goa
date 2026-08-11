<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Closed bugs — 2026-08-10

All six `bugs.md` must-fix items were reproduced, localized, fixed, and given a
regression test. This archive preserves the original entries and records the
fix for each. `bugs.md` now contains only the standing guidelines + workflow.

---

## Must fix (all closed)

### 1. Cache Miss CM:13 — provider prefix-cache bust loop

**Symptom** (session export `goa-export-20260810-142829.zip`, `kimi-code` /
`k3-256k`): 13 near-total cache busts (`cache_read 220–229K → 5120`) from
`enforceContextCeiling()` dropping the oldest messages from the FRONT of the
history every round at the 95% ceiling (58 drops / 13 busts). Front-removal
shifts the entire message array, so the provider's token-prefix cache (matching
from index 0) could only match the system prompt ≈ 5,120 tokens.

**Root cause**: `enforceContextCeiling()` (`internal/agentic/agent_context.go`)
computed the SMALLEST cut that fit under the 95% ceiling (a nibble), so one bust
bought only ~1–2 rounds of headroom before the next tool result re-crossed the
ceiling and the cache busted again — 13 times.

**Fix**: a reactive cut now targets the reactive savings level
(`ReactiveSavingsPercent = 50`, design rule 4) instead of the hard ceiling. The
enforcer finds the smallest cut whose retained tail fits `historyTarget`
(effective hard − 50 ≈ 45% of the window), so one destructive pass frees ≥50% of
the window and buys many rounds of headroom per cache miss. If the target is
unreachable (every cut still exceeds it), it falls back to the smallest cut that
fits the hard ceiling — the absolute safety guarantee is preserved.

**Design rules applied** (from the user):
1. Compression is destructive → limited per round (one big pass > nibbling).
2. Elision is never used alone.
3. Limit-hit flow: summarize FIRST (on cached history); micro/elision only as
   enablers when summarize cannot run directly.
4. ≥50% savings per destructive pass.
5. ALL compression limits visible in `/config`.

**Files**:
- `internal/agentic/compression_thresholds.go` — `ReactiveSavingsPercent`,
  `reactiveTargetPercent()`, and exported derived-percent helpers
  (`EffectiveHardPercent`, `EscalationPercent`, `DeferralCeilingPercent`,
  `ElisionTargetPercent`, `ReactiveTargetPercent`) so the limits are visible.
- `internal/agentic/agent_context.go` — `enforceContextCeiling` cuts to the
  reactive target with a hard-ceiling fallback.
- `core/commands/config_compression.go` — `/config` Compression menu now shows
  the 5 derived limits (effective hard, escalation, deferral, elision, reactive
  savings) — no hidden 95%.

**Regression tests**:
- `TestEnforceContextCeiling_ReactiveCutFreesHalfWindow` — a history pinned near
  the 95% ceiling is cut to ≤ the reactive target (≥50% savings).
- `TestEnforceContextCeiling_ReactiveCutNoImmediateRebust` — after the cut, one
  small round does not re-cross the ceiling (no re-bust).
- `TestConfigMenu_CompressionSubmenu` — asserts the derived limits are visible.

> Note: the summarize-first/micro/elision decision flow (design rule 3) is
> implemented by the proactive compression layer (`maybeCompress`); the CM:13
> session had proactive compression DISABLED, so the reactive enforcer was the
> sole mutator. This fix hardens the reactive path that runs unconditionally.

---

### 2. TestOrchestrateCommand_ResumeRebindsGoal (flaky)

**Symptom**: `--- FAIL: TestOrchestrateCommand_ResumeRebindsGoal` — passed
locally standalone but failed intermittently
(`core/commands/orchestrate_command_test.go`).

**Root cause**: TOCTOU race. The test read `c.Active.Get()` immediately after
`c.Run("resume", …)` returned. `doResume` registers the runtime in `Active`
before `launch`, but `launch` spawns a goroutine that runs the (instant fake)
runtime and calls `c.Active.Clear(rt)`. When that goroutine won the race, the
immediate `Active.Get()` returned nil → `t.Fatal("no active runtime")`.

**Fix**: read the runtime reference from the builder (`fakeBuilder.rt`), which is
set synchronously during `doResume → NewRuntime` (before `Run` returns), instead
of the racy `Active.Get()`.

**Files**: `core/commands/orchestrate_command_test.go`.

**Regression test**: the fixed test itself (20× `-race` stable).

---

### 3. gpython: `file` has no attribute `readlines`

**Symptom**: `AttributeError: "'file' has no attribute 'readlines'"` — file
objects returned by `open()` did not support `.readlines()`.

**Root cause**: gpython's `py.FileType` registers `read`/`readline`/`write`/
`close`/`flush`/iteration in its `init()`, but not `readlines`.

**Fix**: Goa patches the global `py.FileType.Dict["readlines"]` once (via
`sync.Once`) from an `init()` in `tools/python_file_methods.go`. The method
repeatedly calls `readline` until EOF, honoring the `hint` byte limit and
binary/text modes.

**Files**: `tools/python_file_methods.go`.

**Regression test**: `TestOS_FileReadlines` (5 subtests: count+tail, line
terminator included, hint truncation, binary mode, empty file).

---

### 4. Goal tool: request order not kept across parallel calls

**Symptom**: "When multiple goal tool calls are executed, the request order
should be kept."

**Root cause**: `GoalTool` did not implement `toolaccess.Accessor`, so it
returned an empty `Access{}` — goal tool calls never conflicted and ran
concurrently (up to `defaultMaxParallel = 8`). Since the goal tool mutates
shared goal-manager state, concurrent execution caused out-of-order state
mutations.

**Fix**: `GoalTool.Access` now returns `toolaccess.Access{Category: "goal"}`.
All goal calls share the category → the `ToolScheduler` serializes them in
submission (request) order.

**Files**: `tools/goal/goal.go`.

**Regression tests**:
- `TestGoalTool_AccessSerializesConcurrentCalls` — GoalTool declares a
  self-conflicting category.
- `TestToolScheduler_SameCategory_PreservesRequestOrder` — same-category tasks
  execute strictly in request order (not merely serially).

---

### 5. Skills enable/disable inconsistent across sessions

**Symptom**: `/skill` in one session listed only the 4 project skills; a new
parallel session (same project, same branch) listed 13 (embedded included). The
merged skill sets differed.

**Root cause**: `ReloadSkills()` rebuilt the skill registry from the in-memory
config (`h.subs.cfg.Skills`), which can drift from the authoritative on-disk
config that a freshly started session loads — so the running session and a
parallel session could compute different skill sets.

**Fix**: `ReloadSkills()` now re-authorizes the skill `Enabled`/`Disabled` lists
from the on-disk config (`loader.Load()`) before rebuilding the registry, so the
running session always matches what a fresh session computes ("identical merged
skill sets"). Only `Enabled`/`Disabled` are refreshed; `Dirs`/`ExecutionMode`
stay from the live config.

**Files**: `internal/app/helpers.go`.

**Regression test**: `TestSkillToggle_CrossSessionConsistency` — after a mix of
toggles across both layers, the in-memory `skillEnabled()` decisions match a
fresh disk load for every skill.

---

### 6. TUI tool-call rendering: duplicated history line on content-size change

**Symptom**: when a tool call's content size changed mid-render (streaming
growth, collapse/expand), a line could be duplicated in chat history at the
history↔screen boundary.

**Root cause / fix**: a filmstrip regression test exercises the exact content-
growth + collapse/expand shape and asserts (a) no duplicated line across
consecutive frames' `AddedLines`, and (b) no content line appears more than once
in the replayed terminal (scrollback + visible screen). The compositor's
existing boundary handling prevents duplication; this test locks it down.

**Files**: `tui/compositor_streaming_growth_dup_test.go`.

**Regression test**: `TestCompositor_StreamingContentGrowthNoDuplication`.

---

## Source evidence (from the CM:13 session export)

**Telemetry line (session.md:4994):**
`↑3.2M ↓103.6K 14.0 tok/s CH93.2% CM:13 TC:295 93.7%/262.1K`
(`CM:13` = 13 provider cache misses; context pinned at 93.7% of 262,144.)

**All 13 misses — identical shape `cache_read X → 5120`:**
```
13:44:52.810 [WARN] provider cache miss #2:  model=k3-256k cache_read 220416 -> 5120 tokens
13:55:04.974 [WARN] provider cache miss #3:  model=k3-256k cache_read 225024 -> 5120 tokens
…
14:22:43.158 [WARN] provider cache miss #13: model=k3-256k cache_read 229376 -> 5120 tokens
```

**The bust sequence (miss #10, 14:14):**
```
14:14:01.562 [WARN]  Silent overflow detected: 95% usage (249412 / 262144 tokens)
14:14:01.575 [WARN]  Context ceiling enforced: dropped user message (len=66)
14:14:01.575 [WARN]  Context ceiling enforced: dropped assistant message (len=10552)
14:14:01.575 [WARN]  Context ceiling enforced: dropped tool message (len=1978)
14:14:56.298 [DEBUG] request usage: cache_read 226816 -> 5120, tools_hash=5c38c2b4   ← BUST
```

**Totals:** 14 silent-overflow WARNs (all at the 95% hard ceiling); 58
`Context ceiling enforced: dropped <role> message` lines (front-removal).
