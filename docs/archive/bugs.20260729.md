<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug archive — 2026-07-29

All items from the 2026-07-29 session, closed and verified.
See bugs.md for the reporting guidelines.

## Issue 1 — Goal bubble disappears while a goal is active

**Symptom:** a goal is actively executing (driver continuation turns, goal
todos being updated) but no goal bubble is shown above the input line
(export `goa-export-20260729-215020.zip`, issue "No goal bubble"). The
session event log contains NO `goal create` — the goal ("sparky.bee") was
created in a previous app run and restored from the durable goal store.

**Root cause (confirmed by code trace):** the bubble (`tui/goal/bubble.go`)
is fed ONLY by live `GoalUpdate` bus events (`App.handleGoalUpdate` →
`updateGoalFooter`, internal/app/events.go:910). On startup,
`core.NewGoalManager` replays the goal store (core/goal.go:132) and
`NormalizeAfterReplay` demotes active→paused, but NOTHING publishes the
restored goal snapshot to the TUI — the bubble stays nil until the next
live goal event. Contributors:
- (a) **Startup never seeds the bubble/footer** from `Mode.GetGoal()`.
- (b) **Lossy publish**: `goalEventPublisher.Publish`
  (internal/app/subsystems.go:592) does a non-blocking send
  (`select`/`default`) — when the Agent bus is full (exactly the case
  mid-turn, when goal create/resume happens via the tool) the snapshot
  update is silently dropped, so the bubble never appears (or a stale
  bubble never clears).

**Fix plan:**
1. Seed the goal UI at startup: after subsystem assembly, if
   `goalManager.Mode.GetGoal().Goal != nil`, push the snapshot through the
   same `updateGoalFooter` path (bubble + footer + render request).
2. Make goal updates lossless-for-UI: coalescing latest-wins delivery —
   the publisher keeps the newest pending `GoalUpdate`; when the bus is
   full it stores instead of dropping, and a drain re-publishes as soon as
   the bus has room (only the latest snapshot matters for the UI).

**Test approach:**
- App-level test: pre-seed a goal store with an active goal, assemble the
  app, assert `subs.goalBubble.Snapshot() != nil` and footer goal fields
  set after startup (no bus event).
- Publisher test: fill the bus, publish snapshot A then B, drain, assert
  the handler eventually receives B (latest) — no update lost.
- Existing `TestHandleGoalUpdate_*` must stay green.

**Validation steps:** run goa in a project with a persisted active goal
(`.goa/goals/`), restart → bubble visible immediately; create a goal
mid-turn under heavy streaming → bubble appears.

**Status: FIXED.**
- `App.seedGoalUI` (internal/app/events.go) pushes the replayed goal
  snapshot through `updateGoalFooter` at TUI assembly (internal/app/tui.go
  finalizeTUI), restoring bubble + footer with no bus event.
- `goalEventPublisher` (internal/app/subsystems.go) is now lossless and
  ordered: full bus → updates queue in publish order and a single drain
  goroutine delivers them; no more silent `select`/`default` drops.
- Tests (green): `TestSeedGoalUI_RestoresBubbleAndFooter`,
  `TestSeedGoalUI_NoGoalKeepsUIClear`,
  `TestGoalEventPublisher_FullBusDoesNotDrop`,
  `TestGoalEventPublisher_OrderPreservedUnderLoad`; full internal/app
  package green.

---

## Issue 2 — /goal:current command

**Request:** add `/goal:current` to output the currently executed goal.

**Fix plan:** new `current` subcommand in `core/commands/goal.go`
(dispatch + `goalSubcommandKinds`, subNone). Output the active goal as
markdown: name, status, objective (full, untruncated), turns/tokens/
elapsed, completion criterion + verify command when set, and the todo
list with status markers (done/pending/in_progress) — richer than
`/goal:status` (which lacks todos/criterion). No goal → "No current goal."

**Test approach:** `core/commands/goal_test.go` — `/goal:current` with an
active goal carrying todos prints objective, criterion and todo lines with
statuses; without a goal prints "No current goal."; help/parse test for
the new subcommand keyword.

**Validation steps:** interactive: `/goal:new:fix tests` then
`/goal:current` → full details visible in chat.

**Status: FIXED.**
- New `current` subcommand (core/commands/goal.go dispatch +
  goalSubcommandKinds) printing the active goal as markdown: name, status,
  turns/tokens/elapsed, full objective, completion criterion, verify
  command, and todos with [x]/[>]/[ ] status markers.
- Help text updated (core/commands/help/goal.long.md).
- Tests (green): `TestGoalCommand_Current` (no-goal message + full detail
  incl. todos/criterion/verify); parse tests unchanged and green.

---

## Issue 3 — Goal ◈ marker next to the mode in the status line

**Request:** show a goal unicode box next to the mode when a goal is
executed: `◈ coding-posture │ YOLO`.

**Fix plan:** footer line 1 right side currently renders
`profileLabel │ modeBadge` (tui/footer_render.go:43-49). When a goal
exists (`FooterData.GoalStatus != ""`), prefix the profile label with
`◈ ` colored by goal status (active = primary/green, paused = yellow,
blocked = red; reuse existing theme colors). No goal → unchanged.

**Test approach:** `tui/footer_test.go` — render footer with
`GoalStatus: "active"` and `Profile: "coding-posture"` → line 1 contains
`◈ coding-posture │ YOLO` (ANSI-stripped); with empty GoalStatus → no ◈.

**Validation steps:** interactive shell: start a goal → ◈ appears next to
the mode; pause → color change; complete/cancel → ◈ disappears.

**Status: FIXED.**
- `Footer.goalProfileLabel`/`goalStatusColor` (tui/footer_render.go):
  when `GoalStatus != ""`, line 1 renders `◈ <profile>` with the marker
  colored by status (active green / paused blue / blocked red).
- Goal fields now survive routine footer rebuilds: `preserveFooterGoal`
  (tui/footer_data.go) keeps GoalStatus/GoalObjective/GoalPendingTodos
  across stats-only SetData calls; the explicit writer is the new
  `Footer.SetGoalData` (mirrors SetMinorMode's bypass pattern) — used by
  `updateGoalFooter`. This also fixes the goal status flickering off on
  every stats tick.
- Tests (green): `TestFooter_Render_GoalMarker`,
  `TestFooter_Render_GoalMarkerStatuses`,
  `TestFooterGoalFieldsSurviveStatsRebuild`.

---

## Issue 4 — Pending-todo markers after the mode + refresh on goal/todo change

**Request:** after the mode, one ⬩ per pending todo (up to 3); when more
are pending append `+x` (x = pending beyond the 3 shown):
`◈ coding-posture ⬩⬩⬩+x │ YOLO`. Must refresh on goal change AND todo
changes.

**Fix plan:**
1. `tui/footer_data.go`: add `GoalPendingTodos int` (preserve across
   routine footer updates like GoalStatus already is).
2. `internal/app/events.go updateGoalFooter`: count todos with
   status != done from the update snapshot (0 when snapshot nil).
3. `tui/footer_render.go`: after the profile label, render
   ` ⬩×min(3,n)` plus `+(n-3)` when n > 3 (before the ` │ ` separator).
4. Refresh: todo changes flow through `GoalUpdate` events
   (add_todo/update_todo publish snapshots); verify the todo tool path
   emits — if any todo mutation does NOT publish, make it publish so the
   footer refreshes (same lossless delivery as Issue 1).

**Test approach:** footer render tests: 0 → no markers; 2 → `⬩⬩`; 5 →
`⬩⬩⬩+2`; app test: `handleGoalUpdate` with a snapshot of 5 todos
(2 done) → `GoalPendingTodos == 3`.

**Validation steps:** interactive: goal with 4+ todos → markers visible;
mark one done via the model → one marker disappears without restart.

**Status: FIXED.**
- `FooterData.GoalPendingTodos` (tui/footer_data.go, preserved across
  routine rebuilds); `updateGoalFooter` counts not-done todos from the
  snapshot (`countPendingGoalTodos`, internal/app/events.go).
- Render: `goalProfileLabel` appends ` ⬩×min(3,n)` plus `+(n-3)` overflow
  after the profile label (tui/footer_render.go); markers require the ◈
  (no goal → no markers).
- Refresh on todo change: `GoalMode.persistTodosLocked` was `Silent: true`
  — todo mutations never published a snapshot, so the status line could
  not refresh. It now publishes (no Change → no chat marker; bubble +
  footer only), so model- or command-driven todo changes refresh the
  markers live.
- Tests (green): `TestFooter_Render_GoalTodoMarkers` (0/1/2/3/5/7
  pending), `TestFooter_Render_TodoMarkersRequireGoal`,
  `TestHandleGoalUpdate_CountsPendingTodos`,
  `TestTodoMutationsPublishSnapshots` (core/goal).

---

## Issue 5 — /todo command (CRUD over the active goal's todos)

**Request:** a `/todo` command to manage todos — CRUD with the typical
colon format (`/todo:list`), supporting positionals (`/todo:edit:1` edits
todo 1); the edit must use the input line with a title matching the todo
being edited.

**Fix plan:** new `core/commands/todo.go` (self-registered `init()`,
mirroring goal.go structure). Numbering = 1-based position in
`Mode.GetGoal().Goal.Todos` (same order as `/goal:current` list).
Subcommands:
- `/todo` and `/todo:list` — numbered list with status markers
  (`[x]` done, `[>]` in_progress, `[ ]` pending).
- `/todo:add:<title>` — add; bare `/todo:add` prompts via input line
  ("New todo title (ctrl-c to cancel)").
- `/todo:edit:<n>` — opens the main input PREFILLED with the todo's
  current title (`ctx.ShowInputFunc`, prompt `Edit todo <n>: <title>`),
  submit renames; `/todo:edit:<n>:<new title>` edits directly.
- `/todo:done:<n>` / `/todo:undone:<n>` — set status done / pending.
- `/todo:delete:<n>` (alias `rm`) — remove the todo.
Backend: add missing GoalMode todo ops (rename/remove — check existing
AddTodo/UpdateTodo first and reuse) with durable event-log records so
todos survive replay; every mutation publishes a GoalUpdate snapshot
(feeds Issue 4 refresh). No active goal → clear error per subcommand.

**Test approach:** `core/commands/todo_test.go` — table-driven arg parse
(incl. positional n, out-of-range n, non-numeric n); CRUD against a
GoalMode seeded with todos; edit-without-title captures ShowInputFunc and
asserts prefill = current title; replay round-trip preserves
renames/deletes.

**Validation steps:** interactive: `/goal:new:x`, `/todo:add:write
tests`, `/todo:list`, `/todo:edit:1` (input line opens prefilled, title
mentions the todo), `/todo:done:1`, `/todo:delete:1`.

**Status: FIXED.**
- New `core/commands/todo.go` (`/todo`, self-registered in
  `commands.RegisterAll` next to /goal, bound to the same GoalMode):
  `list` (numbered, [x]/[>]/[ ] markers), `add[:<title>]` (bare prompts
  on the input line), `edit:<n>[:<title>]` (bare opens the input line
  PREFILLED with the todo's current title via `ctx.ShowInputFunc`, prompt
  `Edit todo <n>: <title>`), `done:<n>`, `undone:<n>`, `delete:<n>`
  (alias `rm`). Positions are 1-based as printed by /todo:list;
  out-of-range/non-numeric/unknown forms give usage errors; every
  subcommand errors clearly with no active goal.
- Backend: new `GoalMode.RenameGoalTodo` / `GoalMode.RemoveGoalTodo`
  (core/goal/mode.go) — same persistence + snapshot-publish path as
  add/update, so /todo edits refresh the footer ⬩ markers immediately
  and survive replay.
- Help: core/commands/help/todo.long.md.
- Tests (green): `TestTodoCommand_Parse/List/ListNoGoal/Add/
  AddInteractive/Done/EditDirect/EditInteractive/Delete/OutOfRange/
  HelpRegistered`; core/goal `TestRenameGoalTodo`,
  `TestRemoveGoalTodo` (incl. replay round-trips).

---

## Issue 6 — Cancelled-goal tool widgets stay blue/ongoing

**Symptom:** one batch of ~23 parallel `goal cancel` calls: the first ~15
widgets stay `◉` (running/blue) with `elapsed 0.2s`, call detail
truncated mid-stream (`Cancelled goal minty{`) and NO result line; the
remaining widgets complete normally (`✓` + `Cancelled <name>: <obj>`).

**Root cause (to confirm — hypotheses):**
- (a) Cancelling the ACTIVE goal mid-batch aborts the in-flight turn
  (goal-mode StopTurn/interrupt): the sibling in-flight tool calls are
  torn down and their `tool_result` events never reach the widgets → they
  freeze in the streaming/running state (explains the partial-args header
  `minty{` — final args/result never applied).
- (b) `tool_result` events silently dropped on a full event bus (same
  lossy pattern as Issue 1b).
- (c) Turn-end/interrupt path does not force in-flight tool widgets into
  a terminal (interrupted) state.

**Fix plan (per confirmed root cause):** reproduce first (test below);
then ensure every started tool widget always reaches a terminal state —
either its result is delivered (fix the drop) or the turn-abort path
finalizes orphaned widgets as interrupted (✗/muted, "interrupted"), never
leaving a perpetual ◉.

**Test approach:** tui-test skill scenario: drive an agentic event
sequence with N parallel goal-cancel tool calls where the first cancels
the ACTIVE goal; filmstrip-assert every widget ends in a terminal state
with a result/interrupted line. Unit test: abort mid-flight tool calls →
widgets finalize.

**Validation steps:** interactive: queue 5+ goals, ask the model to
cancel all goals → no widget left blue.

**Status: FIXED** — root cause confirmed by deterministic reproduction
(`TestUI_CancelBatchNoStuckOrDuplicateWidgets`, filmstrip + TermEmulator
replay of the raw byte stream): the widgets' terminal transitions DID
happen in the DOM, but rows committed to terminal scrollback while the
widget was running are never repainted (compositor: "canvas rows are
immutable", the watermark culls diffs) — the ✓/✗ state of a
scrolled-off widget was invisible, leaving frozen "◉" ghosts. Hypotheses
(a) turn abort and (b) dropped result events REFUTED (DOM widgets all
reached terminal state; events flow via the unbounded forwarder).
- Fix: `App.echoScrolledOffToolResult` (internal/app/stats.go) — when a
  tool finishes while its widget is fully scrolled off, append a compact
  completion echo rendering the tool renderer's own summary (✓/✗ icon,
  ≤3 lines, ANSI-free) — every completion is visibly closed; wired into
  both the foreground path (`handleToolResult`) and the multi-agent path
  (`handleAgentToolResult`). Visible widgets never get an echo (no
  duplicate outcome).
- New: `ChatViewport.IsScrolledOff` (tui/chat_viewport.go) computes the
  geometry from the folded frame (lineOffset + renderCache vs allocated
  height); unknown geometry → false (never a spurious echo).
- Tests (green): `TestUI_CancelBatchNoStuckOrDuplicateWidgets` (16-call
  batch + mid-batch goal-bubble clear: every cancel has a visible
  completion), `TestUI_VisibleToolResultGetsNoEcho`,
  `TestChatViewport_IsScrolledOff` (+ `_FitsOnScreen`,
  `_UnknownGeometry`); full ./tui and ./internal/app green.
- Residual: the frozen ◉ rows themselves remain in scrollback (erasing
  them requires wiping the user's terminal history — the \x1b[3J wipe was
  deliberately removed); the echo guarantees the outcome is always
  visible, which is what read as "stuck on going".

---

## Issue 7 — Goal driver cannot be stopped (continuation loop after completion)

**Symptom:** after `✓ Goal complete` (verify passed) and `goal list`
reporting `No active goal · queue empty`, the driver keeps launching
`Continue working toward the active goal` turns in an infinite loop until
the user hits ESC. The model repeatedly finds no goal yet new
continuation turns keep arriving (user: "goal cannot be stopped (after
delete?)").

**Root cause (to confirm — hypotheses):**
- (a) TWO goal subsystems: the goal tool bound to one `GoalMode`
  instance, the driver/post-turn hook to another → the tool completes and
  clears instance A while the driver on instance B still sees an active
  goal (would explain tool saying "no active goal" while the driver keeps
  driving).
- (b) The completed goal is re-created after each completion
  (auto-unblock spawner, judge rejection path, or a stale in-memory queue
  copy re-promoting).
- (c) The drive loop's post-turn `GetActiveGoal()` check races with the
  completion clear (state cleared after the check, loop already launched
  the next turn — but then the NEXT check must stop it; a perpetual loop
  needs re-creation, pointing back to (a)/(b)).

**Fix plan (per confirmed root cause):** single source of truth for the
goal subsystem (one GoalManager wiring path — audit
`initGoalSystem`/`/tools:goal:on` factory for duplicate instances);
completion/cancel must reliably terminate the drive loop (post-clear
re-check + driver `Stop` on goal-cleared update); assert no re-creation
path fires after a plain completion.

**Test approach:** core test: real GoalMode + GoalDriver with a fake
runner — model completes the goal mid-turn → Drive exits, post-turn hook
does not restart it, no further `Run` calls. App wiring test: the goal
tool and the driver share the same `GoalMode` instance. Regression:
cancel-all + complete-last-goal scenario → loop stops.

**Validation steps:** interactive: create goal with trivial verify, let
it complete → turns stop; cancel all goals → no continuation prompt.

**Status: FIXED** — root cause (a) REFUTED (one GoalManager only; the tool
and driver share `subs.goalManager`); root cause confirmed as a **queue
storm**: a drive started while the agent is mid-turn (e.g.
`promoteNextQueuedGoal` firing on a mid-turn goal cancel/complete, or
`/goal:resume` typed mid-turn) hit `agent.Run`'s queue-on-busy semantics —
Run returned instantly, the Drive loop hot-spun, and hundreds of
continuation prompts flooded the agent's input queue; those phantom turns
kept draining even after the goal was cleared (ESC's interrupt was the
only stop — matches the transcript).
- `core.ErrAgentBusy` (core/goal_driver.go): a runner signals "agent busy
  with another turn"; Drive treats it as a clean stop (nil return, goal
  stays active — never paused); the in-flight turn's post-turn hook
  re-starts the drive when the agent is idle.
- `agentManagerRunner.Run`/`RunFresh` (internal/app/subsystems.go) return
  `ErrAgentBusy` when `agentMgr.IsRunning()` — a goal turn never queues
  into a busy agent again.
- Post-turn hook moved into `runAgentTurn`'s cleanup defer
  (core/agentmanager.go): fires AFTER `am.running=false` and steering
  dispatch (previously it fired with the turn still marked running, which
  would have tripped the new busy guard on every legit drive start), and
  is skipped after a panicking turn (no re-drive into a panic loop).
- Tests (green): `TestGoalDriver_BusyAgentExitsCleanly`,
  `TestGoalDriver_BusyThenRecovers`,
  `TestRunAgentTurn_PostTurnHookFiresAfterCleanup`,
  `TestRunAgentTurn_PostTurnHookSkippedOnPanic`,
  `TestAgentManagerRunner_BusyReturnsErrAgentBusy`,
  `TestAgentManagerRunner_IdleRunsAgainstCurrentAgent` (+ new test-only
  `AgentManager.SetRunningForTest`); full ./core/... green.
- Residual risk: a 1-prompt queue race (user turn starting between the
  busy check and agent.Run) remains — bounded to a single queued prompt,
  no storm; accepted.

---

## Issue 8 — @ file completion does not select the matching file

**Symptom:** typing `Review @plans/plan.md` shows a `── Most Used ──`
popup of fuzzy matches with `@plans/PLAN-00-TEST-INFRA.md` selected; the
exact match `@plans/plan.md` (the file exists) is neither first nor
selected.

**Root cause (confirmed by code trace):**
- (a) `FileCompleter.Complete` (tui/autocomplete.go:342) builds
  `Completion{Value, Display}` WITHOUT a Category → zero value
  `CatMostUsed` (iota 0) — files render under the wrong `── Most Used ──`
  header (with score-column rendering).
- (b) No ranking: fd results are returned in fd's arbitrary order (and
  the ReadDir fallback mixes prefix + fuzzy matches in directory order) —
  an exact/prefix match is not promoted above fuzzy matches, so the
  default selection lands on an unrelated file.

**Fix plan:**
1. Give file completions their own category (new `CatFiles` →
   `── Files ──` header) so the label is truthful.
2. Rank results: exact match first, then case-insensitive prefix matches
   (basename, then full path), then fuzzy — stable order within a tier.
3. When the typed @-token already names an existing file (exact path),
   suppress the popup (completion is done).

**Test approach:** `tui/autocomplete_test.go` — temp dir with
`plans/plan.md` + `plans/PLAN-*.md`: completing `@plans/plan` puts
`@plans/plan.md` first; category is CatFiles (header `── Files ──`);
completing the full exact path returns no candidates (popup suppressed);
fd-absent fallback ranked the same way.

**Validation steps:** interactive: `Review @plans/plan` → `plan.md`
selected first; full path typed → popup closes.

**Interactive validation (pty, 2026-07-29, real TUI via `script`):**
- Goal created → footer `◈ coding-posture │ YOLO`; adding todos one by
  one showed `⬩` → `⬩⬩` → `⬩⬩⬩` → `⬩⬩⬩+1` live (Issue 3+4 ✓).
- `/todo:list` rendered the numbered marked list; `/todo:done:1` → "Todo
  1 marked done."; `/goal:current` rendered name/status/stats/objective
  + "Todos (0/4 done):" (Issues 2, 5 ✓).
- RESTART with the persisted goal: footer seeded to
  `◈ coding-posture ⬩⬩⬩ │` with zero live events (Issue 1 ✓).
- `Review @plans/plan` popup: `› @plans/plan.md` selected first under
  `── Files ──`, PLAN-*.md fuzzy matches below (Issue 8 ✓).

**Status: FIXED.**
- New `CatFiles` completion category (tui/autocomplete.go) rendered as
  `── Files ──` (tui/editor_render.go); every file completion is stamped
  with it — no more zero-value `CatMostUsed` mislabel.
- `rankFileCompletions` (tui/autocomplete.go) orders candidates exact >
  typed-case prefix > case-insensitive prefix > fuzzy, shorter basename
  first within a tier — applied to BOTH the fd path and the readdir
  fallback (fd/readdir order previously won outright).
- Exact-path suppression: when the typed @-token already names an
  existing regular file, `FileCompleter.Complete` returns nil (popup
  closes — the path is done).
- Tests (green): `TestFileCompleter_CategoryIsFiles`,
  `TestFileCompleter_ExactAndPrefixRankFirst` (fd + forced-fallback),
  `TestFileCompleter_ExactPathSuppressesPopup`,
  `TestRankFileCompletions`, `TestCategoryHeader_Files`.
