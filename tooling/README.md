# tooling/

Reusable analysis scripts for Goa session forensics. These operate on the
append-only session journals in `.goa/sessions/*.jsonl` and are used to
validate provider prompt-cache behavior against **Hard Rule #7**
(conversations append-only; cache IDs context-scoped).

## Scripts

### `session_cache_report.py`

Per-request provider prompt-cache timeline.

```bash
python3 tooling/session_cache_report.py /path/to/.goa/sessions/<id>.jsonl [--full]
```

Columns: `seq`, session-file `line`, `prompt_n` (uncached prompt tokens the
provider processed fresh), `cache_read` (tokens served from the provider
prefix cache), `hit%`, `pred` (predicted/output tokens).

Flags:

- `MISS(cold)` — `cache_read == 0`. Expected only for the first request of a
  conversation or after `/clear` + `ResetCacheForensicsBaseline`.
- `BUST(-N)` — `cache_read` dropped by more than
  `cacheBustDropToleranceTokens` (1024, block-quantization wobble;
  `internal/app/stats.go`). Indicates prefix-cache eviction / divergent
  context.
- `SHRINK(N)` — total prompt (`prompt_n + cache_read`) shrank by > N tokens
  vs the previous request. Evidence of an in-place history rewrite
  (compaction, elision, injection).
- `GROW(+N)` — `prompt_n` (uncached) is large; flags sudden context growth.

Each flagged row is annotated with the nearest preceding session event
(user/tool call/result) so the cause is visible inline.

### `session_growth_report.py`

Sudden-context-growth analyzer. Correlates every request whose uncached
`prompt_n` exceeds a threshold with the flanking tool calls/results and user
injections, and lists the largest tool results in the session.

```bash
python3 tooling/session_growth_report.py /path/to/.goa/sessions/<id>.jsonl \
    [--min-delta 8000] [--top 10]
```

Use it to answer "which tool call dumped Nk tokens into the context?" and to
spot unbounded tool output (large `read`, `bash`, `edit`, `goal` results).

## Interpreting results

**First establish which cache your provider uses** (this changes what a bust
means):

- **Auto prefix cache, no key** (Z.AI, DeepSeek — `ApiOpenAICompletions` +
  `promptCacheKey()` returns "" because not OpenAI/local/long-retention):
  the provider caches the longest matching *content prefix*. A bust means the
  new prompt diverged from the previous one — most often a benign append-only
  burst (goal injection, failed-then-retried tool pair, big tool result).
  The conversation ID is **not** sent, so it cannot be the cause here.
- **Keyed cache** (OpenAI Completions/Responses, Azure, Codex, local
  LM Studio/Ollama/llama.cpp): `prompt_cache_key`/`previous_response_id`/
  `X-Session-ID` carry the `SessionID`. A bust here *can* be a key-stability
  bug (Hard Rule #7) — two chains sharing an ID but not byte-exact appends.

Benign bust causes (either cache kind):

- Goal-mode injections (`[goal]`, `[goal progress]`, sticky instructions,
  stream-loop warnings) are appended mid-conversation and legitimately change
  the suffix, so a later request that reuses an older cached prefix sees a bust.
- Sub-agent / skill-runner / goal-runner requests run with their own history
  but may share the provider cache key; when their contexts diverge, the
  provider evicts.

A bust **is** a bug (Hard Rule #7) when two request chains share a
`SessionID`/`prompt_cache_key` but are **not** byte-exact appends of each
other. The journal in
`internal/agentic/provider/cache_forensics.go` retains the offending requests
(`CacheMissReport`) for the debug bundle; the app-layer detector in
`internal/app/stats.go` counts busts in `CacheMisses`.

See `docs/PROVIDER-CACHE.md` for the full design.

## Cross-references

- `internal/agentic/provider/cache_forensics.go` — request journal + miss
  detection (`cacheSeqKey` = sessionID|provider|model|systemPromptHash).
- `internal/app/stats.go` — app-layer bust counter
  (`cacheBustDropToleranceTokens = 1024`).
- `internal/agentic/agent_streaming.go` — `persistGoalReminder`,
  `buildSystemPrompt` (goal text deliberately kept out of the cached system
  prefix).
