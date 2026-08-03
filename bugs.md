# Bug and feature Tracking

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

---

## Runaway-loop guardrail bricks the session; TUI warning omits the repeated sequence — OPEN

- **Observed** (2026-08-03, TUI session): the runaway-loop guardrail stopped
  the turn and the session could not continue ("session stopped due to a
  runaway loop; please review the conversation and retry"). There was no
  clear warning on the TUI before the stop, and from the transcript the
  alleged loop was not obvious — so it is not possible to tell whether the
  runaway loop was real or a false positive of the detector.
- **Relevant code**: warning/stop messages built in
  `internal/agentic/agent_streaming.go` (`handleStreamLoopStrike` → "stream
  loop detected: the assistant kept repeating the same text after N
  warnings…") and `internal/agentic/agent.go` (`checkLoopStopped` →
  "session stopped due to a runaway loop…", `checkProgressLoop` → "runaway
  loop detected: the assistant repeated the same response N consecutive
  times…"). None of these messages include the repeated sequence itself,
  and the soft-strike warnings are ephemeral hints to the model rather than
  visible TUI warnings.
- **Plan**:
  1. Validate the detection is real: add (or surface) enough evidence in the
     message to confirm a genuine loop — the repeated sequence must be shown
     so the user can judge. Check the detector
     (`checkProgressLoop` / stream-loop strike path) for false positives on
     legitimate near-repetition (e.g. long structured outputs with repeated
     headers).
  2. TUI warning and stop messages must include the repeated sequence so the
     loop is visible, both on the soft warning strikes and on the final
     stop.
  3. Long sequences must be elided as
     `start of repeat...(x chars)...end of repeat` to avoid a multi-line
     dump in the TUI. The elision must kick in as soon as the full sequence
     is longer than its elided form (i.e. only elide when it actually
     shortens the display).
  4. Tests: unit test the elision helper (table-driven: short message kept
     as-is; long message elided; boundary where elided length crosses the
     raw length); agent-level test asserting the warning and terminal error
     strings contain the (elided) repeated sequence.
  5. Validate with guideline 6 checks run separately; verify the rendered
     TUI warning interactively per guideline 5.

---

## Goal management command: execution-order list, +/- reorder, confirmed delete, add rows — OPEN

- **Observed** (2026-08-03): the goal management selector (`/goal:manage`,
  `showQueueManager` in `core/commands/goal.go`) does not match the desired
  workflow:
  1. It lists only QUEUED goals — the active goal is absent, so the list
     is not the execution order.
  2. Reordering is a two-step flow: Enter on a goal, then pick "Move up"
     / "Move down" from a second selector (`promptQueueAction`). There
     are no direct hotkeys.
  3. Deletion fires on the '-' key (and on Delete/Backspace via the
     selector's generic `handleDelete`) with NO confirmation — the goal
     is removed immediately and the manager reopens.
  4. There is no way to add a goal from the manager. Worse, '+' emits
     the selector's generic `__add__` sentinel, which `showQueueManager`
     does not handle: it falls through to `promptQueueAction("__add__")`,
     whose actions then fail with "queued goal … not found".
- **Requested behavior**:
  1. The list shows the goals in EXECUTION ORDER: the active goal first
     (marked), then the queued goals in run order.
  2. '+' moves the highlighted goal UP one position and '-' moves it
     DOWN one position, directly (no submenu). This repurposes '-' from
     delete to move-down.
  3. Deletion happens via Delete/Backspace and asks for confirmation
     before removing the goal.
  4. The list contains sentinel rows `-- add at start --` (first row)
     and `-- add at end --` (last row); selecting one opens the
     create-goal flow inserting the new goal in front / behind in the
     execution order.
- **Relevant code**:
  - `core/commands/goal.go`: `showQueueManager` (item build,
    `__delete__` / `__done__` handling, reopen loop),
    `promptQueueAction` (two-step move/delete menu), `promptFirstOrLast`,
    `promptCreateInteractive`, `showList` (already renders active +
    queued in execution order).
  - `tui/selector.go`: `handlePrintable` ('+' → `__add__`, '-' →
    `__delete__`+value — global for all pickers), `handleDelete`
    (Delete/Backspace → `__delete__`+value), `isSelectorSentinel`
    (exact-match sentinel rows are non-deletable: `__add__`,
    `__custom__`), footer-hint rendering.
  - Queue primitives: `core.GoalQueue` `Move(id, "up"|"down")`,
    `Remove(id)`, `Read` (wired via `goalManager.Queue` in
    `internal/app/subsystems.go`).
- **Open design decisions** (resolve during implementation):
  - The selector's '+', '-', Delete/Backspace bindings are GLOBAL (used
    by the /provider and /model pickers, where '-' must keep meaning
    delete). Make the bindings/emits configurable per selector instance
    (or add explicit move-up/move-down emits) with the current behavior
    as the default, and update the footer hints accordingly.
  - Active goal: displayed (item 1) but not movable; deleting it routes
    through the existing cancel/confirm path or is rejected with a
    flash.
  - `-- add at start --` when a goal is active: insert at the front of
    the queue (runs next) — never silently replace the running goal;
    replacement keeps going through the existing replace-confirm flow.
    When no goal is active, it starts the new goal immediately.
  - New sentinel values (e.g. `__add_first__` / `__add_last__`) must be
    registered in `isSelectorSentinel` so they are neither deletable nor
    movable.
- **Plan**:
  1. Selector: per-instance keybinding/emit configuration for move-up
     ('+'), move-down ('-'), delete (Delete/Backspace) and the add rows;
     defaults keep today's behavior so the /provider and /model pickers
     are unaffected. Footer hints reflect the active bindings.
     `-- add at start --` / `-- add at end --` render as sentinel action
     rows (non-deletable, non-movable).
  2. Manager list: build items as `-- add at start --`, the active goal
     (marked `[active]`, if any), the queued goals in order,
     `-- add at end --`, `Done`.
  3. Reorder: '+'/'-' emits call `Queue.Move(id, "up"|"down")` and reopen
     the manager keeping the cursor on the moved goal; the active goal
     and sentinel rows are not movable.
  4. Delete: a Delete/Backspace emit opens a confirmation selector
     ("Delete goal <name>?" Yes/No); only Yes calls `Queue.Remove` (or
     cancels the active goal via the existing confirm path); No returns
     to the manager. Remove the '-' delete path from the manager.
  5. Add rows: `-- add at start --` / `-- add at end --` open
     `promptCreateInteractive` with front/behind placement per the design
     decisions above; handle the generic `__add__` emit so it no longer
     reaches `promptQueueAction`.
  6. Tests: table-driven selector tests (move-up/down emits, Delete and
     Backspace-with-empty-search delete emits, sentinel rows immune,
     default bindings unchanged); goal-command tests for list order,
     cursor-stable reorder, confirmed delete (Yes/No), add-row routing;
     regression tests that the /provider and /model pickers keep
     '+' = add and '-' = delete.
  7. Validation: run the guideline-6 checks separately; verify the
     manager interactively per guideline 5 (execution order shown,
     +/- moves with stable cursor, delete asks for confirmation, add
     rows create goals front/behind).
