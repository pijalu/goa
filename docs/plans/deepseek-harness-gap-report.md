# Gap Report — goa vs deepseek-harness

Bilateral feature comparison of **goa** (`/Users/muaddib/dev/goa`, Go module
`github.com/pijalu/goa`) against **deepseek-harness** (`/Users/muaddib/dev/deepseek-harness`,
DeepSeek AI's `dsh` agent harness, TypeScript/Cordis plugin monorepo).

**Axes:** caching · tooling · context management · DeepSeek-model-specific features.

**Companion doc:** dsh already ships the *mirror* analysis at
`deepseek-harness/docs/gap-analysis/{README,gap-matrix,action-plan}.md` (gaps in
*dsh* vs goa). This report is the other direction: **what goa is missing**, with
file/line evidence from both repos, plus a modular close-out plan.

**Severity scale:** Critical = correctness/cost defect · High = material capability
missing · Medium = useful hardening/tunability · Low = polish/observability.

> Note on existing parity: goa already matches or beats dsh on cache-miss
> forensics (`internal/agentic/provider/cache_forensics.go:17-33`), cache-hit
> trend surfacing (`internal/app/stats.go:62-111`), layered compression
> thresholds (`internal/agentic/compression_thresholds.go:6-44`), five named
> compression strategies (`internal/agentic/agent.go:455-479`), silent-overflow
> detection (`internal/agentic/agent_compression.go:768`), and DeepSeek
> peak-hour pricing (`internal/agentic/provider/schema/catalog.go:225-229`).
> Those are **not** repeated as gaps below.

---

## 1. Gap matrix

### 1.1 Caching

| # | Gap | goa (evidence) | dsh (evidence) | Severity |
|---|-----|----------------|----------------|----------|
| CA1 | ~~**Compaction summarize call does not reuse the warm KV prefix.**~~ **FIXED (P1).** Goa's `summarizeHistory` used to swap in a *different* system prompt and drop tools, so the summarize request missed the provider prefix cache from token 0 — a full cold-cache call over the largest history of the session. It now replays the conversation prefix (system prompt + tools + history) via the shared `buildProviderContext` and appends the instruction as the final user message. | `internal/agentic/agent_compression.go` (`summarizeInstruction`, `summarizeHistory`); wire proof in `TestSummarizeRequestIsStrictAppendOfConversation` | `packages/compaction/compaction-basic/src/summarizer.ts:21-58` — same design: replay verbatim, instruction as final user message | ~~High~~ **Closed** |
| CA2 | **No purpose attribution on auxiliary LLM calls.** Compaction / title-generation traffic is indistinguishable from conversation traffic on the provider side. | No `purpose` field on the summarize request (`agent_compression.go:191-205`); no purpose headers anywhere in `internal/agentic/provider/` | `GenerateOptions.purpose: 'compaction'` → header `x-deepseek-harness-compact: 1` (`packages/llm/llm-deepseek/README.md` §App attribution); `purpose: 'session-title'` also forces thinking off to reserve output budget | **Low** |
| CA3 | **No request/session correlation headers.** Provider-side debugging and abuse-correlation have no stable anonymous user id / session id on requests. | Only a generic `User-Agent: goa (<os>)` in `internal/agentic/provider/hooks/auth.go:126` | `x-deepseek-harness-user-id` (stable anonymous id) + `x-deepseek-harness-session-id` per request (`packages/llm/llm-deepseek/README.md` §App attribution; `packages/identity/anonymous-user-id`) | **Low** |
| CA4 | ~~**No `llm/stream`-style interception seam around model calls.**~~ **FIXED (P12).** Goa now exposes a `StreamInterceptor` waterfall (`StreamRequest`/`StreamHandler` in `internal/agentic/provider/stream.go`): each wrapper sees/modifies the resolved request (URL, headers, body) and may observe/wrap the event stream before the transport executes. Cache forensics and the `OnResponse` metrics callback are re-implemented as interceptors; `RegisterStreamInterceptor` is the extension point for further consumers (purpose headers, CA2/CA3). | `internal/agentic/provider/stream.go` (chain + registry), `runtime.go` (`streamWithInterceptors`/terminal handler), `internal/agentic/provider/cache_forensics.go` + `interceptors.go` (consumers); wire proof in `TestStreamInterceptorObservesAndTagsCall` and parity in `TestCacheForensicsEndToEnd` / `TestMetricsInterceptorPreservesOnResponse` | `ctx.llm.stream` + `llm/stream` waterfall event (`packages/llm/llm/README.md` §Events) — explicitly documented as the mount point "for caching, logging, or routing" | ~~Medium~~ **Closed** |

### 1.2 DeepSeek-model-specific

| # | Gap | goa (evidence) | dsh (evidence) | Severity |
|---|-----|----------------|----------------|----------|
| DS1 | **Reasoning passback wastes tokens on tool-call-free turns.** Goa writes `reasoning_content` (full thinking text) onto *every* assistant message whenever the DeepSeek compat flag is on, and forces `reasoning_content:""` when absent. The DeepSeek API only *requires* passback on tool-call turns; on plain turns it is ignored — pure token cost, paid on every request. | `internal/agentic/provider/openai/convert.go:128-161` (unconditional `result["reasoning_content"] = block.Thinking` for `ContentBlockThinking`, then forced `""`), flag from `internal/agentic/provider/compat_detect.go:90-95,116` | `packages/llm/llm-deepseek/src/serialize.ts:96-100` — passback **only on assistant turns with tool calls**; "ignored on plain turns, so we drop it there to save tokens" (README §Wire-format notes) | **Medium** |
| DS2 | **No per-model default output-cap resolution (`defaultMaxTokens`).** dsh materializes an adapter-owned per-model output cap (DeepSeek default 256k) into `max_tokens` when the request doesn't set one, so the wire request is always explicit and reconstructable. Goa only decides *which field name* to use (`max_tokens` vs `max_completion_tokens`). | `internal/agentic/provider/compat_detect.go:103-106` (`useMaxTokens`), no per-model output default in `schema/catalog.go` | `packages/llm/llm-deepseek/src/adapter.ts:91-93` (`DEFAULT_CONTEXT_WINDOW=1_000_000`, `DEFAULT_MAX_TOKENS=256_000`), `resolveModelInfo`/`prepareCall` materialization (`packages/llm/llm/README.md` §Public API) | **Low** |
| DS3 | **No live endpoint model discovery.** Goa resolves model metadata from the models.dev snapshot/cache only; there is no "ask this endpoint what it serves" path (`GET /models`), so private/self-hosted DeepSeek-compatible gateways can't be browsed. | `internal/agentic/provider/models/modelsdev.go:27-43` (models.dev API + cache), no `/v1/models` interrogation | `ctx.llm.registerModelDiscovery` / `discoverModels(settingsNs, request)` — interrogates a draft endpoint with a one-shot credential, never stored (`packages/llm/llm/README.md` §Service) | **Medium** |
| DS4 | **No per-provider owned retry policy.** Goa has one global `max_retries` and hard-coded classification; no per-provider policy (mode/codes/backoff), no `always` mode, and no durable retry events a UI can render. | `config/config.go:148` (`max_retries`), `internal/agentic/retry_classify.go` (fixed classification, 30s Retry-After cap) | Per-adapter `retryPolicy{mode: normal\|always, backoff{…}}` captured at route registration; durable `llm/retry` / `llm/retry-started` events with retryId/policy key (`packages/llm/llm-retry/README.md`); provider `Retry-After` honored when ≤ cap | **Medium** |
| DS5 | **No credential format validation / per-request resolution.** Goa reads env keys via `GetEnvAPIKey` but never validates header-legality; a malformed key surfaces as an opaque transport error. dsh validates every resolved key (`INVALID_CREDENTIAL` naming the entry point, never the key) and re-resolves credentials per stream call through a settings/credentials seam. | `internal/agentic/provider/env_keys.go:16-26` (plain `os.Getenv`), no validation | `packages/llm/llm/src/api-key.ts` (`normalizeApiKey`, printable-ASCII check), `MISSING_CREDENTIAL` vs `INVALID_CREDENTIAL` taxonomy; per-request credential re-resolution (`packages/llm/llm-deepseek/README.md` §Dynamic configuration) | **Low** |
| DS6 | **No live settings reload.** Goa config is read at startup (embedded → home → project → local → env → flags); no file watcher, so changing the DeepSeek profile (thinking lock, effort, catalog) needs a restart. | `config/` loader — no fsnotify/watch anywhere under `config/` | `packages/settings/settings-file/README.md` — watched YAML/JSON doc, hot-publish with last-good retention, atomic locked write-back, leaf-level YAML diffs preserving comments | **Low** |

### 1.3 Context management

| # | Gap | goa (evidence) | dsh (evidence) | Severity |
|---|-----|----------------|----------------|----------|
| CX1 | **No pre-compaction tool-result pruner.** Oversized tool results sit in the window at full size until a compaction strategy fires. dsh prunes them *model-free* as soon as pressure qualifies: bounded head + fixed marker + bounded tail, original kept in the durable log. | Only context-level elision during compaction (`elidedToolResultContent`, `agent_compression.go:24-28`); bash truncation at acquisition (`tools/common/truncate.go`) | `packages/compaction/compaction-tool-result-pruner/` (`thresholdChars:8192, headChars:4096, tailChars:1024`, `[... tool result middle pruned ...]`); wired into pressure/overflow path in `compaction-basic` before range selection | **Medium** |
| CX2 | **No generic tool-result spill policy.** Goa's `SaveTruncatedOutput` writes truncated bash output to an unscoped `os.CreateTemp` file with no retrieval guidance. dsh's spill policy caps *any* oversized plain-text tool result: saves the full text to a session-scoped store and returns a budgeted head/tail preview + locator + retrieval hint ("Use read with offset/limit, or grep this path"), never exceeding the inline cap. | `tools/common/truncate.go:178-189` (temp file, no owner scoping, no hint text) | `packages/spill/spill-policy/README.md` (`maxInlineBytes`, notice reserved from budget, best-effort fallback), `packages/spill/spill-local` (session-scoped files), plus a dispatch-log arm for sub-call artifacts | **Medium** |
| CX3 | **Unstructured compaction summary.** Goa's summarize prompt is one line ("preserving key facts, decisions, and context") with no section contract; the replacement history is `[user: prompt][assistant: summary]`. dsh uses a fixed 8-section checkpoint (Primary Request / Key Concepts / Files & Code / Errors & Fixes / Pending Jobs / Current Work / Next Step / Critical Context), framed in `<compacted-summary>` tags, merging the prior checkpoint on later cycles. | `internal/agentic/agent_compression.go:14-19` (`compactSummaryRequestPrompt`) | `packages/compaction/compaction-basic/src/summarizer.ts:21-58` (`COMPACTION_INSTRUCTION`), framing in `packages/compaction/compaction/src/checkpoint.ts:22-52` | **Medium** |
| CX4 | **No typed compaction provenance.** Goa emits a compaction *result* event but not a durable transaction: dsh brackets every compaction with log-only `compaction/start`→`compaction/summary`→(surface replace)→`compaction/end`, carrying a branded `CompactionId`, shadowed range/seqs/token counts, and the summarization call envelope. A crash mid-compaction leaves a *detectable* orphan lock. | `internal/agentic/agent_compaction_event_test.go` (event exists, no transaction id / shadow accounting) | `packages/compaction/compaction/src/types.ts`, `checkpoint.ts:41-52` (`compactCheckpointSource`), surface contract in `packages/compaction/compaction/README.md` §Surface contract | **Medium** |
| CX5 | **Workspace instructions are startup-only.** Goa loads `AGENTS.md`/`CLAUDE.md` once into the system prompt; nested files discovered mid-session, edits, and deletions are never noticed. dsh injects the baseline as durable user messages, then watches successful read/write/edit tool calls to queue *addition / replacement / removal* notices for newly reached or changed scopes. | `internal/context.go:33-88` (load-time ancestor walk), `internal/app/prompt.go:227-229` (rendered into system prompt, static) | `packages/context/agent-instructions/README.md` — per-scope lifecycle, `<system-reminder>` framing with escaping, symlink-aware reads, dedup of byte-identical siblings | **Medium** |
| CX6 | **No durable temporal context.** The model gets no per-step zoned timestamp / elapsed-time message, so time-relative reasoning ("in 10 minutes", "since my last message") is unsupported. | Absent (no time-context injection found under `internal/agentic/`) | `packages/context/time-context/README.md` — per-step ISO timestamp + elapsed, browser-zone aware, refresh interval, survives compaction via durable-event scanning | **Low** |
| CX7 | **No cross-session reference snapshots.** Goa has a live agent bus (`comm.go`) but no way to *mention* another session and inject a bounded, read-only snapshot of its conversation as sourced context. | `internal/agentic/comm.go:18-115` (live message bus only); no `session_search`/reference tooling (grep-confirmed absent) | `packages/context/session-reference/README.md` — `@[label](dsh-session:…)` mentions, `maxReferences:3`, `maxReferenceBytes:65536`, untrusted-content warning framing, checkpoint-aware projection | **Low** |
| CX8 | **Context pressure is estimate-only.** Goa's `computeContextStats` uses local heuristics (chars/token) plus last *provider-reported* usage, but there is no *projected* next-request cost that reprices surface deltas against the last provider sample — dsh's token-meter projects the next request's prompt cost (anchored to provider usage, folded over surface changes) and a composition breakdown (system/tools/messages). | `internal/agentic/agent_context_stats.go:92-145` (`estimateTokens`, 4 chars/token) | `packages/llm/token-meter/src/{usage-projection,projection,surface-fold,breakdown-projection}.ts` (README §tokenUsage/contextPressure/projectedTokens/contextBreakdown) | **Low** |

### 1.4 Tooling

| # | Gap | goa (evidence) | dsh (evidence) | Severity |
|---|-----|----------------|----------------|----------|
| TL1 | **No session-query/self-introspection tools.** The model cannot search or read its own (or another) session's event history — no `session_event_read/search/trace`, no `session_search`. Useful for "what did we decide last week?" workflows. | Absent (grep-confirmed: no `session_search`/`session_event*` under `tools/` or `internal/`) | `packages/session-query/tool-session-query` — `session_event_read`, `session_event_search` (SQLite FTS), `session_event_trace`, `session_search`, `session_trace` (docs/tool-catalog.md:1238-1473) | **Medium** |
| TL2 | **No user-facing scheduler tools.** Goa has internal schedulers (`internal/app/dream_scheduler.go`) but no model-facing `schedule_create/delete/list` with recurrence. | Absent in `tools/` | `packages/schedule/schedule/src/{tools,runtime,recurrence}.ts` — `schedule_create/delete/list` (docs/tool-catalog.md:1037-1135) | **Low** |
| TL3 | **No sandbox-escalation surface on the bash tool.** Goa's bash schema is `command/timeout/workdir/env`; confinement is deployment-fixed (`tools/bash_jail.go` static analysis + `internal/sandbox` hardening). dsh's tool advertises `sandbox_permissions` + `justification` **only when** the mounted executor sandboxes, turning a denial into an approval-driven one-shot widening. | `tools/bash.go:120-139` (schema), `internal/sandbox/manager.go:1-13` (HOME/TMPDIR repoint, rlimits, blocklist — no modes) | `packages/shell/tool-bash/README.md` §Tools (`sandbox_permissions`, `justification`, capability-gated), executor seam in `packages/shell/bash-sandbox/README.md` (read-only / workspace-write / danger-full-access, denial-as-result-fact) | **Medium** |
| TL4 | ~~**No hook-dialect compatibility layer.**~~ **FIXED (P17).** Goa's hooks are its own 4-event format (`beforeTool/afterTool/sessionStart/sessionEnd`). Users with an existing Claude Code `hooks.json` (or Codex hooks) must rewrite. dsh runs the CC/Codex command-hook subsets on its canonical interception points, with env substitution and most-restrictive merge. | `internal/hooks/hooks.go:22-42` (goa-native events only) | `packages/hooks/hooks-claude-code/README.md`, `packages/hooks/hooks-codex/`, shared `packages/hooks/hook-protocol/` | **Low** |
| TL5 | **No skill invocation-policy split.** Goa skills are either runnable (`run_skill`) or knowledge docs; there is no typed `{modelInvocable, userInvocable}` policy, so one catalog can't serve model tools and user-only commands independently. | `skills/loader.go`, `core/commands` registration — no invocation policy fields (grep-confirmed) | `packages/skill/skill/README.md` §Invocation policy (4-combination typed policy, layered registry, `skills/change` invalidation) | **Low** |
| TL6 | **Persistent-terminal tool suite is partial.** Goa has `pty_exec` (start/write/read/stop) and an allow-listed `terminal` tool; dsh ships a full suite (`terminal_open/close/list/read/send/signal`) over a generic terminals service, plus a persistent-shell variant of bash. | `tools/pty_exec.go`, `tools/terminal.go` (allow-list model, `tools/terminal_safety.go`) | `docs/tool-catalog.md:778-944` (`@deepseek-ai/dsh-tool-terminal`), `packages/shell/tool-bash-persistent/` | **Low** |
| TL7 | **No `run_code`-style code-mode dispatch** (model writes one program that fans out to many tool sub-calls in a sandboxed worker, with durable per-sub-call dispatch log). Goa's `python` tool is a single embedded interpreter, not a tool-dispatch fabric. | `tools/python.go` (gpython jail) | `docs/tool-catalog.md:119-150` (`run_code`), dispatch-log spill arm in `packages/spill/spill-policy/README.md` | **Low** |

---

## 2. What goa should NOT copy

- **The Cordis "everything is a plugin" runtime.** dsh's service-definition/provider/consumer
  split is its core architecture; goa's flat `agentic.Tool` registry +
  `init()`-registered commands is simpler and fine. Adopt the *capabilities*,
  not the container.
- **dsh's compaction surface-replacement event model wholesale.** Goa's history is a
  simpler in-memory slice; only the provenance fields (CX4) are worth porting.
- **`models: []` advisory catalogs with pass-through.** Goa already has a compiled
  catalog + models.dev cache + prefix overrides (`models/registry.go:177-199`),
  which is strictly richer for known providers.

---

## 3. Action plan (modular — pick/tune per item)

Each item is **independently actionable**: scope, goa files to touch, dsh reference
to mine, effort (S < 1d · M 1–3d · L 3–5d+), and acceptance criteria. Nothing below
has a hard dependency on another item; suggested order is by severity.

### P1 — Cache-warm compaction summarization (CA1) — HIGH · effort M — ✅ DONE

**Status:** implemented and verified (2026-08-15). `summarizeHistory` now builds the
request via the shared `buildProviderContext` — the conversation's own system prompt,
registered tool schemas, and the standard migrated history — and appends
`summarizeInstruction` as the final user message
(`internal/agentic/agent_compression.go`, `summarizeInstruction` + `summarizeHistory`).
Empty text-less replies (e.g. model answers with a tool call only, now possible since
tools ride the request) fail with an error instead of wiping history.

**Verification (no live model):**
- `TestSummarizeHistoryReusesConversationPrefix` (`internal/agentic/agent_compression_test.go`)
  — request unit test: system-prompt identity, tools-array parity, history prefix +
  exactly one appended user instruction.
- `TestSummarizeHistoryEmptyOnToolOnlyReply` (same file) — tool-only reply ⇒ error.
- `TestSummarizeRequestIsStrictAppendOfConversation`
  (`internal/agentic/provider/protocol/openai_completions_test.go`) — **wire-level**
  proof on the DeepSeek route via `BuildRequest`: system + tools + all history
  messages serialize byte-identical to the conversation request; only the trailing
  user message differs. No network involved.
- Existing overflow-retry count assertions updated for the +1 instruction message
  (`compact_overflow_test.go`, `compact_micro_optional_test.go`).
- Gate: `go vet ./...`, full `go test ./...`, and `-race -cover` on the two touched
  packages all green; `gocognit` 13 ≤ 15 budget for `summarizeHistory`.

**Residual risk:** a model may legitimately answer the instruction with a tool call
(first round yields no text) — the new empty-summary error makes Compact retry/abort
visibly instead of silently storing a blank summary; if providers prove chatty, a
follow-up could strip tools after the first empty round.

**Original change description (for the record):** `summarizeHistory` keeps the
conversation's *own* system prompt and tool schemas as the request prefix and appends
the summarize instruction as the final user message, instead of swapping in a
summarizer system prompt. **dsh reference:**
`packages/compaction/compaction-basic/src/summarizer.ts:21-58`. **Acceptance:** unit
test asserts system prompt identity between last conversation request and summarize
request.

### P2 — Reasoning passback only on tool-call turns (DS1) — MEDIUM · effort S

**Change:** in `convertAssistantMessage`, emit `reasoning_content` only when the
message carries ≥1 tool call; on plain assistant turns drop it (keep the forced
`""` *only* on tool-call turns when thinking is absent — the API requires the field
there, per dsh's null-bricking guard).
**Files:** `internal/agentic/provider/openai/convert.go:128-161`.
**dsh reference:** `packages/llm/llm-deepseek/src/serialize.ts:85-101`.
**Acceptance:** serialized plain-turn assistant messages carry no `reasoning_content`;
tool-call turns always carry it; existing DeepSeek thinking-mode e2e still passes
(`internal/agentic/provider/protocol/openai_completions_test.go`).

### P3 — Tool-result spill policy (CX2) — MEDIUM · effort M

**Change:** a post-execution policy: any final plain-text tool result over
`max_inline_bytes` (new config) is saved verbatim to a session-scoped spill dir
(`~/.goa/spill/<session>/`) and replaced by a budgeted head/tail preview +
`(Omitted N bytes. Full result stored at: <path>. Use read with offset/limit…)`,
with the notice cost reserved out of the budget; storage failure = keep original.
**Files:** new `tools/spill_policy.go` + hook into the tool-result path in
`internal/agentic/agent_tools.go`; generalize `tools/common/truncate.go:178`
(`SaveTruncatedOutput`) into a session-scoped store.
**dsh reference:** `packages/spill/spill-policy/README.md` (rules: skip nested/`read`,
never exceed cap, best-effort).
**Acceptance:** over-cap `bash` result → model-facing content ≤ cap, file exists under
session dir; `read` results and error results never spilled; failure to save leaves
the original inline.

### P4 — Pre-compaction tool-result pruner (CX1) — MEDIUM · effort M

**Change:** before summarize range selection, rewrite over-budget historical tool
results in place: head 4096 chars + `[... tool result middle pruned ...]` + tail 1024
(defaults; configurable). Idempotent (second pass emits nothing).
**Files:** `internal/agentic/agent_compression.go` (new pass ahead of
`summarizeHistory`), config in `config/config.go` alongside `MicroCompactionConfig`
(`internal/agentic/compaction.go:20-45`).
**dsh reference:** `packages/compaction/compaction-tool-result-pruner/README.md`.
**Acceptance:** pruned result ≤ threshold and < original; tool-call/result pairing
intact (callId untouched); compaction re-measures after pruning and skips summarize
when pressure resolves.

### P5 — Structured compaction checkpoint (CX3) — MEDIUM · effort S–M

**Change:** replace the one-line summarize instruction with an 8-section Markdown
contract (Primary Request / Key Concepts / Files & Code / Errors & Fixes / Pending
Jobs / Current Work / Next Step / Critical Context); wrap the resulting checkpoint in
`<compacted-summary>` tags; on later compactions, feed the previous checkpoint into
the summarize input.
**Files:** `internal/agentic/agent_compression.go:14-19` (prompt const),
`:77-95` (history replacement framing).
**dsh reference:** `packages/compaction/compaction-basic/src/summarizer.ts:21-58`.
**Acceptance:** snapshot test asserts all 8 headers in the instruction; re-compaction
includes the prior checkpoint; role-sequence validity preserved (DeepSeek
assistant-first regression stays covered).

### P6 — Compaction provenance events (CX4) — MEDIUM · effort M

**Change:** emit a durable transaction: `compaction_start` (with id) →
`compaction_summary` (shadowed message range, freed tokens, provider/model, usage) →
`compaction_end`. On crash, a start without end is detectable on next boot.
**Files:** `internal/agentic/agent_compression.go`, event types near
`agent_compaction_event_test.go`; wire into session log if persistence is enabled.
**dsh reference:** `packages/compaction/compaction/src/types.ts`,
`checkpoint.ts:41-52`.
**Acceptance:** every compaction emits the triple in order with a shared id; orphan
detection test (kill between start/end).

### P7 — Workspace-instruction lifecycle (CX5) — MEDIUM · effort M

**Change:** after successful `readfile`/`writefile`/`editfile`/`fuzzyedit` calls,
check for newly reachable nested `AGENTS.md`/`CLAUDE.md` scopes and changes/removals
of loaded ones; inject durable user messages (`Additional instructions from…`,
`Updated instructions from…`, `Instructions removed:…`) instead of relying on the
static system-prompt baseline.
**Files:** `internal/agentic/agent_tools.go` (post-tool hook),
`internal/context.go` (scope tracking), `internal/app/prompt.go:227` (keep baseline).
**dsh reference:** `packages/context/agent-instructions/README.md` (prompt shapes,
dedup, symlink rules).
**Acceptance:** creating `pkg/sub/AGENTS.md` mid-session via `writefile` surfaces an
"Additional instructions" message next step; editing a loaded file surfaces "Updated";
deleting surfaces "removed"; byte-identical `CLAUDE.md`/`AGENTS.md` siblings load once.

### P8 — Per-provider retry policy (DS4) — MEDIUM · effort M

**Change:** add `retry_policy {mode: normal|always, max_retries, backoff{initial_ms,
max_ms, jitter}, codes[]}` to provider config; resolve per route at provider
construction; emit agent-log events per scheduled/started attempt.
**Files:** `config/config.go:148` (extend), `internal/agentic/retry_classify.go`,
`internal/agentic/agent_streaming.go:1677-1733` (retry loop), catalog plumbing in
`internal/agentic/provider/schema/catalog.go`.
**dsh reference:** `packages/llm/llm-retry/README.md`.
**Acceptance:** per-provider override beats global `max_retries`; `always` mode retries
every model-request failure until cancel; events visible in `goa.log`.

### P9 — Model discovery from live endpoints (DS3) — MEDIUM · effort M

**Change:** `GET {baseURL}/models` interrogation for OpenAI-compatible providers,
merged with the models.dev cache (live wins), used by `goa model` pickers; one-shot
credential use, never persisted.
**Files:** `internal/agentic/provider/models/registry.go`,
`provider/model_cache.go`, `core/commands/model.go`.
**dsh reference:** `packages/llm/llm/README.md` (`discoverModels` semantics).
**Acceptance:** against a local LM Studio/Ollama the picker lists server-reported
models; unreachable endpoint falls back to cache with a warning; no key written to
disk.

### P10 — Session-query tools (TL1) — MEDIUM · effort L

**Change:** model-facing `session_search` (FTS over persisted session logs) +
`session_event_read` (bounded window read) tools over goa's session store.
**Files:** new `tools/session_query.go`; storage backend in `internal/agentic`
session persistence (check `~/.goa` session format first).
**dsh reference:** `packages/session-query/tool-session-query/`, docs/tool-catalog.md:1238-1473.
**Acceptance:** model can find and quote a prior session's decision; results bounded
(lines/bytes caps); self-session reads allowed, other sessions read-only.

### P11 — Sandbox escalation surface (TL3) — MEDIUM · effort L

**Change:** add optional `sandbox_permissions` (enum: `workspace-write`,
`danger-full-access`) + required-with `justification` to the bash schema, advertised
only when confinement is active; denial → model may retry once wider **after** user
approval through `internal/perms`.
**Files:** `tools/bash.go:120-139`, `tools/bash_jail.go`, `internal/sandbox/manager.go`
(introduce mode vocabulary), `internal/perms/engine.go` (approval path).
**dsh reference:** `packages/shell/tool-bash/README.md`, `packages/shell/bash-sandbox/README.md`.
**Acceptance:** non-sandboxed builds hide the fields; a denied write can be retried
wider only post-approval; non-widening requests fail without prompting.

### P12 — LLM-call interception seam (CA4) — MEDIUM · effort M — ✅ DONE

**Status:** implemented and verified (2026-08-15). The `StreamInterceptor`
chain lives in `internal/agentic/provider/stream.go` — `StreamRequest` (resolved
model/context/options/profile/headers/body/URL) is handed down a waterfall of
`StreamHandler` wrappers (`ApplyStreamInterceptors`), with a canonical chain
(`RegisterStreamInterceptor`/`StreamInterceptors`) applied inside
`GenericStream` (`runtime.go`, `streamWithInterceptors`). The terminal handler
runs the transport and parses the response; interceptors see/modify the request
before the transport executes and may observe/wrap the returned event stream.
The historical ad-hoc wraps are re-implemented as consumers:
`CacheForensicsInterceptor` (`cache_forensics.go`, moved out of
`executeRequest`) and `MetricsInterceptor` (`interceptors.go`, preserves the
`StreamOptions.OnResponse` status/headers callback via
`StreamRequest.OnResponse`). The spec's `hooks/` file was not changed: the
interceptor types and consumers must live in the provider package (provider
imports hooks, so hooks cannot reference `provider.StreamInterceptor` — same
adaptation note as P11's file placement). Purpose headers are the next consumer
(P13, CA2/CA3) and are demonstrated by the header-tagging acceptance test.

**Verification (no live model):**
- `TestApplyStreamInterceptorsOrder` — waterfall semantics (first = outermost).
- `TestStreamInterceptorObservesAndTagsCall` — **acceptance**: a test
  interceptor sees the resolved model/URL/wire body and tags the call with a
  header that reaches the transport.
- `TestStreamInterceptorObservesEventStream` — an interceptor reads terminal
  usage from the wrapped stream.
- `TestCacheForensicsEndToEnd` (existing, unchanged) — forensics parity through
  the canonical chain.
- `TestMetricsInterceptorPreservesOnResponse` / `...FiresOnErrorStatus` —
  metrics parity: the `OnResponse` callback fires with status/headers on
  success and HTTP-error paths.
- `TestRegisterStreamInterceptorAppendsToCanonicalChain`,
  `TestStreamInterceptorsSnapshotIsIsolated` — registry semantics.
- Gate: `go vet ./...` clean; `go test -count=1 ./...` green;
  `-race -cover` on the provider package 85.7%; `gocognit`/`gocyclo` within
  budget on all touched files.

### P13 — Purpose attribution + request correlation headers (CA2, CA3) — LOW · effort S

**Change:** add `Purpose` to `StreamOptions` (`compaction`, `session-title`,
`conversation`); DeepSeek-compat routes emit `x-goa-compact: 1` on compaction calls;
all provider calls emit `x-goa-user-id` (stable anonymous id from `internal/idgen`)
and `x-goa-session-id`.
**Files:** `internal/agentic/provider/options.go`,
`internal/agentic/provider/protocol/openai_completions.go` (header write),
`internal/agentic/agent_compression.go` (set purpose), `internal/idgen.go`.
**dsh reference:** `packages/llm/llm-deepseek/README.md` §App attribution.
**Acceptance:** headers verified in protocol tests; no id inside request bodies;
purpose=session-title forces thinking off (mirrors DS-thinking lock).

### P14 — Temporal context injection (CX6) — LOW · effort S

**Change:** per-turn durable context message with zoned timestamp + elapsed-since-last
-message, with a refresh interval; off by default or wired to scheduling features.
**Files:** new small context contributor in `internal/agentic/agent_context.go`
(step-preparation path).
**dsh reference:** `packages/context/time-context/README.md`.
**Acceptance:** message shape snapshot-tested; interval suppression works across
compaction (scan history for latest injection).

### P15 — Credential validation + per-request resolution (DS5) — LOW · effort S

**Change:** validate every resolved API key (non-empty printable ASCII, no space)
before first use; distinct `MissingCredential` vs `InvalidCredential` tool/user errors
naming the config entry point (never the key).
**Files:** `internal/agentic/provider/env_keys.go:16-26`,
`internal/agentic/provider/hooks/auth.go`.
**dsh reference:** `packages/llm/llm/src/api-key.ts`.
**Acceptance:** malformed key → clear `InvalidCredential` error before network I/O;
missing key → `MissingCredential` listing every env var/config source checked.

### P16 — Skill invocation policy (TL5) — LOW · effort S

**Change:** add `model_invocable` / `user_invocable` (default both true) to skill
front-matter; `run_skill` tool catalog filters model-invocable, TUI skill menu filters
user-invocable.
**Files:** `skills/loader.go`, `tools/registry.go`, TUI skill listing.
**dsh reference:** `packages/skill/skill/README.md` §Invocation policy.
**Acceptance:** a `user_invocable:false` skill never appears in the model's tool
schema; `model_invocable:false` still runs from the UI.

### P17 — Hook-dialect compatibility (TL4) — LOW · effort M — ✅ DONE

**Change:** accept a Claude Code `hooks.json` (`PreToolUse`/`PostToolUse`/
`SessionStart`/`SessionEnd` command hooks) as an additional config source, translating
payloads onto goa's `internal/hooks` events with most-restrictive merge.
**Files:** `internal/hooks/{config.go,hooks.go,store.go}`.
**dsh reference:** `packages/hooks/hooks-claude-code/README.md`.
**Acceptance:** a fixture CC hooks.json vetoes a bash call (exit 2) and its
`additionalContext` reaches the model; goa-native and CC hooks compose.

### P18 — Scheduler tools (TL2) — LOW · effort M

**Change:** expose `schedule_create/delete/list` (one-shot + cron recurrence) backed by
a persistent job store; deliver due jobs as user messages on next turn.
**Files:** new `tools/schedule.go`, store beside `internal/app/dream_scheduler.go`.
**dsh reference:** `packages/schedule/schedule/src/{tools,runtime,recurrence}.ts`.
**Acceptance:** created schedule survives restart; due job injects once; delete/list
round-trip.

### P19 — Terminal suite parity (TL6) — LOW · effort M

**Change:** unify `pty_exec` + `terminal` into a terminals service with
`open/close/list/read/send/signal` actions and a persistent-shell mode for bash.
**Files:** `tools/pty_exec.go`, `tools/terminal.go`, `internal/ptymgr.go`.
**dsh reference:** `docs/tool-catalog.md:778-944`.
**Acceptance:** one tool schema, all six actions; safety allow-list preserved;
existing `pty_exec` callers migrated.

### P20 — Projected context pressure (CX8) — LOW · effort M

**Change:** token-meter projection: anchor on last provider-reported usage, reprice
surface deltas (chars/token heuristic) to project the *next* request's prompt cost;
show `projected/window` in the footer and feed it (not the stale estimate) to
proactive compaction.
**Files:** `internal/agentic/agent_context_stats.go:92-145`, `internal/app/stats.go:1333`,
`internal/agentic/compression_thresholds.go`.
**dsh reference:** `packages/llm/token-meter/README.md` §contextPressure/projectedTokens.
**Acceptance:** projection updates immediately after a large tool result (before the
next request); footer shows projected figure; compaction trigger reads projection.

### P21 — Default max-tokens resolution (DS2) — LOW · effort S

**Change:** add per-model/per-provider `default_max_tokens` to the catalog
(DeepSeek: 256000, window 1000000), materialized into `max_tokens` when the request
doesn't set one; log which fields came from defaults.
**Files:** `internal/agentic/provider/schema/catalog.go` (DeepSeek entry :221-234),
`internal/agentic/provider/protocol/openai_completions.go`.
**dsh reference:** `packages/llm/llm-deepseek/src/adapter.ts:91-93`.
**Acceptance:** wire request carries explicit `max_tokens` on DeepSeek route;
explicit request value always wins.

### P22 — Settings hot-reload (DS6) — LOW · effort L

**Change:** fsnotify watch on the config cascade's writable layers; changed provider
profiles take effect on the next request; invalid edits keep last-good and warn.
**Files:** `config/loader.go`, `internal/app/subsystems.go` (rebuild provider on
change).
**dsh reference:** `packages/settings/settings-file/README.md`.
**Acceptance:** editing `~/.goa/config.yaml`'s model/effort applies without restart;
broken YAML keeps serving last-good with a warning in the log.

### P23 — `run_code` code-mode dispatch (TL7) — LOW (large) · effort L

**Change:** let the model submit one program that performs multiple tool sub-calls in a
jailed worker, with a durable per-sub-call dispatch log and spill-bounded artifacts.
**Files:** build on `tools/python.go` jail + `internal/agentic/tool_registry.go`
dispatch; new dispatch-log persistence.
**dsh reference:** `docs/tool-catalog.md:119-150`.
**Acceptance:** one `run_code` call fans out to ≥3 tools; sub-call results logged and
spill-capped; worker cannot escape the existing jail primitives.

### P24 — Cross-session references (CX7) — LOW · effort L

**Change:** `@[label](goa-session:<id>)` mention parsing in prompts → bounded read-only
snapshot of the referenced session injected as untrusted context with a warning frame.
**Files:** prompt intake in `internal/app`, session store read path; depends on P10's
read API (soft dependency — P10's store reader can be extracted first).
**dsh reference:** `packages/context/session-reference/README.md`.
**Acceptance:** ≤3 references, ≤64KB each, checkpoint-aware projection, escaping of
framing tags; self-reference rejected.

---

## 4. Suggested bundles

- **Cost bundle** (DeepSeek token economics): P1 + P2 + P5 + P13 + P21.
- **Context-robustness bundle:** P3 + P4 + P6 + P20.
- **DeepSeek-parity bundle:** P1 + P2 + P8 + P9 + P15 + P21.
- **Power-tooling bundle:** P10 + P11 + P17 + P18 + P19.
- **UX/context bundle:** P7 + P14 + P24.

Every item is scoped to land independently; within a bundle, listed order is the
recommended landing order.
