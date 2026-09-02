# Provider Prompt Cache — Design & Forensics

How Goa drives the LLM provider's prompt cache, how a "cache miss" is defined
and detected, what actually causes misses, and the stability requirements for
future rework. Written from a live review of session
`frigolite/.goa/sessions/1786862023_dq52kp4s.jsonl` (provider `zai` /
`glm-5-3`, 295 requests) plus the source.

> **Why this matters:** a cache miss has **exponential effective cost**. The
> provider reprocesses the whole uncached tail of the prompt at full prompt
> price and latency. A bust on a 200k-token conversation is not "one slow
> turn" — it re-bills ~200k prompt tokens. Misses must be avoided wherever the
> context did not genuinely change.

---

## 1. Two fundamentally different provider caches

Everything about a "cache miss" depends on which of these the provider uses.
**Determine this first** before classifying any miss.

| | **Auto prefix cache (content-keyed)** | **Keyed cache (ID-keyed)** |
|---|---|---|
| Providers | Z.AI, DeepSeek | OpenAI Completions/Responses, Azure, Codex, local LM Studio / Ollama / llama.cpp |
| Cache key | the prompt **content itself** (longest matching token prefix) | `SessionID` → `prompt_cache_key` / `X-Session-ID` |
| Goa sends an ID? | **No** — `promptCacheKey()` returns `""` | Yes |
| A "miss" means | the new prompt **diverged** from the previous one | the cache entry for that ID was evicted / never written / key changed |
| Conversation-ID stability matters? | **No** (ID never sent) | **Yes — central** |

Source: `internal/agentic/provider/protocol/openai_completions.go`
`promptCacheKey()` (returns `""` unless OpenAI base-URL, long-retention, or
local provider) and `isLocalProvider()`; `schema/catalog.go` (`zai` →
`ApiOpenAICompletions`). The Z.AI session under review therefore used
**content prefix caching** — its `SessionID` was never transmitted.

**Corollary:** for Z.AI/DeepSeek the cache is *already* "linked to the content
of the conversation" — there is nothing more stable to key on. The user's
requirement ("conversation IDs should be as stable as possible, linked to
content") is only actionable for the **keyed-cache** providers.

---

## 2. The append-only contract (Hard Rule #7)

Outside of compression, a conversation's message history is **strictly
append-only**: never rewrite, drop, or reorder already-sent messages.

- Any new context — fresh-context goal, `/clear`, sub-agent, skill runner,
  planner, summarizer, fork — **MUST** get its own dedicated SessionID.
- Two request chains may share an ID only when one is a **byte-exact append**
  of the other; anything else must rotate the ID.
- A shared ID with a diverging context silently evicts the provider cache and
  surfaces as an unexplained miss.

Enforcement / detection lives in two mirrored detectors:

- **App layer** — `internal/app/stats.go`: counts busts in `CacheMisses`.
  A bust = `cache_read == 0` after cache established, OR a `cache_read` drop
  greater than `cacheBustDropToleranceTokens = 1024` (block-quantization
  wobble below this is reporting noise).
- **Provider layer** — `internal/agentic/provider/cache_forensics.go`: a
  rolling journal of complete API requests. Sequences are keyed by
  `cacheSeqKey = SessionID|provider|model|systemPromptHash`. On a detected
  miss it retains the offending request + its predecessor (`CacheMissReport`,
  exported as `logs/cache_miss_requests.json` in the debug bundle) and emits
  a notice into the agent log.

---

## 3. Conversation-ID lifecycle (keyed-cache providers)

`SessionID` is minted by `SessionStore.StartSession()` and applied to the
agent's `StreamOptions`. **Rotation is deliberate and rare:**

| Transition | ID rotated? | Where |
|---|---|---|
| Session start | new ID | `core/agentmanager.go` |
| **Fresh-context goal** (`RunFresh begin=true`) | **Yes** — `ResetConversationID()` | `internal/app/subsystems.go:877` |
| `/clear` / in-place context reset | re-arm baseline (same ID, history cleared) | `Agent.Clear` / `EmitContextReset` → `provider.ResetCacheForensicsBaseline()` (`agent.go:1575`, `agent_events.go:116`) |
| **Same-context goal: create / stop / resume / restart / pause** | **No** | reuse path (`core/goal_driver.go` — only `FreshContext` goals route to `RunFresh`) |

**Design rule confirmed by review:** a same-context stop/resume/restart does
**not** rotate the ID and does **not** rewrite history — so it produces **no
miss**. Verified in the session: exactly **one** `cache_read == 0` cold start
across 404 requests (the very first), i.e. no spurious fresh-context
rotations. Same-context goal transitions are cache-stable by construction.

---

## 4. Goal-mode context injection (the append-only suffix churn)

Goal state reaches the model as **user-role messages appended to history** —
never in the cached system prefix. `internal/agentic/agent_streaming.go`:

- `buildSystemPrompt()` returns only `cfg.SystemPrompt`. Goal text is
  deliberately excluded: a goal reminder in the prefix busted the whole cache
  on every goal transition (the "CRITICAL: /goal destroy caching" note).
- `persistGoalReminder()` appends two messages per turn:
  - **Static** `[goal]` reminder — appended **only when it changes** (goal
    create / destroy / status-flip), deduped via `lastPersistedGoalReminder`.
    Built by `goal.BuildStaticGoalReminder` / `BuildBlockedNote` /
    `BuildPausedNote` (`core/goal/injection.go`).
  - **Dynamic** `[goal progress]` — appended **every turn** (turn/token/
    elapsed counters, todo ticks). Purely append-only; never breaks a prefix.
- `persistStickyInstructions()` appends sticky skill blocks, deduped against
  actual history.

Because these are appends, they extend the suffix and — on a content-prefix
cache — the *next* request recomputes from the point of divergence. That is
**benign and expected**, not a Hard Rule #7 violation.

---

## 5. Classifying the session's misses (evidence)

295 requests, overall 95–99.9% hit rate. 7 flagged events, **none are bugs**:

| seq | cache_read | flag | cause |
|---|---|---|---|
| 1 | 0 | cold start | first request — expected |
| 54 | 10,368 | BUST −102k, SHRINK 15k | blocker-investigation goal activated → new `[goal]` block |
| 61 | 98,688 | BUST −20k | new goal `sparky.puma` created → `[goal]` changed |
| 167 | 119,616 | BUST −88k | failed-then-retried `edit` tool pair appended (see §6) |
| 227 | 191,872 | BUST −41k | terminals/bash results after a long test run |
| 247 | 118,592 | BUST −121k | goal continuation: new `[goal]`+`[goal progress]` + 700s test output |

All followed immediately by a request re-establishing ~99% hit. Every one is
an **append-only suffix divergence** on a content-prefix cache — the exact
scenario `cache_forensics.go` records as a *notice*, not a defect.

---

## 6. Two questions that came up (answered)

**(a) "The failed-then-retried tool pair changed the suffix — how/why?"**
No message was rewritten. A failed `edit` (`call_a098367`, `not_found`) and
its later successful retry (`call_f2b9808`) are **two distinct appended
messages**. Between the last cached checkpoint and the retry, the conversation
appended a grep result + a user message + a new assistant tool-call turn + its
result. On a prefix cache the shared prefix ends at the point of first
divergence (~token 119,616); the ~72k appended after it is recomputed. That is
append-growth, not mutation.

**(b) "Is the goal-mode injection the cause of the big bust? Is it important?"**
Yes — but it is the **static** reminder, not the dynamic progress. The static
`[goal]` block only re-appends when its text *changes* (goal create / status
flip). The dynamic `[goal progress]` ticks every turn but is append-only, so
it never busts a prefix. A steady-state active goal whose progress merely
ticks is cache-friendly; only a goal **transition** (new objective text) adds
a divergent block.

---

## 7. Known gap: unbounded goal payloads (context bloat, not a miss bug)

This is the one real issue surfaced. The short `/goal:list` TUI view the user
sees (<4k) is **not** what enters the context. Three different byte paths:

| Path | Content | Bounded? |
|---|---|---|
| **TUI `/goal:list`** | rendering only, never sent to the LLM | n/a |
| **`[goal]` static reminder** | full `Objective` + `CompletionCriterion` + `VerifyCommand` + `Handoff` + ~2.2KB fixed guidance | **NO truncation** |
| **`goal` tool result** (`get`/`list`) | `json.Marshal(goal.ForModel(snapshot))` — full `GoalSnapshot` | **NO truncation** (`ForModel` only strips `GoalID`) |

Evidence (session line 23982): `goal action:"list"` returned **49,906 chars
(~12.5k tokens)** — it serialized **33 goals** (active `sturdy.otter`
objective 5,437 chars + 32 queued goals each carrying objective + handover +
criterion + verify + todos). `ForModel` strips only the `GoalID`; everything
else is dumped verbatim.

**Impact:** the first persist of a `[goal]` reminder, and any `goal get/list`
call, can inject tens of KB into history. That both bloats context and — on a
content-prefix cache — enlarges the divergent suffix that gets recomputed on
the next turn. A 50KB reminder "does not make sense" (user's words) when the
list view proves a <4k summary suffices for orientation.

### Status: IMPLEMENTED (this review)

The bound is now in code, not just a requirement:

- **`core/goal/serialize.go`** — added `Excerpt(s, max)` (rune-safe,
  ellipsis on truncation) plus caps `ExcerptObjectiveLen = 400` and
  `ExcerptFieldLen = 280`, chosen so a normal goal (a sentence to a short
  paragraph) is unaffected. Added compact list types `GoalSummary` /
  `UpcomingGoalSummary` / `TodoSummary` and converters `SummarizeSnapshot` /
  `SummarizeUpcoming` (drop criterion / verify / handover / terminal fields,
  bound objective + todo titles).
- **`core/goal/injection.go`** — `BuildStaticGoalReminder`, `BuildBlockedNote`,
  `BuildPausedNote` now wrap every embedded field in `Excerpt(...)`. Excerpts
  are deterministic per goal, preserving the byte-identical-across-turns
  invariant the dedup (`lastPersistedGoalReminder`) relies on.
- **`tools/goal/goal.go`** — `handleList` now emits `SummarizeSnapshot` /
  `SummarizeUpcoming` instead of full `GoalSnapshot`s. The summary keeps the
  keys the TUI renderer reads (`name/objective/status/counters/todos`), so the
  display is unchanged. Full detail for one goal stays on `handleGet`
  (unchanged, full `ForModel` snapshot).

Tests: `core/goal` (`TestExcerpt`, `TestSummarizeSnapshot_BoundsFields`,
`TestBuildStaticGoalReminder_BoundsLongFields`,
`TestBuildBlockedNote_BoundsLongFields`) and `tools/goal`
(`TestGoalTool_ListIsCompact`, updated `TestGoalTool_CreateHandover_Queued` —
handover durability now asserted via promote→`get`, not `list`). The TUI
renderer tests (`tui/goal`) pass unchanged against the compact shape.

Residual knob: the excerpt caps (400/280) are a first cut. If a legitimately
long objective needs more context than the excerpt, the model can always call
`goal get`; if that proves common, raise `ExcerptObjectiveLen` rather than
re-exposing the full list.

---

## 8. Cost-aware stability requirements (the through-line)

A miss reprocesses the whole uncached tail at full cost, so **stability is a
cost feature, not a nicety**:

- **Same-context stop/resume/restart:** keep the ID and history untouched →
  **zero miss** (already true; do not regress).
- **Fresh-context goal / start-anew:** rotation + cold-start cost is
  **intended** (new conversation) — but scope it so a *resumable* same-context
  path stays warm. Do not rotate on resume.
- **Keyed-cache providers:** the ID should be **as stable as the content**.
  Session affinity on the Responses APIs rides `prompt_cache_key` only —
  `previous_response_id` is never sent over SSE (it must reference a
  server-issued `resp_*` object; a Goa session ID there is a hard HTTP 400 on
  strict upstreams, e.g. opencode Zen 2026-09-02). Any "content-linked ID"
  rework keys the *cache* fields without touching request chaining.
- **Auto-cache providers (Z.AI/DeepSeek):** nothing to key — minimize suffix
  divergence (§7) instead.

---

## 9. Forensics tooling

Reusable scripts in `tooling/` (see `tooling/README.md`):

- `session_cache_report.py SESSION.jsonl [--full]` — per-request cache
  timeline; flags `MISS(cold)` / `BUST(-N)` / `SHRINK(N)` / `GROW(+N)`,
  annotated with the preceding event. Thresholds mirror
  `cacheBustDropToleranceTokens`.
- `session_growth_report.py SESSION.jsonl [--min-delta N] [--top N]` —
  correlates uncached-`prompt_n` spikes with flanking tool calls/results and
  lists the largest tool results (spot unbounded output).

Live request bodies for retained misses: `CacheForensicsReports()` → debug
bundle `logs/cache_miss_requests.json`; notices via `TakeCacheMissNotices()`.

## Cross-references

- `internal/agentic/provider/cache_forensics.go` — miss journal + `cacheSeqKey`.
- `internal/app/stats.go` — app-layer bust counter.
- `internal/agentic/agent_streaming.go` — `persistGoalReminder`,
  `buildSystemPrompt`, `persistStickyInstructions`.
- `internal/agentic/provider/protocol/openai_completions.go` — `promptCacheKey`,
  `isLocalProvider`; `.../openai_responses.go` — `prompt_cache_key` session
  affinity (no `previous_response_id` over SSE).
- `core/goal/injection.go` — reminder builders; `tools/goal/goal.go` —
  `handleList`/`handleGet`; `core/goal/serialize.go` — `ForModel`.
- `core/goal_driver.go` + `internal/app/subsystems.go` — fresh vs reuse routing.
