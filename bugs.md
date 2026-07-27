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

---

## Issue 9 — Poolside (and 6 other providers) send no Authorization header → 401

**Symptom:** Switching to Poolside (`/model` → `laguna-s-2-1`) and prompting yields
`Error: 401 - {"error":"please check the api-key you provided"}`. Export
`goa-export-20260727-030210.zip` `diagnostics/trace.json` shows the request to model
`poolside/laguna-s-2.1` returned HTTP 401 in ~200ms.

**Investigation (decisive):**
- The configured key is VALID: direct curl to `https://inference.poolside.ai/v1/chat/completions`
  with `Authorization: Bearer <cfg key>` and model `poolside/laguna-s-2.1` returns **200**.
- Server differentiates: **no `Authorization` header → 401**; empty/wrong Bearer → **403**.
  Goa got **401**, so Goa sent **no auth header at all**.
- `provider.ProviderManager.BuildStreamOptions()` correctly resolves the key (len 45).
- But the runtime uses `GenericStream` (protocol path), where auth is injected by
  `hooks/auth.go` `AuthHook.ApplyRequest` → `injectAuth(ctx.Headers, h.profile.Auth, key)`.
  `injectAuth` **returns early when `auth.Header == ""`**. The `profile` comes from
  `schema.ResolveProfile(model)`.
- Probe: `ResolveProfile(poolsideModel)` → `profileID="" auth.Header=""` (EMPTY), while
  `kimi-code/deepseek/zai/opencode-go/...` → `auth.Header="Authorization" prefix="Bearer "`.

**Root cause (confirmed):** there is **no `poolside.json` variant profile** in
`internal/agentic/provider/schema/variants/`. Auth headers come ONLY from a matched
variant profile's `Auth` config. Poolside matches nothing → empty `Auth.Header` → key
never sent → 401. The resolver has **no fallback**: `Resolver.Resolve` returns
`VariantProfile{}` when nothing matches.

**Blast radius (probe of every catalog provider):** 7 providers resolve to NO profile →
all broken the same way: `poolside`, `groq`, `fireworks`, `perplexity`, `zai-api`,
`azure`, `custom`. (`openai-base.json` requires `provider:"openai"`, so it never catches
them.) Every provider WITH a dedicated profile works — matches the user's report that
"the issue seems only to trigger on poolside" (their other configured providers all have
profiles).

**Fix (per user directive — default sane fallback, not per-provider whack-a-mole):**
when no variant profile matches, the resolver returns a **sane default profile** that
includes standard OpenAI-compatible Bearer authentication and baseline compat/tool/cache/
error defaults. This fixes all 7 broken providers, custom endpoints, and every future
provider in one place.

**Test:** resolving an unmatched provider (poolside / custom) yields a profile with
`Auth.Header == "Authorization"`; `AuthHook` injects the Bearer header when a key is
present; end-to-end probe through `provider.Stream` for the active poolside model
returns HTTP 200.

**Status: FIXED.** Implemented in `internal/agentic/provider/schema/resolver.go`:
`Resolver.Resolve` now returns `DefaultProfile(model)` when no variant profile matches
(instead of a zero `VariantProfile{}`). `DefaultProfile` models a generic
OpenAI-compatible endpoint — `Auth{api_key, "Authorization", "Bearer ", Required:false}`,
`max_tokens`, `openai` thinking format, usage-in-streaming, OpenAI schema sanitizer,
cache `none`, retry statuses `[429 500 502 503 504]`. `Required:false` keeps keyless
local/custom endpoints working while guaranteeing a configured key is always sent.

**Verification (live):** probe through the real path (`CascadeLoader` →
`ProviderManager.BuildStreamOptions` → `agenticprovider.Stream`) for the active
poolside model returned **HTTP 200** (`stopReason=end_turn`, 1 content block);
all 7 previously-broken providers now resolve to `default-openai-compat` with
`Auth.Header="Authorization"`.

**Regression tests (all pass, incl. `-race`):**
- `schema`: `TestResolveProfile_UnmatchedProvidersGetDefaultAuth`,
  `TestDefaultProfile_SaneBaseline`, `TestResolver_NoMatchFallsBackToDefault`.
- `hooks`: `TestAuthHook_UnmatchedProviderInjectsKey` (full pipeline injects
  `Bearer <key>` for a poolside model).

---

## Issue 10 — Provider catalog / genmodels not fully reconciled with models.dev

**Symptom / directive:** "all provider configuration (endpoints, type) should be
correctly pre-configured in goa using models.dev" and "make sure all providers from
models.dev are correctly imported".

**Root cause:** three independent hardcoded lists drift apart:
- `internal/agentic/provider/schema/catalog.go` — provider defs (endpoint, API type,
  env keys, `ModelsDevKey`). Poolside IS present here.
- `cmd/genmodels/main.go` `supportedProviders` — models.dev key → goa provider/API/
  baseURL. **Poolside (and several catalog providers) are MISSING**, so their models are
  never imported from models.dev.
- `internal/agentic/provider/schema/variants/*.json` — auth/compat profiles (Issue 9).

**Fix:** reconcile `supportedProviders` with the catalog so every catalog provider with a
`ModelsDevKey` is imported from models.dev with the correct endpoint + API type; add the
missing providers (at minimum Poolside). The Issue 9 default-profile fallback guarantees
that any provider so imported also authenticates.

**Status: FIXED.** Expanded `cmd/genmodels/main.go` `supportedProviders` from 9 → 19
entries, adding the catalog's OpenAI-compatible providers that exist on models.dev:
`poolside`, `perplexity`, `fireworks-ai`, `openrouter`, `opencode`, `opencode-go`,
`moonshotai` (→kimi), `kimi-for-coding` (→kimi-code), `cerebras`, `chutes`. Each maps the
models.dev key → goa provider id, goa wire API type, and the models.dev `api` endpoint
(verified against the live `https://models.dev/api.json`, 172 providers).

Regenerated `internal/agentic/provider/models/models_generated.go`: now imports **18
providers** (was 9). Poolside contributes its 3 models with correct wiring, e.g.
`poolside/laguna-s-2.1 {Api: openai-completions, Provider: poolside,
BaseURL: https://inference.poolside.ai/v1, ContextWindow: 1048576, Reasoning: true}`.

Notes:
- `perplexity` is intentionally absent from the generated file: genmodels only imports
  tool-calling models and every Sonar model has `tool_call=false` (search QA, no
  function-calling) — a pre-existing deliberate filter, not a bug. Its provider still
  authenticates via the Issue 9 default profile.
- models.dev has ~172 providers; goa supports a curated subset (the others need wire-type
  or auth decisions, e.g. `npm: @ai-sdk/anthropic`). The default-profile fallback covers
  auth for any of them if added later; widening the curated set further is follow-up, not
  part of this bug.

**Verification:** `go build ./...` clean; full suite + `-race` green.

---

## Issue 11 — `/model add` two-step flow review (select provider → async known-model list)

**Directive:** "1/ select provider 2/ propose list of known model — run an async model
load of the given provider model ... this should be implemented for model command
addition".

**Current state (`core/commands/model.go`):**
- `runAddModelFromSelector` (L209): uses the **active provider by default** and only
  shows the provider picker when none is active/unknown — does not always "select provider".
- `pickModelFromProvider` (L236): **already async** — wraps the fetch in
  `ctx.SelectOptionAsync("Select model to add:", fetch, onSelected)` (L262-264), merging
  live `/models` + registry via `modelListForProvider` (L151). So step 2's async load
  exists.

**Gap (confirmed by user):** step 1 (explicit provider selection) was skipped whenever a
provider was already active (the flow auto-used the active provider, and also auto-picked
the provider when only one was configured). The user directive: **always ask for the
provider**, and the proposed model list must be **scoped to the chosen provider** — never
models known for other providers.

**Fix:** `core/commands/model.go` `runAddModelFromSelector` — removed the active-provider
shortcut and the single-provider auto-pick. The flow now ALWAYS shows the provider picker
(`SelectOption("Select provider:", ...)`), pre-selecting the active provider (when one is
set) purely as a highlighted default the user can confirm or change. Step 2
(`pickModelFromProvider`) was already async and already provider-scoped via
`modelListForProvider(providerID)` (live `/models` + registry for that provider only), so
no change was needed there — the chosen provider's models are the only ones proposed.

**Test:** `core/commands/model_test.go` `TestModelCommand_AddAlwaysPromptsProvider` —
with an active provider set, asserts the picker IS shown (title `Select provider:`),
pre-selects the active provider, and lists all configured providers. Passes (incl. `-race`
on the package).

**Status: FIXED.**

---

## Fix order / status (continued)
9. Issue 9 — Poolside/6 providers no auth header (default-profile fallback). ✅ FIXED + tested (+race)
10. Issue 10 — catalog/genmodels × models.dev reconciliation. ✅ FIXED (9→18 providers; poolside imported)
11. Issue 11 — `/model add` always prompts provider; model list scoped to chosen
    provider. ✅ FIXED + tested (+race)
12. Issue 12 — model falls back to bash to edit files (large-edit drift). ✅ FIXED + tested
    (+race): bash file-edit guardrail (BlockFileEdits, default ON) + smarter edit
    not-found hint + line-match (M/N) diagnostic.

---

## Issue 12 — Model falls back to bash/node to edit files (large-edit drift)

**Symptom (export `mhh-ace/.goa/exports/goa-export-20260727-032553.zip`, opencode-go /
deepseek-v4-flash):** the model starts editing `game/index.html` via `node -e
fs.readFileSync/replace/writeFileSync` instead of the `edit` tool. The bash edits then
corrupt the file: `<script><script>` double-tag → `Unexpected token '<'` → `Unexpected
token '}'` → extra `}` (depth −2) → lost `<head>`/`<style>`, costing ~40 tool calls to
un-break.

**Investigation (decisive):** `edit` is NOT broken — 69 successes vs 2 failures, both
`[edit error: not_found]` (no crash). The failing call submitted a **8,332-char / 257-line
`old_string`** of which **25% of lines had drifted** from the file (e.g. model wrote
`max-width: 900px`, file had `max-width: 860px; padding: 10px`). Goa's matcher tried
exact → trailing-whitespace → fuzzy and correctly refused. Timeline shows `bash_edit=0`
for the first ~44k events, then bash-for-editing appears and dominates once the model's
stale in-memory copy of the 1300-line file diverged too far.

**Root cause:** not the matcher — the usage pattern. On a large single-file doc the model
reconstructs a big region from memory as one huge `old_string`; as the file evolves the
copy drifts, `edit` correctly fails, and the model escalates to bash (against the
"prefer dedicated tools over bash" guidance) because nothing steers it back to small,
anchored, re-read edits and nothing guards against bash file writes.

**Fix (3 parts, all implemented).** NOTE — revised per user directive "never block, only
hint": part 1 is a NON-BLOCKING nudge, not a block, and is configurable in /config.
1. **Non-blocking hint (configurable)** — `tools/bash.go`: `BashTool.WarnFileEdits`
   (+ `WarnFileEditsResolver` for live toggle). `ExecuteContext` runs the command
   ALWAYS; `fileEditHint`/`detectShellFileEdit` only prepends a "Prefer the 'edit' tool"
   note when the command modifies a file via the shell: redirects/tee to real files
   (allows `/dev/null`, `/tmp` scratch, fd dups), in-place editors (`sed -i`, `perl -pi`),
   and interpreter inline writes (`node writeFileSync`, `python open(...,'w')`/
   `Path.write_text`, ruby `File.write`). Read-only commands stay silent. Config
   `tools.bash.warn_file_edits` (nil=on; merged in `config_merge.go`; setter
   `setBoolPtr` in `config_cli.go`; completion entry). `/config → Bash → Warn on shell
   file edits` toggles it (persisted to home config; live via the resolver).
2. **Smarter not-found recovery hint** — `tools/editfile.go` `searchReplaceError`: now
   tells the model to re-read the region, retry with a SMALLER tightly-anchored edit,
   prefer `replace_lines`/`delete_lines` (immune to content drift), and NOT to use
   bash/node/python to edit files.
3. **Line-match diagnostic** — `tools/fuzzyedit.go` `countMatchingLines` + wired into
   `searchReplace`: the not-found Detail now appends `— M/N lines of old_string matched
   the current file`, so the model sees it's content drift, not a broken tool.

**Tests (all pass, incl. `-race`):** `tools/bash_edit_guard_test.go`:
`TestDetectShellFileEdit` (9 flagged / 8 allowed patterns), `TestBashTool_WarnFileEdits`
(hint prepended + command STILL runs / read-only silent / disabled silent / resolver
override), `TestEditNotFound_LineMatchDiagnostic` (asserts `2/4` match count,
`replace_lines`, re-read, anti-bash guidance). `TestConfigMenu_RootShowsItems` updated
for the new `/config → Bash` root item.

**Status: FIXED (non-blocking, configurable).**

---

## Issue 13 — "15 consecutive tool-calling rounds" forced-answer nudge is a FALSE POSITIVE (thinking present)

**Symptom (export `.goa/exports/goa-export-20260727-035735.zip`, issue.md: "false
positive on tool call"; provider kimi-code, model k3):** mid-session the agent was
interrupted by the ephemeral note *"15 consecutive tool-calling rounds elapsed without
an answer. Now produce the final answer…"*. The user reports there was NOT 15
consecutive silent tool rounds — **the model was thinking** between calls.

**Evidence (logs/agent.log of the export):** compressed event run is
`think x2585 → content x30 → toolcall x213 → Re-streaming(round 16) → think x1171`.
So the round that hit the limit streamed **2585 thinking tokens** before its tool calls.
3756 thinking deltas total vs only 30 content deltas.

**Expected behavior (already the design):** `trackToolCallingRound`
(`internal/agentic/agent_streaming.go:110`) resets `consecutiveToolRounds` when
`contentBuf.Len() > 0 || thinkingBuf.Len() > 0` — a round with thinking is NOT a silent
tool round and must not count toward the forced-answer nudge (`checkConsecutiveToolRounds`,
limit = `execution.max_consecutive_tool_rounds` = 15).

**Root cause (hypothesis to confirm):** with 2585 thinking tokens buffered, the reset
*should* have fired — so the streak counted up anyway. Suspects, in order:
1. **Buffer lifecycle:** `resetStreamRoundState()` clears `thinkingBuf`
   (agent_streaming.go:931-932). It is called from `startStreamRound` (round>0, L172)
   and the recovery/retry paths (L1189, L1300). If any path resets the buffer *after*
   the round's thinking deltas but *before* `trackToolCallingRound()` reads them
   (runStreamRound L76), `hadThinking` is false and the streak increments spuriously.
2. **kimi-code thinking event type:** thinking deltas are logged (`[delta] thinking`)
   via `handleThinkingDelta`→`thinkingBuf.WriteString`. If k3 emits reasoning under a
   different wire field that the agent logs but does NOT route to
   `provider.EventThinkingDelta`/`handleThinkingDelta`, `thinkingBuf` stays empty while
   the log still shows thinking. (The `[delta] thinking` TRACE is emitted inside
   `handleThinkingDelta`, so this is less likely — but verify the dispatch table at
   agent_streaming.go:363-371 actually fires for k3's reasoning events.)

**Repro / next step:** write an agent-level test that streams N rounds each producing
thinking deltas followed by tool calls (mirroring the kimi-code k3 event sequence), then
assert no forced-answer nudge is injected and `consecutiveToolRounds` stays 0. The
existing `TestTrackToolCallingRound_NudgeNotFiredWithThinking` covers the unit logic
(buffer-set → reset), so the regression must be reproduced at the stream/event level to
pin whether it is (1) buffer-reset timing or (2) event-type routing.

**Root cause (CONFIRMED via stream-level repro + UI evidence):** two design defects in
`internal/agentic/agent_streaming.go`.
1. **Streak mis-attribution.** `trackToolCallingRound` only reset the
   `consecutiveToolRounds` streak when the *current* round's `contentBuf`/`thinkingBuf`
   was non-empty. The kimi-code k3 model front-loads a large reasoning block in round 0
   (export: 2585 thinking deltas) then issues many tool calls across later rounds with no
   *fresh* reasoning each round — so those productive rounds were wrongly counted as
   "silent" and the streak climbed to the 15 limit, forcing an answer. UI transcript
   confirms content/thinking ("Let me …" messages) between tool calls.
2. **Hard numeric caps.** A forced-answer nudge at 15 rounds AND a hard 250-round cap
   ended the turn on a *number*, regardless of progress.

**Side-effect — model self-reports a bogus round limit (VERIFIED via export
`goa-export-20260727-035938.zip`, issue "rounds ???", provider kimi-code/k3):** the TUI
showed the model claiming *"I have limited rounds left (this is round 29/30)"*. Verified
facts about that figure:
- The literal string `29/30` appears **nowhere** in the export (`session/events.jsonl`,
  `session.md`) — it is not present as an injected value.
- The model received **no** turn/round budget: the goal turn-budget injector
  (`core/goal/injection.go formatTurnBudget`, format `turns N/M (remaining K)`) was
  **inactive** (manifest `activeMode: coding-posture`, no goal). And no guardrail message
  emits an `N/30` figure — they say `limit: %d` with values like 2 (repeat) or 15
  (consecutive rounds), never a 30 denominator.
- The model's own numeric limits in this session were `max_consecutive_tool_rounds=15`
  and `max_stream_rounds` default 50 — **neither is 30**.

Conclusion: the "29/30" was generated by the model itself in its Thinking text while
under pressure to converge (it had just been told to wrap up by a guardrail note). It is
the model rationalizing a made-up budget, NOT a real Goa-injected limit. The underlying
trigger — being pushed to wrap up mid-work — is the false-positive consecutive-tool-round
nudge fixed above. (If a real `N/M` budget is ever seen, check the goal turn-budget
injector, which is the only component that emits that exact `N/M` shape.)

**Fix (agent_streaming.go):**
- Added `turnSawContent` / `turnSawThinking` (agent.go), set in `handleTextDelta` /
  `handleThinkingDelta`, reset per turn in `prepareTurn`. `trackToolCallingRound` now
  resets the streak if the model produced a message/thinking **anywhere earlier in the
  turn**, not just the current round — so any message/thinking between tool calls counts.
- Reworked convergence to be **message-driven, not a number**: removed the forced-answer
  nudge and the hard 250-round cap. The turn now runs a recovery (final-answer) stream
  only when `trackToolCallingRound()` reports the model has gone *truly silent* for the
  configured consecutive rounds (no message AND no thinking anywhere in the turn).
  `checkConsecutiveToolRounds`/`trackToolCallingRound` now return the limit-reached bool
  instead of self-resetting + injecting a nudge, so the caller owns convergence.

**Regression tests (all pass, incl. -race):**
- `agent_toolrounds_repro_test.go` (stream-level): `TestConsecutiveToolRounds_ThinkingEachRound_NeverNudges`,
  `_BatchThinking_NeverNudges`, `_ToolThenThinking`, `_ContentMessageEachRound` — all assert
  no convergence while reasoning/messages present, across 20 rounds (> 15).
- `ephemeral_system_test.go` (unit): `TestConsecutiveToolRounds_LimitReported`,
  `TestTrackToolCallingRound_LimitReachedWithoutThinking`, `_LimitNotReachedWithThinking`,
  `_TurnReasoningResetsStreak` (Issue 13 core), `TestMaxConsecutiveToolRounds_CustomThreshold`,
  `_ZeroDisables`.
- `TestAgent_ExecutesTool_Stream` (convergence still terminates on silent loop).

**Status: FIXED.**
12. Issue 12 — model falls back to bash to edit files (large-edit drift). ✅ FIXED + tested
    (+race): NON-BLOCKING bash file-edit hint (tools.bash.warn_file_edits, default ON,
    /config → Bash toggle, persisted) + smarter edit not-found hint + line-match (M/N)
    diagnostic.
13. Issue 13 — "15 consecutive tool-calling rounds" nudge FALSE POSITIVE (thinking
    present, kimi-code k3). ✅ FIXED + tested: turn-level reasoning/message tracking
    (turnSawContent/turnSawThinking) resets the streak; convergence is message-driven
    (forced-answer nudge and hard 250-round cap removed).

---

## Issue 14 — Guardrail nudges are invisible to the user (not shown as TUI bubbles / not in chat history)

**Symptom (user directive):** "make sure nudge appear in the TUI as a bubble — the user
*must* be aware of nudge sent to the model" and "make sure nudge are part of history of
the chat." The system-update note in this very session (*"15 consecutive tool-calling
rounds elapsed…"*) reached the model but the user never saw its content.

**Root cause:** `Agent.InjectEphemeralSystemMessage` (`internal/agentic/agent.go`)
appended the nudge to the *model's* history but emitted only a transient `EventProgress`
("System guardrail: model told to wrap up or adjust behavior.") — a status line with a
GENERIC message, NOT the actual nudge text/numbers, and NOT a persistent chat bubble. The
real content was invisible (and stripped at turn end), so the user could never see what
the model was told. The old comment justified this ("rendering it as a bubble would
confuse the user… the model tends to parrot it as a user-facing 'budget'") — the opposite
of the required transparency.

**Fix:** every host control note (prefixed `[goa-system]`) is now ALSO emitted as an
`EventContent` with `Role: System` + `Metadata{"category":"system-notification"}` — the
same path used for "Error: 401" notices — which the interactive app renders via
`chat.AddSystemMessage` (stats.go:202) as a **durable chat bubble that is part of chat
history**. Applies to all nudge sources: recovery/round-limit, tool-budget, loop
guardrail, premature-stop auto-continue, and goal stall `Remind`.

**Tests (pass):** `ephemeral_system_test.go`:
`TestEphemeralSystemMessage_InModelHistoryAndUserBubble` (nudge in model history AND
user bubble with full text), `TestInjectEphemeralSystemMessage_EmitsVisibleBubble`
(full-text system-notification content event, Role=System),
`TestInjectEphemeralSystemMessage_NoBubbleForNonControl` (non-control messages stay silent).

**Status: FIXED.**
14. Issue 14 — guardrail nudges invisible to user (no TUI bubble / not in chat history).
    ✅ FIXED + tested: nudges now emitted as durable system-notification chat bubbles.

---

## Issue 15 — No automatic retry on HTTP 408 from initial stream request

**Symptom:** a local LLM server returns HTTP 408 "request timeout" before the SSE
stream starts. The TUI shows a friendly connection hint saying *"goa will retry
automatically"*, but no retry happens and the turn ends with a diagnostic bundle.

**Root cause:** `Agent.runStreamRound` (`internal/agentic/agent_streaming.go`) routes
`consumeStream` errors through `handleStreamFailure` (which retries with backoff), but an
error returned directly from `startStreamRound` / `provider.Stream` is returned
immediately as a terminal failure:

```go
stream, err := a.startStreamRound(...)
if err != nil {
    return false, err   // bypasses retry classification
}
```

HTTP 408 and other transient open failures therefore never reach the retry path even
though the user-visible hint promises they will.

**Fix:** route `startStreamRound` errors through the same `handleStreamFailure` path as
mid-stream errors. The function already resets per-round state, classifies retryability,
backs off, and re-opens the stream, so initial-connection failures are handled
identically.

**Test:** `TestAgent_RetriesInitialStreamError_408` (`internal/agentic/agent_test.go`)
registers a provider whose `Stream()` returns an HTTP 408 response error once, then
succeeds. The agent must retry, recover, and emit the successful assistant content.

**Status: FIXED.**
- `internal/agentic/agent_streaming.go`: `runStreamRound` calls `handleStreamFailure` on
  `startStreamRound` errors just like `consumeStream` errors.
- Test `TestAgent_RetriesInitialStreamError_408` passes, proving 408 on the initial
  request is retried. ✔

---

## Issue 16 — gpython embedded `json` module is missing `load`/`dump` helpers

**Symptom:** using the `python` tool to read a JSON file fails:

```python
import json
with open('testdata/altertab3.json') as f:
    data = json.load(f)
```

Error: `AttributeError: "'module' has no attribute 'load'"`.

**Root cause:** the embedded gpython interpreter only exposes the JSON module partially;
`json.loads`/`json.dumps` may be present, but the file-oriented helpers `json.load` and
`json.dump` are missing. The tool is otherwise unable to parse JSON files directly.

**Fix:** extend the gpython JSON bindings to provide `json.load(fp, ...)` and
`json.dump(obj, fp, ...)` as thin wrappers over `json.loads`/`json.dumps` that read from
and write to the supplied file object. Alternatively, expose a complete JSON module that
passes the standard CPython `json` API surface.

**Test:** `python` tool test that runs `json.load` on a temporary JSON file and asserts the
parsed structure matches the source data.

**Status: OPEN.**

---

## Issue 17 — Retry budget must reset after a successful retry

**Directive:** "make sure the retry count is reset after a successful retry" — after a
stream failure is recovered by a retry, a LATER failure (same turn or next turn) must
get a full fresh retry budget (attempts restart at 1/`MaxRetries`), not keep consuming
the previous episode's budget.

**Investigation (current state — structurally correct, but UNVERIFIED by test):**
- Single retry loop: `Agent.retryStream` (`internal/agentic/agent_streaming.go:1268`)
  — the attempt counter is a LOCAL loop variable
  (`for retry := 0; retry < opts.MaxRetries; retry++`), so every `handleStreamFailure`
  invocation starts a fresh budget. No agent field carries a retry count across
  episodes (verified: `agent.go` only has `overflowRecoveryAttempted`, the deliberate
  once-per-turn compress+retry guard, reset per turn).
- No retry loop in the provider protocol/runtime layer: `provider.Stream` returns the
  error to the agent, and classification (`shouldRetryStreamError` /
  `hooks.ErrorContext.IsRetryable`) is stateless per error.
- `opts.MaxRetries` (default 5, `provider/options.go:45`; configurable
  `provider.max_retries`, `provider/manager.go:879`) is read per episode, never
  decremented.

**Gap / risk:** the invariant "recovered stream → next failure gets a full budget" has
NO regression test, and `overflowRecoveryAttempted` shows how easily a retry-adjacent
counter becomes turn-persistent. A future refactor hoisting the loop counter into
agent state would silently degrade recovery: after one recovered failure, later
failures would get fewer/zero retries (TUI would show "Reconnecting (attempt N/5)…"
starting at N>1 and exhaust early).

**Fix:**
- Stream-level regression test: provider fails once → agent retries → stream recovers
  and produces content → provider fails AGAIN in the same turn → assert episode 2
  restarts at attempt 1 and gets the full `MaxRetries` budget (fail its first
  `MaxRetries-1` attempts, succeed on the last; assert recovery). Also assert the
  user-visible progress restarts at "Reconnecting (attempt 1/N)...".
- Cross-turn case: episode 1 in turn 1, episode 2 in turn 2 → same assertion.
- Keep `overflowRecoveryAttempted` once-per-turn (documented exception — bounds
  compression loops; must NOT be reset mid-turn after a successful compress+retry).

**Test:** `internal/agentic/agent_test.go` — `TestAgent_RetryBudgetResetsAfterSuccess`
(same-turn and cross-turn episodes; flaky provider counting `Stream()` calls; assert
full budget per episode and attempt-1 restart).

**Status: OPEN.**

---

## Issue 18 — Runtime fatal errors (e.g. `concurrent map writes`) are not captured anywhere

**Symptom (user report, live TUI):** the process died with
`fatal error: concurrent map writes` mid-session. The Go runtime writes fatal errors
directly to fd 2 and the TUI's alt-screen rendering mangled the stack trace, so the
crash is undiagnosable after the fact. `handleShutdown` (`internal/app/app.go:403`)
only recovers **panics** — a runtime fatal error is NOT recoverable, so nothing
persisted it.

**Root cause:** no crash-capture infrastructure: `Main` sets `log.SetOutput(io.Discard)`
and nothing tees stderr to disk. A fatal error's stack exists only on the terminal.

**Fix:** complete the pending `internal/app/crash_log.go` work:
- `setupCrashLog(projectDir)`: open `.goa/crash.log` (env override `GOA_CRASH_LOG`,
  else project `.goa/crash.log`, else `~/.goa/crash.log`), route the `log` package
  there (replacing `io.Discard`), and **tee fd 2** through a pipe
  (`os.Pipe` + `dup2` + copy goroutine → original stderr AND file) so runtime fatal
  errors land on disk. Platform split: unix `teeStderr` (real tee), other platforms
  `noOpCloser` (log+panic only).
- Wire `setupCrashLog` into `Main()` once (before the `runApp` relaunch loop).
- `handleShutdown`: on recovered panic, also `writeCrashLog(r, debug.Stack())` directly
  to the file (flushed even when `os.Exit` follows).
- TUI renders to stdout (verified `tui/terminal.go`), so teeing stderr is safe.

**Test:** `internal/app/crash_log_test.go` — path resolution (env/project/home),
`log` output lands in file, `writeCrashLog` persists panic text, `teeStderr` captures
an `os.Stderr` write into the file while still forwarding to the original fd, cleanup
restores stderr. Serial test (mutates process fd 2).

**Status: FIXED.**
- `internal/app/crash_log_unix.go` (`//go:build unix`): `teeStderr` — dup fd 2, pipe,
  `unix.Dup2`, copy goroutine → original stderr AND file. Pipe write end made blocking
  (`unix.SetNonblock(false)`) so the runtime's raw write(2) of a large fatal dump is
  not dropped on EAGAIN. Cleanup restores fd 2, drains, closes.
- `internal/app/crash_log_other.go` (`//go:build !unix`): `teeStderr` = `noOpCloser`
  (log + recovered-panic capture only).
- `internal/app/app.go`: `Main` calls `setupCrashLog(wd)` once (deferred BEFORE
  `handleShutdown` so the handler recovers while the file is still open);
  `handleShutdown` writes the panic + stack via `writeCrashLog` before printing.
- Tests: `TestCrashLogPath`, `TestSetupCrashLog_CapturesLogOutput`, `TestWriteCrashLog`,
  `TestTeeStderr` (capture + forwarding + restore). ✔ Live smoke: `goa mcp list`
  through `Main` creates the log with the startup header.

---

## Issue 19 — CRITICAL: `fatal error: concurrent map writes` crash in live TUI session

**Symptom (user report, kimi-code/k3, single-agent coding session):** process aborted
with `fatal error: concurrent map writes` (goroutine 2174) right after a `read` tool
call was dispatched, while other tool widgets/search results were on screen. Two
goroutines wrote the same Go map simultaneously. Stack trace unrecoverable (Issue 18).

**Investigation so far (audit, not yet reproduced):**
- RULED OUT: `agentStreamRegistry.streams` (`internal/app/agent_streams.go:49`) —
  mutex-guarded; and the registry is only non-nil during orchestration (this session
  was single-agent).
- RULED OUT (claimed-single-goroutine): `tooltracker.Tracker.byID/noID`
  (`internal/tooltracker/tracker.go:39`) — documented "not safe for concurrent use;
  the app drives it exclusively from the engine command loop". **Verify the claim**:
  tool-execution goroutines emit `EventToolProgress` — if any progress/result path
  reaches the tracker (or chat viewport) WITHOUT passing through the engine loop's
  serialization, that is the race.
- SUSPECTS: (a) tool-progress events emitted from tool goroutines reaching
  tracker/chat maps off-loop; (b) a lazily-initialized renderer registry map
  (`tui/register_renderers.go`, `tools/*_renderer.go`) written on first concurrent
  use; (c) parallel tool-call execution goroutines sharing a map in
  `internal/agentic`; (d) `tui` viewport/component maps touched by both the render
  loop and an event goroutine.

**Repro / next step:** drive concurrent tool progress + result events through the app
layer under `-race` (filmstrip-style event sequence with ≥2 in-flight tool calls
emitting progress), or stress `tooltracker` + chat viewport from two goroutines to
identify the exact map. The race detector report names the map and both stacks.

**Fix:** serialize the identified shared-map access at the correct layer (engine-loop
handoff for UI mutations, or a mutex for genuinely concurrent structures). Do NOT
paper over with `sync.Map` where a single-owner discipline is the design.

**Test:** the `-race` reproduction must go from RED (race detected) to green after the
fix; keep as a regression test.

**Status: FIXED — root cause found: the LSP `serverClient.versions` map.**

The ToolScheduler executes independent tool calls in PARALLEL goroutines (serialized
only on file-access conflicts). Since Issue 7, every `read`/`edit`/`write` tool
notifies the LSP manager (`touchLSP` → `Manager.DidChange`/`OpenDocument`). Files of
the same language share one `serverClient`, whose `versions` map
(`internal/lsp/manager.go`) was accessed with NO mutex (`DidChange`: read/`++`/read;
`OpenDocument`: write; `ensureOpen`: read) — `Manager.mu` only guards the client
registry. Two parallel `read` calls on Go files (exactly the crash transcript:
`tui/goal/tool_renderers.go` + `tui/goal/panel_test.go`, go.mod project → gopls
active) did `c.versions[uri]++` on the same map → `fatal error: concurrent map
writes`.

Reproduced RED with `go test -race`: `TestManager_ConcurrentDidChange` fires the
detector at `manager.go` versions write/read instantly (pre-fix).

**Fix (`internal/lsp/manager.go`):** `serverClient` gains `mu sync.Mutex`; new
helpers `notifyOpen`/`notifyChange`/`didChangeLocked`/`isOpen` hold it across BOTH
version bookkeeping AND the RPC send, so wire order matches version order (the
JSON-RPC client already serializes writes via writeMu — no added contention). A
concurrent opener of the same uri now degrades to a full-content `didChange` instead
of a protocol-violating duplicate `didOpen`. `OpenDocument`/`DidChange`/`ensureOpen`
are thin wrappers. `Diagnostics` (separate type) was already RWMutex-guarded.

**Tests (all pass, `-race`):** `internal/lsp/manager_concurrent_test.go`:
`TestManager_ConcurrentDidChange` (8 goroutines × 50 ops on one server),
`TestManager_ConcurrentOpenSameFileSingleDidOpen` (16 racers, exactly 1 didOpen),
`TestManager_ConcurrentOpenAndChange` (mixed open/change/position requests). Full
`internal/lsp`, `tools` suites green with `-race`.

---

## Issue 20 — Tool widget's LAST line loses its status background when the widget sits directly above the spinner

**Symptom (user report):** when a tool call renders at the END of the console — right
over the spinner — the **last line** of the widget does not get the correct background
color (e.g. `tool_success_bg` green after success). Observed with the `read` tool.

**Investigation so far (widget side RULED OUT):**
- The widget paints every row — header, body, and the blank bottom-pad row — with the
  status background: `toolBox.build` (`tui/tool_execution.go:158`) ends with a
  bottom-pad `bgLine("", width)`, and `padToWidthStyled` (`tui/utils.go:111`) returns
  `bgAnsi + padded + ansi.Reset` (full-width fill, nested-reset re-apply). Empty
  content still yields a fully bg-colored row. So the missing bg is introduced
  DOWNSTREAM of the widget: compositor or chat-viewport row handling.
- Positional clue ("right over the spinner"): the affected row is the widget's last
  row = the bottom-pad row of bg-colored SPACES, adjacent to the footer/spinner rows.

**Hypotheses (to discriminate by reproduction):**
1. **Differential repaint skips bg-only changes on blank rows.** If the compositor's
   row-diff keys on *visible text* (all spaces ⇒ "unchanged"), the bottom-pad row is
   not repainted when the status change flips only its background (pending→success),
   leaving the stale/default bg.
2. **Off-by-one clobber when the spinner/footer row repaints.** The spinner ticks
   constantly; footer redraw paths (`tui/footer_render.go`, `tui/status.go`,
   compositor `\x1b[2K` row rewrites at `tui/compositor.go:669/720/730/893/954/990/
   1015`) may rewrite the adjacent chat row from a stale or unstyled line buffer.
3. **Erase-without-BCE:** `\x1b[2K` erases to default bg on terminals without
   back-color-erase; if the row's content rewrite is skipped (hypothesis 1), the
   erase leaves default bg visible.

**Repro (tui-test filmstrip):** drive a `read` tool call → result (success) as the
LAST chat entry with the spinner active below; assert the final rendered frame's row
for the widget's last line carries `tool_success_bg` across the full width. Run the
same at pending→success transition with a spinner tick between.

**Fix:** depends on the winning hypothesis — (1) include style in the row-diff key for
blank rows (or never skip bg-carrying rows on style change), (2) fix the footer/chat
row index arithmetic, (3) rewrite content after every `2K` on bce-less terminals.

**Test:** filmstrip regression asserting full-width status bg on the widget's last row
when the spinner is active beneath it.

**Status: OPEN.**