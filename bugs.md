# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell/filmstrip to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
    - `go vet ./...`
    - `staticcheck ./...`
    - `gocognit -over 15 .`
    - `gocyclo -over 12 .`
    - `go test -count=1 -race -cover ./...`
    Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

Use goals to execute the fix plan - focus on micro tasks goals with new contextto lower context usage - use todos for micro tasks that should share context

Commit at the end of each fix with a clear and descriptive commit message

## Report format
Describe the bug or feature request under `# To fix` below. Keep one section
per item with a short title, the observed behavior, and the expected behavior.

# TODO

## team: run breaks on slow LLM — stuck screen, companion error, incorrect statusbar
Observed: With a slow local LLM (LM Studio, google/gemma-4-e4b; companion model
qwen3.8-27b via nano-gpt), a `/team` run does not complete. The companion cycle
fails with `Companion error: companion run: LLM request failed (not retryable):
context deadline exceeded` (agent.log: `stream failure: context deadline exceeded`
→ `stream error not retryable; surfacing immediately`). The screen is left stuck
mid-run ("companion · cycle 1", thinking truncated at "Need") and the statusbar
shows stale/incorrect state. A single slow-LLM timeout kills the whole team run
instead of being isolated/retried per member.
Evidence: /Users/muaddib/dev/testt/.goa/exports/goa-export-20260816-125612.zip
(issue.md, logs/agent.log:776, companion started 12:53:16, failure 12:55:16).
Expected: (a) a per-member/companion LLM timeout must not hang the UI or abort the
whole team run — surface the error for that member and keep the screen/statusbar
accurate; (b) the "not retryable" classification of `context deadline exceeded`
should be revisited for slow providers (a slow model is recoverable).

## team: allow defining member order / workflow (feature)
Feature request: `/team` should let the user define the order and workflow of the
team — e.g. architect ⇄ coder ⇄ code reviewer — including bidirectional
hand-off/feedback loops, not just a fixed unordered set of members. Members should
pass work in a configurable sequence with the ability to iterate back (reviewer →
coder) until done.

# Archive

## race: TestRunWizardWithTerminal_FirstFrameRenders (fixed — InitialClear unsynchronized)
Root cause: `Compositor.InitialClear()` (tui/compositor.go) wrote the clear
sequence to the shared terminal WITHOUT holding `c.mu`, while every other
Compositor method (`Render`/`Restore`/`Clear`/`Buffer`) locks it. `TUI.Start()`
calls `InitialClear` on the caller's goroutine (NOT the renderLoop), so during
the wizard's `Start() → RenderNow() → RunLoops() → Stop()` lifecycle a
concurrent shutdown (`Stop`→`Restore`, which holds mu) or an in-flight frame
could interleave terminal writes with the clear — corrupting the CSI-2026 sync
stream and racing the detector in the environment where the failure was seen.
Fix: `InitialClear` now takes `c.mu` (one-line change + doc), restoring the
documented invariant "mu serializes Render/Restore/Buffer" for ALL terminal
access. Regression test `TestCompositor_InitialClear_SerializedWithRender`
(tui/compositor_initialclear_race_test.go) blocks the clear's terminal write
inside its critical section and issues a concurrent `Render`+`Restore` to
exercise the now-serialized path.
Note on reproducibility: the race could NOT be reproduced locally (500+ runs
of the wizard suite and the full `go test -race ./...` all pass before AND
after the fix — the test's mutex-guarded fake terminal and the atomic
`stopped`/`started` reads mask the interleaving on this machine). The fix is a
correctness hardening of a genuine invariant violation; the regression test
documents the intended serialization. Gates: `go vet`, `staticcheck`,
`gocognit -over 15`, `gocyclo -over 12` clean on changed files (the two
reported warnings — `render_trace.go sceneLayersTrace` U1000 and
`scrollOffUnstable` gocyclo 13 — are pre-existing, confirmed on stashed HEAD,
unrelated to this change). `go test -race -cover ./tui ./config` PASS
(tui 74.8%, config 80.2%).

## config: run_code not enabled by default (fixed — test isolation)
Root cause: `TestRunCodeDefaultsLoaded` (config/run_code_config_test.go) did NOT isolate
`HOME`, so `NewCascadeLoader` used the real `~/.goa` and loaded the developer's home
config — which set `tools.enabled.run_code: false`, overriding the embedded default.
Confirmed: with an isolated `HOME` the test passed; with the real HOME it failed.
Not a production-code bug: `loadDefaults` → `DeepMerge` → `ToolEnabledConfig.ApplyTo`
correctly record and merge the embedded `run_code: true`.
Fix: added `t.Setenv("HOME"/"USERPROFILE", t.TempDir())` to `TestRunCodeDefaultsLoaded`,
matching every sibling run_code test. Verified: `go test ./config/ -run TestRunCode`
all PASS.

## goal manage: '+' and '-' do not reorder goals (not reproducible — regression test added)
Investigated the full path. The reorder hotkeys are correctly wired end-to-end:
`/goal:manage` opens the selector via `SelectOptionKeyed(..., ReorderMode)`
(goal.go:947) → the TUI wires `SelectOptionKeyedFunc` → `ShowSelectorKeyed` →
`sel.SetKeymap(ReorderMode)` (commandcontext.go:75, tui.go:977) → selector
`handleHotkey` emits `__moveup__`/`__movedown__` on '+'/'-' (selector.go:272-284) →
`handleManagerSelection` → `moveManagerGoal` → `GoalQueueStore.Move` persists and the
manager reopens with the cursor on the moved goal (goal.go:1008-1063).
Verification: added `TestGoalCommand_ManageReorderKeyedRealSelector`
(core/commands/goal_test.go) driving a REAL `tui.Selector` (ReorderMode) fed the actual
'+'/'-' keys through `showQueueManagerAt`; all four scenarios pass. A separate probe
drove keys through the real `TUI.handleKey` router with the selector as a capturing
overlay and confirmed `+` emits `__moveup__` (previously existing tests only fabricated
the emit via SelectOptionFunc, bypassing the keyed path). Pre-existing
`TestGoalCommand_ManageMoveHotkeys` and `GoalQueueStore` move tests also pass.
Conclusion: no defect found in current code — the feature works as specified. If a
specific terminal still fails to reorder, reopen with the terminal type and the
`/goal:manage` key-log (`SetKeyLog`) trace.

## Cache HIT (fixed in 0f1e434)
Status bar shows CH:<avg last 10>%▸<last>% with per-element evolution coloring.
Colors: bold green >=+1pt, green minor fluctuation, red >=5pt drop. Orange removed.

## stats:cache (fixed)
/stats:cache now reads from the current session's turn history (all agent calls),
not the persistent cross-session usage store. Session summary (/stats:session)
and global usage (/usage) include average cache hit rate.
