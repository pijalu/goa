<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Archive — 2026-07-24

All items implemented, tested, and validated. Quality gates run separately per
guideline #6: `go vet` ✅ · `staticcheck` ✅ (1 pre-existing unrelated warning in
tools/edit_renderer.go) · `gocognit -over 15` ✅ (2 pre-existing, unrelated) ·
`gocyclo -over 12` ✅ · `go test -count=1 -race -cover` ✅ on all touched packages.

## Open items

### L1 — LSP diagnostics never surface in real sessions (silent bootstrap + fixed-sleep race)

**Observed (2026-07-24, live in Goa):** writing a Go file with a deliberate
error via the `write` tool returned `✓ Written — 95 bytes, 9 lines` with **no**
`Diagnostics (gopls):` block, even though `go vet` confirmed the error
(`undefined: undefinedSymbolHere`). A scan of all `.goa/sessions/*.jsonl` found
**zero** genuine `Diagnostics (gopls):` tool results across **53 sessions** that
wrote/edited `.go` files. The `internal/lsp` package itself is proven correct
(live test against real gopls: `START OK`, `DIAG main.go:4:2 sev=1: undefined:
undefinedFunc` in 0.24s; 89.7% coverage, `go vet`/race clean).

**Root causes (two compounding):**
1. **Silent bootstrap failure.** `internal/app/bootstrap.go:newLSPManager` only
   `log.Printf("Warning: failed to start LSP manager: %v")` and returns `nil`
   on any failure (gopls not on the PATH Goa inherited, or the 5s
   `context.WithTimeout` expiring on a cold first init). The tools then
   short-circuit on `t.LSPManager == nil` and emit nothing — no gopls child
   process exists for the running `goa` (confirmed via `ps`). Nothing surfaces
   this to the model or the user.
2. **Fixed `150ms` settle sleep races gopls.** `tools/writefile.go:lspDiagnostics`
   and `tools/editfile.go:notifyLSP` do `time.Sleep(150 * time.Millisecond)`
   then read. A cold package load on a new/changed file frequently exceeds
   150ms, so the tool reads before `publishDiagnostics` arrives → empty block.

**Fix plan:**
- Surface LSP state: on start failure, record a user-visible warning (status
  line / `EmitEvent`) and expose a `tools.lsp.enabled` config flag; do not
  silently nil the manager. Consider retrying `Start` lazily on first
  `.go` write instead of only once at bootstrap.
- Replace the fixed sleep with a **bounded poll**: poll `DiagnosticsFor` every
  ~50ms up to ~1s, returning as soon as diagnostics are non-empty (or the
  deadline passes). Same helper shared by write+edit.
- Test approach: extend `tools/lsp_integration_test.go` fake manager to
  deliver diagnostics after a delay and assert the tool result still contains
  them; a filmstrip test (`internal/app/lsp_diagnostics_filmstrip_test.go`)
  asserting the diagnostics block renders in a real turn.
- Validation: `go vet ./...`, `go test -count=1 -race -cover ./internal/lsp/
  ./tools/ ./internal/app/` (separately), then filmstrip-validate a `.go`
  write with a deliberate error shows `Diagnostics (gopls):` in the tool block.
### S1 — ESC must restore queued steering text into the input line (stop + restore + clear bubble)

**Observed:** while the agent is mid-turn, user input is buffered as steering
(`internal/app/submithandler.go:maybeSteerAgent` → `SteeringQueue.Append` +
`chat.AddSteeringPending`). Pressing **ESC** (`internal/app/tui.go:handleEscape`)
calls `sq.Flush()` — this **drops the text** and, because it never calls
`chat.ClearSteeringPending()`, the steering bubble is **left on screen as a
non-editable UI leftover** (the text is gone from the queue but the bubble stays
and cannot be edited). The user loses the message and is left with a dead bubble.

**Expected (user requirement):** on ESC, do all of:
1. **Stop** the agent / goal driver / orchestrator (existing hard-stop behavior — unchanged).
2. **Restore** the queued steering message(s) into the **input line** (pre-fill the
   editor) so the user can decide to resend / edit / discard — exactly the existing
   Alt+E path `handleEditSteering` (`Flush` → `strings.Join(pending,"\n\n")` →
   `inp.SetText` → `chat.ClearSteeringPending`).
3. **Remove the steering bubble** (`chat.ClearSteeringPending()`).

**Root cause:** `handleEscape` uses `Flush()` purely as drain-and-discard and never
had the "restore to input" / "clear bubble" steps that `handleEditSteering` added
for Alt+E. The two paths diverged.

**Fix plan:**
- Extract a shared helper (e.g. `restoreSteeringToInput(engine, chat)`) implementing
  Flush → join → `SetText` → `ClearSteeringPending`, used by BOTH `handleEscape` and
  `handleEditSteering` (DRY, cannot diverge again). Guard on `sq.Len() > 0` and nil
  input; never auto-send the restored text.
- Ordering: run the stop first, then the (cheap, synchronous) restore.
- Boundary note: the workflow-orchestrator steering path (`maybeSteerWorkflow` →
  `foregroundOrch.InjectSteering`) has no drainable queue — only the main-agent
  `SteeringQueue` is restored.

**Test approach:**
- App test: queue steering via `maybeSteerAgent` on a running fake agent, invoke
  `handleEscape`; assert (a) queue empty, (b) input text == joined message, (c)
  bubble cleared, (d) no follow-up turn dispatched. Follow `internal/app/steering_test.go`.
- Regression: ESC with an EMPTY queue leaves existing input text untouched.
- **Filmstrip (`tui-test` skill) — validate the 3 visible outcomes:** on ESC the
  process stops, the input line contains the queued steering message, and the
  steering bubble is gone.

**Validation:** run each separately (rule #6): `go vet ./...`, `staticcheck ./...`,
`gocognit -over 15 .`, `gocyclo -over 12 .`, `go test -count=1 -race -cover
./internal/app/ ./tui/ ./core/`; then the filmstrip run.

### S2 — /goal: `UpdateGoal` unavailable + consolidate to ONE `goal` tool (stable array + exec-time gate)

**Observed (2026-07-24, model transcript):** in a `/goal` continuation turn the
model reasoned *"I need to use the UpdateGoal tool. But it's not in the available
tools list… There's no UpdateGoal tool!"* — the prompt (`core/goal_driver.go`,
`core/goal/injection.go`) orders "call UpdateGoal", but the tool was never
registered, so the model cannot end the goal and the driver loops forever.

**Root cause:** `internal/app/subsystems.go` registers goal tools only behind
`if cfg.Tools.Enabled.Goal || opts.Goal`. `ToolEnabledConfig.Goal` is opt-in
(default false) and `config/configs/default.yaml` `tools.enabled:` doesn't list
`goal:` → tools absent, while the goal *system* (injector + driver + prompt) is
wired unconditionally.

**Design decisions (locked):**
- **STABLE tool array (cache rule #9).** A cache probe (LM Studio
  `google/gemma-4-e4b`) showed adding ONE tool to `tools[]` with byte-identical
  messages raised the rendered prompt 2832→2872 tokens — llama.cpp renders
  `tools[]` into the prompt near the HEAD, so swapping tools mid-session busts the
  prefix cache. Therefore register the goal tool UNCONDITIONALLY and keep the set
  fixed for the session. (The earlier "dynamic add/remove + user-message announce"
  idea — proven feasible on gemma — is rejected on cache grounds.)
- **Consolidate to ONE tool.** The 4 tools (`CreateGoal`/`UpdateGoal`/`GetGoal`/
  `SetGoalBudget`) share `GoalMode` and differ only in args+handler → one
  `goal(action,…)` dispatcher. Cuts ~3.6KB descriptions + 3 schema headers off
  every request and removes names the small model confused.
- **Gate = EXECUTION-time, and ONLY outside an active goal.** The
  `tools.enabled.goal` flag (kept working) blocks ONLY autonomous `create` when
  **no goal is active**. ALL goal actions (`create`, `update`, `get`, `set_budget`)
  are allowed while a goal is running. So: `create` with gate OFF **and** no active
  goal → `ToolError` ("autonomous goal creation disabled; start one with /goal");
  `create` allowed if gate ON **or** a goal is active (replace/redirect).

**Implementation plan:**
1. New `tools/goal/goal.go`: a single `GoalTool{Mode, ReminderFn, CreateAllowed
   func() bool}` with `Schema().Name = "goal"`, `required:["action"]`,
   `action ∈ {create, update, get, set_budget}`; per-action fields optional in
   schema, validated in `Execute` (ToolError on mismatch). Implements
   `agentic.ResultTool`; `update` with complete/paused/blocked sets `StopTurn`
   (preserve current behavior). Reuse `core/goal.GoalMode` unchanged.
   `CreateAllowed` = `func() bool { return gateOn || Mode.GetActiveGoal() != nil }`.
2. New embedded description `prompts/goal/goal.md` (+ `GoalDescription()` in
   `prompts/goal/descriptions.go`). Remove the 4 old tools + their md.
3. `tools/goal_tools.go`: `NewGoalTools` returns the single tool.
4. `internal/app/subsystems.go`: ALWAYS `registerGoalTools` (drop the registration
   gate); pass the flag (`cfg.Tools.Enabled.Goal || opts.Goal`) into the tool.
5. **Update every model-facing reference** from `UpdateGoal`/`CreateGoal`/etc. to
   the new `goal {action:…}` form (G2 back-compat): grep and fix
   `core/goal_driver.go` (ContinuationPrompt), `core/goal/injection.go`,
   `internal/agentic/comm.go`, `core/goal_injection_wrapper.go`, `core/commands/goal.go`,
   `core/commands/orchestrator_goal_binder.go`, plus any skill/docs. **Then proxy-
   capture a real `/goal` turn and assert no message/system-prompt sent to the model
   still names `UpdateGoal`** (validates the note: every prompt uses the updated name).

**Test approach:**
- Table-driven per action, incl. field/action-mismatch ToolErrors; `update`
  complete/blocked → `StopTurn`; `get` returns snapshot; `set_budget` validates units.
- Gate: `create` with gate OFF + no active goal → ToolError and NO state change;
  `create` with gate OFF + an active goal → succeeds; gate ON → always succeeds.
- Regression (the reported bug): a `/goal` continuation turn can call the tool even
  when `tools.enabled.goal` is false.
- Filmstrip (`tui-test`): drive a `/goal` session; the model calls
  `goal {action:"update", status:"complete"}` and the goal ends (no infinite loop).

**Validation:** run each separately (rule #6): `go vet ./...`, `staticcheck ./...`,
`gocognit -over 15 .`, `gocyclo -over 12 .`, `go test -count=1 -race -cover
./internal/app/ ./core/... ./tools/ ./internal/agentic/`; filmstrip + proxy-capture
name check above.


---

## Resolution summary

- **L1 — FIXED.** LSP start failures now surface via a startup banner
  (`showLSPBanner`, driven by `Manager.StartError()`); the fixed 150ms sleep was
  replaced by a bounded poll `collectLSPDiagnostics` (50ms interval, 1s timeout,
  ctx-aware) shared by write+edit. Tests: `tools/lsp_diagnostics_test.go` (incl. the
  300ms-late-diagnostic regression), `internal/lsp/manager_test.go` StartError test,
  plus the pre-existing `TestFilmstrip_ToolResultWithDiagnostics` filmstrip (green).

- **S1 — FIXED.** ESC now restores queued steering into the input line instead of
  discarding it, via the shared `restoreSteeringToInput` helper used by both
  `handleEscape` and `handleEditSteering` (DRY). The pending bubble is cleared and
  the restored text is never auto-sent. Tests: `TestHandleEscape_RestoresSteeringToInput`,
  `TestHandleEscape_EmptyQueueKeepsDraft` (internal/app/steering_test.go).

- **S2 — FIXED.** Goal tools consolidated into ONE `goal` tool
  (`action ∈ {create, update, get, set_budget}`) registered UNCONDITIONALLY (stable
  tool array, cache rule #9). Autonomous `create` is gated at EXECUTION time and only
  when no goal is active (`CreateAllowed = flagOn || activeGoal != nil`). All
  model-facing prompts reworded from `UpdateGoal`/`SetGoalBudget`/`CreateGoal`/`GetGoal`
  to the `goal {action:…}` form (injection.go, goal_driver.go, comm.go, goal.md).
  Renderer consolidated to a single `GoalRenderer`. Tests: `tools/goal/goal_test.go`
  (gate + StopTurn + per-action), `w2_errors_test.go`, filmstrip
  `TestFilmstrip_GoalToolCallRenders`; goal-driver tests confirm a goal ends via the tool.