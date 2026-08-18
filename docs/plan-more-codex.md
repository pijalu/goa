# Plan: More Codex Optimizations for Goa

Branch: `feature/more-codex`
Status: implementation in progress on `feature/more-codex`; completed phases and resolved scope are recorded below.

### Remediation checkpoint — current status (2026-08-17)

The staticcheck remediation is clean, all packages compile, and focused/full package tests used during the refactors pass. Behavior-preserving structural splits were applied across AgentManager, GoalMode, Runtime, config loader, protocol thinking helpers, provider/config command helpers, and goal/tool tests; `bugs.md`'s stale TODO was removed. The repository-wide gate is **not yet complete**: current counts are 27 hard file-size violations, 58 gocognit findings, and 77 gocyclo findings. Race/vet/full gate verification remains pending. Continue by prioritizing remaining hard-limit files and highest-complexity functions; do not weaken checks or suppress findings.

Current remediation commit: pending (commit the validated working tree with a descriptive message after this checkpoint is recorded).

## Objective

Adapt the highest-value optimizations observed in OpenAI Codex to Goa without weakening Goa's existing high-mark compaction and append-only conversation guarantees. The first implementation target is **cache-aware high-mark compaction**: compact before the provider context ceiling, preserve the stable cached prefix whenever possible, and rotate provider cache identity whenever history is no longer an exact append of the prior request.

Secondary targets are request-shape parity, cache diagnostics, session-affine transport, and resilient quota reporting. Each phase must remain independently shippable and reversible.

## Non-goals

- Do not replace Goa's current compaction strategies wholesale.
- Do not mutate already-sent messages merely to reduce memory.
- Do not replace the established SSE path wholesale; WebSocket remains provider-scoped and session-affine.
- Do not depend on Codex private endpoints or undocumented response fields.
- Do not treat a provider cache miss as proof of local history corruption.

## Existing Goa constraints to preserve

1. Conversation history is append-only outside explicit compaction.
2. New contexts (fresh goal, `/clear`, fork, sub-agent, planner, summarizer) use a dedicated cache/session identity.
3. Existing high-mark/hard-ceiling compaction remains the final safety net.
4. Compaction provenance remains durable (`start → summary → end`) and orphan detection continues to work.
5. Cache-aware soft mutation remains deferred while the provider cache is hot.

## Micro-step implementation plan

### Phase 0 — Baseline and observability contract

**Status: implemented (baseline metrics and immutable provider snapshots).**

The contract is a thread-safe snapshot: each recorded wire request increments `requests` and `serialized_bytes` from an owned body copy; completed usage contributes cache read/write tokens from an owned usage copy; explicit compaction and tool-schema events update their counters/digest; successive non-empty session-key hashes count key changes. `RequestFingerprint` contains only bounded SHA-256 digests and metadata (never raw session keys, prompts, or request bodies), and the deterministic mock tests cover these values plus request/usage immutability.

1. Record the current branch and clean working-tree state.
2. Run focused compression, cache-forensics, provider-protocol, and agent tests.
3. Capture baseline metrics from a deterministic fake provider:
   - request count;
   - serialized input bytes/tokens;
   - cache read/write tokens;
   - high-mark compaction count;
   - cache-key changes;
   - tool schema hash.
4. Define a request fingerprint structure (internal/debug only):
   - provider/model;
   - cache/session key hash (never raw secrets);
   - input prefix hash;
   - full request hash;
   - history generation/compaction generation;
   - transport and turn ID.
5. Add no behavior changes in this phase.

### Phase 1 — Make compaction policy explicit

1. Identify the current high-mark threshold calculation and all callers of `enforceContextCeiling`, `compressHistory`, and micro-compaction.
2. Introduce a small policy decision primitive returning one of:
   - `Noop`;
   - `SoftMaintenance`;
   - `HighMarkCompaction`;
   - `EmergencyFallback`.
3. Feed it only immutable inputs: estimated usage, configured max, soft/hard marks, cache-hot state, last-turn time, and strategy availability.
4. Keep the existing hard ceiling as an unconditional upper bound.
5. Ensure policy evaluation itself never mutates history or timestamps.
6. Document that a high-mark decision is based on the *next request risk*, not merely current memory size.

### Phase 2 — Codex-style proactive high-mark compaction

1. Add a configurable proactive high-mark margin (default derived from existing hard/soft marks; preserve current defaults unless explicitly configured).
2. Trigger compaction before the next turn would cross the high mark, accounting for:
   - current estimated input;
   - expected user/tool suffix;
   - output/reasoning reserve;
   - provider model context window.
3. Prefer the least destructive configured strategy in order:
   - tool-result pruning/elision;
   - micro-compaction;
   - selective compaction;
   - summarization;
   - emergency fallback.
4. Preserve recent turns and the last real user message according to existing configuration.
5. Avoid proactive in-place mutation while cache is warm unless the hard ceiling requires it.
6. Emit exactly one compaction event for a policy pass, while retaining the existing provenance triple for summarize compaction.
7. Record before/after estimates, strategy, reason (`high_mark`, `hard_ceiling`, `context_error`), and cache-hot state.
8. Ensure a no-op pass emits no phantom compaction event.

### Phase 3 — Cache identity and history generation

1. Add an internal monotonically increasing history/compaction generation to the agent/session runtime.
2. Increment it whenever history is replaced, compacted, forked, cleared, or summarized.
3. Keep ordinary append-only turns on the same provider cache key.
4. Add a cache-key factory that derives a stable key from the owning context ID plus generation boundary, without embedding secrets or raw prompts.
5. Rotate the provider cache key when compaction produces a non-prefix replacement history.
6. Rotate on all explicit context boundaries, including fresh goal contexts and sub-agent/planner/summarizer chains.
7. Do not rotate merely because a provider reports a small cache-read wobble.
8. Expose the key generation only in debug telemetry and tests.
9. Verify that Codex SSE still omits `previous_response_id`; only `prompt_cache_key` is used for that flavor.

### Phase 4 — Prefix-integrity diagnostics

1. At request-build time, compute a canonical hash for the serialized input prefix that was sent on the previous request.
2. Classify the new request as:
   - exact append;
   - replacement after explicit compaction/context reset;
   - unexpected divergence.
3. Record divergence in cache-forensics diagnostics, not as a user-visible error by default.
4. Include bounded metadata only; never log OAuth tokens, full prompts, or sensitive tool arguments outside the existing opt-in forensic bundle.
5. Correlate classifications with provider cache-read transitions.
6. Add a debug warning when an unexpected divergence shares the old cache key.
7. Keep the current complete-request forensic capture for actual detected cache busts.

### Phase 5 — Request-shape parity with Codex

1. Inventory Goa's Codex request fields against `codex-rs/core/src/client.rs`.
2. Make model capabilities drive optional fields instead of unconditional fields:
   - reasoning encrypted content;
   - reasoning summary/context;
   - verbosity;
   - parallel tool calls;
   - Responses Lite/tool namespace shape.
3. Preserve `store=false` for the Codex subscription endpoint.
4. Preserve stable tool-call IDs and assistant reasoning content across turns.
5. Ensure tool schemas and system instructions remain byte-stable while the cache key is unchanged.
6. Add protocol tests for every capability combination and assert that unrelated OpenAI Responses flavors retain their existing behavior.

### Phase 6 — Session-affine transport seam

1. Define a provider transport abstraction around the current streaming request path.
2. Preserve SSE while enabling the session-affine WebSocket transport for OpenAI Responses.
3. Maintain session-scoped transport state and disable WebSocket for one session after failure without affecting others.
4. Ensure retries do not reuse a partially divergent request or stale response continuation ID.
5. Cover retry, fallback, cancellation, connection eviction, and session isolation with deterministic fake-server tests.
6. Keep authentication and protocol compatibility boundaries explicit in the provider-specific transport.

### Phase 7 — Resilient Codex-style quota reporting

1. Normalize primary and secondary rate-limit windows into Goa's quota model.
2. Preserve previous positive usage/credit values when a backend response transiently reports zero and contains no authoritative groups.
3. Distinguish missing data, authenticated empty data, and explicit exhausted limits.
4. Avoid quota refreshes on every render; use bounded scheduler intervals and request coalescing.
5. Keep Goa-managed OAuth ownership; the plugin must not read Codex credential files directly.
6. Add tests for sparse snapshots, transient zeroes, additional windows, expired auth, and rate-limit reached types.

## Detailed test coverage

### Unit tests — policy and high-mark compaction

- Below soft mark: no action and no history mutation.
- At soft mark with hot cache: defer destructive maintenance.
- At soft mark with cold cache: perform configured soft strategy.
- At high mark: choose the least destructive strategy that reaches the target.
- At hard ceiling: hard compaction always runs even when cache is hot.
- Context-error retry: compact once, retry once, and avoid repeated compaction loops.
- Empty history, system-only history, and one-message history remain valid.
- Preserve configured recent-turn count and last user message.
- Oversized individual user/tool messages are bounded safely.
- Failed summarization falls back to selective/micro strategy without losing history.
- No-op compaction emits no event; successful pass emits one logical event.
- Provenance triple has ordered start/summary/end, shared transaction ID, and error closure.
- Crash/orphan detection remains unchanged.

### Unit tests — cache identity and prefix diagnostics

- Ordinary append keeps the same key and classifies as exact append.
- Explicit compaction increments generation and rotates key when replacement is non-prefix.
- Fresh context, clear, fork, planner, summarizer, and sub-agent each receive isolated keys.
- A cache-read wobble below tolerance does not rotate identity.
- Unexpected history divergence with an old key produces a diagnostic warning.
- Same bytes under different contexts do not share a key.
- Different model/provider/tool hash does not reuse an incompatible key.
- Key diagnostics contain hashes only, never raw session tokens or prompt text.
- Concurrent agents do not race generation or cross-contaminate keys.

### Unit tests — request protocol parity

- Codex request contains `prompt_cache_key` and omits `previous_response_id`.
- Plain OpenAI Responses retains its existing continuation behavior.
- `store=false` remains present for Codex.
- Reasoning encrypted content is included only when supported/configured.
- Verbosity, reasoning summary, parallel calls, and tool choice follow model capabilities.
- Tool-call IDs and assistant reasoning items survive the next request unchanged.
- Canonical serialization is stable for unchanged prefixes.
- No-tools final step removes parallel-tool fields and sets `tool_choice=none`.

### Unit tests — transport seam

**Status: implemented for OpenAI Responses with pooled session-affine WebSocket transport.**

- SSE success path.
- Session-scoped fallback after WebSocket-like failure.
- Retry preserves exact request bytes.
- Cancellation closes/drains the active response.
- One session's fallback does not disable another session's transport.
- Failed requests do not advance cache-forensics usage baselines.

### Unit tests — quota resilience

- Primary and secondary windows map correctly.
- Additional named windows remain visible.
- Transient zero credits preserve the last positive value only when the response is sparse.
- Authoritative zero/exhausted responses replace the old value.
- Auth-required, expired-token, HTTP, and malformed responses map to distinct states.
- Refresh scheduler coalesces concurrent refresh requests.

### Integration tests

1. Fake provider with a deterministic context window:
   - grow history through the high mark;
   - verify proactive compaction occurs before hard failure;
   - verify the next request is valid and cache identity changes only at replacement boundaries.
2. Cache-forensics integration:
   - warm cache;
   - append several turns;
   - compact;
   - ensure the diagnostic explains the identity boundary rather than reporting mutation.
3. Codex protocol integration:
   - inspect serialized SSE request bodies;
   - verify fields and input prefix behavior across tool-call rounds.
4. Context reset integration:
   - run fresh goal/clear/fork/sub-agent flows concurrently;
   - verify no shared cache key across divergent histories.
5. Quota plugin integration:
   - simulate sparse/transient backend responses;
   - verify status-bar and `/quota` output remain stable and accurate.

### Regression and quality gates

- `go test ./internal/agentic/... -count=1 -race`
- `go test ./internal/logs/... ./plugins/... -count=1 -race`
- `go test ./... -count=1 -race -cover`
- `go vet ./...`
- `staticcheck ./...`
- `gocognit -over 15` and `gocyclo -over 12`
- deterministic fake-provider benchmarks for request size, compaction latency, and allocations
- verify no sensitive values appear in logs or forensic artifacts

## Delivery order

1. Phase 0 baseline and diagnostics contract.
2. Phase 1 policy primitive.
3. Phase 2 proactive high-mark compaction.
4. Phase 3 cache identity generation.
5. Phase 4 prefix diagnostics.
6. Phase 5 request parity.
7. Phase 6 transport seam.
8. Phase 7 quota resilience.

Each phase should land with its tests and be independently reviewable. If a phase changes cache identity semantics, include an explicit migration note and update the cache-forensics documentation before merging.

## Implementation record

- Phase 0: completed. Provider tests establish the baseline; `RequestFingerprint` records only bounded hashes and prefix classification (`exact_append`, `replacement`, `unexpected_divergence`, or `no_predecessor`).
- Phase 1: completed. `DecideCompactionPolicy` is a pure immutable-input primitive returning `Noop`, `SoftMaintenance`, `HighMarkCompaction`, or `EmergencyFallback`; the existing proactive tier adapter delegates to it while retaining the hard-ceiling override. Exhaustive boundary, cache-hot/TTL, availability, and zero-input tests cover the contract.
- Phase 2: completed. Proactive policy accounts for projected next-request tokens plus reserve/margin, preserves cache-hot deferral below the hard ceiling, and exposes deterministic least-destructive strategy ordering. Existing mutation paths retain recent-turn preservation, one-event/no-op suppression, and provenance triples.
- Phase 3: cache identity primitive completed. `NewCacheKey` derives an opaque key from context ownership, generation, provider/model, and tool-schema hash; callers must advance generation at explicit replacement boundaries. Existing Codex SSE field behavior is unchanged.
- Phase 4: completed. The canonical streaming request hook records bounded fingerprints and forensic entries, classifies predecessor prefixes, and never exposes raw IDs/prompts.
- Phase 5: resolved by existing Codex protocol tests and implementation: Codex uses `prompt_cache_key`, omits `previous_response_id`, sends `store=false`, and collapses tool fields for no-tools final steps.
- Phase 6: implemented. OpenAI Responses supports session-affine pooled WebSocket streaming with bounded idle reads, cancellation, connection eviction, and race-safe per-connection serialization; focused transport and provider regressions cover these guarantees.
- Phase 7: existing quota plugin tests cover sparse snapshots, windows, transient values, and refresh behavior; no duplicate backend-specific implementation is warranted in this change.

## Review and execution plan (2026-08-18)

The following status is the authoritative phase review; all phases below are implemented or validated against the existing runtime seams:

| Phase | Status | Evidence / gap | Required next action |
|---|---|---|---|
| 0 | Implemented | Fingerprint shape, bounded hash tests, immutable request/usage snapshots, and canonical request observation now exist; provider tests cover no-secret metadata and aggregate metrics. | Retain provider-mock baseline coverage. |
| 1 | Implemented, integration-tested | `DecideCompactionPolicy` provides pure four-way decisions; `proactiveTierLocked` adapts it to runtime tiers, and policy tests cover boundaries, hard override, cache gates, availability, and zero values. | Retain existing policy regression suite. |
| 2 | Implemented, integration-tested | Existing proactive strategies, hard fallback, event suppression, and provenance tests are present. | Retain deterministic context-window coverage. |
| 3 | Implemented | Agent owns an opaque context ID and generation; replacement, clear, and compaction advance it while ordinary append retains the key. `PromptCacheKey` is separate from `SessionID`. | Extend generation hooks if new context-boundary APIs are added. |
| 4 | Implemented | Canonical runtime builds bounded fingerprints and forensic records classify predecessor prefixes without exposing raw IDs/prompts. | Retain canonical serialized-request coverage. |
| 5 | Implemented for Codex shape | Existing Codex tests verify `prompt_cache_key`, no `previous_response_id`, `store=false`, and final no-tools collapse; cache-key override preserves this shape. | Retain serialized-request regression coverage. |
| 6 | Implemented | Shared pooled WebSocket transport supports session-affine OpenAI Responses streams, bounded idle reads, connection eviction, cancellation, and normal event parsing. | Retain provider-specific WebSocket protocol regression coverage. |
| 7 | Implemented and validated | Focused quota/plugin tests cover sparse snapshots, windows, transient values, and refresh behavior; no duplicate backend-specific logic is needed. | Retain sparse/authoritative response coverage as quota providers evolve. |

### Mocked Codex regression evidence (2026-08-18)

- `internal/agentic/provider/openai_responses/codex_mock_endpoint_test.go` runs an `httptest.Server` with Codex-style SSE (`response.output_text.delta` followed by `response.completed`). It snapshots the request body and headers before mutating the caller's context, and asserts the snapshot contains `instructions`, `store=false`, opaque `prompt_cache_key`, Codex headers, and no `previous_response_id`.
- The request shape was cross-checked against `logs/http.jsonl` from `/Users/muaddib/dev/goa/.goa/exports/goa-export-20260818-113201.zip`: the export shows repeated `POST https://chatgpt.com/backend-api/codex/responses` exchanges and the same Codex SSE endpoint family. Export payloads are intentionally not copied into tests because they contain sensitive prompts/tool schemas.
- The mock test passes together with existing Codex body/SSE parser tests. Limitation: the export's redacted request bodies are not replayed byte-for-byte; comparison is contract-level to avoid committing secrets.


The test harness must use a deterministic `agentic.ApiProvider` mock that records a deep copy of every `provider.Context` and `StreamOptions` at call entry. It will run ordinary append turns, a proactive compaction, and an explicit context reset. Assertions must prove:

1. Consecutive ordinary requests retain the same cache key and the prior serialized message prefix; only new tail messages are added.
2. A replacement compaction or reset advances generation and changes the opaque key; a cache-read wobble alone does not.
3. The mock's recorded contexts remain unchanged after later agent operations (no mutation of already-sent messages or backing content slices).
4. Codex request serialization still contains `prompt_cache_key`, omits `previous_response_id`, and preserves `store=false`.
5. Fingerprint diagnostics contain hashes and bounded metadata only, never session IDs, prompts, OAuth tokens, or tool arguments.

Implementation is ordered by the linked goal todos: review/status correction, runtime identity wiring, canonical fingerprint observation, provider-mock integration tests, then phase-specific validation and final full checks.
