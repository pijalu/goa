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

### FEATURE: Review gap of `/goal` vs kimi-code's goal implementation

**Observed:** Goa ships a `/goal` command and GoalDriver, but no comparison
against the goal mode in `/Users/muaddib/dev/kimi-code` has been done.
kimi-code's goal handling (its "keep pursuing the objective autonomously"
loop) may cover behaviors goa lacks (or vice versa).

**Expected:** Produce a gap review comparing goa's `/goal`
(command surface, lifecycle: create/resume/pause/complete, budgets,
continuation driving, stop conditions) against kimi-code's equivalent, and
file concrete follow-up items in this file for each gap worth closing.

**Investigate:** read kimi-code's goal/goal-mode implementation under
/Users/muaddib/dev/kimi-code (command handlers, prompt/loop wiring, stop
criteria) and diff against goa's core/commands goal command + agentic
GoalDriver. Note: this overlaps with the open `/goal:resume` bug above —
fixing that may close part of the gap.

**Test approach:** gap list reviewed; any adopted behavior gets its own
item here with the standard test/validation plan (filmstrip validation for
any TUI-visible behavior per guideline #5).

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

### 🔬 INVESTIGATED (fix designed, not yet implemented) — BUG/UX: Steering is injected very late in the turn

**Root cause (confirmed in code):** steering typed while the agent is running
never reaches the in-flight turn. Two queues sit in series:
1. `AgentManager.SendUserInput` (core/agentmanager.go:265) — when
   `alreadyRunning`, the input goes to `am.steering.Append(input)` and returns.
2. `runAgentTurn` only calls `am.steering.Flush()` AFTER the runner finishes
   (line 322), stores it in `pendingSteering`, and the deferred block
   (lines 299-302) re-dispatches it via `SendUserInput` as a BRAND-NEW user
   turn once `running=false`.
So the steering is delivered as a separate, later turn — not woven into the
turn the user was watching. That is the "very late" behavior.

There is also a second, deeper queue: at the `Agent` level, `runInternal`
(internal/agentic/agent.go:863) appends to `a.queue` when `a.processing` and
drains it only after the current turn's loop ends (lines 901-909) — same
lateness one level down.

**Earliest cache-safe injection point (design):** the stream-round loop
(internal/agentic/agent_streaming.go:28). After a round's tool results are
appended (`completeStreamTurn`/`executeBufferedToolCalls`) and BEFORE the next
`runStreamRound`, drain the steering queue and append the steering as a user
message at the CURRENT tail. Because it is appended after the last completed
assistant/tool message and never rewrites earlier bytes, request N+1 stays a
strict prefix-extension of request N (guideline #9 satisfied). The very next
model call then already contains the steering.

**Proposed change:** give `Agent` a steering source (the existing
`core.SteeringQueue`, shared from `AgentManager`), and in the round loop call
`a.drainSteeringIntoHistory()` between rounds. `AgentManager` stops holding
steering until turn end for the mid-turn case (still flushes leftovers after
the turn for steering that arrives during the final no-tool round).

**Blocker to implementing blind:** the item asks to mirror `../pi`'s exact
injection mechanism, but pi's source is outside the project jail and could not
be read this session. The design above is derived from goa's own loop and the
guideline-#9 constraint; before merging, confirm pi's approach (does pi cancel
the in-flight stream and re-issue, or inject between tool calls?) and align.

**Test approach (ready to write):** drive a multi-round tool loop with a fake
provider, enqueue steering mid-turn, assert (a) the NEXT provider request
already contains the steering as a user message at the tail, (b) request bytes
remain a prefix-extension of the prior request, (c) filmstrip shows the
steering rendered at the moment it is typed.

<details><summary>Original report</summary>

When the user types a steering message mid-turn (ESC steering), it is added
very late — effectively only picked up at/after the end of the current turn —
so the model keeps executing the stale plan. Compare with how `../pi` injects
steering; adopt an earlier (cache-safe) injection point.

</details>

---

### FEATURE/TODO: Goal setup option — run goal in a NEW agent / clean context (default: reuse current agent)

**Observed / request:** Today a goal always runs its continuation turns on the
CURRENT agent, inheriting the whole conversation context. For long or
self-contained goals this is wasteful (every continuation re-sends the full
history — the exact cost guideline #9 warns about) and can derail the goal
with stale context. The model should be able to decide AT GOAL SETUP whether
execution runs on a NEW agent with a CLEAN context, via a simple boolean
flag.

**Expected:**
1. A boolean flag (e.g. `fresh_context` / `new_agent`) on goal creation —
   exposed in the `/goal` command and the goal tool's create input. When NOT
   set, the default is to REUSE the current agent (today's behavior).
2. When set, the goal's continuation turns run on a NEWLY-created agent with
   a CLEAN context: the model is given the goal objective (and any explicit
   handoff text) but NOT the prior conversation tokens.
3. HISTORY IS PRESERVED across the agent boundary — the prior conversation is
   kept in the durable transcript/session record (nothing is deleted), it is
   simply not SENT to the new agent's context.
4. The context "reset" must be VISIBLE: a clear marker/boundary is rendered
   in the conversation (and recorded in history) so the user can follow where
   one agent/context ended and the new one began.

**Investigate:** how a goal continuation turn obtains its agent —
`GoalDriver.Agent` is an `AgentRunner` (core/goal_driver.go) backed by
`agentManagerRunner` → `agentMgr.CurrentAgent()` (internal/app/subsystems.go).
A clean-context goal needs the driver to run against a FRESH agent session
instead of `CurrentAgent()`: how agent sessions are created/reset
(AgentManager), how the transcript is persisted across sessions (session
store / export), and how to thread the `fresh_context` flag from
`CreateGoalInput` (core/goal/mode.go) through to the driver's per-turn agent
selection. Also how to render the reset boundary in the chat viewport
(a system/banner component) and record it in history (an event) without
breaking the append-only rule for the NEW agent's own requests (guideline #9:
the new agent's system prompt + history must itself stay prefix-stable).

**Test approach:** unit test: create a goal with the flag set and assert the
driver runs turns on a new/clean agent context while the flag-unset default
reuses the current agent; assert the prior history is still present in the
persisted transcript but absent from the new agent's outgoing requests.
Integration: a two-goal run where the second uses fresh context — verify the
boundary is recorded and the new agent's first request contains only the
objective (not the old history). Filmstrip validation (guideline #5) of the
visible context-reset marker.

---

### BUG/UX: `/tools` help renders the raw SPDX license header for the `goal` tool

**Observed:** `/tools` lists every tool with a clean one-line description
except `goal`, whose description renders the tool doc's raw SPDX HTML comment:

```
goal <!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright...
```

The goal tool's description is loaded from an embedded Markdown file that
begins with an SPDX license header (`<!--\nSPDX-License-Identifier: ... -->`),
and the loader does not strip the leading HTML comment before using the text
as the inline description. All other tools show clean summaries.

**Expected:** the `goal` tool shows a clean one-line summary like every other
tool; license headers in embedded doc files are stripped (or skipped) before
the text is used as a description.

**Investigate:** how the goal tool builds its `Schema().Description` / doc —
likely a `//go:embed` of a `.md` file under `tools/goal/` that starts with the
SPDX comment. Check the doc-loading path for whether leading HTML comments are
stripped; other tools either have no header or strip it.

**Test approach:** unit test asserting the goal tool's description contains no
"SPDX" or "<!--" and starts with a real summary sentence; filmstrip / terminal
validation (guideline #5) of `/tools` output showing a clean goal row.

---

### BUG: `/tools:goal:on` reports success but the goal tool still errors — "✗ ◆ Started goal Phase 1..."

**Observed:** after enabling the goal tool at runtime with `/tools:goal:on`
(which prints "⚡ Tool goal on"), invoking the goal tool produces a result
flagged as an ERROR (`✗`) even though its body reads "◆ Started goal Phase
1: ...". The error marker and the success body contradict each other.

**Expected:** enabling the goal tool takes effect cleanly; a successful goal
start renders as a normal (non-error) tool result, and a genuine failure
renders as an error with a real error message — never an error marker on a
success body.

**Investigate:** this follows the earlier fix for "/tools:goal:on cannot
instantiate at runtime" (the `"goal"` case added to `makeToolFactory`). Re-check
the runtime-constructed goal tool's Execute path: does it return a Go `error`
(or a result the renderer treats as an error) alongside the "Started goal"
text? Compare the startup-registered goal tool's result vs the factory-built
one — the `newGoalTool` helper may wire a different result/error contract.
Also check how the tool renderer decides ✗ vs ✓ (non-empty error field,
non-zero exit, or the ToolError type?).

**Test approach:** regression test driving `/tools:goal:on` then a goal create
through the factory-built tool, asserting the result is NOT error-flagged when
the goal starts; filmstrip validation (guideline #5) of the goal tool widget
state (✓ / no ✗).

---

### BUG/UX: embedded python tool fails on `import struct` with cryptic FileNotFoundError

**Observed:** the `python` tool (embedded gpython) fails on
`import struct, math` with:

```
FileNotFoundError: 'Failed to resolve "struct"'
```

The model reached for `struct` (a normal stdlib module) and got a confusing
"file not found" style error plus a generic hint.

**Expected:** either (a) the gpython interpreter supports `struct` (and other
commonly-needed stdlib modules), or (b) when a module is unavailable the tool
returns a clear, actionable message naming the unsupported module and
directing the caller to `bash python3` for stdlib modules beyond the supported
subset — not a raw FileNotFoundError.

**Investigate:** which stdlib modules the embedded gpython supports (the tool
description lists os/re/json/collections/base64/hashlib/datetime/itertools/
urllib/random/math/string/glob); `struct` is not among them. Decide: expand
the interpreter's stdlib, or intercept the import failure and rephrase the
error + update `tools/python.long.md` and the tool description so the model
knows the boundary up front (tool DESCRIPTION is static text, so this is
cache-safe under guideline #9).

**Test approach:** unit test that `import struct` (and a couple of other
unsupported modules) yields the improved, module-naming error (or, if support
is added, that struct.pack/unpack round-trip works); verify the tool
description/long doc states the supported-module boundary.

---

### BUG: TUI streaming stutters — streamed text re-paints duplicated/overlapping fragments

**Observed:** during a live stream the chat shows stuttering: the same streamed
text is repainted with duplicated/overlapping fragments. Two captured forms
(export `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260724-221601.zip`):

1. Repeating trailing lines — the tail of a thinking block is emitted twice:
   ```
   ...findNestedAggregate function doesn't descend into subqueries properly for UNION ALL cases. But adding subquery traversal
   the main outer WHERE is ... BETWEEN 1 AND 1.
   The issue is that the findNestedAggregate function doesn't descend into subqueries properly for UNION ALL cases. But adding subquery traversal
   to findNestedAggregate is complex.
   ```
2. Concatenated re-paint — a re-wrapped long line is painted again with no
   newline, gluing sentences together:
   ```
   Let me check the full pass/fail summary for all three suites:Let me get the full pass/fail summary:Let me get a summary of all tests:Let me get
   the full pass/fail summary:Let me get a complete pass/fail list:...
   ```

The stutter is NOT constant — it starts only at some point in the stream,
which points to a rebuild that goes wrong once the block crosses a threshold
(viewport bottom / a wrap boundary / a line-count trigger), not to a
deterministic per-token bug.

**Key constraint (per reporter):** the model itself behaves correctly — tool
calls are correct, the streamed CONTENT is right. Only the TUI display
stutters. This isolates the bug to goa's stream→viewport→compositor path, NOT
the agent SDK or the model.

**Expected:** each streamed token is painted exactly once; a growing stream
block appends new lines and re-wraps the tail without re-emitting already-
painted lines or gluing re-wrapped lines together.

**Investigate (the rebuild path):** the streaming block grows via
`UpdateLastMessage` → the entry is marked dirty and re-rendered → the
compositor diffs the new canvas against `prevLines`. Stuttering that starts
"at some point" strongly suggests the diff mis-tracks once the growing block's
rendered line count changes frame-over-frame: when a block grows from N to N+k
lines, the rows below it shift, and `unchangedRow` (which compares canvas row
i to `prevLines[i - vt + c.vt]`) can treat a shifted row as changed and
repaint it, or the scroll-advance path can re-emit a row it already scrolled.
Check:
- whether the growing-block re-render keeps a STABLE lineOffset (does
  `UpdateLast`/`updateLastEntry` patch the frame cache at the right offset
  when the block's height changes?);
- the interaction between the block growing past the viewport bottom and the
  watermark (the same class as the /quota chrome-change bug, but for pure
  in-transcript growth);
- the "concatenated" form specifically: a re-wrapped long line whose trailing
  segment is repainted without a preceding line-feed — suggests a repaint that
  resumes mid-line (cursor left at the wrong column after a partial-row
  update).

**Repro:** replay the export's `session/events.jsonl` through the filmstrip /
TermEmulator harness (guideline #5) and find the exact frame where a row is
painted twice or glued; then localize to the specific diff/watermark lines.

**Test approach:** filmstrip + TermEmulator regression: grow a thinking block
in realistic increments (varying token sizes so the block crosses the viewport
bottom AND re-wraps), assert each distinct content line appears exactly once
and no two sentences are glued onto one row. Likely shares the fix with the
chrome-change watermark work — verify the chrome-change fix does not already
resolve this, then fix any remaining pure-growth desync.

---

### BUG/INVESTIGATE: model stops mid-work without clear reason, even when instructed to finish

**Observed:** the model frequently stops mid-task — producing a partial result
or a "let me summarize and stop" turn — even when explicitly instructed to
complete the work. Export:
`/Users/muaddib/dev/goa/.goa/exports/goa-export-20260724-222028.zip`.

**Question to answer (per reporter):** is the premature stop caused by a goa
HARNESS request rather than the model's own choice? Suspects are the injected
harness messages that a model could read as a stop/wrap-up signal:
- **tool-call budget window** (`recordToolCallInBudgetWindow`,
  `shouldBufferToolCall` in internal/agentic/agent_budget.go) — emits
  "[goa-system] budget exceeded" / "tool call budget" warnings;
- **loop / duplicate guardrail** — "[goa-system] Loop guardrail" / "identical
  to the previous" when a call repeats;
- **near-duplicate bash hint** (`nearDuplicateHint`, bash_reuse.go) — appended
  to a tool result telling the model to save-and-refilter;
- **round / turn limits** — "[goa-system] round limit reached" or result
  truncation notices;
- **goal driver stop/budget conditions** — a goal continuation turn hitting a
  turn/token budget and pausing or completing early.

Any of these, if worded like a hard stop or delivered at the wrong moment,
could make the model conclude it should halt and summarize instead of
continuing.

**Expected:** the model keeps working until the task is actually done (or a
genuine, clearly-signalled limit is hit). Harness nudges must be worded and
timed so they guide the NEXT action (e.g. "refilter saved output") without
being mistaken for a "stop now" instruction, and hard limits must surface a
clear, distinct reason the user can see.

**Investigate:** replay the export's events and locate the turn where the model
stopped. Inspect the immediately preceding harness injections: which
"[goa-system]" messages (if any) were delivered in the rounds before the stop,
and whether the model's stop message references them ("budget", "too many
calls", "repeat", "round limit"). Check the exact text of the budget/loop/
near-dup/round-limit injections for stop-signal wording, and whether a budget
warning is delivered even when the budget is not actually exhausted (false
positive → spurious stop). Confirm whether the stop correlates with a goal
budget (turns/tokens) rather than a tool-call guard.

**Test approach:** regression: drive a multi-round agentic loop with a fake
provider that would naturally continue, inject each harness nudge in turn, and
assert (a) informational nudges do NOT cause the agent to end the turn early,
(b) a genuine budget/limit produces a clearly-labelled stop reason visible to
the user. Fix wording/timing so nudges steer rather than halt.

---

### FEATURE: goal queue + automated todo management + per-goal clean-context flag

**Request:** the model should be able to add goals to a QUEUE of goals, not
just run one goal at a time. Goals should enable automated todo-list
management by the framework (the framework decomposes a goal into an ordered
todo/queue of sub-goals and works through them). Independently, at goal
creation the model can declare whether the goal runs with a CLEAN/NEW context
(see the existing "Goal setup option — run goal in a NEW agent / clean
context" item above — this request subsumes/depends on it).

**Expected:**
1. **Goal queue.** The model (and `/goal`) can enqueue multiple goals. The
   GoalDriver runs them in order: when the active goal completes/blocks, the
   next queued goal becomes active automatically. The queue is inspectable
   (`/goal` list shows queued + active) and reorderable/cancellable.
2. **Automated todo management.** The framework can turn a goal into a managed
   todo list (create/update/check-off items as the goal progresses), surfaced
   to the model and the user, so multi-step goals self-track instead of
   relying on the model to remember remaining work.
3. **Per-goal clean-context flag.** Each goal carries a `fresh_context` /
   `new_agent` flag (default: reuse current agent). When set, that goal's
   continuation turns run on a new agent with a clean context (objective +
   handoff only), with history preserved in the transcript and a visible
   context-reset boundary — exactly as the existing fresh-context item
   describes. The flag must be settable per-goal, including per queued goal.

**Investigate:** current GoalDriver/GoalMode lifecycle (create/resume/pause/
complete) supports a single active goal — how to extend it to a FIFO queue
(core/goal, core/goal_driver.go, core/commands/goal.go); how a todo list is
represented and persisted (is there an existing todo/plan structure to reuse —
core/plan?); how the goal tool's create input is schema'd so the model can pass
queue + clean-context parameters (tools/goal). Thread the clean-context flag
from CreateGoalInput through to per-turn agent selection as described in the
fresh-context item.

**Test approach:** unit: enqueue N goals, assert they run in FIFO order with
the driver auto-advancing on complete/block; assert a queued goal with the
clean-context flag runs on a new agent while a default goal reuses the current
one; assert the todo list is created/updated/checked off across the goal's
turns. Filmstrip validation (guideline #5) of the queue/todo display and the
context-reset boundary.
