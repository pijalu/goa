# Standalone Todo markers survive session/tool lifecycle changes

**Status:** closed and validated on 2026-11-27.

## Observed behavior

The status bar could show `coding-posture ⬩⬩⬩+1` after the visible work was
over. Four genuinely open standalone Todos intentionally render as three glyph
markers plus `+1`; that overflow presentation was not the defect. Reproduction
and lifecycle review found stale source state and preserved footer counts after
session and tool replacement:

- `/new` left the existing standalone list intact.
- Runtime disable/re-enable could construct a fresh empty tool while the footer
  retained the old `SessionPendingTodos` value.
- `/session:restore` and `/fork` retained the previous list and markers.
  Standalone Todo changes are footer events, not persisted agent events, so
  replay can neither reconstruct nor clear that state.

## Expected behavior

The footer reflects the current session's actual standalone list. New, restored,
and forked sessions clear standalone state. Tool teardown cannot leave orphaned
markers. Goal-linked Todos and active-goal precedence remain independent, and
valid `⬩⬩⬩+1` overflow rendering is preserved.

## Reproduction and fix plan

1. Drive four real standalone Todo additions through production event handlers;
   confirm the exact `coding-posture ⬩⬩⬩+1` footer, then run `/new` and assert
   both source state and rendered markers reset.
2. Disable and re-enable `todo_list` with four open items; assert the retained
   tool and footer are both empty immediately.
3. Restore a session with no persisted standalone Todo state and fork with an
   existing item; assert both boundaries clear the source list and first fresh
   terminal frame.
4. Re-run ordinary Todo mutations and goal/session isolation filmstrips,
   including paused/blocked goals and standalone restoration after completion.
5. Localize ownership at the application/tool lifecycle boundary, keep source
   and footer updates synchronous from the user's perspective, and run all
   required quality gates separately.

## Regression evidence

Before the fix, the restore/fork filmstrip command failed with:

- `standalone Todos survived fork: 1 items` and a rendered `coder ⬩` footer;
- `standalone Todos survived session restore: 4 items` and a rendered
  `coder ⬩⬩⬩+1` footer.

The original `/new` reproduction likewise retained all four items and rendered
`coding-posture ⬩⬩⬩+1`. Tool lifecycle testing showed an empty replacement tool
with the old four-item footer count.

## Implementation

- The application owns one shared `TodoListTool`; startup registration and
  runtime re-enable use the same instance instead of independent list owners.
- `TodoListTool.ResetSession` clears standalone items, resets ID allocation,
  unlocks before notification, and publishes the normal zero-count event.
- `/new`, tool teardown, session restore, and fork reset standalone source state
  and synchronously clear the preserved footer count. Restore/fork reach this
  through an optional `core.Context.ResetSessionTodosFunc` host callback before
  replay; headless contexts remain nil-safe.
- ResetSession does not mutate goal-linked Todos. Goal status still takes
  precedence in the footer, and standalone status can be restored when the goal
  completes.
- Valid overflow rendering was not changed or suppressed.

## Regression coverage

- `internal/app/todo_status_filmstrip_test.go`: real four-add reproduction,
  `/new`, disable/re-enable, named session restore, ordinary mutations, and
  goal/session isolation.
- `internal/app/fork_filmstrip_test.go`: the existing production selector/replay
  filmstrip now also proves standalone state and footer markers clear on fork.
- `tools/todo` tests exercise the real tool state and notifications.

After the fix, the restore filmstrip ends with `coder │ SOLO` and no Todo glyph;
the fork, `/new`, and tool lifecycle frames are also marker-free. Four open
items still render `coding-posture ⬩⬩⬩+1` before the lifecycle boundary.

## Validation results

- The original failing restore/fork filmstrips pass under the race detector.
- Focused tests for all touched packages pass:
  `go test -count=1 -race -cover -timeout 120s ./config ./tools/todo ./core ./core/commands ./internal/app`.
  Todo coverage is 92.2%; app coverage is 67.4%.
- `go vet ./...`: pass.
- `staticcheck ./...`: pass.
- `gocyclo -over 12 .`: pass with no findings.
- `gocognit -over 15 .`: no changed-code findings. It exits nonzero only for six
  known unrelated pre-existing functions: `TestResolveStartPlanAPIKey` (23),
  `TestAgentMCPCloseRemovesExtractedArtifacts` (23),
  `TestMakeToolFactory_BuildsEveryConfigurableTool` (19),
  `AgentManager.executeRunner` (16), `initGoalSystem` (16), and
  `TestConfigurableTools_AllDefaultsMirrorEmbeddedConfig` (16).
- `go test -count=1 -race -cover -timeout 180s ./...`: pass across the full
  repository.
- `git diff --check`: clean.
- No validation server or browser was started, so no validation process cleanup
  was required.

## Residual risk

Standalone Todo state is intentionally session-memory only; restore/fork clear
it rather than attempting to reconstruct it from unrelated persisted events.
No known goal-precedence, race, or rendering regression remains.
