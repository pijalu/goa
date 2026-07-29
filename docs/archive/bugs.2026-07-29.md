# Bug archive — fixed 2026-07-29

All items below were fixed, tested, and validated in this session. The fix plan
(with root causes, test approach, and validation steps) follows the bug list;
a session validation summary closes the file.

---

# TODO
## Goal scheduling
For goal management: there should be goal parameters tool to allow the model to create new front items, set the current goal to a postponed state and start the new scheduled goal.

This is very unclear:
```
✓ Goal complete — Implement G07: Performance Optimization. Profile-driven optimization. Targets: BenchmarkInsert <2000ns, Ben
Worked 11 turns over 3m20, using 26.8k tokens.
╭────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ [goal] auto-promoted queued goal: FIX: Error message formatting for schema-qualified table names — the "main." prefix is inconsistently applied in │
│ "no such table" errors. Two changes: (1) Remove incorrect "main." prefix from view expansion code (line 3553-3563 in engine.go). (2) Add "main."   │
│ prefix for trigger-execution table-not-found errors. Expected: alterlegacy tests 5.7, 3.3.1, 4.1a, 4.1c produce correct error messages.            │
╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
◦ Goal blocked by the system
  no measurable progress
  needs: user direction on how to proceed
```

Goal should be usable as todos/piece of elements/scheduling - the framework should allow model to schedule them as required

## Todo documentation
make sure todo are fully documented - especially how they differ from goals

## Timeout hint
On timeout - the hint should be clear:
```
 ✗ $ cd /Users/muaddib/dev/frigolite && go test ./... 2>&1 | grep -E "FAIL|ok|---" | head -30 (timeout 120s)
 Error: [bash error: timeout]
 Command timed out after 120s
 Hint: See /docs TOOLS or /tools bash for usage.
 Took 120.0s
```
the hint should be: timeout

## Model list
Some entry in model list ghav
## ask_user_question
ask_user_question should allow the user to provide an alternate response to a question - it seems to enforce a specific format that does not match the user's input.

## Goal management tool issue
Model are having difficulty to manage goals/move them around/reorder them.
Check goal tool call for better alignment with the model - log: /Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260729-102137.zip

## Goal completion screen corruption
the screen as an unaligned line: "The goal "Implement G05: ALTER TABLE..."
```
Let me create the execution goals and mark this investigation complete.


✓ ◆ Reported goal complete
Goal marked complete.

✓ Goal complete — UNBLOCKING INVESTIGATION — find a solution for a blocked goal.

                                                                               The goal "Implement G05: ALTER TABLE Toke
Worked 1 turn over 2m34, using 24.9k tokens.
╭────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ [goal] auto-promoted queued goal: Implement G05: ALTER TABLE Token-Level Rename. Replace regex-based string replacement with token-level           │
│ processing using the lexer. Handle RENAME TABLE, RENAME COLUMN, ADD COLUMN, DROP COLUMN. Update trigger/view/index SQL. See                        │
│ plans/PLAN-05-ALTER.md.                                                                                                                            │
╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

## Slow performance on very large conversations
The TUI  may take a long time to respond on command.
eg: Sending a /goal:resume will take an important amount of time to complete.

Input line becomes unresponsive and CPU will spike for couple of seconds at 100% on new line - likely related to a complete redraw of the conversation history

If required, a huge conversation history may cause the TUI to become unresponsive: 
/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260729-095239.zip

## Corruption on goal change
On goal change - the input line is sometimes corrupted:
```
FAIL   github.com/pijalu/frigolite     0.418s
FAIL
Took 0.62s

⬢ Sending request...
───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
⟐ [sunny.otter] UNBLOCKING INVESTIGATION — find a solution for a blocked goal.
command pass.
the harness needs an expect field to treat this as expected.
─────────────1. analyze6: Add "expect" field to duplicate-step tests (2.4, 2.7) in testdata/analyze6.json — the second step intentionally violates UNIQUE constraint;──
────────────────────────────────────────────────────────────2. analyzeC: Fix EXPLAIN QUERY PLAN output in explain.go — generate multi-line output when ORDER BY...─────

───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
~/dev/frigolite (✱ main)                                                                                                                          coding-posture │ YOLO
↑3.0M ↓749.7K 88.3 tok/s CH98.4% TC:1125 $1.1387 16.9%/1.0M (auto)                                                               (opencode-go) deepseek-v4-flash • high
```

## Goal list issue
Goal does not render markdown + text should wrap to multiple lines
```
╭────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ## Goals                                                                                                                                                   │
│                                                                                                                                                            │
│ **1. [active] crimson.toad**                                                                                                                               │
│ *status active · turns 2 · 65.4k tokens · 3m31*                                                                                                            │
│ Implement G04: Query Planner & Index Selection. Build cost-based planner that uses indexes for seeks, range scans, and JOINs. Implement EXPLAIN QUERY PLAN m
│                                                                                                                                                            │
│ **2. [queued] fuzzy.lark**                                                                                                                                 │
│ Implement G05: ALTER TABLE Token-Level Rename. Replace regex-based string replacement with token-level processing using the lexer. Handle RENAME TABLE, RENA
│                                                                                                                                                            │
│ **3. [queued] witty.falcon**                                                                                                                               │
│ Implement G06: ATTACH Multi-Database. Implement ATTACH/DETACH with full schema dispatch. Schema-qualified name resolution (aux.t1, temp.t1, main.t1). Cross-
│                                                                                                                                                            │
│ **4. [queued] trim.crane**                                                                                                                                 │
│ Implement G07: Performance Optimization. Profile-driven optimization. Targets: BenchmarkInsert <2000ns, BenchmarkSelect <100000ns, BenchmarkSelectWhere <500
│                                                                                                                                                            │
│ **5. [queued] minty.koala**                                                                                                                                │
│ Implement G08: FTS3/4/5 Full-Text Search. Extend vtab framework. Implement tokenizer (simple/unicode61), inverted index, FTS3 query parser, MATCH operator.
│                                                                                                                                                            │
│ **6. [queued] noble.panda**                                                                                                                                │
│ Implement G09: C-API Test Pattern Migration. Update converter to process the 123 C-API test files instead of skipping them. Extract SQL from sqlite3_prepare
│                                                                                                                                                            │
│ **7. [queued] trim.hare**                                                                                                                                  │
│ Implement G10: Quality & Full Green. Final verification gate. Run full test suite (zero FAIL). Empty knownUnsupported list or justify remaining entries. Run
│                                                                                                                                                            │
│                                                                                                                                                            │
╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

---

# Fix plan (executed)

# Fix plan — bugs.md (2026-07-29)

Mode: debug → fix per bug. Each bug: root cause → minimal fix → regression test → validate.

## Bug 1 — Timeout hint
**Observed:** bash timeout shows generic hint `See /docs TOOLS or /tools bash for usage.`
**Root cause:** `tools/bash.go toolErr()` attaches the generic hint to every error type,
including `timeout` (ExecuteContext ~L198).
**Fix:** give the timeout error a specific actionable hint: increase `timeout`
(default 60s, max 300s) or split the command. Other error types keep their hints.
**Test:** table test in tools/bash_test.go: timeout error carries the timeout hint;
non-timeout errors keep the generic/specific hints.
**Validate:** `go test ./tools/ -run Bash -count=1`; manual: run a 1s-sleep with timeout 1 in the TUI and read the hint.

## Bug 2 — Model list: local models shown red; confirmed-available should be green
**Observed (user-confirmed):** "Seems local model are in red in the list — if confirmed it should be green."
**Root cause:** `configuredModelItemsFiltered` (model.go:569) paints `error` color whenever
`!validator.IsValid(id)`. `ModelValidator` is two-state: `status[id]` is false when
(a) never probed yet (startup window), (b) probe errored (provider offline/slow at probe
time — common for localhost LM Studio), or (c) model truly missing. All three render red,
and a transient local outage stays red until the next 5-min probe. There is no positive
"confirmed available" signal (valid models just get the default color).
**Fix:** tri-state validity in `ModelValidator` (unknown / valid / invalid):
- unknown (not yet probed) → default color (never red).
- valid (probe succeeded, model listed) → success color (green).
- invalid (probe succeeded AND provider reachable AND model not listed) → error color (red).
A probe ERROR (provider unreachable) must NOT mark invalid — it leaves the last known state
(first time: unknown). This stops local models from going red on a transient LM Studio
outage while keeping true "model removed" detection when the provider answers.
**Test:** provider/model_validator_test.go table: pre-probe → unknown; fetch error → stays
unknown; listed → valid; reachable-but-missing → invalid; error-after-valid keeps valid.
Selector items: green for valid, red for invalid, default for unknown.
**Validate:** `go test ./provider/ ./core/commands/ -count=1`; manual /model with LM Studio
running → green; LM Studio off → not red.

## Bug 3 — ask_user_question: alternate response mangled
**Observed:** user free-text answer is forced into an option format.
**Root cause:** `canonicalizeAnswer` (tools/ask/ask_user.go:231): when options exist and
`AllowFreeText` is false it returns `closestOption()` (or the FIRST option) — and
`AllowFreeText` defaults to Go's `false` while the schema documents "Default true".
**Fix:** `AllowFreeText *bool` — nil (omitted) = true; update docs if needed.
**Test:** table tests: omitted → raw text preserved; explicit false → restricted; numeric/exact match still canonicalize.
**Validate:** `go test ./tools/ask/ -count=1`.

## Bug 4 — Goal management tool alignment
**Observed (export goa-export-20260729-102137.zip):**
a) `{"status":"blocked",...}` without `action` → `invalid goal action ""`.
b) `create` while a goal is paused/blocked → `a goal already exists; use replace`
   (GetActiveGoal filters status==active; CreateGoal rejects any state).
**Fix:**
a) Infer action when omitted: status→update, objective(s)→create, todoTitle→add_todo,
   todoId/todoStatus→update_todo, goalId+direction→reorder, goalId→cancel, value/unit→set_budget,
   none→get. Keep schema requiring action (encourages correct calls) but tolerate omission.
b) `handleCreate`: enqueue when ANY goal exists (not only active) — todo-list semantics:
   create never fails with "already exists" when a queue is wired; only no-queue setups
   still require replace.
**Test:** tools/goal/goal_test.go: action inference table; create-while-paused/blocked enqueues.
**Validate:** `go test ./tools/goal/ ./core/goal/ -count=1`.

## Bug 5 — Goal completion screen corruption
**Observed:** `The goal "Implement G05...` printed at column ~80, truncated, after
`✓ Goal complete — UNBLOCKING INVESTIGATION ...`.
**Root cause:** `tui/goal/completion.go Render` runs `padToWidth` over a MULTI-LINE
objective (unblock objectives contain `\n\n`). Raw-mode LF moves down without CR →
continuation prints at the column where line 1 ended (col 80) and the row model breaks.
**Fix:** wrap per paragraph (split on `\n`, ansi.Wrap each) so every emitted entry is a
single visual row; pad each to width.
**Test:** completion with multi-line objective → no line contains `\n`; all rows ≤ width;
first row starts with the ✓ prefix.
**Validate:** `go test ./tui/goal/ -count=1`; tui-test skill filmstrip of a goal completion.

## Bug 6 — Corruption on goal change (bubble/markers/footer)
**Observed:** separator lines mashed with todo text (`────1. analyze6...──`) above the input.
**Root cause:** same newline class as Bug 5:
- `tui/goal/bubble.go fullText`: `ansi.Wrap(marker+prefix+objective)` — Wrap's contract is
  single-paragraph; embedded `\n` survive into rendered rows.
- `tui/goal/markers.go wrapDetail`: multi-line reason/expectation → same.
- `tui/footer_render.go formatGoalStatus`: objective truncated to 30 but `\n` survives into
  the single-line footer.
**Fix:** newline-safe wrap helper in tui/goal (split paragraphs → ansi.Wrap → flatten) used
by bubble + markers + completion; footer strips newlines before truncate.
**Test:** bubble/marker render with multi-line objective/reason → no `\n` in any row,
bubble still caps at 3 lines with ellipsis; footer row has no `\n`.
**Validate:** `go test ./tui/... -count=1`; tui-test filmstrip of goal change sequence.

## Bug 7 — Goal list: markdown not rendered + no wrap
**Observed:** `/goal:list` box shows raw `## Goals` / `**1. [active]...**`, objectives clipped.
**Root cause:** `tui/chat_viewport_markdown.go hasMDHeader` only detects `# ` (h1); `## Goals`
isn't seen as markdown → long objective lines (≥2 × >60 chars) flip `isPreformatted` to true →
raw line rendering, no markdown, no wrapping.
**Fix:** detect ATX headers `#{1,6} ` (## … ######).
**Test:** isPreformatted/looksLikeMarkdown table incl. `## Goals` sample from the bug;
`/goal:list` output renders through MDStreamRenderer (bold/header styling, wrapped objectives).
**Validate:** `go test ./tui/ -count=1`; manual `/goal:list` in TUI — verify markdown + wrap.

## Bug 8 — Slow performance on very large conversations
**Observed:** 100% CPU for seconds on each newline; /goal:resume very slow; 159k-event session.
**Root cause (compositor):** ANY bottom-chrome height change (editor +1 row on newline,
goal/steering bubble appearing on /goal:resume) → `classifyFrame` → `frameGeometryReset` →
`drawWindowResetScrollback`: scrollback wipe + FULL transcript re-emit (O(history) terminal I/O)
plus `compose(0)` full-canvas materialization. Introduced by e6d8a2f as a safe hammer for
watermark desync on chrome changes.
**Fix (incremental, correctness-preserving):**
- Route chrome-height changes to `frameDiff` (width changes keep the reset path).
- Make `prevWindowFull` geometry-correct: track `prevChromeH`; the prev-canvas content end is
  `len(prevLines) - prevChromeH`. The steady-scroll natural path then stays valid across
  chrome deltas; otherwise the sound top-down fallback handles it.
- unchangedRow is screen-position based (vt-delta adjusted) — already correct.
**Test:** compositor tests: chrome grow/shrink (±1, ±N) × stream/static assert
(a) no duplicated/lost rows via TermEmulator, (b) frame kind is diff (no full reset) — assert
via render trace path != "full-reset"; existing quota-during-stream regression tests must stay green.
Benchmark: newline with 10k-line transcript stays O(visible).
**Validate:** `go test ./tui/ -count=1 -race`; tui-test skill; manual: GOA perf session,
type newlines, watch CPU; /goal:resume responsiveness.

**Issues found during testing (fixed, plan updated):**
1. Deeper latent bug exposed once chrome resets stopped masking it: mid-transcript
   insertion (stream block growing ABOVE later-appended content, the /quota case) broke
   the incremental emission's row-identity assumption — wrong rows scrolled into
   scrollback, real ones skipped (quota-stream test failed: chunks LOST). Fix: added
   `scrollOffUnstable` guard in Render — malignant identity change in the scroll-off
   region reroutes that frame to `drawWindowResetScrollback` (sound rebuild; benign
   blank↔content transitions stay incremental).
2. First reroute re-emitted the CULLED canvas (rows below the watermark are never
   placed in steady frames) — 96 empty rows. Fix: guard lives in `Render`, re-composes
   with `cullFloor=0` before the resync.
3. False positive: big appends mismatched at previous-chrome-band indices (editor/footer
   displaced by growth) — would resync per append. Fix: compare only within the previous
   transcript (`len(prevLines) - prevChromeH`).
4. `TestCompositor_ChromeChangeDoesNotDuplicateScrollback` (exactly-once invariant)
   encoded the old wipe behavior: updated to the corruption invariant (no loss, no
   within-scrollback dup, no within-window dup); cross-boundary overlap on chrome SHRINK
   (≤Δchrome rows) is sanctioned (windowTop dip regime) and preferable to wiping the
   user's scrollback per keystroke.
5. tui-test filmstrip: stats format is "2m34" (not "2m34s") — test expectation fixed;
   completion/bubble/streaming behaviors validated with frame dumps as evidence.

## Bug 9 — Goal scheduling (postpone/promote) + promote→blocked clarity
**Request:** model needs goal-tool parameters to create front items (exists: priority
front), set the current goal to a postponed state, and start the new scheduled goal.
Goals should be usable as todos/scheduling.
**Fix:** new goal actions — `postpone` (demote active to BACK of queue; the clear event
drives app auto-promotion of the next scheduled goal, same path as completion) and
`promote` (activate queued goal by id NOW; current demoted to FRONT atomically via
CreateGoal replace). Schema enum + prompt docs + GOALS.md scheduling section.
The watchdog blocking after promotion is by design (probe = todo transitions + git
workspace changes; resets on goal change) — no code change; the scheduling primitives
are the remedy.
**Test:** tools/goal/goal_test.go: postpone (back, StopTurn, no-active error, empty-queue
park), promote (front-demotion, no-active activate, unknown/missing id), schema enum.
**Validate:** `go test ./tools/goal/ ./internal/app/ -count=1`.

## Bug 10 — Standalone todo tool (available outside goals) + docs
**Request:** todo available outside of goal, single tool with add/update/list parameters,
follows tools enabled/disabled; goal-linked (blank at goal start, contained — todos do
not escape the goal; standalone list keeps its own); on goal achieved with open todos,
remind the model; fully documented, esp. todo-vs-goal distinction.
**Fix:**
- Restored the dormant `TodoListTool` (single `todo_list` tool, action params) and added
  goal linkage: `Mode *goal.GoalMode` delegation when a goal is active; session list
  preserved underneath; remove/clear refused while goal-linked.
- Config `tools.enabled.todo_list` (opt-OUT default true) + default.yaml +
  tools.ConfigurableTools entry + /tools factory case (full runtime toggle).
- Completion reminder: `formatOpenTodosReminder` appended to the CompleteClosed result.
- Docs: GOALS.md (todos-vs-goals table + linkage + scheduling), TOOLS.md (todo_list
  entry), todo.short/long.md, prompts/goal/goal.md (containment note).
**Test:** tools/todo (session CRUD, goal linkage lifecycle, containment refusals),
tools/goal (completion reminder with/without open todos).
**Validate:** `go test ./tools/todo/ ./tools/goal/ ./config/ -count=1`.

## Cross-cutting validation (guideline #6, run separately)
- `go vet ./...`
- `staticcheck ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...`
- Interactive TUI validation for the terminal-visible bugs (5,6,7,8).
- Archive bugs.md → docs/archive/bugs.2026-07-29.md; leave guidelines only.


---

# Session validation summary

Quality gates (each run separately, per guideline #6):
- `go vet ./...` — PASS (exit 0)
- `staticcheck ./...` — byte-identical to the pre-existing baseline (15 findings,
  all unrelated to the change and explicitly noted: goal_test.go:1037 SA4006 from
  commit e9d4591, plus 14 pre-existing unused/ST1005 findings).
- `gocognit -over 15 .` — no new production-code findings. New *test* functions
  exceed 15, consistent with long-standing project tolerance for test complexity
  (e.g. TestEventJSONRoundTrip at 101); the budget targets production logic.
- `gocyclo -over 12 .` — the two functions introduced by this session above 12
  (inferAction, executeWithResult at 13) were refactored to <= 12 (rule table +
  dispatch map); remaining findings are pre-existing or test functions.
- `go test -count=1 -race -cover ./...` — PASS (exit 0, no FAILs).

Interactive/real-terminal validation (guideline #5):
- Timeout hint: `go run` of the bash tool shows the new hint verbatim:
  "The command exceeded the 1s timeout. Increase the \"timeout\" parameter
  (default: 60s, max: 300s) or split the command into smaller/faster steps."
- PTY-driven goa session (160x45) against the real frigolite project:
  - `/goal:list` renders markdown (## / ** consumed) with objectives wrapping
    to multiple lines inside the panel.
  - `/model` selector: lmstudio models (LM Studio down) render DIM (unknown),
    NOT red; deepseek-v4-flash, glm-5-2, kimi-for-coding, laguna-s-2-1 and the
    active k3 render GREEN (confirmed available); no model renders red
    incorrectly.
  - `/goal:new` with an active goal presents the scheduling selector
    (Do not create / Queue it for later / Replace the active goal).
  - The user's pre-existing goal state was left untouched (verified via
    goal-events.jsonl: last event unchanged).
- Filmstrip (tui-test skill) validation: goal completion with a multi-line
  objective renders newline-free, left-aligned rows (frame dump evidence);
  goal bubble renders multi-line objectives capped at 3 body rows;
  streaming + steering chrome add/clear emits NO scrollback wipe (no \x1b[3J)
  and keeps input/footer pinned.
- qa-e2e (live-LM e2e) NOT run: LM Studio was down (connection refused);
  noted as environment limitation.
