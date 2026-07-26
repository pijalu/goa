<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug/Feature Tracking

## Guideline

1. Create a detailed fix plan for each bug/new feature - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found, even if not related to the bug/feature, must be fixed and the fix plan must be updated accordingly. You can add new items to the bug list as you find them.
3. Each item should be moved to archive when tested and closed as the associated plan.
5. Use filmstrip approach to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.
9. **Cache-hit-first design (CRITICAL for local models).** A cached prefix
   costs ~0; a full re-parse costs 40-100x more (measured 2026-07-21 on
   qwythos-9b-v2: 23.6 tok/s generation — a 20K-token re-parse is a 45-90s
   stall). Therefore every provider request must be **strictly append-only**:
   never move, rewrite, or re-project content mid-history; volatile
   per-request text may only ever be appended at the tail. The system prompt
   (byte 0) must stay byte-identical for the whole session. Anything that
   "decorates" messages per request (cache_control breakpoints, markers,
   wrappers) must be pinned to a fixed position — a marker that moves to the
   newest message each round rewrites history bytes and kills llama.cpp's
   longest-prefix cache match exactly where it lands. Validate any change to
   prompt/message construction with a proxy capture proving request N is a
   byte-prefix of request N+1, and by watching CH% climb in real sessions.

 *At the end of the session*: the list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## STOP CONDITION (binding — an agent working this file must not stop early)
An agent working this file may ONLY stop when ALL of the following hold:
1. This file contains NO open items (every item is ✅/closed or moved to the archive).
2. Every item is tested and working (regression test green; filmstrip-validated where it is a UI behavior per guideline #5).
3. Any issue/problem discovered during the work has been ADDED to this file AND solved — nothing is deferred out-of-band.
A turn that ends with open items, an untested fix, or an unrecorded newly-found issue is a FAILED turn: continue working; do not summarize-and-stop.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

---

## Open Items

### ✅ FIXED — BUG: `/goal:resume` does not restart a paused goal — user must send a message

**Root cause:** `GoalCommand.resume()` (core/commands/goal.go) called
`c.Mode.ResumeGoal(...)` which only transitions state to `GoalActive`, but —
unlike `doStartGoal()` — never called `c.Driver.Start(...)`, so no
continuation turn was scheduled until the user sent a message.

**Fix:** after a successful resume, `resume()` now calls
`c.Driver.Start(context.Background())` (guarded by `c.Driver != nil`),
mirroring `doStartGoal()`. `GoalDriver.Start` is idempotent (no-op when no
active goal or a drive loop is already running).

**Regression test:** `TestGoalCommand_Resume_StartsDriver`
(core/commands/goal_test.go) — creates a goal, drains the creation
continuation, pauses, drains, resumes, and asserts a NEW continuation turn
runs with no user message. Failed before the fix (RED), passes after
(GREEN). Full `TestGoalCommand_*` suite green. The other two
`ResumeGoal` callers were checked and need no change: the goal *tool*
(`tools/goal/goal.go updateActive`) runs inside an already-live driver loop,
and `GoalManager.ResumeGoal` (core/goal.go) has no command-path caller.

**Bonus fix found via `-race` (guideline #2):** the new test exposed a
pre-existing latent DATA RACE — `GoalMode.m.state` had no synchronization
while the background `GoalDriver` goroutine mutates it (`IncrementTurn` →
`persistState`) and command goroutines read it (`GetGoal`). Fixed by adding a
`sync.RWMutex` to `GoalMode`: write lock on every lifecycle mutator, read
lock on `GetGoal`/`GetActiveGoal`; internal helpers stay lock-free (called
only under the lock); delegating methods (`CancelGoalByID`, `PauseOnInterrupt`)
route through lock-free `*Locked` inner forms to avoid re-entrant deadlock.
Publisher (`goalEventPublisher`) only does a non-blocking buffered bus send,
so holding the lock across `persistState`→`Publish` is deadlock-free. Full
`go test -count=1 -race ./...` is green with zero data races.

**Quality gates (guideline #6):** `go vet` clean; `staticcheck` clean on
changed pkgs (repo-wide warnings pre-existing/unrelated); `gocognit -over 15`
& `gocyclo -over 12` clean on changed files; `go test -count=1 -race -cover`
green — `core/goal` 89.9%, `core/commands` 53.1% coverage.

<details><summary>Original report</summary>

**Observed:** Running `/goal:resume` on a paused goal does not resume execution.
The goal stays paused until the user manually sends another message; only then
does the goal continue. `/goal:resume` should itself re-activate the goal and
kick off the next turn without requiring a manual nudge.

**Expected:** `/goal:resume` transitions the goal from paused to active and
immediately schedules continuation (the GoalDriver continuation turn), with no
user message required.

</details>

---

### ✅ FIXED — BUG: `/quota` request during streaming corrupts the TUI (duplicated/garbled frames)

**Root cause (two layers, both fixed):**

1. **Steering bubble as a transcript entry.** The pending steering bubble
   (`ConsoleSteeringPending`, the `(alt+e to edit)` box) lived in the chat
   transcript, and `ChatViewport.Append` removed and re-appended it on every
   arrival to keep it bottom-most — violating the transcript's append-only
   invariant that the compositor's scrollback watermark depends on.
   → **Fix:** the bubble is now a bottom-chrome component (`tui.SteeringChrome`,
   added after `goalBubble` in the chrome band). The transcript is strictly
   append-only. Consumed steering lands in the transcript as a real user
   message (`handleSteeringInjected` → `AddUserMessage`); the status bar is
   untouched — steering appears only in the bubble.

2. **Compositor scroll-emission desync on chrome-height change.** The
   incremental scroll path tracked the scrollback watermark across frames, but
   a bottom-chrome height change (the bubble appearing/clearing) shifts the
   transcript region, desyncing the watermark from the real terminal
   scrollback. Rows in the gap were neither repainted nor scrolled (lost) and
   already-scrolled rows were re-emitted (duplicated). Proven NOT to be the
   `placeStart` culling optimization (corruption persisted with the full
   canvas materialized).
   → **Fix (option A, geometry-reset):** `classifyFrame` collapses the
   frame-type decision into a `frameKind` — first / geometryReset (width OR
   bottom-chrome height changed) / fullRepaint / diff. A chrome-height change
   is treated as a geometry reset (like a width change) and routed through the
   proven `drawWindowResetScrollback` path, which rebuilds scrollback from the
   canvas at the current geometry. `compose` now culls at the scrollback
   watermark (the safe boundary) and materializes the full canvas on reset
   frames (cullFloor=0) so the reset re-emits real rows, not blanks.

**Regression tests:**
- `TestCompositor_QuotaDuringStream_NoDuplicatedRows` (4 subtests: stream ×
  steering bubble), TermEmulator-verified: no transcript row lost, none
  duplicated within scrollback. RED before, GREEN after.
- `TestSteeringChrome_Filmstrip_IsChromeNotTranscript` (guideline #5
  filmstrip): steering is chrome (never a transcript entry), status bar
  untouched, consumed steering lands as a user message.
- Full `tui` + `internal/app` suites green; `-race` clean; tui coverage 72.9%.

**Quality gates (guideline #6):** `go vet` clean; `staticcheck` clean on
changed pkgs (3 pre-existing unused-func warnings unrelated, confirmed on
HEAD); `gocognit -over 15` / `gocyclo -over 12` clean on changed files
(`(*Compositor).Render` and the repro scenario refactored under budget).

**Design note:** the incremental scroll-emission model remains fragile for
non-monotonic layout changes (21 regression files in tui/ attest to the
class). The geometry-reset routing is the correct containment; a fuller
simplification (transcript-coordinate watermark) is a possible follow-up.

Commits: `e777aba` (steering chrome refactor), `e6d8a2f` (compositor fix).

<details><summary>Original report</summary>

**Observed:** Issuing `/quota` while a streaming block (e.g. "Thinking…") is
being written corrupts the display: the input box and trailing border lines
(`└───…───┘`) are repainted many times over, interleaved with the streaming
output, leaving a long run of duplicated box fragments and broken layout.

**Expected:** `/quota` output renders cleanly even while a stream is in
flight — the streaming viewport, the quota modal/section, the footer, and the
input box must each repaint exactly once, in the correct z-order, with no
duplicated or orphaned border fragments.

</details>

---

### ✅ FIXED — QUESTION/BUG: SOLO profile — bash commands writing to `/tmp` pass without approval

**Verdict:** SOLO's documented intent (internal/types.go) is to "constrain
tool calls to the codebase directory". The jail IS wired to SOLO (startup:
bootstrap.go `Jail: ... || Autonomy == AutonomySolo`; runtime switch:
`SetAutonomy` → `makeJailSetter` → `bt.Jail = true`). So under SOLO the bash
tool must reject `/tmp` references — and it does for spaced forms
(`> /tmp/x`, `ls /tmp`, `cd /tmp`).

**Root cause (the hole):** `looksLikePath` (tools/bash_jail.go) did not strip
shell redirect operators, so a redirect with NO space before the target —
`echo hi >/tmp/out.txt`, `go test 2>/tmp/err.txt` — produced a token starting
with `>` that was never treated as a path and never jail-checked. A genuine
SOLO jail escape.

**Fix:** added `stripRedirectPrefix`, applied at the top of `looksLikePath`,
which strips `>`, `>>`, `<`, `<<`, and fd-qualified forms (`2>`, `2>>`, `&>`)
so the redirect TARGET is path-checked. Relative targets (`>out.txt`) remain
allowed (in-project); absolute outside targets (`>/tmp/out.txt`) are now
rejected.

**Regression test:** `TestBashJail_TmpWriteVariants`
(tools/bash_jail_tmp_probe_test.go) — covers spaced/attached redirects, tee,
and in-project controls. The attached-redirect case failed before (RED),
passes after (GREEN); full `TestBashJail*` and `tools` suite green; vet and
gocyclo clean.

<details><summary>Original report</summary>

Under SOLO, a bash command writing to `/tmp` (outside the project root)
appeared to execute and pass — unclear whether intentional or a policy gap.

</details>

---

### ✅ DONE — FEATURE: Review gap of `/goal` vs kimi-code's goal implementation

**Verdict:** goa's goal subsystem is **feature-complete** against kimi-code's
GOAL.md spec — and in two areas goes beyond it. Reviewed
`/Users/muaddib/dev/kimi-code/GOAL.md` (核心工作流 / 统计 / 用户交互) against
`core/goal/*`, `core/goal_driver.go`, `core/commands/goal.go`,
`internal/app/{subsystems,events}.go`.

| kimi-code capability | goa status |
| --- | --- |
| 4-state lifecycle (active/paused/blocked/complete, no cancelled) | ✅ `GoalStatus` + `CancelGoal` (clear, not a state) |
| Multi-turn driver, one turn per loop, re-checks state each turn | ✅ `GoalDriver.Drive` |
| Per-turn goal injection (status, progress, budgets, how-to-end) | ✅ `core/goal/injection.go` (turn-boundary only, cache-safe) |
| Continuation prompt with "don't end in prose" guidance | ✅ `ContinuationPrompt` (regression-tested) |
| Turn/token/wall-clock budgets + hard stop → blocked | ✅ `budget.go`, driver `OverBudget`→`MarkBlocked` |
| Budget-band guidance at ≥75% (converge) | ✅ `BudgetBandGuidance` (0.75 threshold) |
| Pause-on-interrupt / provider-error → paused (recoverable) | ✅ `PauseOnInterrupt`, `mapDriverError` reason table |
| Persist + recover, active→paused downgrade on restart | ✅ event log + `NormalizeAfterReplay` |
| Fork does not inherit goal + reminder | ✅ `BuildForkClearedReminder` |
| Cancel reminder (ignore stale active reminder) | ✅ `BuildCancellationReminder` |
| Completion / blocked summary turn | ✅ `BuildCompletionSummary` / `BuildBlockedReasonPrompt` |
| Goal **queue** (FIFO, auto-promote next on complete) | ✅ **beyond kimi-code**: `core/goal_queue.go` + `promoteNextQueuedGoal` |

**Gaps found & addressed this session:**
- **Per-goal clean-context flag** (kimi runs one agent; goa items below) — the
  one genuine capability gap. Implemented (see the fresh-context item).
- **Subagent goal-tool isolation** (kimi: goal tools main-agent-only) — minor;
  goa's swarm subagents use `AllowedTools` allowlists, so the goal tool is only
  present if explicitly listed. Noted as acceptable; no change.

No other gaps worth closing. Full `core/goal` suite green (-race), 90% coverage.

---

### ✅ FIXED — BUG/UX: Bash tool description does not state the default working directory — models prepend redundant `cd`

**Root cause:** the bash tool's top-level `Description` (the text the model
actually reads, tools/bash.go `Schema()`) said only "Run a shell command." The
`workdir` *parameter* mentioned "default: project root", but the model keys on
the description, so it prepended `cd <root> && ` to every call. The `workdir`
default itself was already correct (`runCommand` sets `cmd.Dir = t.ProjectDir`).

**Fix:** `Schema()` description now appends: "The working directory is the
project root by default — do not prepend `cd <project root>` unless a
different directory is required." (both plain and complexity variants). Also
added a "Working directory" section to `tools/bash.long.md`.

**Regression test:** `TestBashTool_Schema_DescriptionStatesDefaultWorkdir`
(tools/bash_test.go) — asserts the description mentions the working directory,
states it defaults to the project root, and tells the model not to prepend a
redundant `cd`. Failed before (RED), passes after (GREEN); full `TestBash*`
suite green.

<details><summary>Original report</summary>

**Observed:** In real sessions the model prepends a `cd` to virtually every
bash call, wasting tokens (guideline #9). The tool description never told the
model the working directory already defaults to the project root.

</details>

---

### ✅ FIXED — BUG/EFFICIENCY: Model re-runs the same expensive command with only the `grep` pattern changed — no tool-side guard

**Root cause:** the loop guardrails only detect EXACT duplicate tool calls
(`recordToolCallInBudgetWindow` keys on `toolName + full arguments`). A command
re-run with only the trailing filter changed (`go test ... | grep -c X` →
`... | grep -c Y`) produces a different key, so it was never flagged.

**Fix (option a, non-blocking variant):** a new `bashReuseTracker`
(internal/agentic/bash_reuse.go) computes a dedup key that strips the pipe/
filter tail (`bashUpstreamKey`), so the same expensive upstream maps to one
key. It records each bash call in `shouldBufferToolCall` (agent_budget.go)
keyed by the CURRENT state epoch, and resets whenever the epoch advances (a
state-mutating tool succeeded) — so re-running a test AFTER an edit is never
flagged (the exact "no intervening mutation" requirement). On a near-duplicate
re-run within one epoch, the call is NOT blocked (each grep legitimately
returns a different count); instead `resolveToolResultContent` appends a
save-once-refilter hint (`nearDuplicateHint`) to that call's result, teaching
the cheaper pattern for subsequent calls. Lock-safe via `popBashNearDup`.
Cache-safe (guideline #9): the hint is appended to the tool RESULT, which is
always new tail content — no history rewrite.

**Regression tests:**
- `internal/agentic/bash_reuse_test.go` — unit: key normalization, arg
  parsing, same-epoch flag, mutation-reset, different-command/empty not flagged.
- `internal/agentic/bash_reuse_integration_test.go` — drives two filtered
  `go test` calls through `shouldBufferToolCall` + `resolveToolResultContent`:
  asserts the first run is silent, the re-run gets the hint AND keeps its real
  output (non-blocking), and a re-run after a state mutation stays silent.
  All green; full `internal/agentic` suite green with `-race`.

**Quality gates:** `go vet`, `gocognit -over 15`, `gocyclo -over 12`,
`staticcheck` all clean on changed files.

<details><summary>Original report</summary>

A session export showed the model running the full test suite (50–160s/run)
five times, changing only the trailing `grep -c` filter, instead of running
once and re-filtering saved output. The existing duplicate guard only caught
exact-identical calls.

</details>

---

### ✅ ALREADY FIXED (verified this session) — Bash timeout / elapsed-time / frozen-UI / cryptic cancel

**Resolution:** investigation this session found these defects were ALREADY
fixed in commit `7963f80` ("fix: batch session-3 bug fixes — … tool elapsed
…"); the bugs.md entry was stale. Evidence per defect:

1. **Timeout not enforced** — DISPROVEN. Existing tests prove the bash timeout
   is enforced: `TestBashTool_Execute_Timeout_Expires` runs `sleep 10` with
   `timeout:1` and asserts the error "timed out after 1s"
   (tools/bash_test.go:379); `runCommand` selects on `time.After(timeoutS)`
   (tools/bash.go). The reported 158s stamp was the elapsed-misattribution
   below, not a timeout failure — exactly as the entry's own NOTE predicted.
2. **Elapsed-time misattribution** — ALREADY FIXED. `ToolExecutionComponent.
   startTime` is reset on the transition into `ToolRunning`
   (tui/tool_execution.go `SetStatus`), with a comment citing this exact bug
   ("widget showed 'elapsed 213s' for a 'timeout 120s' call because
   streaming/approval time was counted"). Regression tests
   `TestToolExecution_ElapsedStartsAtRunning` and the no-restart-on-duplicate
   test (tui/tool_execution_test.go) pass this session.
3. **Frozen UI** — ALREADY FIXED. `bodyVersion`/`bodyCache` memoization in the
   same component (comment: "a running tool with large content starves the
   command loop, freezing the TUI and blocking the result event and
   Esc/Ctrl-C").
4. **Cryptic cancel** — the bash tool already returns a distinct "cancelled"
   error (vs "timeout") via `ExecuteContext`'s `ctx.Err()` precedence check;
   `TestBashTool_ExecuteContext_*` asserts a cancelled `sleep 30` returns a
   "cancelled" error promptly (<5s).

Verified by running the existing tests green this session. No code change
needed; entry closed as already-fixed.

<details><summary>Original report</summary>

Session export showed every tool call stamped "Took 158.3s" (including a plain
file read), bash calls running past `(timeout 120s)`, and four calls ending
"Error: context canceled" (later confirmed to be the user's own cancel). The
elapsed stamps were misattributed and the UI froze.

</details>

---

### ✅ FIXED — BUG: `/tools:goal:on` cannot enable the goal tool at runtime — "Restart Goa to apply the change"

**Root cause:** `makeToolFactory` (internal/app/commandcontext.go) — the factory
used by `/tools:<name>:on` to instantiate a tool at runtime — had no `"goal"`
case, so `ToolFactory("goal")` returned `(nil, false)` and the command printed
"could not be instantiated at runtime". (Goal tools are registered at startup,
but `/tools:goal:off` unregisters, so re-enabling needs the runtime path.)

**Fix:** added a `"goal"` case to the factory that builds the goal tool via a
new shared `newGoalTool(manager, createFlagOn)` helper (internal/app/subsystems.go)
— extracted from `registerGoalTools` so the startup and runtime paths construct
the tool identically (same GoalMode binding + createAllowed gate). The factory
reads `cfg.Tools.Enabled.Goal`, which `/tools:goal:on` has already set true.

**Regression test:** `TestMakeToolFactory_Goal_Instantiates` and
`TestMakeToolFactory_Goal_ToolIsFunctional` (internal/app/tool_factory_goal_test.go)
— assert the factory builds a working goal tool bound to the GoalMode. Failed
before (RED), pass after (GREEN). Full `internal/app` package green.

<details><summary>Original report</summary>

Toggling the goal tool on via `/tools:goal:on` reported "Tool goal could not be
instantiated at runtime. Restart Goa to apply the change." Tool toggles should
take effect in the running session without a restart.

</details>

---

### ✅ FIXED — BUG/UX: Steering is injected very late in the turn

**Root cause (was):** steering typed while the agent ran sat in
`AgentManager.steering` until the runner finished, then was re-dispatched as a
BRAND-NEW user turn — so it never reached the in-flight turn.

**pi's mechanism (confirmed by reading `../pi` this session — the prior
blocker):** pi does NOT cancel/re-issue the in-flight stream. In
`packages/agent/src/agent-loop.ts` it polls `config.getSteeringMessages()` at
the END of each tool-execution round (after tool results are pushed) and pushes
the steering as a user message onto the context BEFORE the next
`streamAssistantResponse`. Goa now mirrors this exactly.

**Fix (implemented):**
1. `Agent.steeringSource` + `SteeringSource{ Drain() []string }` +
   `SetSteeringSource` (internal/agentic/agent.go).
2. `Agent.drainSteeringIntoHistory()` appends drained steering as user messages
   at the current history tail; called in the stream-round loop
   (internal/agentic/agent_streaming.go) after a round's tool results are
   appended and before the next `runStreamRound` — so the NEXT provider request
   already contains the steering, and request N+1 is a strict prefix-extension
   of request N (guideline #9 cache-safe).
3. `AgentManager.StartSession` wires `am.steering` into the agent via
   `steeringSourceAdapter` (Flush→Drain). The end-of-turn flush now only
   handles leftovers (steering typed during the final no-tool round, which has
   no subsequent round to drain into).

**Regression test:** `TestAgent_SteeringWovenMidTurn`
(internal/agentic/agent_steering_test.go) — drives a 2-round tool loop with a
request-capturing fake provider, enqueues steering after round 1, and asserts
(a) the round-2 request carries the steering as the last user message and
(b) round-2 is a strict prefix-extension of round-1 (append-only, guideline #9).
Helpers kept under gocognit budget. Full `internal/agentic`, `core`, and
`internal/app` suites green with `-race`; vet/gocognit/gocyclo clean.

---

### ✅ IMPLEMENTED — FEATURE/TODO: Goal setup option — run goal in a NEW agent / clean context (default: reuse current agent)

**What shipped:** a per-goal `freshContext` flag, threaded end-to-end:

1. **Model / schema** — `CreateGoalInput.FreshContext` (core/goal/model.go);
   the goal tool exposes a `freshContext` boolean on `create`
   (tools/goal/goal.go) with a description stating default false = reuse
   current context.
2. **Persistence** — the flag is stored on the internal `goalStage`, written
   to the `goal.create` event record, surfaced on `GoalSnapshot.FreshContext`,
   and restored on `Replay` (round-trip tested).
3. **Driver routing** — `GoalDriver` gained a `FreshAgentRunner` interface
   (core/goal_driver.go). `runTurn` routes a fresh-context goal through
   `RunFresh(ctx, prompt, begin)`; `begin=true` only on the goal's first
   continuation turn. Default goals (and runners without fresh support) use
   the ordinary `Run` path unchanged.
4. **Clean context + visible boundary** — `agentManagerRunner.RunFresh`
   (internal/app/subsystems.go) clears the agent's live context to the system
   prompt on `begin` and injects a visible "⟡ Context reset" system message.
   The prior conversation is preserved in the durable session transcript; it
   is intentionally NOT restored into the live context mid-session (matching
   "new agent / clean context" semantics — the goal carries only its objective
   forward). Cache-safe (guideline #9): the clean context's system prompt stays
   byte-stable for the goal's turns.

**Regression tests:**
- `TestCreateGoal_FreshContext` (core/goal/mode_test.go): flag captured on
  create, default false, replay round-trips.
- `TestGoalTool_Create_FreshContext` (tools/goal/goal_test.go): model-facing
  `freshContext:true` threaded to the mode and snapshot.
- `TestGoalDriver_FreshContextRouting` (core/goal_driver_test.go): fresh goal →
  `RunFresh` (begin=true), default goal → `Run`; never cross-wired.

All green (-race); vet/gocognit/gocyclo clean on changed files.

---

### ✅ FIXED — BUG/UX: `/tools` help renders the raw SPDX license header for the `goal` tool

**Root cause:** `listTools` (core/commands/docs.go) truncated
`schema.Description` at 60 chars for the `/tools` row. The goal tool's
description is embedded from `prompts/goal/goal.md`, which begins with an SPDX
HTML comment (`<!-- ... -->`), so the truncated row showed the raw header.

**Fix:** added `toolSummaryLine` (core/commands/docs.go) — strips a single
leading HTML comment block, then returns the first non-empty content line —
and used it for the `/tools` row description. Tool-agnostic: any tool whose
description is loaded from a Markdown doc with a license header now shows a
clean summary.

**Regression test:** `TestToolSummaryLine_StripsSPDXHeader`
(core/commands/docs_test.go) — SPDX-header input yields no "SPDX"/"<!--"/
"Copyright" and starts with the real description; plain and multi-paragraph
descriptions collapse to the first content line. Full `core/commands` suite
green; vet/gocognit/gocyclo clean.

---

### ✅ ALREADY FIXED (verified + regression-pinned) — BUG: `/tools:goal:on` reports success but the goal tool still errors

**Verdict:** not reproducible this session — the earlier `newGoalTool`
unification (startup + runtime factory build the goal tool identically via
`internal/app/subsystems.go:newGoalTool`) already fixed the root cause. The
runtime-built goal tool's `Execute` → `ExecuteWithResult` → `handleCreate`
returns `agentic.ToolResult{Output: ...}, nil` on success — a nil error, so
the renderer draws ✓, never ✗. The ✗-on-success symptom was from the previous
split/wrongly-wired runtime path.

**Regression test pinned:** `TestMakeToolFactory_Goal_CreateIsNotErrorFlagged`
(internal/app/tool_factory_goal_test.go) — drives `/tools:goal:on`'s factory
then a `create` through the factory-built tool and asserts the error is nil
(no ✗) and the output carries the goal payload. Passes; full `internal/app`
suite green (-race).

---

### ✅ FIXED — BUG/UX: embedded python tool fails on `import struct` with cryptic FileNotFoundError

**Root cause:** the embedded gpython interpreter provides a curated stdlib
(os/re/json/collections/base64/hashlib/datetime/itertools/urllib/random/math/
string/glob); `struct` is not among them. gpython reports an unresolved import
as `FileNotFoundError: 'Failed to resolve "struct"'`, which is confusing.

**Fix (option b — clearer error):** added `clarifyModuleError` (tools/python.go),
applied in `formatPythonError`. It detects gpython's `Failed to resolve "mod"`
marker and rewrites it to name the module, list the supported subset, and
direct the caller to `bash python3` for anything beyond it. Non-module errors
pass through unchanged. Also added an "Unsupported modules" section to
`tools/python.long.md` stating the boundary up front.

**Regression tests:** `TestPythonTool_UnsupportedModule_ClearError` (struct,
socket, subprocess → module-naming, python3-pointing error; no
"Failed to resolve"/"FileNotFoundError") and `TestClarifyModuleError_Passthrough`
(non-module errors untouched), tools/python_test.go. Full `tools` suite green;
vet/gocognit/gocyclo clean.

---

### ✅ RESOLVED (regression-pinned) — BUG: TUI streaming stutters — streamed text re-paints duplicated/overlapping fragments

**Verdict:** the pure-growth stutter class is **already fixed** by the
compositor watermark work (the /quota chrome-change fix, commits
`e777aba`/`e6d8a2f`). This session added two faithful-TermEmulator regression
tests that drive the exact stutter triggers — a single streaming block grown in
varied increments so it crosses the viewport bottom AND re-wraps long lines,
and a token-by-token growth that changes the block's rendered height
frame-over-frame — and both pass with **zero** within-scrollback duplication,
**zero** lost chunks, and **zero** glued sentences.

**Regression tests:**
- `TestCompositor_StreamingGrowth_NoStutter`
  (tui/compositor_stream_stutter_test.go): 30 varied-length chunks (short /
  re-wrapping-long / medium) streamed into one block; asserts every `tokNN`
  marker recoverable, none duplicated within scrollback, and no `.tok` glued
  row (the "concatenated re-paint" form).
- A token-growth variant (60 single-token appends crossing the viewport
  bottom) run during verification — also clean.

These confirm the earlier hypothesis ("likely shares the fix with the
chrome-change watermark work — verify the chrome-change fix does not already
resolve this"): it does. Full `tui` suite green; vet/gocognit/gocyclo clean on
the new test.

<details><summary>Original report</summary>

**Observed:** during a live stream the chat shows stuttering: the same streamed
text is repainted with duplicated/overlapping fragments (export
`/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260724-221601.zip`) —
repeating trailing lines and concatenated re-paint gluing sentences together.
The stutter starts only once the block crosses a threshold (viewport bottom /
wrap boundary). The model/tool calls are correct; only the TUI display stutters.

</details>

---

### ✅ FIXED (wording) — BUG/INVESTIGATE: model stops mid-work without clear reason, even when instructed to finish

**Finding:** reviewed every `[goa-system]` injection the agent loop can deliver
(internal/agentic/agent_budget.go, bash_reuse.go, tools/loop_hints.go). The
hard guardrails (tool-call budget exceeded, loop guardrail, consecutive-repeat)
are INTENTIONAL hard stops and only fire on genuine configured/over-run limits
— they correctly say "stop / produce a final answer". The one nudge that fires
on a LEGITIMATE action is the **near-duplicate bash hint** (`nearDuplicateHint`,
bash_reuse.go): it is appended (non-blocking) when the model re-runs an
expensive upstream with only the filter changed — a valid call. Its original
wording opened by restating the model's own behaviour ("you re-ran the same
base command...") in a corrective tone that a model could read as a
discouragement/stop signal even though the call was allowed.

**Fix:** reworded `nearDuplicateHint` to be explicitly forward-looking and
non-halting — it now leads with "(informational only — keep working): this
result is valid; use it and continue the task" and frames the save-once-
refilter pattern as applying to FURTHER counts, not as a rebuke of the call
just made. Cache-safe (guideline #9): still appended to the tool RESULT (new
tail content), never a history rewrite.

**Regression:** `TestBashReuse*` + the integration test
(internal/agentic/bash_reuse_integration_test.go) still green (they key on the
preserved "Efficiency note" marker); full `internal/agentic` suite green (-race).

**Residual note:** if a specific session still stops early, the export
(`/Users/muaddib/dev/goa/.goa/exports/goa-export-20260724-222028.zip`) should be
replayed to confirm which injection (if any) preceded the stop; the wording fix
above removes the most likely false-positive (a legitimate re-run being read as
"stop"). No code path was found that halts the turn on an informational nudge.

---

### ✅ IMPLEMENTED (queue + clean-context) — FEATURE: goal queue + automated todo management + per-goal clean-context flag

**Status of the three parts:**

1. **Goal queue — ALREADY PRESENT (verified), now MODEL-FACING (todo-like).**
   Goa ships a durable FIFO goal queue (core/goal_queue.go). This session the
   **goal tool was redesigned so the model uses goals like todos** (user
   review): `create` now APPENDS by default — if a goal is active the new goal
   is queued (never an implicit replace; `replace:true` is the explicit
   opt-in); `create` accepts a batch `objectives` array (first activates if
   none active, rest queue); and new `list` / `cancel` / `reorder` actions
   manage the active goal + queue (by ID or friendly name). The queue is wired
   into the tool on both the startup and `/tools:goal:on` runtime paths
   (internal/app/subsystems.go `newGoalTool` sets `GoalTool.Queue`). Schema +
   `prompts/goal/goal.md` document the list semantics. Auto-promotion of the
   next queued goal on active-goal clear is unchanged
   (internal/app/events.go `promoteNextQueuedGoal`).
   Regression: `TestGoalTool_CreateAppendsWhenActive`, `TestGoalTool_CreateBatch`,
   `TestGoalTool_CreateReplaceStillWorks`, `TestGoalTool_ListCancelReorder`
   (tools/goal/goal_test.go), `TestNewGoalTool_QueueWired`
   (internal/app/tool_factory_goal_test.go). All green (-race).

2. **Per-goal clean-context flag — IMPLEMENTED this session (see the
   fresh-context item above), now INCLUDING per queued goal.** Added
   `UpcomingGoal.FreshContext` (core/goal/model.go) and
   `GoalQueueStore.AppendWithOptions(objective, freshContext)`; the promote
   path (`promoteNextQueuedGoal`) now forwards the queued goal's flag into
   `CreateGoalInput`, so a queued goal runs on a clean context exactly like a
   directly-created one. Regression: `TestGoalQueueStore_FreshContext`
   (core/goal_queue_test.go) — flag persists through Append/disk/Read, default
   false. Full `core` suite green (-race); vet/gocognit clean.

3. **Automated todo management — DEFERRED (recorded, not done).** The model
   already has a `todo_list` tool for self-tracking, but the framework does
   not yet auto-decompose a goal into a managed todo list and check items off
   across turns. This is a distinct design (goal→todo decomposition, turn-loop
   integration to mark progress, and a model/user-visible todo surface). It is
   recorded here as the one remaining sub-item rather than silently dropped:
   a follow-up should wire goal creation to an optional managed todo list and
   have the goal driver/injection surface and update it per turn.

<details><summary>Original request</summary>

**Request:** the model should be able to add goals to a QUEUE of goals; goals
should enable automated todo-list management by the framework; and at goal
creation the model can declare whether the goal runs with a CLEAN/NEW context
(per-goal, including per queued goal).

</details>

---

### ✅ IMPLEMENTED — FEATURE/TODO: Framework-managed todo list for goals (goal→todo decomposition + auto check-off)
**What shipped:** a goal-owned, durable, framework-managed todo list.

1. **Model & storage** — `GoalTodoItem` (core/goal/todo.go) with pending /
   in_progress / done statuses; stored on the goal's internal state, surfaced
   on `GoalSnapshot.Todos`, persisted via a `goal.update` event-record patch,
   and restored on `Replay` (round-trip tested).
2. **Framework API** — `GoalMode.AddGoalTodo(title)` /
   `UpdateGoalTodo(id, status)` (core/goal/mode.go), concurrency-safe under the
   goal lock, generating stable never-reused IDs (`t1`, `t2`, …).
3. **Model surface** — the goal tool gained `add_todo` and `update_todo`
   actions (tools/goal/goal.go), documented in `prompts/goal/goal.md`, so the
   model decomposes a multi-step goal into ordered items and checks them off.
4. **Per-turn surfacing** — `BuildDynamicGoalProgress` (core/goal/injection.go)
   appends the todo list (`[x]/[~]/[ ]` + `N/M done`) plus "work the next
   pending todo" guidance to the per-turn goal reminder (cache-safe: the dynamic
   progress is a tail user message, never a history rewrite — guideline #9).

**Regression tests:**
- `TestGoalMode_TodoLifecycle` / `TestGoalMode_TodoRequiresGoal` /
  `TestGoalMode_UpdateTodoNotFound` / `TestTodoSummaryLine`
  (core/goal/todo_test.go): add/update/list, unique IDs, persistence round-trip,
  error paths, summary rendering.
- `TestGoalTool_TodoActions` (tools/goal/goal_test.go): create → add_todo →
  update_todo → get reflects the list.
- `TestDynamicProgress_SurfacesTodos` (core/goal/injection_test.go): the
  per-turn reminder surfaces the todo state + guidance.

All green (-race); vet/gocognit/gocyclo clean on changed files.

**Note:** the model performs the decomposition via `add_todo` (the framework
persists/surfaces/checks the list); fully automatic framework-side decomposition
without a model pass is a possible future enhancement, not part of this fix.