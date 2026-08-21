# Bug Fix Plan — /goal:next reuse shorthand (rfirst / rlast)

## Bug

> /goal:next:reuse does seems to imply a new context instead of reuse —
> validate code and check if goals can be created with a reuse — all
> goal:next command should support an optional reuse — eg: rlast/rfirst to
> reuse

## Investigation findings (validated against the code)

1. **`/goal:next:reuse:<text>` works correctly end-to-end.** Parse
   (`splitGoalNextArgs`) extracts the `reuse` token → `resolveFresh` maps it
   to `FreshContext: false` → `queueNext`/`queueLast` persist it in the queue
   → `promoteQueuedGoal` carries `FreshContext` into `CreateGoal` → the
   driver routes `FreshContext=false` goals through the ordinary `Run` path
   (conversation reused). Verified by routing `/goal:next:reuse:fix tests`
   through the real `Run` in a harness: the queued goal persisted
   `FreshContext=false`, objective intact. Docs (`goal.long.md`, GOALS.md)
   describe exactly this. **No bug in the plain `:reuse` form.**

2. **The `rfirst`/`rlast` shorthand does NOT exist.** `splitGoalNextArgs`
   only recognizes `first|last|fresh|reuse`; `rfirst` lands in the objective
   text (harness log: `mode="" objective="rfirst fix tests"`). A user typing
   `/goal:next:rfirst:…` silently queues a FRESH-context goal (the configured
   default) whose objective starts with the literal word "rfirst" — which is
   almost certainly the observed "reuse implies a new context".

3. **The interactive paths carry no context mode.** `/goal:next` bare,
   `/goal:manage` add rows, and the first-or-last prompt all call
   `resolveFresh("")` — the configured default — with no way to say reuse.
   That is by design (the text form exists); no change.

## Fix

Add the reuse-with-placement shorthand tokens to `splitGoalNextArgs`:

- `rfirst` → placementNext + mode "reuse" (explicit form of the default
  placement; symmetry with `first`)
- `rlast` → placementLast + mode "reuse"

Both still allow an explicit `fresh` override afterwards (e.g.
`/goal:next:rlast:fresh:x` — placement rlast, context fresh) since the token
loop consumes tokens in any order.

Also add `rfirst`/`rlast` to the `/goal:next:` tab-completion options and
document them in `goal.long.md`.

## Tests

- Extend `TestGoalCommand_parseNextArgs`: `rfirst`, `rlast`, `rlast fresh`
  orderings, and bare `rlast` (→ interactive with reuse mode).
- End-to-end: `/goal:next:rlast:fix tests` through `Run` queues
  `FreshContext=false` at placementLast (real store, mirrors the harness used
  in the investigation).

## Validation

- New + existing goal command tests pass.
- Gates: `go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12`,
  `go test -count=1 -race -cover ./core/commands/...`.

## Execution log

Status: **verified closed**.

### Changes

1. `core/commands/goal.go` — `splitGoalNextArgs` recognizes `rfirst`/`rlast`
   (reuse + placement shorthand); dispatch of the bare interactive forms
   (`/goal:next:reuse`, `/goal:new:reuse` with no text) now threads the parsed
   context mode into `promptCreateInteractive` instead of dropping it.
2. `core/commands/goal_command_create.go` — `promptCreateInteractive` takes
   the resolved `fresh` bool.
3. `core/commands/goal_command_manager.go` — manager add rows pass
   `resolveFresh("")` explicitly (same behavior, signature update).
4. `core/commands/goal_completion.go` — tab completion lists
   `next:rfirst`/`next:rlast`.
5. `core/commands/help/goal.long.md` — documents the shorthand.

### Tests

- `TestGoalCommand_parseNextArgs` — 5 new cases (rfirst, rlast, bare rlast →
  interactive+reuse, rlast+fresh ordering, fresh+rfirst ordering).
- `TestGoalCommand_NextAdd_ReuseShorthand` (new, end-to-end): rfirst → front
  of queue, rlast → end, both `FreshContext=false`, token stripped from the
  objective. Fails without the fix (token lands in the objective, goal is
  fresh by default).
- `TestGoalCommand_NextInteractive_ReuseCarriesContextMode` (new): bare
  `/goal:next:reuse` + typed objective queues with `FreshContext=false`.
- `TestGoalCommand_CompleteArgs_NextOptions` updated for 6 options; extracted
  `assertCompletionValues` helper to stay under the complexity budget.

### Investigation evidence

The plain `/goal:next:reuse:<text>` form was verified correct end-to-end
before any change (harness: queued goal persisted `FreshContext=false`);
`promoteQueuedGoal` carries the flag into `CreateGoal`, and the driver routes
`FreshContext=false` goals through the ordinary `Run` path. The reported
"reuse implies a new context" matches the pre-fix behavior of the
then-unrecognized `rfirst` token (silently queued fresh with the token in the
objective) and of the bare interactive form (mode dropped at dispatch).

### Quality gates (each run separately)

1. `go vet ./core/...` — clean
2. `staticcheck ./core/...` — clean
3. `gocognit -over 15 ./core/commands/` — 1 pre-existing warning
   (`TestGoalCommand_ManageReorderKeyedRealSelector`, unrelated)
4. `gocyclo -over 12 ./core/commands/` — pre-existing test warnings only
5. `go test -count=1 -race -cover ./core/commands/...` — pass (61.6%)
