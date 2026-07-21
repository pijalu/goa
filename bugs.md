<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug/Feature Tracking

## Guideline

1. Create a detailed fix plan for each bug/new feature - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found, even if not related to the bug/feature, must be fixed and the fix plan must be updated accordingly. You can add new items to the bug list as you find them.
3. Each item should be moved to archive when tested and closed as the associated plan.
5. Use filmstrip approach to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

*At the end of the session*: the list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

# Open TODO
## CRITICAL: /goal destroy leaves stale goal content in the cached prompt

**Reported 2026-07-21.** Destroying a goal (`/goal cancel`, queue `delete`,
`/goal replace`) does not fully clear goal content from what the model sees:
goal text is cached into the prompt in two places, and at least one of them
appears to survive the destroy.

### Investigation so far (localized)
- Static reminder: `Agent.buildSystemPrompt`
  (internal/agentic/agent_streaming.go:1264-1271) prepends
  `GoalInjector.ActiveGoalReminder()` to the system prompt as the *cacheable
  prefix*. The injector reads live state (`GoalMode.GetGoal` → fresh snapshot
  each call, core/goal/mode.go:384-390) — so the static prefix itself
  correctly disappears/changes on destroy (busting the provider prompt cache
  from byte 0 — expensive but correct).
- Queue store: `GoalQueueStore` (core/goal_queue.go) re-reads
  `upcoming-goals.json` on every op — no in-memory cache; a queue `delete`
  persists immediately.
- **Prime suspect (to verify):** `mergeGoalProgress`
  (agent_streaming.go:1285+) injects the *dynamic* per-turn goal progress
  ("Status: active, Progress: N turns…") as a dedicated system message before
  the last user message. If those progress messages persist into `a.history`
  (not just the outgoing provider slice), then after a destroy the
  conversation permanently carries "active goal, N turns" steering text for a
  goal that no longer exists — the model keeps working a dead goal.

### Fix plan
1. Confirm whether `mergeGoalProgress` mutates `a.history` or only the
   per-request slice.
2. Repro: `/goal create` → one turn → `/goal cancel` → inspect next request
   → assert no goal reminder/progress text present.
3. Fix per finding: strip goal-progress slot messages from history on goal
   destroy, or make the slot strictly per-request.
4. Regression test: destroy → next `buildProviderHistory` contains zero
   goal-progress messages.
5. Validate: guideline #6 gates separately.

# Open TODO
## Tool on-going marker must not use the spinner — keep the yellow dot

**Reported 2026-07-21.** The running-tool status icon currently renders the
animated spinner frame. It must stay the yellow dot.
- Root: `ToolExecutionComponent.statusIcon` (tui/tool_execution.go:816-824)
  returns `CurrentSpinnerFrame()` for `ToolRunning` (fallback `⟳`), colored
  `tool_running`.
- Fix: return a static yellow dot for `ToolRunning` (theme `tool_running`
  color); no spinner frame.
- Tests: running tool renders the dot, not a spinner frame; icon stays stable
  across spinner frame advances.

## Terminal title must use the hexagon spinner

**Reported 2026-07-21.** Per the original idea, the animated terminal title
should spin with the hexagon animation; the first implementation was a
misunderstanding (it reuses the status spinner definition and appends the
" - <context>" suffix).
- Root: `titleController` (internal/app/title.go) animates with
  `spinnerDefFor(cfg)` frames + contextual suffix; final idle title is the
  contextual "⬡ - <project>".
- Expected per original idea: idle title is the bare `⬡`; while working the
  title animates the hexagon-based sequence (see hexagon-black item) — no
  project suffix on the animated/working title.

## Startup sequence / terminal title not working

**Reported 2026-07-21.** The g⬡a → g⬡ → ⬡ startup sequence does not behave as
requested in the original item (boot brand g⬡a, transition to ⬡ on the
startup-done hook / 5s fallback).
- Verify: boot brand visible at startup; transition plays on the explicit
  hook (plugins+history done) or the 5s fallback; final title is `⬡`.
- Fix whatever the reproduction shows (hook wiring, phase transitions, or
  title writer races).

## New spinner: hexagon-black (⬢⬣) — slow — default for terminal title

**Reported 2026-07-21.** Add a two-frame "hexagon-black" sequence and make it
the default spinner for the terminal title animation:
```
⬢⬣
```
- Add `hexagon-black` to internal/spinner/spinners.json (frames ⬢⬣, slow
  interval).
- Use it as the title controller's default animation frames (independent of
  the status spinner); keep it selectable by name.
- Tests: frames/interval exact; title animation defaults to hexagon-black.

## Finish: LM Studio e2e cache validation + archive

**Pending from the /goal destroy cache fix (9396e69).**
- LM Studio live on :1234 (models: qwythos-9b-v2, qwen/qwen3.5-9b,
  google/gemma-4-e4b). goa built to /tmp/goa-test; headless run works.
- Server log: ~/.lmstudio/server-logs/2026-07/2026-07-21.1.log records
  per-request `prompt eval time = X ms / N tokens` — the cache-reuse signal
  (cached prefix → small eval; bust → full re-eval).
- Remaining: (1) e2e run — turn 1 (goal active) → cancel → turn 2 in the
  same session against LM Studio, compare prompt-eval token counts (needs a
  small driver wiring Agent + real GoalMode; headless can't cancel
  mid-session). (2) Archive all items to docs/archive/bugs.2026-07-21.md and
  reset bugs.md to guidelines-only.

## CRITICAL: goa frozen until the title animation completes

**Reported 2026-07-21.** The app is unresponsive while the startup title
transition (g⬡a → g⬡ → ⬡ at 1s/frame) plays — input is frozen until the
animation completes.
- Likely root: `titleController.startupDone` / `playTransition` runs the 2s
  sleep-driven sequence on a goroutine, but if any title write or the
  controller mutex path blocks the command loop / input path, the UI stalls.
  The startup hook goroutine and the animation ticker must never block input.
- Fix: the transition must be fully asynchronous and non-blocking; input and
  rendering stay responsive throughout. Validate with a filmstrip.

## z.ai still not showing quota (fix did not work)

**Re-reported 2026-07-21 with screenshot.** After switching to model glm-5-2
(provider zai), /quota shows only Kimi (Advanced) Session/Weekly + Local
(inferred) rows — NO Z.ai row at all (not even an error/no-key/auth row).
Footer shows `(zai) glm-5.2 • high` with NO quota segment for zai.
- This is the same shape as the earlier re-report: the row is completely
  absent, so `_cache["zai"]` is nil in the running app — refreshDue("zai")
  never produced an entry.
- Previous analysis ruled out: stale bundle (byte-identical), fetcher
  registration (probe shows zai registered), id-only config matching
  (regression test passes). The failure persists in the user's real runtime,
  so the remaining leads are environmental: which provider entry the ACTIVE
  model switch leaves (provider id vs identity), whether the zai fetcher
  errors BEFORE caching (a throw outside the try/catch, or auth gate), or the
  bundled plugin actually loaded in the user's build predating the fix.
- Repro: exact user config + active zai provider; dump /quota:json; inspect
  [plugin] logs for the zai fetch. Validate the fix with a filmstrip of
  /quota showing the Z.ai row.

## False positive loop detection

**Export:** `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260721-142256.zip`
- Analyze the bundle: which detector fired (thinking-loop / tool-loop), on
  what input, and why it is a false positive. Fix + regression test.

## CRITICAL: thinking-loop detector false positive on code/quote-heavy reasoning

**Reported 2026-07-21 (two exports).** The agent is killed mid-work with
"the model kept repeating the same line of reasoning (thinking loop)" while
doing legitimate deep debugging of a SQL parser bug. The thinking text quotes
code blocks (`func (p *Parser) parseColumnDefs()...`) and repeats the token
sequence (PRIMARY/UNIQUE/CHECK/FOREIGN/CONSTRAINT) across reasoning
paragraphs — the detector sees the repeated substring and fires.

**Exports:**
- `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260721-142256.zip`
- `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260721-142545.zip`

**Pattern (user-specified):** the repeated-unit detection must strip
code/quote/tool-call content from the thinking text BEFORE matching; the
remaining prose ("Let me check… wait… so … didn't handle it") is normal
iterative reasoning, not a loop. The repeated block here is a QUOTED CODE
FENCE, not model repetition.

**Root-cause direction:** the thinking-loop detector matches on raw thinking
text including fenced code and tool-call echoes; identical quoted code across
turns reads as "same line repeated N times". The matcher must normalize/strip
fenced code blocks, quoted tool input/output, and inline code before computing
repetition, and/or require the repeated unit to be the model's OWN prose
(sentence-level) rather than quoted artifact content.

**Fix plan:**
1. Analyze both exports: extract the thinking deltas that tripped the
   detector; confirm the repeated unit is a fenced code/quote block.
2. Strip fenced code (``` … ```), block quotes, inline `code`, and tool-call
   echoes before repetition analysis in the thinking-loop detector.
3. Regression test: thinking text that quotes the same code fence twice plus
   normal iterative prose must NOT trip the detector; genuine single-line
   prose repetition still must.
4. Validate with guideline #6 gates + a filmstrip of the turn NOT being killed.
