# Session Todo status was absent from the status bar

Date: 2026-11-23
Status: Fixed and validated

## Observed behavior

Todo status changes were not reflected directly on the status bar. The same
live-update contract also had to be verified for goal lifecycle changes.

## Expected behavior

Every session Todo mutation and every goal-linked Todo mutation immediately
refreshes the visible open-Todo markers. Pending and in-progress items remain
open; done items stop counting. Goal creation, pause, resume, completion,
cancellation, and queue promotion must leave the footer showing current goal
state without waiting for an unrelated stats tick or restarting Goa. Session
and goal-linked lists remain isolated.

## Reproduction and root cause

The following command was run before the fix:

```text
go test -count=1 -timeout 30s ./internal/app -run 'TestFilmstrip_(SessionTodoStatusUpdatesFooter|GoalAndTodoStatusUpdatesFooter)' -v
```

`TestFilmstrip_SessionTodoStatusUpdatesFooter` failed: every real `todo_list`
mutation published zero status events and the rendered footer contained no
Todo markers. `TestFilmstrip_GoalAndTodoStatusUpdatesFooter` passed, including
pending, in-progress, done, pause, resume, and completion.

The defect was localized to the standalone session-Todo path. It had no change
publication and the footer had no session-list state to render. Goal snapshots
already published and reached the footer correctly.

## Executed fix plan

1. Drove the real `todo_list` and `goal` tool entry points, production goal
   publisher, app event handlers, footer, and Filmstrip recorder.
2. Added a tool-level session mutation callback that reports the open count
   outside the Todo mutex. Goal-linked mutations continue through `GoalMode`.
3. Wired startup and runtime tool construction to a typed `SessionTodoChange`
   footer event. The app handles it on the TUI command loop and requests a
   render immediately.
4. Added dedicated session-Todo footer state, preserved across routine
   `SetData` rebuilds. An active goal's linked Todo count takes precedence;
   clearing or pausing the goal reveals the independent session list again.
5. Added regression tests for add, in-progress, done, remove, clear, overflow
   markers, goal lifecycle updates, and session/goal list transitions.
6. Inspected actual Filmstrip terminal output, ran every quality tool
   separately, and reran the full race/coverage suite.

## Validation evidence

Targeted race tests passed:

```text
go test -count=1 -race -timeout 45s ./internal/app ./tui ./tools/todo -run 'TestFilmstrip_(SessionTodoStatusUpdatesFooter|GoalAndTodoStatusUpdatesFooter|SessionAndGoalTodoListsStayIsolated)|TestFooter_SessionTodoCountPreservedAndGoalTakesPrecedence|TestTodoList'
```

The interactive Filmstrip test was also run with `-race -v`. Its actual
terminal transcript showed, in order:

```text
coding-posture ⬩ │ YOLO       # one standalone open Todo
◈ coder │ SOLO                # active goal hides the session list
◈ coder ⬩ │ SOLO              # first goal-linked open Todo
◈ coder ⬩⬩ │ SOLO             # todo_list adds to the active goal list
◈ coder ⬩ │ SOLO              # one goal Todo done
◈ coder │ SOLO                # all goal Todos done
coder ⬩ │ SOLO                # completion restores the session Todo
coder │ SOLO                  # session Todo done
```

The session sequence separately verified `⬩⬩⬩+1` overflow rendering and its
transitions back to three, two, one, and zero open items. A footer unit test
also verified preservation across stats-only rebuilds and goal precedence.

Quality checks, each run separately:

- `go vet ./...` — passed.
- `staticcheck ./...` — passed.
- `gocyclo -over 12 .` — passed with no findings.
- `go test -count=1 -race -cover -timeout 5m ./...` — passed for all packages.
- `gocognit -over 15 .` — reported only unrelated pre-existing findings:
  `tools.TestAgentMCPCloseRemovesExtractedArtifacts` (23),
  `provider.TestResolveStartPlanAPIKey` (23),
  `app.TestMakeToolFactory_BuildsEveryConfigurableTool` (19),
  `app.initGoalSystem` (16), `core.(*AgentManager).executeRunner` (16), and
  `config.TestConfigurableTools_AllDefaultsMirrorEmbeddedConfig` (16). None
  of those functions was changed by this fix.
