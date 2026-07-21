<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bugs closed 2026-07-21

All items below were reproduced/localized, fixed, and validated with the
guideline #6 quality gates (go vet, staticcheck, gocognit -over 15,
gocyclo -over 12, go test -count=1 -race -cover — each run separately).
Resolution per item:

- /stats completion + /stats:project + cache tracking — FIXED (42eaf07):
  StatsCommand implements core.ArgCompleter (session/project), new
  /stats:project view (provider/model/cache), cache column in usage tables;
  regression tests + interface assertion.
- edit replace_lines silent deletion — FIXED (d3f8416): new_string fallback
  for line ops, missing_parameter guard, accurate removed/inserted message;
  regression tests.
- Model delete on __delete__-polluted entry — FIXED (5877479): selector '-'
  guard narrowed to exact sentinels; loader strips __delete__-prefixed IDs;
  regression tests.
- Quota command unresponsive — FIXED (237b1ff + b4d1cb9): bare /quota renders
  from cache, cold cache acknowledges immediately + async render via
  goa.output; priming off the load path; RunFile VM-lock race fixed; harness
  made deterministic.
- Quota (z.ai not visible) — CANNOT REPRODUCE on current main (already fixed
  by 0e669ab + 708e2e5); the 2026-07-21 re-report was a stale binary.
  Verified with the user's exact config shape (id-only zai, kimi-code
  active); regression test TestQuota_ZaiIdOnlyConfigAppearsInFullQuotaOutput.
- Agent stops mid-task (premature finish_reason=stop) — FIXED earlier
  (0c30a4c auto-continue; 7819fa1 thinking-loop hardening).
- ctrl-k deletes to end of buffer — FIXED earlier (0e669ab): line-scoped
  kill + multi-line tests.
- Arrow up/down history on dirty input — FIXED earlier (0e669ab): dirty flag
  gating + tests.
- z.ai still not visible in /quota (re-report) — CANNOT REPRODUCE (see Quota
  item above); probe of /quota:json shows the zai fetcher registered and
  rendering.

---

# Archived items (all closed)
## /stats completion + /stats:project + cache tracking

**Reported 2026-07-21.** Three related gaps in the `/stats` command:

1. **`/stats` and `/stats:session` missing from completion proposal** — typing
   `/stats` or `/stats:` in the input offers no completion. Root cause:
   `StatsCommand` (core/commands/transparency.go) does not implement
   `core.ArgCompleter` (`CompleteArgs`), so the arg completer
   (internal/app/tui.go buildArgCompleter) returns nil for `/stats:` and
   `/stats <tab>`. `/stats` itself is registered so should appear in the base
   command list; verify with a completer test.
2. **`/stats:project` unsupported** — add a `:project` subcommand (+
   completion) showing project-level stats: total usage, provider, model.
   Data source: the persistent usage store already aggregated by
   `/stats` default → `UsageCommand` (project/provider/model breakdown).
   `/stats:project` should render the current project's totals (input/output
   tokens, cache read/write, per-provider and per-model rows).
3. **All `/stats` should also track cache use** — per-turn detail already
   prints `Cache: R=… W=…` when non-zero; ensure the session summary and the
   new `/stats:project` view also surface cache read/write totals (global
   `UsageCommand` already aggregates CacheRead/CacheWrite — reuse).

### Fix plan
1. Implement `StatsCommand.CompleteArgs` returning `session`, `project` (and
   keep numeric turn drill-down untracked). Verify `/stats` base completion
   with a CommandCompleter test.
2. Add `:project` branch in `StatsCommand.Run` rendering project-level totals
   (provider + model rows + cache columns) from the usage store.
3. Ensure session summary (`writeSummaryStats`) prints cache totals.
4. Tests: completer proposes `/stats`, `/stats:session`, `/stats:project`;
   `/stats:project` output includes provider/model rows and cache figures;
   session summary includes cache line when cache was used.
5. Validate: go vet, staticcheck, gocognit -over 15, gocyclo -over 12,
   go test -count=1 -race -cover (each separately).

## edit replace_lines silently deletes lines when model sends new_string instead of new_content

**Export:** `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260721-082715.zip` · **Session:** `1784574228_n4qzkao8` · **Provider/Model:** `opencode-go` / `deepseek-v4-flash`

### Observed
Model called `edit` with `{"operation": "replace_lines", "new_string": "...", "start_line": 116, "end_line": 127}`. The tool deleted lines 116-127, inserted nothing, reported "0 lines affected", and left `frigolite.go` syntactically broken (unclosed for-loop). Same failure on `internal/exec/engine.go` (line 482). Model burned ~50 rounds recovering via Python.

### Root cause (verified from bundle + source)
- `tools/editfile.go:193-201` — `editByOperation` builds `newLines` only from `p.NewContent`; `new_string` is ignored for line-based ops.
- `tools/editfile.go:338-350` — `replaceLines` with empty `newLines` = pure deletion.
- `tools/editfile.go:225` — "0 lines affected" message is false: lines were removed.

### Fix plan
1. In `editByOperation`: for `replace_lines`/`insert_after`/`insert_before`, if `new_content` is empty and `new_string` is non-empty, fall back to `new_string` and prepend a note to the result.
2. If both are empty for a content-requiring op → return a `missing_parameter` ToolError naming `new_content` (never silently delete). `delete_lines` unaffected.
3. Fix the affected-lines message to report removed vs inserted counts accurately.
4. Tests (`tools/editfile_test.go`): replace_lines with `new_string` only → fallback works, file correct; replace_lines with neither → error, file untouched; insert_after with `new_string` → fallback.
5. Validate: `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`, `go test -count=1 -race -cover ./tools/...`.

### Note
Duplicated assistant text in the transcript is provider-side repetition (deepseek-v4-flash), not a TUI bug — deltas repeat in `events.jsonl` itself.

## Model delete
Doing '-' on the following screen, on the model '__delete__deepseek-v4-flash' should trigger the delete but nothing occurs !
(other model seems to work fine)
```
Select model:
────────────────────────────────────────────────────────────
search>
────────────────────────────────────────────────────────────
› __delete__deepseek-v4-flash  provider=zai model=__delete__deepseek-v4-flash
  deepseek-v4-flash  provider=opencode-go model=deepseek-v4-flash
  glm-5-2  provider=zai model=glm-5.2
  google/gemma-4-e4b  provider=lmstudio model=google/gemma-4-e4b
  ✓ k3  provider=kimi-code model=k3 (active)
  kimi-for-coding  provider=kimi-code model=kimi-for-coding
  qwen/qwen3.5-9b  provider=lmstudio model=qwen/qwen3.5-9b
  qwythos-9b-v2  provider=lmstudio model=qwythos-9b-v2
(1 more)
────────────────────────────────────────────────────────────
  ↑↓ nav  /  type filter  /  enter  /  esc  /  + add / - delete
```

## Quota command unresponsive
when typing `/quota` there is a delay before the screen is shown - there should be a clear indication that the command is being processed
(the inputline feels frozen)

## Quota
z.ai does not show up in the quota - no message / no error / no direction:
```
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ │ /model                                                                                                                                                    │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ✓ /model completed successfully                                                                                                                             │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ⚡ Switched to model: glm-5-2                                                                                                                               │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ │ /quota                                                                                                                                                    │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ## Session Usage (current)                                                                                                                                  │
│ ┌──────┬───────┬────────┐                                                                                                                                   │
│ │ Msgs │ Input │ Output │                                                                                                                                   │
│ ├──────┼───────┼────────┤                                                                                                                                   │
│ │ 0    │ 0     │ 0      │                                                                                                                                   │
│ └──────┴───────┴────────┘                                                                                                                                   │
│                                                                                                                                                             │
│ ## Provider Quotas                                                                                                                                          │
│ ┌──────────────────┬────────────────┬──────────────┬──────────┬───────────┬────────────────┐                                                                │
│ │ Provider         │ Window         │ Usage        │ At reset │ Resets in │ Status         │                                                                │
│ ├──────────────────┼────────────────┼──────────────┼──────────┼───────────┼────────────────┤                                                                │
│ │ Kimi (Advanced)  │ Session (5h)   │ ░░░░░░░░ 0%  │ 0%       │ +27m      │ plenty of room │                                                                │
│ ├──────────────────┼────────────────┼──────────────┼──────────┼───────────┼────────────────┤                                                                │
│ │ Kimi (Advanced)  │ Weekly         │ ████░░░░ 52% │ 92%      │ +3d 1h    │ close to limit │                                                                │
│ ├──────────────────┼────────────────┼──────────────┼──────────┼───────────┼────────────────┤                                                                │
│ │ Local (inferred) │ Session tokens │ 0            │ —        │ —         │ —              │                                                                │
│ └──────────────────┴────────────────┴──────────────┴──────────┴───────────┴────────────────┘                                                                │
│                                                                                                                                                             │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ │ /model                                                                                                                                                    │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ✓ /model completed successfully                                                                                                                             │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ⚡ Switched to model: k3                                                                                                                                    │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯

───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────

───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
~/dev/goa (⎇ main)                                                                                                                        coding-posture │ YOLO
                                                                                                                               (kimi-code) k3 • high • [0%|52%]
```
                                                                                                                               

# Archived validation items (all closed)

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

---

# Ideas implemented 2026-07-21 (from ideas.md)

All three items implemented, tested, and validated with the guideline #6
gates; full-repo -race suite green (2 consecutive runs, 0 failures).

- Hexagon spinner as default — IMPLEMENTED (307ccbb): hexagon frames ⬡⬢⬣⬢ at
  400ms added to spinners.json; spinner.Default() prefers hexagon (arc still
  available by name); status test updated to the new default.
- Title bar startup sequence — IMPLEMENTED (307ccbb): titleController (single
  title writer) shows boot brand g⬡a from TUI creation; explicit startup-done
  hook fires when BOTH async plugin load AND background history load complete
  (decided 2026-07-21), with the 5s timer as fallback only; transition
  g⬡a → g⬡ → ⬡ plays at 1s/frame, then settles on the contextual title.
- Title bar animated while working — IMPLEMENTED (307ccbb): status spinner
  spin-state drives the controller (working → spinner frames + context
  suffix, idle → static title); configurable via tui.animated_title (default
  true), frames from tui.spinner (default hexagon).

## Archived items (all closed)
## Spinner: hexagon spinner as default

**Source:** ideas.md (2026-07-21). Use the hexagon spinner (looping, slow) as the default spinner:
```
⬡⬢⬣⬢
```

### Fix plan
1. Add a `hexagon` definition to `internal/spinner/spinners.json`: frames `["⬡","⬢","⬣","⬢"]`, slow interval (~400ms).
2. Change `spinner.Default()` to prefer `hexagon` (fall back to `arc`, then any).
3. Tests: frames/interval exact-match (mirror `TestRequestedSpinners`); `Default()` returns hexagon.

## Title bar: startup sequence

**Source:** ideas.md (2026-07-21). Set the terminal title as early as possible to `g⬡a`; when the startup sequence is done — explicit hook after async plugin/history load completes (decided 2026-07-21), with a 5s fallback timer — transition to the final title `⬡` via a slow animation (1s frame rate): `g⬡a → g⬡ → ⬡`.

### Fix plan
1. `internal/app/tui.go`: `engine.SetTitle("g⬡a")` before/around `engine.Start()` (interactive TUI only — skip headless/tests).
2. Add an explicit startup-done hook fired after async plugin + history load completes; on fire (or 5s fallback, whichever first), animate `g⬡a → g⬡ → ⬡` at 1s/frame, then hand the title over to the animated-title controller.
3. Tests: fake terminal captures SetTitle sequence; startup-done hook fires exactly once; fallback timer fires when hook never called.

## Title bar: animated while working

**Source:** ideas.md (2026-07-21). Animate the terminal title with the spinner animation while goa is working; configurable (default on), spinner from `tui.spinner` config (default hexagon).

### Fix plan
1. Title animator owned by the app layer (single writer; startup sequence hands off to it).
2. Hook agent state transitions (working → animate with configured spinner frames at its interval; idle → static `⬡`).
3. Config: `tui.animated_title` (default true) — reuses `tui.spinner` for the frame set.
4. Tests: working→idle transitions drive SetTitle with spinner frames then static title; config off disables animation.
