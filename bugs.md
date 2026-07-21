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

## Agent stops mid-task requiring manual "continue" (premature `finish_reason=stop`)

**Export:** `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260720-221251.zip` · **Session:** `1784574228_n4qzkao8` · **Provider/Model:** `opencode-go` / `deepseek-v4-flash` · autonomy yolo

### Observed
During a long "execute the REVIEW.md fixes" task, the agent stopped 3+ times mid-work; the user had to type `continue the fixing`, `all the items of the plan must be fixed`, and `resume` to keep it going. Each stop left the task incomplete.

### Root-cause analysis (verified from the bundle)
The stops are **provider-side premature turn terminations, not goa guardrails**:
- `diagnostics/trace.json` + `logs/http.jsonl`: the turn before each manual continue ends with `finish_reason="stop"` (not `length`, not an error, not a goa round-limit).
- Decisive data point (seq 13, 21:59:33): the API returned `stop` after only **25 output tokens** (13 reasoning + the fragment `"Let me fix both the call site and the function:"` — an *incomplete sentence* ending in a colon). The model was mid-fix on a UNION `SetOp` bug and the stream simply ended.
- Prior to the stop: 24-25 consecutive `finish_reason="tool_calls"` rounds (deep tool-work loop, ~427 messages, ~285K cache-read tokens).
- After each stop, goa correctly goes idle (State 2) — there is no auto-continue, so the turn just ends and waits for the user.
- No `context.Canceled`, no transport error, no goa consecutive-round nudge fired (nudges are ephemeral + stripped; not present in the exported request — and even if fired, they *force an answer*, they don't truncate to 25 tokens).

### Why this happens
1. **DeepSeek/opencode-go emits a spurious `stop`** after long tool-calling streaks — a provider quirk where the model terminates mid-completion (seen before as "reasoning loop / early stop" behavior). The `finish_reason=stop` is indistinguishable from a genuine end-of-turn from goa's perspective.
2. **goa has no "premature stop" detector**: when `finish_reason=stop` arrives but the turn clearly isn't done (assistant text ends mid-sentence / task plan incomplete / last message was a tool result awaiting follow-up), goa treats it as a normal turn end instead of auto-resuming or nudging the model to continue.
3. **No auto-continue on truncated turns**: unlike the auto-resume that exists for transport aborts, a `stop` after a tool-result chain with incomplete output is surfaced as final.

### Fix directions
- **Detect suspicious stops:** flag `finish_reason=stop` as premature when (a) the final assistant content ends mid-sentence (trailing `:`, `,`, no terminal punctuation, or very low output-token count with high reasoning ratio), or (b) the preceding N rounds were all `tool_calls` and the assistant produced no conclusive answer. On detection, auto-continue with a "continue" steer (like the transport-abort resume) instead of ending the turn.
- **Provider quirk mitigation:** for `deepseek`/`opencode-go` profiles, consider a `continuations`-style retry when `stop` arrives with an unfinished tool-work chain (the anomaly detector in `diagnostics/trace.json` already flags "last request sent a tool result; verify the model responded" — wire that into an auto-resume).
- **Surface it:** if goa does end the turn on a suspicious stop, tell the user ("model stopped early; reply `continue` to resume") rather than silently going idle.
- **Files:** `internal/agentic/agent_streaming.go` (`handleStreamDone`, turn-end logic), `internal/agentic/agent_turn_stats.go`, anomaly flag in `internal/logs/export` (trace.go); provider profile `variants/opencode-go.json`, `variants/deepseek.json`.

### Repro / verification
RED: replay a tool-call-chain stream ending in `finish_reason=stop` with mid-sentence content (e.g. trailing "…the function:") → turn ends, no auto-continue.
GREEN: same stream → goa detects the premature stop and auto-steers "continue" (or surfaces a clear "stopped early" notice), verified by a filmstrip of the turn not ending.

---

## ctrl-k deletes to end of buffer instead of end of line

**Observed 2026-07-20:** in a multi-line input, `ctrl-k` deletes everything from the cursor to the end of the whole buffer. Readline/bash semantics: kill from cursor to end of the *current line* only.

- **Root cause (localized):** `tui/line_editor.go:197` `deleteToEnd()` — `e.buf = e.buf[:e.pos]` truncates the entire buffer at the cursor, ignoring newlines.
- **Fix:** delete from `e.pos` to the next `'\n'` (or buffer end if none). `findLineEnd(text, cursor)` already exists in `tui/word_navigation.go:76` and returns the line-end offset — reuse it.
- **Also check (same class):** `deleteToStart()` (`ctrl-u`, line_editor.go:189) deletes to *buffer* start; readline kills to *line* start. Verify expected behavior and fix symmetrically if it deviates (unix-line-discard in readline kills to line start).
- **Tests:** table-driven cases — single-line buffer (unchanged behavior), multi-line buffer with cursor mid-line (only current line tail removed), cursor at line start (whole line removed, following lines intact), cursor on last line.

## Arrow up/down must recall history only on non-dirty input

**Observed 2026-07-20:** pressing ↑ with typed (unsent) content in the input recalls a history entry, clobbering the in-progress text. Expected: history navigation only when the input line is non-dirty (user has not typed/edited content since last submit or recall); when dirty, ↑/↓ should only move the cursor between visual lines (or do nothing at first/last line).

- **Root cause (localized):** `tui/editor_input.go:193` `handleCursorUp` recalls history (`navigateHistory(-1)`) whenever the buffer is empty OR the cursor is on the first visual line (`isOnFirstVisualLine`) — regardless of whether the buffer content was typed by the user. `handleCursorDown` (line 215) is symmetric.
- **Fix direction:** add a dirty flag to `Editor` — set on any user text mutation (insert/delete/paste), cleared on submit and on history recall/reset (`histIdx` transitions, editor.go:319,704). Gate `navigateHistory` on `!dirty` (still allow while actively browsing history, `histIdx > -1`). While dirty, ↑/↓ fall through to visual-line movement only.
- **Tests:** type text → ↑ keeps text, no recall; empty input → ↑ recalls; after recall → ↑/↓ continues browsing; edit recalled entry (dirty) → ↑ does not navigate; submit clears dirty.

## z.ai still not visible in /quota

**Re-reported 2026-07-20:** after the auth-store key fix (`708e2e5`, pluginProvidersMap now falls back to `ProviderManager.ResolveAPIKey`), z.ai **still** does not appear in `/quota` output. The earlier fix addressed `no_api_key`; the persistence means a second root cause exists.

- **Leads:**
  1. `/quota` command rendering path (`plugins/bundled/provider-quota/plugin.js`) — check which entries the command lists: only fetchers with successful cached results? Are error entries (fetch failure, `authError`) hidden for the zai row while shown for others?
  2. Fetch silently failing at runtime — check plugin logs (`[plugin]` prefix) for the zai fetch: 401 (key rejected), 404 (monitor URL wrong for the user's endpoint variant, e.g. `open.bigmodel.cn` vs `api.z.ai`, coding-plan vs paas), or JS exception swallowed by the `try/catch` in `refreshDue` (plugin.js:118-122).
  3. Identity mismatch with the user's real config — `providerConfigFor("zai")` (plugin.js:47) direct-matches `providers["zai"]`; if the user's provider was added via `/config add provider` with a different id (e.g. `z.ai`, `zhipu`, custom) and empty `provider:` field, matching depends on normalizeId/aliases (covers `z.ai`→`zai`, `zhipu`) — reproduce with the exact user config shape (`~/.goa` providers + active_provider).
  4. Plugin enablement — confirm `cfg.Plugins.BundledEnabled(provider-quota)` is true in the user's config (footer shows other quota segments, so likely fine, but the zai fetcher could be unregistered if its file failed to load).
  5. Refresh scheduling — zai.js declares `quotaEndpoint: true`, `refreshInterval: 300000`; verify a forced `/quota:refresh` surfaces the row (distinguishes fetch failure from render filtering).
- **Repro harness:** `plugins/quota_zai_test.go` (`newQuotaTestEnv`) with the exact user config shape, then `callCommand("quota", "")` and inspect the command output (not just the segment).

**Update 2026-07-21 (second report, with screenshot):** `/quota` run while active provider is `kimi-code` shows Kimi + Local rows only — **no Z.ai row at all, not even an error/no-key/auth row**. User's real config (`~/.goa/config.yaml`): provider `id: zai`, `endpoint: https://api.z.ai/api/coding/paas/v4`, `api_key` present, and **no `provider:` identity field** (unlike sibling entries which carry `provider: lmstudio` / `provider: opencode-go`).

This narrows the leads considerably:
- `appendProviderRows` (plugin.js) renders a row for *every* cached entry — success, `no_api_key`, `auth_required`, or generic fetch error. A completely absent row means `_cache["zai"]` was **nil**: the zai fetcher never produced a result, i.e. `refreshDue("zai", …)` never ran or threw before caching (the `try/catch` still caches `{error: …}`, so a caught error cannot explain a missing row).
- Most consistent hypotheses remaining:
  1. **Stale installed bundle** — the running plugin may come from the plugin lockfile/installed copy under `~/.goa` rather than the in-repo `plugins/bundled/provider-quota`, predating the monitor-host URL fix and error rows. Verify which source the loader prefers (`plugins/bundled_load.go`, `plugins/lockfile.go`) and what version is installed at the user's machine (`version: 1.1.0` in plugin.yaml).
  2. **Fetcher registration dropped** — if `require("./fetchers/zai.js")` failed at load in the deployed bundle, `_fetchers["zai"]` would be absent (plugin would still work for the rest). The Go-side `plugins_quota_test.go` asserts zai presence in the providers map, but nothing asserts the *fetcher registry* contents at runtime.
  3. **Test gap:** every zai harness test sets `provider: "zai"` in the config map; the user's entry has *no* `provider:` field (id-only match). Add a harness case `setProvider("zai", {"id": "zai", "apiKey": "k", "endpoint": …})` (no `provider` key) to prove id-direct matching is not the failure.
- **Next step:** reproduce with the exact config shape above + active `kimi-code`, dump `_fetchers`/`_cache` via `/quota:json`, and check `[plugin]` logs for the zai fetch.
