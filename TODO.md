# TODO

Plan mode — see [specs/plan-mode-tasks.md](specs/plan-mode-tasks.md) (micro-task execution plan; implements [specs/plan-mode.md](specs/plan-mode.md)).

## Review-fix plan (2026-07-17, vs main)

Critical review of `feature/plan` found the items below. Status: ✅ done · 🔲 pending.

### bugs.md fixes (correctness)

1. ✅ **Connect-phase timeout** — `TransportRequest.Timeout` now bounds dial→first-response-header only (`ResponseHeaderTimeout` on a cloned transport), not the whole stream. Slow local models (LM Studio) can stream for hours; a server that never answers is aborted at the default 5 min. Tests: `TestHTTPTransportHeaderTimeoutFiresOnHang`, `TestHTTPTransportHeaderTimeoutAllowsSlowStream`.
2. ✅ **Progress-clear no-op** — `handleProgressEvent` ignored `EventProgress{Text:""}`; the `finishProcessing` cleanup emission never reached the spinner. Now empty text → `statusMsg.Clear()`. Test: `TestBugs_ProgressClearClearsStatus`.
3. ✅ **Write tool stats (still wrong)** — root cause: `buildWritePreview` fences only 10 lines; post-completion the renderer counted the preview. `resolveContent` now prefers retained args when complete (result wins on error for the "(interrupted)" sentinel). App also normalizes `OutputEvent.ToolResult` → `Text`. Tests: `TestWriteFileRenderer_CompletedWriteShowsTotalLines`, `TestBugs_WriteToolStatsShowsTotal`.
4. ✅ **Steering edit-before-send** — Alt+E recalls pending steering into the editor, flushes the queue, clears bubble+footer. Preview skips leading blank lines. Tests: `TestHandleEditSteering_*`, `TestSteeringPending_Render_LeadingBlanksSkipped`.
5. ✅ **Sessions restore** — `ListSessions` filters conversation-less sessions (no user/assistant content) and sorts newest-first with a name tiebreak. Tests: `TestSessionListSessions_FiltersEmptySessions`, `TestSessionListSessions_NewestFirst`.

### Plan-mode integration

6. ✅ **Use-after-close crash (P0)** — `PlanBinder.startExecution` closed the store via `defer` while the background goroutine later called `store.Fail` → panic on write to closed file. Store ownership now transfers to the run goroutine; panic recovery; `Finish`/`Fail` recorded; flash progress via event bus. Tests: 6× `TestPlanExecution_*`.
7. ✅ **Dead pager/status events** — `ShowPlanPager`/`ShowPlanStatus` were never emitted/consumed; `cmdReview`/`cmdStatus` returned "not yet wired". Now: commands emit events (headless → Markdown fallback), app opens overlays (`showPlanPager`/`showPlanStatus`), pager approve → execution via `OnPlanApproved` + fresh store handle. Review modal handlers generalized with help-title param. Tests: `TestShowPlanPager_OpensOverlayAndCloseClosesStore`, `TestShowPlanPager_ApproveChainsAndCloses`, `TestShowPlanStatus_OpensOverlayAndClosesStore` (internal/app/plan_overlay_test.go).
8. ✅ **Double store handle + approve retry** — `cmdApprove` passes its own open store to `StartExecution` (single handle); re-approve after a failed start skips `Approve()` (idempotent retry). Tests: `TestPlanApprove_*`.
9. ✅ **Housekeeping** — TODO.md phase statuses ✅ corrected below. bugs.md archived per guideline #8 (→ `docs/archive/bugs.2026-07-17.md`) with corrected stuck-in-sending root cause (connect-phase timeout + `executeRunner` EventEnd + wired progress-clear — NOT the no-op emission originally credited) and the two new items (sessions, write stats). bugs.md reset to guidelines-only.
10. ✅ **Full gate** — `go vet` ✅, `go build` ✅, `staticcheck` ✅ (7 pre-existing unused-func warnings, none from this branch), `gocognit -over 15` / `gocyclo -over 12` ✅ (all flags pre-existing at merge-base, none introduced by this branch), `go test -count=1 -race -cover ./...` ✅ (all 60+ packages pass, zero failures; includes fix for `AllowEmptyResponse` multiagent regression).

> **Handoff**: all review-fix items complete. Gate passed. Branch ready for merge.

## Live debug session (2026-07-17, later): "session stops without any message"

Reproduced live in the goa TUI (provider `kimi-code`, model `k3`): the turn
ends mid-reasoning, spinner clears, **no answer, no error**. Session
`.goa/sessions/1784300211_i94za4if.jsonl` + export `goa-export-20260717-171452.zip`.

**Diagnosis (two independent defects):**

1. **Silent empty turn (root cause of the visible symptom).** The k3 reasoning
   model returns `finish_reason: stop` after emitting **only thinking tokens** —
   zero answer content, zero tool calls (session: 942 thinking deltas, 0
   content, 0 tool calls, then `end`). `finishStreamTurn` → `completeStreamTurn`
   → `finalizeStreamTurn()` appended the (thinking-only) message to history and
   emitted `EventEnd`; the UI only ever got `StateThinking` deltas, so the
   spinner cleared with nothing shown. Trace confirmed `lastRole=tool`,
   `finish=stop` (model stopped after a tool result instead of continuing).
   **Fix:** `finalizeStreamTurn` now emits a non-transient `system-notification`
   when a turn ends with `contentBuf` empty but `thinkingBuf` non-empty,
   telling the user the model stopped mid-reasoning (reasoning/output-token
   limit) and to send "continue". File: `internal/agentic/agent_streaming.go`.
   Tests: `TestAgent_ThinkingOnlyTurnEmitsNotice` (RED without fix),
   `TestAgent_ContentTurnNoNotice` (no false positive).
   New file: `internal/agentic/agent_silent_stop_test.go`.

2. **HTTP log blindness (user: "are you sure a http error would correctly be
   logged?" — answer was NO).** `logOnCloseBody.finalize()` (a) never recorded
   a mid-stream body-read error → entries always showed `err=None`, and (b)
   froze `DurationMs` at header-arrival time → a stalled/long stream logged as
   an instant. Made stream health undiagnosable from the log.
   **Fix:** `finalize(readErr)` records non-EOF read errors into `entry.Error`
   and computes `DurationMs` at stream termination. File:
   `internal/agentic/provider/transport/http.go`. Tests:
   `TestHTTPLogRecordsMidStreamError`, `TestHTTPLogDurationReflectsStreamTime`
   (both RED without fix).

**Rebuild:** `go build ./...` (or `go install ./cmd/goa`). Both fixes are in
`internal/agentic/`; no config/flag changes. Verified: build + vet clean;
`internal/agentic/...` and `provider/transport` test suites pass.

**Follow-ups (not done):** consider surfacing the model's raw `finish_reason`
(e.g. `length`) in the notice; consider auto-retry/nudge once on thinking-only
stop instead of only notifying. Also note: the *reason* k3 stops mid-reasoning
is provider-side (output/reasoning token budget) — goa now reports it instead
of hiding it.

### Swallowed-error hardening (2026-07-17, k3 load)

User: "make sure there are no swallowed errors — k3 has load issues / can
trigger error unexpectedly." Audited the provider→agent→UI error path. Two
real swallows found and fixed (in addition to the thinking-only notice above):

3. **Fully-empty clean stream was a silent stop.** A 200+`[DONE]`/EOF with
   **zero stream events** (no text/thinking/tool deltas — k3 truncation under
   load) ended the turn silently: `finishStreamTurn` → `completeStreamTurn` →
   `finalizeStreamTurn` → `EventEnd`, no message. **Fix:** `finishStreamTurn`
   now returns a synthesized `errEmptyResponse` (transient → retried via the
   existing `handleStreamFailure` retry+notify path, then a surfaced message on
   exhaustion) instead of silently completing. Files:
   `internal/agentic/agent_streaming.go`, `internal/agentic/retry_classify.go`.
   Tests: `TestAgent_EmptyResponseRetried`, `TestAgent_EmptyResponseExhaustsSurfaced`,
   `TestShouldRetryStreamError_EmptyResponse` (all RED without the guard).
   **Scoping (important):** the guard fires only when the stream emitted **no
   events at all** (`genStartTime` unset) **and** no real tool executed this
   turn (`turnHadToolExecution`, new per-turn flag set in
   `executeBufferedToolCalls`). This avoids false positives on (a) legitimate
   empty "done, nothing more to say" turns after a tool result
   (`internal/agentic/testutil` harness), and (b) loop-detector fixtures that
   emit empty-string text deltas (`TestAgent_EmptyAssistantRepeat_Stops`,
   `TestAgent_AssistantRepeat_WarnsThenStops`). Full `internal/agentic/...`
   tree green (18 packages).

**Error-path audit result (no change needed, verified OK):** HTTP `>=400`
(5xx/429 k3 load) → `CloseWithError` (runtime.go:130) → retry-classified and
surfaced with status+body; mid-stream `CloseWithError` (all providers) →
`stream.Err()` → `handleStreamFailure`; SSE premature-end detection
(openai/stream.go:366) → transient retry. These already surface; the only
silent gaps were the thinking-only and fully-empty clean stops, both now fixed.


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
`go build ./...` ✅
`go vet ./...` ✅
`staticcheck ./...` ✅ (pre-existing warnings only)
`gocognit -over 15` / `gocyclo -over 12` ✅ (pre-existing flags only)
`go test -count=1 -race -cover ./...` ✅ (all packages pass)
