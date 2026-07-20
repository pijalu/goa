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
