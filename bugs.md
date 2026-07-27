# Bug Reports & Fixes

Tracked issues reported by user. Each entry: symptom → root cause → fix → test.

---

## Issue 1 — Compression trigger % menu too coarse

**Symptom:** `/config` → Compression → "Trigger threshold" only offers `50/75/80/90/100`.
User wants any percentage in **10% increments starting at 10%** (10, 20, … 100).

**Root cause:** `core/commands/config_compression.go` — `settingCompressionThreshold()`
hard-codes five options instead of a 10-step range.

**Fix:** Generate items 10→100 step 10 programmatically. Keep descriptions for the
notable presets (early/balanced/default/late/limit) where they land on the range.
The underlying setter `configSetters["context_compression.thresholds.trigger_percent"]`
already accepts any 0–100 int (`setIntRange`), so no validation change needed.

**Test:** `core/commands/config_menu_test.go` — assert the threshold submenu lists
10,20,…,100 and that selecting e.g. "30" persists `TriggerPercent=30`.

**Status: FIXED.** `settingCompressionThreshold` builds 10,20,…,100 via a loop with
`triggerPercentDescription` annotating 10/50/80/90/100.
`TestConfigMenu_CompressionThresholdOptions` asserts the 10 options in order and that
selecting 30 persists `TriggerPercent=30`. ✔

---

## Issue 2 — Trigger threshold changes are not reflected

**Symptom:** changing trigger threshold via `/config` does not take effect (menu
display and runtime keep showing the old value).

**Root cause (confirmed):** the **legacy alias `threshold_percent` shadows** the new
tiered field in two places:
- Display: `core/commands/config_compression.go` `compressionTriggerValue()` returns
  `ThresholdPercent` when `> 0`, so after an edit the menu re-renders the stale value.
- Runtime: `core/agentmanager_lifecycle.go` `resolveAgenticThresholds()` — `if
  legacyTrigger > 0 { out.TriggerPercent = legacyTrigger }` — the agent keeps the old
  trigger.

The menu setter only writes `Thresholds.TriggerPercent`; it never clears the legacy
`ThresholdPercent`, so any config file that still carries `threshold_percent:` (from an
older goa) permanently shadows edits. This is documented in
`TestConfigMenu_CompressionThresholdChange` ("legacy alias wins on read-back").

**Fix:** when the user sets `context_compression.thresholds.trigger_percent`, also
clear the legacy alias:
- In the setter for that key: set the tiered field **and** zero
  `cfg.ContextCompression.ThresholdPercent`.
- Persist the clear: also `SaveHomeField(["context_compression","threshold_percent"], 0)`
  (or remove the key) so a stale home-layer value cannot re-shadow after reload.
Backwards compat is preserved: users who never touch the setting keep legacy behavior.

**Test:**
- Setter test: setting trigger clears `ThresholdPercent`.
- Menu test: with `ThresholdPercent: 80` seeded, choosing 50 results in
  `compressionTriggerValue(cfg) == 50` and `resolveAgenticThresholds` trigger == 50.
- Regression: existing "legacy alias wins" test must be updated to reflect that an
  *explicit user edit* clears the alias (the alias only wins when untouched).

**Status: FIXED.**
- `config_cli.go`: new `setTriggerPercentClearLegacy` setter sets the tiered field and
  zeroes `ThresholdPercent`; wired into `configSetters` for
  `context_compression.thresholds.trigger_percent`.
- `persistConfigValue`: after saving the tiered value, calls
  `ConfigSaver.DeleteHomeField(["context_compression","threshold_percent"])` so a stale
  home-layer legacy key cannot re-shadow after reload (interface already had
  `DeleteHomeField`).
- Backwards compat preserved: `compressionTriggerValue`/`resolveAgenticThresholds` still
  honor the legacy alias for configs that never edit the tiered field
  (`TestCompressionTriggerValue_LegacyAliasWins` unchanged, still green).
- Tests: `TestSetTriggerPercentClearLegacy` (setter + range validation) and
  `TestConfigMenu_TriggerEditReflectsWithLegacyAlias` (menu-level regression). ✔

---

## Issue 3 — `/new` may not redraw the screen correctly

**Symptom:** after `/new`, the screen may be left in a bad state (stale content /
incorrect redraw).

**Investigation so far:** `runNew` (`core/commands/session.go`) → `ctx.NewSession()` →
`App.handleNewSession` (`internal/app/events.go:252`) which: `chat.Clear()` →
`clearStats()` → `agentMgr.StopSession()` → `tuiEngine.ClearTranscript()` →
`startAgentSession` → `RequestRender()`.

`ClearTranscript` (`tui/tui.go:586`) is documented as the "deliberate transcript reset"
that wipes screen + scrollback and resets the compositor watermark. **Suspect:** ordering
— `chat.Clear()` runs before `ClearTranscript()`, but `startAgentSession` may push new
entries and trigger a render *before* the watermark reset fully propagates; or the
compositor differential renderer retains a stale frame when the viewport shrinks to empty
then refills. Needs a reproduction via the `tui-test` skill (Filmstrip) driving
`/new` mid-session and asserting a clean first frame.

**Status: COULD NOT REPRODUCE as an independent bug — likely the same root as Issue 4.**

Investigation:
- `handleNewSession` order is correct: `chat.Clear()` → `clearStats()` → `StopSession()`
  → `tuiEngine.ClearTranscript()` (compositor `Clear()`: wipes screen+scrollback, zeroes
  `scrollTop`, sets `prevLines=nil`) → `startAgentSession` → `RequestRender()`.
- After `Clear()`, the next `Render` classifies as `frameFirst` → `drawWindow`, which
  repaints EVERY row with `\x1b[2K` + content. No stale-row path.
- Regression test `TestNewSessionClearsTranscript` (`internal/app/newsession_redraw_test.go`)
  drives 30 lines of content then the real `handleNewSession` and asserts neither the
  viewport (`chat.Messages()`) nor the rendered frame (`AgentFrame.Dump()`) retains any
  pre-/new text. It PASSES on current code.

Conclusion: `/new` correctly clears viewport + compositor at the data and frame-model
level. The "screen may not be correctly redrawn" report is most plausibly the SAME
stream-retry duplication as Issue 4 (which the user observed "even on new context"),
now fixed. The regression test above guards the `/new` clear path. If a distinct `/new`
redraw artifact resurfaces, reproduce it with a concrete frame capture (the AgentFrame
model cannot see raw-terminal bytes; a real compositor byte-stream capture would be
needed) and reopen.

---

## Issue 4 — Streaming text "repeats" (TUI artifact; changes on scroll)

**Symptom:** assistant text appears duplicated, e.g. the same sentence twice; the
duplicated region **moves/changes when scrolling** → rendering artifact, not data.

**Root cause (CONFIRMED via filmstrip reproduction):** NOT the wrap cache. The
duplication comes from the **stream-retry path**. When a transient stream error occurs
mid-answer, `Agent.handleStreamFailure` (`internal/agentic/agent_streaming.go`):
1. calls `resetStreamRoundState()` — resets `contentBuf` (so persisted history stays
   CLEAN — verified: the session `.jsonl` had 0 occurrences of the repeated text), and
2. `undoLastAssistantMessage()` — removes the partial message from agent history, then
3. emits a system "reconnecting" notification and re-streams the SAME answer from
   scratch.

In the TUI (`internal/app/stats.go handleAssistantContent`), the partial pre-retry text
had already been rendered into an assistant bubble. The retry notification's
`endCurrentStream()` only reset the *text accumulator* — the **rendered bubble was left
behind**, and the re-stream created a SECOND bubble. Result: the answer appears twice
on screen (with cumulative growth across multiple retries), while history stays clean.
The duplicate "shifts on scroll" because it is a long re-wrapping block, matching the
user's observation.

Reproduced RED with `TestStreamRetryDoesNotDuplicateAssistantText`
(`internal/app/stream_retry_dup_test.go`): partial deltas → retry notification →
re-streamed deltas → conversation contained the answer 2×.

**Status: FIXED.**
- `internal/agentic/agent_streaming.go`: the retry notification now carries
  `Metadata{"stream_retry": "true"}`.
- `internal/app/stats.go`: `handleUserOrSystemContent` detects `isStreamRetry` and calls
  `chat.RemoveLastMessageOfType(ConsoleAssistantMessage, ConsoleThinkingBlock)` to
  retract the orphaned in-progress bubble before the re-stream. `RemoveLastMessageOfType`
  only removes when the most recent entry is the in-progress bubble, so finalized earlier
  turns and tool widgets are untouched.
- Test: `TestStreamRetryDoesNotDuplicateAssistantText` asserts the answer appears exactly
  once after a retry. ✔  Full `internal/app`, `internal/agentic`, `tui` suites green.

**Residual note:** Issue 3 (`/new` redraw) is a SEPARATE concern (compositor watermark on
transcript reset), still tracked below.

---

## Issue 5 — `/stats:verbose` for global provider/model split

**Request:** the general stats should also be executable via `/stats:verbose`, which
prints, **for all known projects**, the split by **provider/model**.

**Current state:** `StatsCommand` (`core/commands/transparency.go:238 Run`) routes:
- no args → global summary via `UsageCommand.Run(ctx, args)`
- `:project` → current-project breakdown (`/usage here`)
- `:session` / `<n>` → per-turn session detail
`CompleteArgs` only offers `session` and `project`. There is **no `verbose` subcommand**.

The underlying `UsageCommand` (`core/commands/usage.go`) already supports dimensions
`project` / `provider` / `model` and cost/activity views, and the store
(`usageStore.Query(dim, project, since)`) can aggregate by provider/model — but only
one dimension at a time and only globally or per-project, not "every project ×
provider/model" in one verbose listing.

**Fix:** add a `verbose` subcommand to `/stats`:
- Register `verbose` in `CompleteArgs` (and `:`-prefixed form) with a description.
- In `Run`, handle `args[0] == "verbose"`: enumerate all known projects from the usage
  store and, for each, print the per-provider and per-model split (tokens, cost, cache).
- Reuse `UsageCommand` rendering primitives rather than duplicating formatting; a small
  iterator over `Query(ByProject …)` then per-project `Query(ByProvider …)` /
  `Query(ByModel …)` keeps it composable.
- Update `LongHelp`/`help.LongHelp("stats")` to document `:verbose`.

**Test:** `core/commands/` test seeding a fake usage store with two projects × two
providers/models; assert `/stats:verbose` output lists each project and its provider and
model rows; assert `verbose` appears in completions.

**Status: FIXED.**
- `usage.go`: added `verbose` to `usageRequest` + `parseUsageArgs`, and `writeVerbose`
  which enumerates projects via `Query(ByProject, "", since)` then renders per-project
  `By provider`/`By model` sections reusing `writeUsageSection`.
- `transparency.go` `StatsCommand`: routes `verbose`/`:verbose` and offers it in
  `CompleteArgs`.
- `help/stats.long.md`: documents `:verbose`.
- Tests: `TestStatsCommand_Verbose` (2 projects × provider/model splits) and
  `TestStatsCommand_VerboseCompletion`. ✔

---

## Issue 6 — CRITICAL: thinking loop not caught (A/B alternation + short lines)

**Symptom (critical):** the model loops ~200 times alternating two phrasings of the same
intent, and loop detection never fires:
```
Let me check the full file.
Let me read the full file.
Let me check the full file.
Let me read the full file.
... (repeats for hundreds of lines)
```

**Root cause (two compounding gaps in `core/loopdetector.go`):**

1. **A/B alternation defeats exact-line matching.** `RecordThinkingDelta`
   (line 397) counts a line only when it is **byte-identical** to a prior line
   (`h := hashInput(line); ld.thinkLineCounts[h]++`, line 428–429). Two alternating
   phrasings produce two *different* hashes, so neither line's individual count climbs
   fast enough to cross `thinkInterruptThreshold` within the window. The tool-call
   detector makes the same "a real loop never alternates" assumption (comment line 158).

2. **Short lines are filtered out.** `minThinkWordCount = 10` (line 129) ignores any
   line with fewer than 10 words. "Let me check the full file." is 5 words, so these
   lines are **never counted at all** — even a pure (non-alternating) repeat of a short
   line escapes detection. The 10-word floor is meant to skip list markers/separators
   but is far too high for short repetitive filler phrases.

**Why history/export confirms it:** the loop is genuine repeated model output (a real
reasoning loop), distinct from Issue 4's TUI rendering repeat — here the model really is
emitting the same intent over and over.

**Fix design (loop detector hardening):**
- **Detect near-duplicate / alternating lines**, not just exact matches. Normalize each
  significant line (lowercase, strip punctuation, collapse whitespace) to a canonical
  form, and/or track a small set of recent line hashes to catch A/B cycles. A cheap
  approach: keep the last N normalized lines and detect when the *set* of distinct
  normalized lines in a sliding window stays small while the window grows (i.e. low
  line diversity over many lines = loop), which catches alternation regardless of
  per-line exact counts.
- **Lower or make adaptive the word-count floor** for loop *detection* (distinct from
  the floor for *counting toward interrupt*), OR count short repeated filler lines under
  a separate, lower threshold so a pure short-line loop still trips. Keep the 10-word
  floor for the *exact-line* path to avoid false positives on legitimate structure.
- Preserve the existing "no false positive on legit deep-debugging" guarantee: only
  escalate when line diversity is low *and* volume is high, and still strip code/tool
  blocks first.

**Status: FIXED.** Three root causes addressed in `core/loopdetector.go`:

1. **`isStructuralLine` misclassified "Let me …" prose as code.** The JS keyword
   `"let "` prefix-matched "Let me check/read the full file.", so every line of the
   loop was treated as structural and never counted. Removed prose-colliding declarative
   keywords (`let`, `do`, `new`, `type`, `val`, `final`, plus `int/bool/string/from/
   using/end/begin`) from the keyword list; Go declarations are still caught by
   `startsWithIdentifierAndCode`, and other languages' code is fenced (handled by
   `skipCodeFenceLine`).

2. **A/B alternation defeated exact-line matching.** Added a sliding-window
   **diversity detector** (`thinkRecentLines`/`thinkRecentCounts`, window 24): when the
   window is full and the number of DISTINCT normalized prose lines stays in 2..3, the
   model is cycling among a tiny phrase set → warn/interrupt. Lines are normalized
   (lowercase, punctuation stripped, whitespace collapsed) so near-identical filler
   counts as one phrasing.

3. **Short lines were filtered out entirely.** The diversity window tracks prose lines
   down to 5 words (below the exact counter's 10-word floor), so a short filler loop
   ("Let me read the full file.") is caught. A single repeated LONG line is still left
   to the exact counter, and a single repeated SHORT line only fires when it recurs —
   protecting the genuine "repeated sentence across code quotes" case.

`ResetThinking` resets all diversity state so a latched window cannot kill the next turn.

**Tests (all green, `go vet`/`gocognit`/`gocyclo` clean):**
- `TestLoopDetector_ThinkingLoop_AlternatingPhrases` — A/B loop → LoopInterrupt.
- `TestLoopDetector_ThinkingLoop_ShortLineLoop` — pure short-line loop → ≥ LoopWarning.
- `TestLoopDetector_ThinkingLoop_VariedReasoningNoFalsePositive` — 40 distinct short
  lines → LoopOK.
- Corrected `TestLoopDetector_ThinkingLoop_MultiLineCodeFenceNotAloop` to vary the prose
  per iteration (it previously conflated fence-skipping with a verbatim 5× prose repeat,
  which IS loop-like); fence-skipping invariant still verified.
- All pre-existing fence / latch / no-false-positive tests still pass.

---

## Issue 7 — Validate LSP works on Go edit + expose LSP features to the model

**Request:** (a) validate that LSP actually works on Go code edits, and (b) allow the
model to use LSP features (not just passive diagnostics).

**Current state (investigated):**
- `internal/lsp/` implements a gopls-backed manager (`Manager`) + JSON-RPC `Client`.
- `internal/app/bootstrap.go:636 newLSPManager` auto-starts gopls when the project has a
  `go.mod`; startup failure is recorded via `StartError()` and surfaced in the banner.
- The **only** capability used is **diagnostics**: `Client` implements just
  `initialize`, `initialized`, `textDocument/didOpen`, `textDocument/didChange`,
  `shutdown`, `exit`. `Manager` exposes `OpenDocument` / `DidChange` / `DiagnosticsFor`
  / `HasErrors` / `Close`.
- `tools/lsp_diagnostics.go` polls `DiagnosticsFor` after an edit/write and appends a
  "Diagnostics (gopls):" block to the tool output (`editfile.go:330`, `writefile.go`).
- **No query features**: no `textDocument/definition`, `references`, `hover`,
  `documentSymbol`, `rename`, `completion`, etc.
- **No model-facing LSP tool**: the model cannot query the language server; LSP output
  reaches it only as a by-product of editing a file.

**Work items:**
1. **Validate diagnostics path** — integration test (or live check) that a Go edit with
   a deliberate type error surfaces gopls diagnostics in the tool result. There is
   already `tools/lsp_integration_test.go`; confirm it runs (may need `gopls` on PATH)
   and covers the edit→diagnostic round-trip, not just unit-level polling.
2. **Add LSP query capabilities** to `internal/lsp` client + manager: start with the
   highest-value navigation ops — `textDocument/definition`, `textDocument/references`,
   `textDocument/hover`, `textDocument/documentSymbol`. Follow the existing
   request/notify pattern in `client.go`.
3. **Expose an LSP tool to the model** — a new `tools/lsp.go` implementing
   `agentic.Tool` (e.g. `lsp` with `op: definition|references|hover|symbols`, `path`,
   `line`, `column`) so the model can navigate code precisely instead of grep+guess.
   Register it in the tool registry and add a renderer (`tools/lsp_renderer.go`) per
   project conventions. Gate behind a config flag (`tools.lsp.enabled`, default on for
   Go projects) and reuse the shared `LSPManager` from bootstrap.
4. Keep it SOLID: small primitives in `internal/lsp` (one method per LSP op), a thin
   composable tool on top — not a fat per-op API.

**Test:**
- `internal/lsp`: unit tests for each new request method (mock JSON-RPC server).
- `tools/lsp_test.go`: tool schema/validation + a fake `LSPDocumentManager`/manager for
  definition/references/hover.
- integration (guarded by `gopls` availability): open a Go file, query definition of a
  symbol, assert a non-empty location.
- renderer test for the TUI output.

**Status: FIXED — and generalized to a full multi-language LSP subsystem (OpenCode parity).**

Implemented far beyond the original Go-only scope, following OpenCode's LSP architecture
(`packages/opencode/src/lsp`):

- **Query capabilities (7b):** `textDocument/definition`, `references`, `hover`,
 `documentSymbol` added to `internal/lsp` `Client` + `Manager`.
- **Multi-language server registry (SOLID + embedded config):** `internal/lsp/servers.yaml`
 embeds 34 declarative server specs ported from OpenCode's `server.ts` (gopls, pyright,
 typescript-language-server, jdtls, rust-analyzer, clangd, ruby-lsp, zls, lua-ls, bash,
 terraform, dockerfile, and more). Each spec: id, extensions, root markers, command,
 language_id, optional npx fallback + install. `servers.go` loads it via `//go:embed`.
- **Per-file client selection (OpenCode `getClients` model):** the `Manager` selects the
 right server by file extension, resolves the project root by walking up for marker
 files, and spawns one client per (server, root) lazily, with single-flight spawning.
 Spawned servers use a manager-owned lifecycle context so a query's ctx cancellation
 never kills a long-lived server.
- **Auto-install (OpenCode parity):** PATH → `npx --yes <pkg>` (Node servers, per the
 user's npx direction) → `go install` / npm / download installers into `~/.goa/bin`.
 Gated by `lsp.disable_download` (OpenCode's `disableLspDownload`).
- **Model-facing tool (7c):** `tools/lsp.go` (`op: definition|references|hover|symbols`),
 `ExecuteContext` for cancellation, config-gated via `tools.enabled.lsp` (opt-out
 default true), runtime-toggleable via `/tools:lsp:on|off` (factory path — no restart).
- **File-action linking (OpenCode `touchFile`):** read/edit/write notify the manager so
 diagnostics stay fresh (`tools/readfile.go` touch; edit/write already linked).
- **Config (OpenCode schema):** `lsp: false` disables everything globally (tool +
 linking + manager); `lsp.servers.<id>` disables/overrides a builtin or defines a custom
 server (command/extensions/env/initialization/markers/language_id); `lsp.disable_download`.

**Tests (all green, vet + gocognit + gocyclo clean):**
- `internal/lsp/manager_test.go` — per-file server selection, lazy spawn + client reuse,
 version increments, no-server-for-extension, broken-server marking (loopback fake conn).
- `internal/lsp/query_integration_test.go` — real-gopls definition/references/hover/symbols.
- `internal/lsp/servers_test.go` — registry load, merge (disable/override/custom),
 specForFile, FindRoot, LanguageIDFor.
- `config/lsp_config_test.go` — `lsp: false`, server overrides, disable_download parsing.
- `internal/app/lsp_global_disable_test.go` — global `lsp: false` removes the manager.
- `tools/lsp_test.go` — tool schema/validation/ops against a fake manager.

---

## Issue 8 — New session (goal / `/new`) must use a fresh conversation id so cache clears

**Request:** when a session is started — via a goal or via `/new` — make sure the
conversation id is a NEW one, so provider-side cache is cleared and the new
conversation does not inherit stale prompt-cache affinity / previous-response linkage.

**Why it matters:** `StreamOptions.SessionID` is not just an internal label — it drives
provider caching/identity:
- OpenAI Responses: `previous_response_id` + `prompt_cache_key`
  (`internal/agentic/provider/protocol/openai_responses.go:96-99`,
  `openai_completions.go:439-444`).
- Session-affinity / prompt-cache headers (`provider/hooks/cache.go:54`,
  `anthropic/stream.go:46-47`, `runtime.go:110` `X-Session-ID`).
Reusing an id across a logically-new conversation pins the new turn to the old cache /
response chain — wasted cache reads and potential cross-conversation bleed.

**Current state (investigated):**
- `SessionID` is set in exactly one place: `AgentManager.StartSession`
  (`core/agentmanager.go:193-195`) → `sessionStore.StartSession()` which mints
  `time.Now().Unix() + "_" + randomID(8)` (`core/sessionstore.go:188`).
- `BuildStreamOptions` (`provider/manager.go:797`) never sets `SessionID` — good; no
  stale id is baked into shared opts.
- **`/new` path is correct**: `runNew` → `ctx.NewSession()` → `App.handleNewSession`
  (`internal/app/events.go:252`) → `startAgentSession` → `agentMgr.StartSession` →
  fresh id. ✔
- **Session restore intentionally reuses the id** via `StartSessionWithID`
  (`core/sessionstore.go:197`) — correct for resume (keep the cache/identity).
- **GAP — goal path**: a goal started on the *current* session (`/goal:new`, queued-goal
  promotion, `goal` tool) drives turns on the EXISTING agent via the GoalDriver — it does
  NOT call `StartSession`, so the conversation id (and cache key) is unchanged. If a goal
  is meant to be a fresh conversation (cleared cache), it currently is NOT. Conversely if
  a goal is meant to continue the current conversation, reusing the id is correct — the
  desired semantics need to be pinned down.

**Work items:**
1. Pin down semantics: should a new goal start a *fresh conversation* (new id, cleared
   cache) or *continue* the current one? (Likely: a goal is a new objective but the SAME
   conversation — so id reuse is fine; but a goal that starts a NEW session, e.g. a
   fresh sub-agent or a `/new`-then-goal flow, must mint a new id.)
2. Add an explicit guarantee + test that **every** new agent session (`/new`, first
   start, headless, ACP) yields a distinct non-empty `SessionID` — assert two
   consecutive `StartSession` calls produce different ids.
3. If a goal is intended to start a fresh conversation, route it through
   `agentMgr.StartSession` (or a `ResetConversationID`) so `SessionID` is regenerated;
   verify the new id propagates to `prompt_cache_key`/`previous_response_id`.
4. Regression test: after `/new`, the agent's effective `SessionID` differs from the
   prior session's; after restore, it matches.

**Test:**
- `core/sessionstore_test.go`: two `StartSession()` calls → distinct ids.
- `core/agentmanager_test.go`: `StartSession` → agent `StreamOptions.SessionID` non-empty
  and changes across sessions; `StartSessionWithID` preserved on restore.
- goal-driver test: starting a goal that opens a new session produces a new id.

**Status: FIXED.**
- Confirmed `/new` and first-start already mint fresh ids (`StartSession` →
  `sessionStore.StartSession()`). Added regression tests:
  `TestSessionStartDistinctIDs` (store) and
  `TestAgentManager_StartSession_FreshConversationID` (agent StreamOptions).
- **Found & fixed the goal gap**: a fresh-context goal (`RunFresh begin=true`) cleared
  the agent's HISTORY (`SetHistory(nil)`) but kept the prior `SessionID`, so the clean
  context stayed pinned to the old provider cache / response chain. Added
  `AgentManager.ResetConversationID()` (mints a new id via the session store and applies
  it to the active agent's `StreamOptions`) and called it from
  `agentManagerRunner.RunFresh` when `begin=true`. Test `TestAgentManager_ResetConversationID`.
- Semantics pinned: a *normal* goal continues the current conversation (id reuse is
  correct — preserves cache); only a *fresh-context* goal rotates the id. Restore keeps
  identity via `StartSessionWithID`. ✔

---

## Fix order / status
1. Issue 1 — compression trigger % menu 10–100 step 10. ✅ FIXED + tested
2. Issue 6 — CRITICAL thinking-loop not caught (A/B alternation, short lines,
   "Let me" misclassified as code). ✅ FIXED + tested
3. Issue 2 — trigger threshold edits shadowed by legacy alias. ✅ FIXED + tested
4. Issue 5 — `/stats:verbose` provider/model split per project. ✅ FIXED + tested
5. Issue 4 — streaming repeats (stream-retry orphaned bubble). ✅ FIXED + tested
6. Issue 8 — fresh conversation id (incl. fresh-context goal cache reset). ✅ FIXED + tested
7. Issue 3 — `/new` redraw: COULD NOT REPRODUCE (regression test added; likely same
   root as Issue 4). ⏸ monitored
8. Issue 7 — LSP: full multi-language subsystem (OpenCode parity). ✅ FIXED + tested
   (embedded 34-server registry, per-file selection, npx/go-install, model tool,
   file-action linking, global + per-server config).
