# TODO

Plan mode — see [specs/plan-mode-tasks.md](specs/plan-mode-tasks.md) (micro-task execution plan; implements [specs/plan-mode.md](specs/plan-mode.md)).

## Review-fix plan (2026-07-17, vs main)

Critical review of `feature/plan` found the items below. Status: ✅ done · 🔲 pending.

### bugs.md fixes (correctness)

1. ✅ **Connect-phase timeout** — `TransportRequest.Timeout` now bounds dial→first-response-header only (`ResponseHeaderTimeout` on a cloned transport), not the whole stream. Slow local models (LM Studio) can stream for hours; a server that never answers is aborted at the default 5 min. Tests: `TestHTTPTransportHeaderTimeoutFiresOnHang`, `TestHTTPTransportHeaderTimeoutAllowsSlowStream`.
2. ✅ **Progress-clear no-op** — `handleProgressEvent` ignored `EventProgress{Text:""}`; the `finishProcessing` cleanup emission never reached the spinner. Now empty text → `statusMsg.Clear()`. Test: `TestBugs_ProgressClearClearsStatus`.
3. ✅ **Write tool stats (still wrong)** — root cause: `buildWritePreview` fences only 10 lines; post-completion the renderer counted the preview. `resolveContent` now prefers retained args when complete (result wins on error for the "(interrupted)" sentinel). App also normalizes `OutputEvent.ToolResult` → `Text`. Tests: `TestWriteFileRenderer_CompletedWriteShowsTotalLines`, `TestBugs_WriteToolStatsShowsTotal`.
4. ✅ **Steering edit-before-send** — Alt+E recalls pending steering into the editor, flushes the queue, clears bubble+footer. Preview skips leading blank lines. Tests: 🔲 owed (`handleEditSteering`, blank-leading preview).
5. ✅ **Sessions restore** — `ListSessions` filters conversation-less sessions (no user/assistant content) and sorts newest-first with a name tiebreak. Tests: `TestSessionListSessions_FiltersEmptySessions`, `TestSessionListSessions_NewestFirst`.

### Plan-mode integration

6. ✅ **Use-after-close crash (P0)** — `PlanBinder.startExecution` closed the store via `defer` while the background goroutine later called `store.Fail` → panic on write to closed file. Store ownership now transfers to the run goroutine; panic recovery; `Finish`/`Fail` recorded; flash progress via event bus. Tests: 6× `TestPlanExecution_*`.
7. ✅ **Dead pager/status events** — `ShowPlanPager`/`ShowPlanStatus` were never emitted/consumed; `cmdReview`/`cmdStatus` returned "not yet wired". Now: commands emit events (headless → Markdown fallback), app opens overlays (`showPlanPager`/`showPlanStatus`), pager approve → execution via `OnPlanApproved` + fresh store handle. Review modal handlers generalized with help-title param. Tests: 🔲 overlay e2e owed.
8. ✅ **Double store handle + approve retry** — `cmdApprove` passes its own open store to `StartExecution` (single handle); re-approve after a failed start skips `Approve()` (idempotent retry). Tests: `TestPlanApprove_*`.
9. 🔲 **TODO.md/bugs.md housekeeping** — TODO.md phase statuses corrected below; bugs.md archive per guideline #8 with accurate root causes.
10. 🔲 **Full gate** — `go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12`, `go test -count=1 -race -cover ./...` (each separately).

### Known limitations (accepted)

- Plan-bound runs use a process-lifetime context (no UI cancel affordance yet); progress is surfaced via chat flashes.
- `plan_id` is stamped on `run_started`; run agents do not yet auto-report `task_outcome`, so `Finish` may leave plans in `executing` (surfaced to the user via flash).

## Implementation Status

### ✅ Phase 1 — Plan model, store, renderer (`core/plan/`)
All 7 tasks complete. Coverage: 90.7%.

### ✅ Phase 2 — Config (`config/`)
Role context_window/max_tokens fields, validation, plan retention config, defaults, merge.

### ✅ Phase 3 — Tools (`tools/plan/`)
Plan tool (11 actions + phase enforcement), task_outcome tool, TUI renderers, registration.

### ✅ Phase 4 — Pager extraction (`tui/`)
`tui/annotate/` generic pager core extracted. ReviewPager tests unchanged (26 tests). PlanPager with navigation, comments, approve, submit.

### ✅ Phase 5 — Commands (`core/commands/`)
`/plan` command skeleton with subcommands (new/review/approve/status/replan/list/delete). Planner prompt (`prompts/plan/planner.md`). Event types (`ShowPlanPager`, `ShowPlanStatus`).

### ✅ Phase 6 — Execution binding
`Runtime.SetPlanID`, plan_id in run_started payload. `PlanBinder` in `internal/app/` wires approval → orchestrator run.

### ✅ Phase 7 — Status overlay + footer
`tui/plan_status.go` exists (was implemented with Phase 4-6 but never wired).
Wiring completed in the review-fix round: `/plan status` opens the overlay via
`ShowPlanStatus`; `/plan review` opens `PlanPager` via `ShowPlanPager`.

### ⏳ Phase 8 — Housekeeping + docs
`docs/PLAN.md` ✅ created. Pending: retention sweep, `--plan` headless flag,
deprecation notes.

### Gate status
`go build ./...` ✅ (2026-07-17 review-fix round)
`go vet ./...` ✅
`go test -count=1 -race ./...` ⏳ run per-package during fixes; full-tree run owed (item 10)
