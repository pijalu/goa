<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bugs closed 2026-07-21 (session 3 — goal injection design A, cache-hit-first, ESC, quota, timeout, title)

All items reproduced/localized, fixed (or verified working), and validated
with guideline #6 gates (vet, staticcheck, gocognit -over 15, gocyclo -over
12, go test -count=1 -race -cover — each separately). Full-repo -race suite
green. LM Studio end-to-end validation via logging proxy byte-diffs.

# Open TODO

## ESC must cancel a running tool exec / bash

**Reported 2026-07-21.** Pressing ESC while a bash/tool call is running
should cancel the execution.
- Existing chain (code read): ESC → `Editor` fires `OnEscape`
  (tui/editor_input.go:67-78, only history-search consumes it first) →
  `App.handleEscape` (internal/app/tui.go:168) → `AgentManager.Interrupt()`
  (core/agentmanager.go:467) cancels the turn ctx →
  `BashTool.ExecuteContext` ctx.Done branch (tools/bash.go:208-212) →
  `killBashProcessTree` SIGKILLs the process group → result "cancelled".
  Tool-level kill proven working (3s bound incl. background children).
- VERIFIED 2026-07-21 — no code defect; the chain works end-to-end:
  1. ESC reaches the handler while a tool runs: regression test
     internal/app/ui_escape_test.go
     (TestEscapeReachesInterruptWhileToolRuns) drives the app into a
     running-bash state and asserts SendKey("escape") fires OnEscape
     exactly once — GREEN. The uiScenario harness gained an `editor`
     field for key-injection tests.
  2. Kill mechanics: tools/bash_test.go
     TestBashTool_ExecuteContext_CancelInterruptsLongCommand (real
     `sleep 30`, cancelled in 300ms, prompt return) — already covered.
  3. Non-bash paths: handleEscape also calls ptyMgr.Cleanup() and
     stopBackgroundProcesses() (bg_exec StopAll + LSP shutdown)
     (internal/app/tui.go:173-178) — covered.

## z.ai quota: unit:6 window mislabeled "1d window" — it is Weekly

**Reported 2026-07-21.** /quota shows the z.ai TOKENS_LIMIT window as
"1d window"; it should be weekly.
- Live API capture 2026-07-21 (level=pro): `{"unit":6,"number":1}` resets
  +63.6h out — impossible for a 1-day window, exact for a weekly cycle
  (start 2026-07-17 08:56, reset 2026-07-24 08:56). `{"unit":5,"number":1}`
  (web-search credits) resets +28.7d out — monthly cycle.
- Root cause: `windowPeriodMs`/`windowLabel` in
  plugins/bundled/provider-quota/fetchers/zai.js mapped unit 6 = days
  (guess from the earlier harness which used number:7, accidentally landing
  on 7d = "Weekly").
- FIXED: unit 3 = hours, 5 = months (30d), 6 = weeks; labels Weekly /
  Monthly / Nw / Nmo / Nh. Harness updated to the exact live shape
  (unit:6/number:1) asserting "Weekly", "Monthly", and NOT "1d window".

## Goal slots rejected by LM Studio: "System message must be at the beginning"

**Reported/confirmed 2026-07-21** (LM Studio server log, qwythos-9b-v2):
HTTP 400 `Jinja Exception: System message must be at the beginning` — every
goal turn fails and goa reports the misleading "Paused after provider
connection error".
- Proxy capture (127.0.0.1:18234→1234) proved `mergeGoalProgress` injected
  `[goal]`/`[goal progress]` as SYSTEM-role messages mid-conversation; strict
  chat templates reject any system message after the first.
- FIXED (design A, persist-per-turn, kimi-code parity — user decision):
  `persistGoalReminder` (internal/agentic/agent_streaming.go) appends the
  static reminder + dynamic progress snapshot as ordinary USER-role history
  messages ONCE per turn (runInternal, right after the user message);
  `mergeGoalProgress` is a passthrough. Reminders are append-only history,
  frozen per-turn snapshots; after /goal cancel no new ones are appended
  (accepted trade-off: old text remains, superseded by the engine's explicit
  "Goal cancelled" history note).

## CH (cache-hit) rate drops — request sequence must be strictly append-only

**Reported 2026-07-21** (status-bar CH% falls during goal work). Three
distinct busts found and fixed, each proven by proxy capture byte-diffs:
1. Goal slots as mid-conversation system messages (see above) → HTTP 400.
2. Per-round volatile slots rewriting request bytes mid-history → prefix
   cache never matched beyond index 2 (19.8K tokens re-sent for a 6.4K
   request). Fixed by design A above.
3. **Moving cache_control breakpoint** (the hidden one): the OpenAI-compatible
   layer stamped `cache_control: ephemeral` on the LAST message every round —
   identical text, marker moved → llama.cpp longest-prefix match died at that
   message. Production path was protocol/openai_completions.go
   (`applyOpenAICacheControl`); a duplicate in provider/openai/cache_control.go
   had the same flaw. FIXED both: the conversation marker is pinned to the
   FIRST user message (opening turn, fixed forever).
- FINAL VALIDATION (fresh binary, LM Studio qwythos-9b-v2, goal run with 9
  tool calls): req_52→54→55 are STRICT APPENDS (+assistant/+tool only);
  system prompt, tools, history, goal reminders, cache marker all
  byte-identical. Goal completes; no 400.
- Regression tests: protocol/protocol_test.go
  (TestOpenAICompletionsCacheMarkerPinnedAcrossRounds),
  openai/cache_control_test.go (pinned marker + byte-prefix across rounds),
  agent_goal_test.go (per-turn persistence, destroy keeps prefix, no system
  role).
- Design guideline #9 (cache-hit-first) added to this file's guidelines.
- Follow-up (not a regression; interactive-only path never exercised live):
  /goal start's autonomy switch goes through `AgentManager.SetMode` →
  `ModeManager.SetMode` (core/modemanager.go:61-71), which returns
  change-info even for no-op/autonomy-only sets → `queueMajorModePrompt`
  (agentmanager.go:912) → `injectModePrompt` (:673) appends a full mode
  body as a persistent history message. Under design A this is an append
  (cache-safe), but it is history churn per goal start — worth gating on a
  real Major-mode change when the interactive path is next touched.

## Tool-call timeout display: widget "elapsed" overruns the declared timeout

**Reported 2026-07-21.** Tool widget shows `(timeout 120s)` but `elapsed 213.4s`
(twice: 213.4s / 213.0s) — user conclusion: "tool call timeout does not work".

### Investigation (localized)
- Tool-level timeout is CORRECT: `BashTool.runCommand` (tools/bash.go:213-216)
  fires `time.After(timeoutS)` → `killBashProcessTree` (SIGKILL to the process
  group, tools/bash_unix.go:25-32). Reproduced with the exact failing command
  shape (`go test -v -run TestSQLite_ ... | grep | head`, timeout 20s, frigolite
  workdir): killed at 20.00s. `sleep 300` + background children: killed at
  3.00s. The timeout itself works.
- The DISPLAYED elapsed is the bug: the widget timer starts at widget CREATION
  (`tui/tool_execution.go:197`, `NewToolExecution` from chat_viewport.go:682 on
  the first — possibly streaming-delta — tool-call event), not at execution
  start. `ToolRunning` is only set when the non-delta call event arrives
  (internal/app/stats.go:574-575). Elapsed renders as
  `time.Since(tc.startTime)` (tool_execution.go:329).
- So elapsed = arg-streaming time + permission/queue wait + execution. With a
  slow local model (LM Studio 9B) streaming a tool call can take tens of
  seconds; the timeout bounds only the execution phase. 213s ≈ ~93s
  pre-execution + 120s bounded execution.

### Fix plan
1. `ToolExecutionComponent.SetStatus`: on the transition INTO `ToolRunning`
   (old != ToolRunning), reset `startTime = time.Now()` so the displayed
   elapsed measures execution only and can never exceed timeout+ε on screen.
2. Update the `startTime` field comment to document the semantics.
3. Tests (`tui/tool_execution_test.go`): create widget, backdate startTime,
   SetStatus(ToolRunning) → elapsed restarts near zero; SetStatus on already
   running → timer keeps running (no reset).
4. Validate: filmstrip-style render check; guideline #6 gates separately.

## Terminal title: keep static hexagon during activities (no spinner)

**Reported 2026-07-21.** User directive: disable the terminal title spinner
during activities; keep the normal hexagon title. The `tui.animated_title`
feature (default true) animates the title with spinner frames while working.

### Fix plan
1. Flip `tui.animated_title` default to false in config/config.go (feature
   stays opt-in; startup sequence g⬡a → g⬡ → ⬡ is separate and unaffected).
2. Update title tests + config completion description for the new default.
3. Validate: guideline #6 gates separately.
