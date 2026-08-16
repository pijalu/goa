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

## CRITICAL: context compaction shows "Unexpected compression method: ceiling" and drops messages instead of summarizing
Observed (screenshot): a banner `⚡ Context compacted (ceiling): 89% → 44% · 265
messages dropped · ~133415 tokens freed`, plus a surfaced string "Unexpected
compression method: ceiling". The user config intent is **95% → summarize**
(only soft/hard/error layers; no proactive "trigger"). Two problems:
(1) "ceiling" is the reactive context-ceiling *trigger label*, surfaced where a
compression strategy/method name is expected — confusing and unactionable.
(2) The reactive ceiling enforcer DROPPED 265 messages instead of the hard layer
SUMMARIZING them. Root cause (internal/agentic/agent_streaming.go:1705-1708 and
:170-173): each turn/round runs `maybeCompress()` (hard layer → Compact() →
summarize) and then UNCONDITIONALLY `enforceContextCeiling()`. When the hard
summarize fails (LLM summarize error — logged only at line 1706/171) the history
is untouched, still over the ceiling, so `enforceContextCeiling` front-cuts the
oldest messages and emits `emitCompactionResult("ceiling", ...)` — the destructive
drop the user saw. (Dev home config sets hard_percent: 60, so this fired at 60%.)
Expected: when hard=summarize and the summarize fails, surface the failure
clearly (no confusing "ceiling"/"method" wording) and do NOT silently
message-drop as if it were a normal compaction; the reactive ceiling should be a
last resort clearly labeled, and the failed summarize error must be visible.

DESIGN CONSTRAINT (user): the compression model must be EXACTLY three layers —
**soft / hard / error** — and nothing else. Remove the "trigger" layer
(thresholds.trigger_percent, strategies.trigger, the deprecated threshold_percent
alias, the tierTrigger tier, the legacy single `strategy` field) and remove the
derived "magic value" helpers that invent extra levels (escalationPercent =
hard−5, deferralCeiling = hard−10, elisionTargetPercent = hard−20,
reactiveTargetPercent = hard−50). Every threshold reference must be a configured
soft or hard value (or the single hard default 95), never a hard−N invention.
Default: hard = 95% → summarize.

## /cache:stats should show a bar chart of recent cache-hit values
Observed: `/cache:stats` shows cache stats per turn / as aggregate text.
Expected: render a horizontal bar chart of the LATEST api/cache value returned
per completion (not a per-turn breakdown). The last (rightmost) bar must be the
most recent completion. Width adapts to the available terminal columns: draw
"last X" values, up to 20, where X = min(20, available columns after the
label/percent gutter). Each bar's height/length encodes the cache-hit value for
that completion, labeled with its percentage; the chart should reuse the
existing CH color thresholds (bold green >= +1pt improvement, green minor
fluctuation, red >= 5pt drop) if per-bar coloring is supported.

## /tools output is unreadable — one long wrapped line per section
Observed: `/tools` renders the tool list as a single box where each section is
one very long line with tool names and full descriptions concatenated and
soft-wrapped (e.g. "ask_user_question Ask the user for clarification. bash Run a
shell command… goal Goal list manager: 1 ACTIVE goal pursued autonomously across
turns; queue auto-starts python Execute Python…"). Tool names run into
descriptions, descriptions wrap mid-sentence, and there is no per-tool line
break or column alignment — the list is effectively unreadable.
Expected: one tool per line, aligned columns (name / short description), full
sentences never wrapped mid-token, and the section headers on their own lines —
e.g. a two-column table:
  ask_user_question   Ask the user for clarification.
  bash                Run a shell command. …
Long descriptions should be truncated with an ellipsis to fit the terminal
width, with /tools:<name> available for the full text.

# Archive

## /tools output is unreadable — one long wrapped line per section (fixed)
Root cause: `listTools` (core/commands/docs.go) rendered each section as
`"  %-20s %s\n"` rows; at the panel's inner width the description column ran
long and the terminal soft-wrapped it mid-sentence, so names ran into
descriptions with no per-tool column alignment — the reported wall-of-text.
Fix: `listTools` now emits a **markdown table** (`| Tool | Description |` +
`| --- | --- |` + one `| `name` | desc |` row per tool), section headers as
bold text. Command output is added via `chat.AddSystemMessage` → `newSystemMessage`
→ `renderGoaPanel`, which markdown-renders non-preformatted text with the
`MDStreamRenderer` — and that renderer draws GFM tables as aligned bordered
tables. `mdTableCell` escapes any literal `|` in descriptions so the column split
holds. Tests: `TestIsPreformatted_MarkdownTable` + `TestRenderGoaPanel_MarkdownTable`
(tui/preformatted_test.go) pin that the table routes to the markdown renderer
(not the preformatted line path) and produces box-drawing columns; existing
`TestListTools_*` still pass. Gates: vet/staticcheck/gocyclo/gofmt clean;
gocognit `toggleTool`(16) is pre-existing on HEAD (verified via git stash).

## run_code tests fail on dev machine — HOME isolation (fixed)
Root cause: `TestRegisterTools_RunCodeRespectsEnabled` and
`TestRegisterTools_RunCodeDispatchDirEmptyWithoutProject` (internal/app/bootstrap_test.go)
load config via `config.NewCascadeLoader` WITHOUT isolating the goa home, so the
developer's real `~/.goa/config.yaml` (which sets `tools.enabled.run_code: false`)
overrode the embedded default and the tests failed asserting run_code is
registered by default. Same class as the archived `TestRunCodeDefaultsLoaded` fix.
Fix: `internal/app` package `TestMain` (headless_integration_test.go) now sets
`GOA_HOME` to a scratch dir for the whole package, so every cascade load sees only
embedded + test layers. `TestCrashLogPath/home_fallback` (crash_log_test.go) was
updated to compute its expectation from `internal.GoaHome()` (GOA_HOME-aware, the
same source `crashLogPath` uses) instead of `os.UserHomeDir()`. Verified: full
`go test -race ./internal/app` PASS with the real user HOME (run_code + crash tests
green). Gates: vet/staticcheck/gocognit/gocyclo/gofmt clean on changed files.

## team: allow defining member order / workflow (implemented)
Feature delivered: a team definition now carries an ordered `workflow:` of member
stages, run in list order, with optional feedback loops. Config
(`config/teams.go`): new `TeamWorkflowStage{Member, Prompt, LoopBackTo,
MaxIterations}` and `TeamDefinition.Workflow []TeamWorkflowStage`; validation
(`validateTeamWorkflow`, wired into `validateTeamDefinition` and exposed as
`TeamDefinition.ValidateWorkflow`) enforces that every stage references a defined
member, stage members are unique, and `loop_back_to` points to an EARLIER stage
(so the workflow has a defined entry and loops are backward). Example:
`workflow: [{member: architect}, {member: coder}, {member: reviewer,
loop_back_to: coder, max_iterations: 3}]` = architect → coder → reviewer ⇄ coder.
Execution (`multiagent/foreground_orchestrator.go`): new
`ForegroundOrchestrator.RunPipeline(ctx, *Pipeline, input)` runs caller-supplied
ordered stages reusing the existing runStage machinery (context accumulation
across stages, gates, steering); `nextStageIndex` implements the loop back-edge
(jump to the named earlier stage while `MaxIterations` remain, then continue
forward). `PipelineRun.RunStageAt` (multiagent/pipeline.go) supports revisiting a
stage on loop-back (NextStage's len cap would refuse); `LoopConfig.LoopBackTo`
added. Command (`core/commands/team.go`): `/team:run:<name>` activates the team
(registers members in the pool), builds the pipeline (`teamWorkflowPipeline`),
prompts for the task, and runs it; `/team:show:<name>` renders the ordered
workflow with loop annotations (`reviewer ⇄ coder (max 3)`) and the run hint;
completion + LongHelp updated.
Tests: config table cases (valid loop, unknown member, missing member, duplicate
member, unknown/forward loop target) + `TestTeamsYAML_ParseWorkflow`;
`TestRunPipeline_LoopsBackReviewerToCoder` proves the loop executes
(architect×1, coder×3, reviewer×3); command tests for the pipeline builder,
`/team:show` workflow display, and `/team:run` no-workflow guard.
Gates: `go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12` clean on
changed files (two `agent_tool_visibility_test.go` U1000 warnings are
pre-existing on HEAD); `go test -count=1 -race -cover ./config ./multiagent
./core/commands` PASS (80.0% / 67.6% / 60.4%).

## team: run breaks on slow LLM — stuck screen, companion error, incorrect statusbar (fixed — statusbar cleanup)
Root cause: the framework-driven companion stream marks the UI busy
(`SetCompanionBusy(true)`, `CompanionActivity:"reviewing"/"thinking"`) on
`stream_start`/`thinking_start`, but those indicators are cleared ONLY on
`stream_end` (`internal/app/orchestrator.go` `handleOrchestratorContentStream`
`stream_end` case). `stream_end` is emitted solely on a clean `EventEnd`
(`handleAgentEndEvent`). On a slow local LLM the companion request hits the
`multiagent.message_timeout` (default 120s) deadline and `companion.Run`
returns before `EventEnd` — so NO `stream_end` ever fires: the footer stays
stuck on "reviewing"/busy and the transcript companion section stays open
(the reported stuck screen + incorrect statusbar). The run itself did NOT
"kill the team run": `CompanionCoordinator.RunPostTurn` runs the review in a
background goroutine and surfaces the error as a flash, leaving the main agent
usable — the visible symptom was the never-cleared busy state.
Fix (a) — statusbar/screen accuracy: on companion-run error, `AfterMainTurn`
(multiagent/foreground_orchestrator.go) now emits a companion `stream_end`
(`From=companion, To=stream_end, Kind=content`) before returning the error.
This routes through the SAME cleanup path as a normal completion — clears
`CompanionBusy`, resets `CompanionActivity`, and collapses the section via
`SetDone("")` — so the UI always returns to idle even on timeout. The error is
still surfaced as a flash.
Fix (b) — "not retryable" revisited: the classification is already correct in
principle: `shouldRetryStreamError` retries `context.DeadlineExceeded` WHILE
the parent ctx is alive (a request-scoped deadline, e.g. a slow LM Studio load
— confirmed by the default 5-retry/backoff plan engaging in tests). The
failure surfaced as "not retryable" only because the companion's own
`messageTimeout` deadline had fired (parent dead → retry pointless). That hard
bound is intentional fail-open for a background reviewer; it is configurable
via `multiagent.message_timeout` (raise it for slow models, e.g. `5m`). No
code change needed for (b); the per-member `thinking_level`/timeout knobs plus
the (a) cleanup resolve the reported behavior.
Tests: `TestAfterMainTurn_FailureEmitsCompanionStreamEnd` (new, failing
provider aborts before EventEnd → asserts the companion cleanup `stream_end`
is emitted); full `TestAfterMainTurn_*`/`TestForegroundOrchestrator_*` and
`internal/app` orchestrator/companion/footer suites pass under `-race`.
Gates: vet/staticcheck/gocognit/gocyclo clean on changed files. NOTE: two
`internal/app` `run_code` bootstrap tests FAIL on this machine both before AND
after the change (pre-existing HOME-isolation issue — the developer's
`~/.goa` sets `tools.enabled.run_code:false`; see the archived run_code
entry); unrelated to this fix.
Evidence (original): /Users/muaddib/dev/testt/.goa/exports/goa-export-20260816-125612.zip.

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
