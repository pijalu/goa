<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bugs closed 2026-08-03

Moved from bugs.md per guideline workflow.

---

## /config saves cross-field-invalid configuration (soft_percent > trigger_percent) — FIXED

- **Observed** (2026-08-03): setting compression thresholds via `/config`
  (menu or `/config:set`) accepted values that violate cross-field
  invariants; the invalid configuration was persisted to
  `~/.goa/config.yaml` and the NEXT start failed with:
  ```
  Config error: validation errors (1):
  context_compression.thresholds: soft_percent (85) must be ≤ trigger_percent (80)
  ```
  Reproduce: `/config:set context_compression.thresholds.trigger_percent 80`
  then `/config:set context_compression.thresholds.soft_percent 85` — both
  reported "Set ... = ..." and the bad value was saved.
- **Localization**: `core/commands/config_cli.go` `applyConfigSet` — the
  per-key setters (`setIntRange`, `setTriggerPercentClearLegacy`) check only
  their own range; nothing ran `Config.Validate()`
  (`config/config_validate.go` `validateThresholdOrder`: soft ≤ trigger ≤
  hard) before `persistConfigValue` wrote the value to the home config.
- **Fix applied**:
  1. `applyConfigSet` now applies the change to a `DeepCopy()` candidate
     first and runs `Config.Validate()` on it; on failure it reports the
     validation error in-band AND via `ctx.Flash` (internal-command router
     output is dropped by `echoCommandResult`, so without the flash the
     rejection would be silent in the TUI), leaving the live config, the
     runtime agent, and the on-disk config untouched. Only a validated
     candidate is committed and persisted.
  2. `Config.DeepCopy` was rewritten as a faithful structural deep copy
     (reflection over reference-kind fields: pointers, maps, slices,
     interfaces). The previous DeepMerge-based implementation silently
     dropped the entire ContextCompression block when compression was
     disabled — which both lost settings on `Save()` (latent data loss in
     the loader save paths) and would have let the
     `context_compression.enabled` toggle bypass candidate validation.
- **Tests**: `TestApplyConfigSet_RejectsCrossFieldInvalidThresholds`
  (soft/trigger/hard out-of-order rejected in both directions: live config
  untouched, home config untouched, rejection cites the invariant, flash
  emitted; in-order control still applies and persists),
  `TestApplyConfigSet_RejectsEnableWithInvalidThresholds` (enable toggle
  with pre-existing out-of-order thresholds rejected — the faithful-copy
  path), `TestDeepCopy_Faithful` / `TestDeepCopy_NoAliasing` (copy fidelity
  incl. disabled-compression block and `yaml:"-"` fields; no shared
  maps/slices/pointers). All failed before the fix (RED), pass after.
- **Validation**:
  - `go test ./core/commands/ ./config/ -count=1` green.
  - Guideline-6 checks run separately: `go vet ./...` clean; `staticcheck
    ./...` only pre-existing findings (15 U1000, 1 SA4006, ST1008/ST1005 in
    unrelated files); `gocognit -over 15 .` and `gocyclo -over 12 .` no new
    violations (only line shifts of pre-existing entries);
    `go test -count=1 -race -cover ./...` exit 0.
  - Interactive (guideline 5, ptydrive + real TUI): `/config:set
    context_compression.thresholds.trigger_percent 80` persisted, then
    `/config:set context_compression.thresholds.soft_percent 85` rendered
    the flash `⚡ Rejected context_compression.thresholds.soft_percent = 85:
    context_compression.thresholds: soft_percent (85) must be ≤
    trigger_percent (80) (not applied, not saved)` and was NOT persisted;
    control `/config:set ... soft_percent 75` persisted. Final
    `~/.goa/config.yaml`: `trigger_percent: 80`, `soft_percent: 75`.

**Status**: FIXED + validated.

---

## Goal-command tests broke after /goal:next front-insert (commit 7d476cb) — FIXED

- **Observed** (2026-08-03): `TestGoalCommand_NextAndReorder`,
  `TestGoalCommand_QueueManagerManage`, `TestGoalCommand_QueueManagerMoveAndDelete`
  failed on main (`invalid item index 2`; queue missing goals). Found while
  running guideline-6 checks for the /config fix above; fixed per "failure
  must be fixed, regardless of the source".
- **Root cause**: commit 7d476cb made `/goal:next` insert at the FRONT and
  reserved `first|last|fresh|reuse` as placement/context tokens. The tests
  queued objectives literally named "first"/"second"/"third", so "first" was
  parsed as a placement token (bare interactive form) and never queued; the
  remaining assertions targeted the old append order.
- **Fix applied**: tests updated to non-reserved objectives
  (alpha/beta/gamma) with expectations matching front-insert order;
  `TestGoalCommand_QueueManagerMoveAndDelete` was also strengthened to
  exercise a real move (second item moved up becomes front) and a precise
  delete (front item removed, other goal kept). Code behavior was the
  intended design — the tests predated the syntax change.
- **Validation**: the three tests pass; full `go test -count=1 -race -cover
  ./...` exit 0; `go test ./core/commands/ -count=3` stable.

**Status**: FIXED + validated.

---

## /goal:list omits goal details — no context run type, criterion, verify command, handover, budget, todos — FIXED

- **Observed** (2026-08-03): `/goal:list` (`showList` in
  `core/commands/goal.go`) shows only a subset of the recorded goal
  information. The current-goal entry shows name, managedBy, status,
  turn/token/elapsed counters and the objective; queued entries show only
  name and objective. The goal list should show ALL information on goals,
  including the context run type (fresh vs reuse context) — currently
  invisible, so there is no way to tell which context a goal will run with
  short of reading the queue file. Also missing: completion criterion,
  verify command, handover presence, budget limits, terminal
  reason/expectation (paused/blocked) and the todo list — all of which ARE
  recorded (and returned by the model tool's `list` action JSON).
- **Localization**: `core/commands/goal.go` `showList` +
  `writeGoalListEntry` — the current-goal metadata line is hardcoded to
  `status … · turns … · tokens … · elapsed` and queued entries pass an
  empty metadata string plus only the objective.
- **Plan**:
  1. Render every recorded field: context run type (`context fresh|reuse`)
     for both current and queued entries; completion criterion, verify
     command and handover presence as bullet blocks (mirroring
     `/goal:current`); budget used/limit summary when limits are set;
     terminal reason/expectation for paused/blocked current goals; todos
     with status markers for the current goal; queue time for queued goals.
  2. Keep the markdown block structure (blank-line-separated blocks) so the
     chat renderer keeps blocks distinct (bugs.md "Goal list issue").
  3. Tests: `TestGoalCommand_ListShowsAllInfo` — current goal with reuse
     context + criterion + verify command + handover + budget + todos;
     queued goal with fresh context + criterion + verify command; assert
     every element in the output; pause the goal and assert the terminal
     reason renders. Existing `TestGoalCommand_List` assertions (execution
     order, untruncated objectives) must keep passing.
  4. Run the guideline-6 checks separately; validate the rendered list
     interactively per guideline 5.

- **Resolution** (2026-08-03): `showList` rewritten to render every recorded
  field. New helpers in `core/commands/goal.go`: `contextRunLabel`
  (fresh/reuse context run type), `formatGoalBudget` (used/limit summary),
  `writeGoalAttrs` (criterion / verify command / handover-presence bullets),
  `writeGoalTerminalState` (reason/expectation bullets for paused/blocked),
  `writeCurrentGoalListEntry` (status, counters, context type, budget,
  attrs, terminal state, todos) and `writeQueuedGoalListEntry` (context
  type, queue time, attrs). Blank-line-separated markdown blocks preserved.
- **Tests**: `TestGoalCommand_ListShowsAllInfo` (current goal: reuse
  context, criterion, verify command, handover marker, budget, todos;
  queued goal: fresh context, criterion, verify command, queue time;
  paused goal: status + reason) — RED before the fix (verified via stash),
  green after. Existing `TestGoalCommand_List` still passes.
- **Guideline-6 checks** (run separately): `go vet` clean; `staticcheck`
  no new findings (SA4006 at goal_test.go:1531 is pre-existing in
  `TestGoalCommand_ManageDeleteHotkey`); `gocognit -over 15` and
  `gocyclo -over 12` no new violations; `go test -count=1 -race -cover
  ./...` exit 0.
- **Interactive validation** (ptydrive, real TUI, seeded queue file):
  `/goal:list` rendered `status paused · turns 1 · 0 tokens · 3s · context
  reuse · budget turns 1/50`, `• Reason: Paused after provider connection
  error`, `2. [queued] amber.owl`, `context fresh · queued 2026-08-03
  17:00`, `• Completion criterion: …`, `• Verify command: …`, `• Handover:
  attached`. All elements verified in the raw terminal output.


---

## Startup /config shows compression values that don't match the user config — stale full-config dump in `.goa/config.yaml` silently shadows the home config — FIXED

- **Observed** (2026-08-03, startup): `/config` → Compression settings showed
  `Trigger strategy  micro` while the user's config
  (`~/.goa/config.yaml`) sets `context_compression.strategy: tool_elision`;
  the project layer's other stale keys also mismatched the trigger threshold
  (80% displayed vs 90 configured) and the micro ratio (50% vs 0.8).
- **Reproduced** (2026-08-03, ptydrive real PTY, real home+project cascade):
  `/config` → filter `compression` → Enter → filter `trigger` rendered
  `Trigger strategy  micro` / `Trigger threshold  80%` instead of
  `tool_elision` / `90%`.
- **Root cause**: the project layer `.goa/config.yaml` held a stale
  `context_compression` block (`strategy: micro`, deprecated
  `threshold_percent: 80`, `min_context_ratio: 0.5`) that wins over the home
  config in the cascade (embedded → home → project → local). The block was
  not hand-written: `SaveProjectConfig` (`config/loader.go`) — invoked by
  `/mode`, `/autonomy` and the autonomy-cycle hotkey
  (`internal/app/shortcuts.go`) — marshaled the ENTIRE merged in-memory
  config (embedded defaults + home values) into the project file whenever
  the file did not exist yet (older versions did so unconditionally, before
  the field-scoped fix for Tools.Enabled). One hotkey press baked the
  then-effective values — including the embedded default `strategy: "micro"`
  from `config/configs/default.yaml` — into the project layer, where they
  silently shadowed every later home-config edit. The /config UI displays
  only effective merged values with no layer provenance, so the mismatch was
  invisible until the user opened the menu.
- **Fix applied**:
  1. `SaveProjectConfig` (`config/loader.go`): when `.goa/config.yaml` does
     not exist (or is unreadable as YAML), it now persists ONLY the `mode:`
     section — the documented field scope of this entry point — via
     `writeModeOnlyProjectConfig`, which marshals a
     `map[string]ModeConfig{"mode": ...}` rather than a sparse `Config`
     (most Config fields lack `omitempty`, so a sparse marshal would emit
     zero-value sections that shadow home values, e.g.
     `tools.terminal.sandbox.enabled: false`). The file-present path
     (on-disk fields preserved, only `Mode` overlaid) is unchanged.
  2. Repaired the already-contaminated project file: removed the stale
     `context_compression` block from this repo's `.goa/config.yaml`
     (backup retained), so the home config (`tool_elision`, trigger 90,
     ratio 0.8) applies.
- **Tests**: `TestSaveProjectConfig_NoExistingFile_WritesModeOnly`
  (`config/loader_saveproject_test.go`) — file-absent save writes only
  `mode:` (asserts the raw file contains none of `context_compression`,
  `providers:`, `models:`, `tools:`, `telegram:`, `execution:`,
  `active_model`, `orchestrator:`), the caller's mode change round-trips
  through a reload, and the compression strategy still resolves from the
  lower layers. RED before the fix (full dump contained every banned key),
  green after. Pre-existing
  `TestSaveProjectConfig_PreservesOnDiskToolFlags` keeps passing.
- **Guideline-6 checks** (run separately): `go vet ./...` clean;
  `staticcheck ./...` only pre-existing U1000 findings in unrelated
  packages (none in `config/`); `gocognit -over 15` / `gocyclo -over 12`
  only pre-existing violations in untouched files; `go test -count=1 -race
  -cover ./...` green (80 packages ok, 0 FAIL).
- **Interactive validation** (ptydrive, real TUI):
  1. Save path: fresh project dir, `/autonomy:solo` → created
     `.goa/config.yaml` contains ONLY the `mode:` section (no
     `context_compression`, no providers/models/tools) — verified against
     the raw file.
  2. Display path (the original failing scenario): real home+project
     cascade, `/config` → Compression → filter `trigger` renders
     `Trigger strategy  tool_elision` / `Trigger threshold  90%`; filter
     `ratio` renders `Micro: min context ratio (own gate)  80%`. All match
     the user config — verified in the raw terminal output.
- **Follow-ups filed in bugs.md**: latent config-merge/persist issues found
  during diagnosis (`Strategies` block never merged, `MicroCompaction`
  replaced wholesale, `enabled: false` in a config file ignored on load,
  `Save` home full-dump) — separate OPEN entry.

---

## Runaway-loop guardrail bricks the session; TUI warning omits the repeated sequence — FIXED

- **Observed** (2026-08-03, TUI session): the runaway-loop guardrail stopped
  the turn and the session could not continue ("session stopped due to a
  runaway loop; please review the conversation and retry"). There was no
  clear warning on the TUI before the stop, and from the transcript the
  alleged loop was not obvious — impossible to tell a genuine loop from a
  false positive of the detector.
- **Localization**: none of the guardrail messages carried the repeated
  sequence: `handleStreamLoopStrike` / `recoverFromStreamLoop`
  (`internal/agentic/agent_streaming.go`), `checkProgressLoop` /
  `checkLoopStopped` (`internal/agentic/agent.go`); the first-repeat
  progress strike was only an ephemeral model hint (no visible TUI event);
  and the goal driver's pause reason (`core/goal_driver.go`
  `handleTurnError`) dropped the guardrail error detail entirely, so the
  exact field scenario (goal session) never surfaced the stop text.
- **Fix applied**:
  1. `streamLoopScan` now returns the repeated sample: one byte-exact unit
     for Detector A, the shingle-dominated scanned tail for Detector B;
     `checkStreamLoop` stores it in `streamLoopSample` (reset per round).
     `exactChainSample` snaps the sample start to the nearest word boundary
     (≤ `streamLoopSampleSnap` = 20 chars) because Detector A fires at the
     smallest qualifying period and otherwise cuts the unit mid-word
     ("entence…" → "sentence…").
  2. New `internal/agentic/agent_loop_message.go`: `elideLoopSample`
     flattens whitespace and elides as `head...(N chars)...tail` (60/30
     runes, rune-safe) ONLY when the elided form is actually shorter
     (boundary: 106 runes kept, 107 elided); `loopEvidenceSuffix` renders
     ` (repeated: "…")`; `progressLoopSample` extracts the repeated content
     (content → thinking → tool-call/empty placeholder).
  3. All five message surfaces now carry the evidence: stream soft warning
     event, stream terminal error, the NEW visible progress-loop warning
     event on the first repeat (was ephemeral-only), the progress terminal
     error, and the latched-turn rejection (`loopStoppedSample`, cleared
     with the latch; `checkProgressLoop` restructured so the event emits
     after `a.mu` is released).
  4. `core/goal_driver.go`: a runaway pause stores
     `PauseRunawayLoop + ": " + err.Error()` — the bounded one-line
     guardrail message — so the TUI goal panel and `goal-events.jsonl`
     show what looped.
- **Detector validation (false positives)**: all existing FP fixtures pass
  unchanged (Option A/B/C analysis, quoted SQL evidence, enumerated lists,
  topical repetition); NEW `TestStreamLoop_NoFalsePositiveOnStructuredHeaders`
  — a long markdown report with repeated headers/table separators and varied
  per-section content — does not trip, including every mid-stream prefix.
  No detector tuning needed: the surfaced evidence lets the user judge
  borderline cases, which is what the field incident lacked.
- **Tests**: `TestElideLoopSample` (table: short kept, whitespace
  collapsed, 106/107 boundary, long, multibyte), `TestLoopEvidenceSuffix`,
  `TestProgressLoopSample`, `TestExactChainSample`,
  `TestProgressLoop_MessagesShowRepeatedSequence` (visible warning event +
  terminal + latch all carry the elided repeat; RED before the fix),
  `TestStreamLoopStrike_MessagesShowRepeatedSequence`,
  `TestStreamLoopStrike_TerminalErrorElidesLongRepeat` (kilobyte repeat
  elided, middle cut, bounded length),
  `TestStreamLoop_NoFalsePositiveOnStructuredHeaders`,
  `TestGoalDriver_RunawayPauseReasonShowsRepeatedSequence`.
- **Guideline-6 checks** (run separately): `go vet ./...` clean;
  `staticcheck ./...` no new findings (pre-existing unrelated:
  `hasStalled`/`repeatString` U1000, `runStrikeTurn` ST1008, all in code
  untouched by this change); `gocognit -over 15 .` / `gocyclo -over 12 .`
  no violations in changed files (one new test exceeded gocognit during
  development and was refactored below budget); `go test -count=1 -race
  -cover ./...` exit 0 (80 packages ok).
- **Interactive validation** (guideline 5, ptydrive + deterministic
  OpenAI-compatible stub server, real TUI, raw terminal output inspected):
  1. Stream loop (stub streams 8 copies of an 80-char block): the TUI
     rendered `Stream loop detected (warning 1 of 3)` / `(warning 2 of 3)`
     boxes and the terminal error, each with
     `(repeated: "sentence repeats verbatim while the stub server keeps talking")`
     — word-boundary-snapped evidence.
  2. Progress loop (goal-driven session, stub answers every turn with the
     same >107-char response): turn 2 rendered the visible
     `Runaway-loop warning: the assistant repeated the same response as the
     previous turn (repeated: "Here is the identical stub answer — a
     deliberately long resp...(91 chars)...d with a head marker and
     tail.")` box BEFORE the stop (previously no visible warning); turn 3
     paused the goal with the elided evidence in the TUI pause surface and
     persisted in `goal-events.jsonl`.

---

## Goal management command: execution-order list, +/- reorder, confirmed delete, add rows — FIXED

- **Observed** (2026-08-03): the goal management selector (`/goal:manage`,
  `showQueueManager` in `core/commands/goal.go`) did not match the desired
  workflow: (1) it listed only QUEUED goals — the active goal was absent, so
  the list was not the execution order; (2) reordering was a two-step flow
  (Enter on a goal, then "Move up"/"Move down" in a second selector,
  `promptQueueAction`) with no direct hotkeys; (3) deletion fired on '-'
  (and Delete/Backspace via the selector's generic `handleDelete`) with NO
  confirmation; (4) there was no way to add a goal from the manager — worse,
  '+' emitted the generic `__add__` sentinel, which fell through to
  `promptQueueAction("__add__")` and failed with "queued goal … not found".
- **Localization**: `core/commands/goal.go` `showQueueManager` (queue-only
  item build, unhandled `__add__` fall-through), `promptQueueAction`
  (two-step menu); `tui/selector.go` `handleHotkey`/`handleDelete` (global
  '+' = add, '-' = delete bindings shared with the /provider and /model
  pickers, where '-' must keep meaning delete), `isSelectorSentinel`.
- **Fix applied**:
  1. **Per-instance selector keymap** (the open design decision): new
     `tui.SelectorKeymap` with `ReorderMode`. The zero value keeps the
     default bindings ('+' → `__add__`, '-' → `__delete__`+value) so the
     /provider and /model pickers are untouched; with `ReorderMode`,
     '+'/'-' emit `__moveup__`/`__movedown__`+value for the highlighted
     non-sentinel row and are consumed silently on sentinel rows, while
     Delete/Backspace keeps emitting `__delete__`+value. Plumbed through
     `Selector.SetKeymap`, `TUI.ShowSelectorKeyed`, the new optional
     `core.Context.SelectOptionKeyedFunc` / `Context.SelectOptionKeyed`
     (falls back to `SelectOption` with default bindings when unwired),
     and `internal/app` wiring. Footer hints follow the active keymap
     ("+ up / - down / del delete" vs "+ add / - delete").
  2. **Execution-order list**: `managerItems` builds
     `-- add at start --`, the active goal (`[active] name — objective`,
     "running — not reorderable here"), the queued goals in run order,
     `-- add at end --`, `Done` — all `PreserveOrder` so the default
     alphabetical sort cannot scramble the execution order. The manager
     now opens even with an empty queue (previously printed
     "No queued goals." and returned) so the add rows are reachable.
  3. **Direct reorder**: `__moveup__`/`__movedown__` emits call
     `Queue.Move(id, "up"|"down")` and reopen the manager with the cursor
     (and ✓ marker) on the moved goal, so repeated presses keep moving it.
     The two-step `promptQueueAction` menu is removed; Enter on a goal row
     flashes the hotkey cheat sheet and reopens.
  4. **Confirmed delete**: a Delete/Backspace emit opens
     "Delete goal <label>?" (Yes/No, cursor defaulting to the safe "No");
     only Yes calls `Queue.Remove`; No/Escape returns to the manager with
     the cursor back on the goal. Move/delete/Enter emits on the
     `__active__` row are rejected with a flash pointing at
     /goal:pause|cancel|replace.
  5. **Add rows**: `__add_first__`/`__add_last__` open
     `promptCreateInteractive` — with an active goal, front = prepend to
     the queue (runs next) and end = append, never silently replacing the
     running goal; with no active goal the new goal starts immediately.
     The generic `__add__` emit (only reachable via the fallback host)
     routes to the same flow instead of the removed action menu. The new
     sentinels `__add_first__`/`__add_last__`/`__done__` are registered in
     `isSelectorSentinel` (also fixing the bogus
     "queued goal \"__done__\" not found" error when pressing Delete on
     the Done row).
- **Tests** (all RED before the fix where they pin new behavior):
  - `tui/selector_test.go` (table-driven): reorder-mode move emits
    (+/- → `__moveup__`/`__movedown__`+value, incl. on the `__active__`
    row), sentinel rows consumed without emit or search pollution, Delete
    and Backspace-with-empty-search still emit `__delete__` in reorder
    mode, the new sentinels non-deletable under the default keymap,
    reorder-mode footer hint.
  - `core/commands/goal_test.go`: `ManageListExecutionOrder` (layout,
    `[active]` marker, PreserveOrder, reorder keymap requested),
    `ManageMoveHotkeys` (4 table cases incl. boundary no-ops, stable
    cursor), `ManageActiveRowRejected` (4 emits, flash + untouched state),
    `ManageDeleteHotkey` + `ManageDeleteConfirmNo` (confirmed delete,
    Yes/No/Escape paths), `ManageAddRows` (4 table cases: front/behind ×
    active/no-active, prompt texts, immediate start), `ManageGenericAddEmit`
    (regression: `__add__` no longer hits a goal lookup),
    `ManageEnterOnGoalRow`, plus updated `ManageEmpty`,
    `QueueManagerManage`, `QueueManagerMoveAndDelete`, `runManageEditCase`.
  - **Regression**: `TestSelector_DefaultKeymapUnchanged` (zero keymap:
    '+' = `__add__`, '-' = `__delete__`+value, Delete/Backspace =
    `__delete__`+value), `TestProviderCommand_PickerKeepsDefaultKeymap`
    and `TestModelCommand_PickerKeepsDefaultKeymap` (both pickers use the
    default bindings — never `SelectOptionKeyedFunc` — and '+' still opens
    the add flow, '-' the remove confirmation).
- **Validation**:
  - Guideline-6 checks run separately: `go vet ./...` clean; `staticcheck
    ./...` clean for all touched files (one new SA4006 in the rewritten
    test fixed; remaining findings pre-existing in untouched files);
    `gocognit -over 15 .` / `gocyclo -over 12 .` no violations in changed
    files (new table-driven tests initially exceeded gocognit and were
    refactored below budget with shared helpers; pre-existing violations
    elsewhere untouched and unrelated); `go test -count=1 -race -cover
    ./...` exit 0 (all packages ok).
- **Interactive validation** (guideline 5, ptydrive against the real TUI in
  a PTY, isolated HOME + workdir, raw terminal output inspected):
  1. `/goal:manage` rendered the execution order — `› -- add at start --`,
     `one — goal one A`, `two — goal two B`, `three — goal three C`,
     `-- add at end --`, `Done` — with the footer
     `+ up / - down / del delete / e edit`.
  2. '-' on "goal one" persisted queue order [two, one, three] and the
     reopened manager showed `› ✓ one — goal one B` (cursor stable on the
     moved goal); '+' on "goal two" persisted [two, one] with
     `› ✓ two — goal two A` at the front.
  3. Delete on "goal one" rendered `Delete goal one — goal one?` with the
     cursor on the safe default `› ✓ No, keep it`; Enter (No) kept the
     goal, and a second Delete + `› Yes, delete it` removed it — final
     queue [goal two].
  4. With an active goal the manager rendered
     `[active] spry.lemur — main goal  running — not reorderable here`
     between the add rows; Enter on `-- add at start --` opened the
     `Queue a goal to run next — right after the active goal` input and
     prepended (queue [goal front, goal one]), Enter on `-- add at end --`
     opened `Queue a goal at the end of the queue` and appended (final
     queue [goal front, goal one, goal last]).
  5. With no active goal, Enter on `-- add at start --` opened
     `Set new goal objective (ctrl-c to cancel)` and the submitted goal
     started immediately (goal.create event; queue untouched).
