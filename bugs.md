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
9. **Cache-hit-first design (CRITICAL for local models).** A cached prefix
   costs ~0; a full re-parse costs 40-100x more (measured 2026-07-21 on
   qwythos-9b-v2: 23.6 tok/s generation — a 20K-token re-parse is a 45-90s
   stall). Therefore every provider request must be **strictly append-only**:
   never move, rewrite, or re-project content mid-history; volatile
   per-request text may only ever be appended at the tail. The system prompt
   (byte 0) must stay byte-identical for the whole session. Anything that
   "decorates" messages per request (cache_control breakpoints, markers,
   wrappers) must be pinned to a fixed position — a marker that moves to the
   newest message each round rewrites history bytes and kills llama.cpp's
   longest-prefix cache match exactly where it lands. Validate any change to
   prompt/message construction with a proxy capture proving request N is a
   byte-prefix of request N+1, and by watching CH% climb in real sessions.

*At the end of the session*: the list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## STOP CONDITION (binding — an agent working this file must not stop early)
An agent working this file may ONLY stop when ALL of the following hold:
1. This file contains NO open items (every item is ✅/closed or moved to the archive).
2. Every item is tested and working (regression test green; filmstrip-validated where it is a UI behavior per guideline #5).
3. Any issue/problem discovered during the work has been ADDED to this file AND solved — nothing is deferred out-of-band.
A turn that ends with open items, an untested fix, or an unrecorded newly-found issue is a FAILED turn: continue working; do not summarize-and-stop.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

---

# HANDOVER STATE (2026-07-22) — read this first

**Branch:** main, 13 session commits on top of `7963f80` (7 unpushed vs origin). Working tree clean.
**Suite status:** `go test ./tui ./internal/app` green EXCEPT `TestPluginCommandExecutesThroughRouter` — a PRE-EXISTING flaky test under `-race` (fails 2/5 runs on clean HEAD `7963f80`, unrelated to this session's changes; see OPEN item F below).
**Guideline #6 gates** (run separately, not chained): `go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12` are all clean on every committed fix. The full-repo `go test -count=1 -race ./...` gate has NOT been run yet (see OPEN item G).

## Status legend
- ✅ CLOSED — fixed, committed, user-confirmed or proven by test.
- 🔧 FIXED — fixed & committed with regression test; awaiting user live validation (real terminal).
- ⏳ OPEN — not yet fixed; localization notes below so the next agent does not re-derive them.

---

# CLOSED / FIXED items

## ✅ ~~Start-up~~ CLOSED (user-confirmed 2026-07-22)
Inputline unresponsive ~2s at startup. Two root causes, both committed:
- `341f5ad` — removed synchronous `RefreshLocalContextWindow` HTTP probe (up to 15s block) from `startAgentSession`; redundant with the async post-first-delta refresh (`maybeRefreshContextWindow`).
- `426e623` — released the goja VM lock (`runOutsideVMLock`) across blocking `goa.http.fetch`; the actual felt delay, which matched the quota status segment appearing (quota prime held `vmMu` across synchronous HTTP, starving the command loop). Regression test: `TestJS_HTTPFetchReleasesVMLock`.

## ✅ ~~Goal status line~~ CLOSED (proven by test, commit `53b5767`)
Original report: goal execution shows no footer details (progress/context/cache). VERIFIED ALREADY CORRECT in current code — discriminating test `TestGoalTurn_TokenStatsUpdateFooter` (`internal/app/goal_footer_stats_test.go`) proves a goal-driven turn's `EventTokenStats`/`EventContextStats` reach `handleTokenStats` and update the footer (`↑12.0K ↓800 CH25.0% 39.7%/32.8K`). Chain: `agentManagerRunner.Run` → agent emits stats events (independent of `SendUserInput`) → `forwardEvent` → bus → `handleAgentOutputEvent` → `handleTokenStats` → footer. The frozen footer in the original screenshot was from an older build.

## ✅ ~~Multi-tool calling and timeout~~ CLOSED (commit `3759d5a`)
Root cause: `Tracker.finalize()` marked a widget `Running` when its ARGS completed; for a multi-call batch all args complete at batch end, so all startTimes stamped the same instant → every widget showed the same `elapsed 37.2s`. Fix: `finalize` no longer pre-marks Running; the first progress event (emitted when a call's `Execute` actually starts, `executeToolWithResult`) transitions it, restarting that call's own clock. `findRunningNoID` widened to match id-less progress to Pending args-complete widgets. Regression test: `TestTracker_BatchElapsedStartsAtOwnExecution`.

## ✅ ~~ESC: hard stop for ALL ongoing activities~~ — PARTIAL: goal-drive part CLOSED (`d60124e`), remainder ⏳ OPEN (see OPEN item D)
**Closed part:** the /goal command started the driver on `context.Background()` (`core/commands/goal.go`), so ESC → `Interrupt()` killed the current turn but the loop immediately launched the next continuation — an active goal was unkillable by ESC. Fix: `GoalDriver.Drive` derives a cancellable loop ctx + checks `ctx.Err()` before each continuation; new `GoalDriver.Stop()`; `App.handleEscape` calls `goalDriver.Stop()` alongside `agentMgr.Interrupt()`. The interrupted turn maps to "Paused after interruption" (existing `mapDriverError`), so the goal stays resumable via `/goal:continue`. Regression test: `TestGoalDriver_StopEndsDrive` (race-clean).

## 🔧 ~~Mascot/logo redraw~~ FIXED (commits `53d0a02`, `e5073d6`) — awaiting user live validation
Symptom: mascot redrawn mid-session, rest of screen black; scrollback duplicated / off-by-one line. Two symptoms, ONE root cause.
**Root cause:** goa ran with terminal auto-wrap (DECAWM) ENABLED. The compositor positions every row by absolute CUP and truncates to exactly terminal width; a row filling the last column leaves the terminal in pending-wrap state, so the next line-feed/write wraps onto an extra row and every subsequent row index shifts by one. On a scroll this duplicates/mis-orders lines including the header. Only manifests on a REAL terminal (pending-wrap); the fakeTerminal/screenEmulator don't model it, which is why many in-harness probes (single-layer, chrome-confined DECSTBM, tool-widget churn, batch vs incremental — all clean) initially missed it.
**Fix:** `53d0a02` emits `\x1b[?7l` (DECAWM off) at session start after `SetRaw`; `Stop()` re-enables `\x1b[?7h` for the parent shell. Regression guard: `TestCompositor_NoFullWidthRowDuringScroll`. Complementary: `e5073d6` removed the redundant `\x1b[2J` blanking wipe on full redraws (in-place row replacement instead), plus `TestSessionReplay_MascotNeverRedrawn` (91,595-event real-session filmstrip replay, validated).
**Next:** user must confirm on a real terminal that the mascot no longer blinks and scrollback is clean. If it persists, next suspect is the `terminal.Size()` ioctl transient (H2) — enable render trace (`logging.render_trace`) to capture a frame.
**Rejected approaches (do not retry):** (1) compositor vt-retention via a `vtMax` high-water mark — broke 9 scrollback tests because `compose(c.vt)` skips rows above placeStart leaving `""` in canvas, so prevLines stored blanks; (2) app-level header-drop — corrupted the diff baseline (canvas shrank by headerHeight without telling the compositor, misaligning `prevLines`/`vt`/`scrollTop` → whole-frame duplication). Both reverted to `e5073d6`.

## ✅ ~~Terminal title animation~~ CLOSED (user-confirmed 2026-07-22)
Did not play. Resolved by the Start-up fixes: the title controller's writer goroutine + startup-done hook were starved by the same blocking I/O (startup HTTP probe, quota-prime VM-lock hold) that froze the input line.

## ✅ ~~Stats: cache write is always 0~~ CLOSED (commit `933a2c3`)
Verdict: NOT a parse bug — the plumbing is complete (Anthropic `cache_creation_input_tokens` → stats → usage DB → cost). OpenAI-style/local providers never report cache writes (the OpenAI parser reads `prompt_tokens_details.cache_write_tokens`, which those servers don't emit), so 0 is legitimate. Per instruction, the display now HIDES cache-write when 0: `/usage` summary shows `Cache: X read` and the Cache R/W column shows read-only; full `X read / Y write` form kept when writes exist. Regression test: `TestUsageCommand_CacheWriteHiddenWhenZero`.

## ✅ ~~Tool call loop detector: false positives~~ CLOSED (commit `64fbc62`)
Root cause (proven from `goa-export-20260721-233247.zip`): `RecordToolCall` keyed on exact name+sha256(input) and accumulated counts for the WHOLE session (`Reset()` has no production callers); a long session ran identical `go build ./...` 11× across dozens of turns (edit→build→edit) and the detector killed the agent at the 10th. A true loop repeats calls BACK-TO-BACK; legitimate work reuses identical commands with other work in between. Fix: consecutive-streak model — any different call resets every streak; only an unbroken run of identical calls reaches warn(7)/interrupt(10). Trade-off (documented): an alternating A-B-A-B cycle keeps each streak at 1 and stays undetected. Regression tests: `TestLoopDetector_LegitRebuildCycleDoesNotTrip` (the incident replay), `TestLoopDetector_TrueRunawayStillInterrupts`.

---

# OPEN items (for the next agent — localization included)

## ⏳ A. Session: slow commands need an "executing xyz..." placeholder
Every /command must immediately show an "executing xyz..." placeholder, replaced by the result. No silent gap between submit and first feedback.
**Localization:** command dispatch runs through `core/commands` router (`CommandRegistry.Resolve` → `cmd.Run`). The placeholder should hook the dispatch point before `cmd.Run` executes (status message or chat system line), cleared on completion. Bare `/quota` already follows this contract (acknowledges async fetch immediately — see `plugins/bundled/provider-quota/plugin.js` `scheduleAsyncQuotaRender`). Test: command-context harness asserting the placeholder precedes output; filmstrip-validate per guideline #5.

## ⏳ B. Session command: list ordering, filtering, timestamps
The /session picker list is wrong on three axes: (1) most recent on TOP + first/default selection; (2) must not list sessions without an actual model turn; (3) show date/time — date only if not today (today → time only), hh:mm, append :ss only to disambiguate duplicate hh:mm entries.
**Localization:** `core/commands/session_persist.go` `buildSessionItems` (line ~309) builds picker items from `store.ListSessions()`. `core/sessionstore.go` `ListSessions` (line ~322) ALREADY filters no-conversation sessions (`hasConversation` check, ~347) and sorts newest-first (~367), with tests (`TestSessionListSessions_FiltersEmptySessions`, `_NewestFirst`). So: (1) ordering — verify `buildSessionItems` preserves store order and `SelectOption` defaults cursor to item 0 (the picker may not default to first); (2) "actual model turn" — `hasConversation` may count a user msg with no assistant reply as conversation; tighten to require an assistant/model turn; (3) timestamps — `SessionInfo.Date` (file ModTime) exists but is NOT formatted into the picker item Label/Description; add format pass in `buildSessionItems` (needs all dates first to detect duplicate hh:mm for the :ss rule).

## ⏳ C. ESC remainder: orchestrator/swarm + steering drain
ESC goal-drive part is CLOSED (see above). Remaining: (1) orchestrator/swarm runs — verify their contexts are reachable from the ESC-triggered cancellation root (`handleEscape` → `agentMgr.Interrupt()`); sub-agent/orchestrator contexts may be detached; (2) steering/queued continuations — `handleEscape` does not drain the steering queue (`core/agentmanager.go` `steering`), so queued user input submitted mid-turn still dispatches after the interrupt. Test: uiScenario ESC during an orchestrator run + during queued steering, assert all stop and prompt returns.

## ⏳ D. (merged into C — the ESC goal-drive part that IS done is recorded in CLOSED above)

## ⏳ E. Flaky pre-existing test: TestPluginCommandExecutesThroughRouter (-race)
Fails ~2/5 runs under `-race` on clean HEAD `7963f80` — NOT caused by this session's changes (verified by stash/retest on clean tree). Output shows `/quota:refresh` returning `undefined` instead of `Quota refreshed.`. Suspected: the plugin's `goa.setTimeout(...,0)` quota prime racing the synchronous `:refresh` path — the command executes while the VM is mid-prime. Investigate the `wrapRegisterCommand` result handoff (`plugins/plugin.go:137,194`) and the prime/command interleaving under the VM lock (now released across HTTP by `426e623`, which may have widened the race window — re-check).

## ⏳ H. Mascot redraw REGRESSION — still happening (user report 2026-07-22)
The DECAWM fix (`53d0a02`/`e5073d6`) did NOT fully cure it: the mascot re-appeared mid-session **during a tool call, at the end of the session**. Evidence:
- Session export: `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260722-180656.zip`
- Real terminal log: `/Users/muaddib/dev/term.log`
**Plan:** (1) extract `session/events.jsonl` from the export and replay it through the session-replay filmstrip harness (`TestSessionReplay_MascotNeverRedrawn` pattern) to see whether the header layer re-emits; (2) correlate the wall-clock moment in `term.log` (grep the mascot art bytes) with the in-flight tool-call event; (3) next suspect per handover remains the `terminal.Size()` ioctl transient (H2) — a 0×0 or shrunk intermediate size would re-place the header; enable `logging.render_trace` on the capture if the replay is clean. The fact that it lands **during a tool call at end of session** points at a widget-height change forcing a full redraw while DECAWM state or scroll margin is momentarily wrong.

## ⏳ I. Scrollback first line duplicated / off-by-one (user report 2026-07-22)
The 1st line of history/scroll is often a DUPLICATED line; the issue disappears during work/redraw. Example transcript:
```
 ✓ search func .*SelectOption\( in *.go under ~/dev/goa (non-recursive) max:10
 ✓ search func .*SelectOption\( in *.go under ~/dev/goa (non-recursive) max:10
 search func .*SelectOption\\( (2 matches)
```
The completed tool widget's header line (`✓ search ...`) is emitted TWICE at the top of the scroll region, then the live body follows. Smells like the same pending-wrap/scroll-margin off-by-one family as H: when a widget transitions running→done, its first line is painted once in-place and once more on the next scroll. **Plan:** reproduce in the filmstrip harness by driving tool_call→tool_result with a viewport exactly full (widget top line == scroll-region top), assert no duplicated line in `Filmstrip.Render()`; then fix the row-index bookkeeping at the scroll boundary.

## ⏳ J. Search tool display: garbled query line (user report 2026-07-22)
The search tool widget shows an odd reconstructed query line:
```
 ✓ search func .*SelectOption\( in *.go under ~/dev/goa (non-recursive) max:10
 search func .*SelectOption\\( (2 matches)
```
The second line renders the pattern as `func .*SelectOption\\(` — a DOUBLE backslash that was not in the request (single `\(`). Likely the renderer re-escapes the already-escaped pattern when building the summary line (JSON-unescape then regex-escape round trip). **Plan:** localize the search tool renderer (`tools/*_renderer.go` for the search tool), find where the `(2 matches)` summary line is built, stop the double-escaping; regression test asserting the summary shows the pattern byte-identical to the tool input.

## ⏳ K. Orchestrate MUST persist all sub-agent work into the session (user report 2026-07-22)
Two gaps: (1) orchestrate logs too little — all sub-agent work must exist in the session events; (2) resuming an orchestrate restores "more than the orchestrator command" (restored transcript does not match what the run actually did — likely replays stale/unrelated events because the run's events were never scoped to the resumed session). **Plan:** (1) trace what `OrchestrateCommand` writes to the session store (`recordCommandInSessionStore` only writes the bare `/orchestrate...` user line — sub-agent turns are NOT persisted); wire the orchestrator runtime's event stream into `sessionStore.WriteEvent` per sub-agent turn (respecting cache-hit-first: session-store writes are local, not prompt bytes); (2) on resume, restore exactly the events of the persisted run, no more; regression test: run a fanout orchestrate in the integration harness, assert the session file contains every sub-agent turn, then restore and diff transcripts.

## ⏳ L. Pre-existing broken test: TestQuota_FullBreakdownWithProviders expects removed "Local" row
Found while fixing item E. Fails on clean HEAD `f054fcf` (verified via stash/retest): `quota_plugin_test.go:77: /quota output missing "Local"`. Root cause: commit `fd1534a` intentionally dropped the "Local (inferred)" row from `/quota` output, but this test was never updated. NOT caused by the item-E VM-lock change. **Plan:** update the test expectation to match the post-`fd1534a` output (local section shows session usage stats, not a "Local" quota row); confirm the assertion still guards the local fallback rendering.

## ⏳ G. Final gates + archive (do last)
1. Run the full-repo gate per guideline #6, EACH separately: `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...`. Expect item E's flaky test to be the only -race failure — fix it first (item E), then this gate should be green.
2. Move the bug list to `docs/archive/bugs.<fixdate>.md` and reset this file to guidelines-only (per workflow step 8 + the "end of session" note).
