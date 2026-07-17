# TODO

Plan mode — see [specs/plan-mode-tasks.md](specs/plan-mode-tasks.md) (micro-task execution plan; implements [specs/plan-mode.md](specs/plan-mode.md)).

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

### ⏳ Phase 7 — Status overlay + footer
Not yet implemented. Requires `tui/plan_status.go`.

### ⏳ Phase 8 — Housekeeping + docs
Retention sweep, `--plan` headless flag, deprecation notes. `docs/PLAN.md` created.

### Gate status
`go build ./...` ✅
`go vet ./...` ✅
`go test -count=1 -race ./...` ✅
