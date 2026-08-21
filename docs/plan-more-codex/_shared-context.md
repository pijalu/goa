# Shared Context — More Codex Optimizations

This file holds the **small, reusable context** every micro-task needs. It exists so each
task file can stay short and each goal can run with `freshContext: true` and a low token
budget — a task references the sections it needs instead of re-stating them.

Reference implementation: `../codex/codex-rs/core/src/` at commit `230791fd1f` (read-only).

---

## 1. Verified Codex facts (what we are matching)

| Mechanism | Codex behavior | Codex source |
|---|---|---|
| Transport | **Dominant path is incremental WebSocket**: send only the new tail of `input` chained by `previous_response_id`; full-history SSE is the fallback. | `client.rs` `get_incremental_items` / `prepare_websocket_request` (≈1221–1360), send at ≈1697–1706 |
| WS delta precondition | Prior request must match on model, instructions, tools, tool_choice, parallel_tool_calls, reasoning, store, stream, include, service_tier, prompt_cache_key, text (`responses_request_properties_match`), AND new input must be a strict append of `last_input + last_response.items_added`. | `client.rs` `responses_request_properties_match` (≈308–361) |
| Compaction trigger | **Reactive buffered-limit rollover**, not proactive. Fires when `auto_compact_scope_tokens >= buffered_limit` OR `active_context_tokens >= full_context_window_limit`, at post-sampling (MidTurn) and pre-sampling (PreTurn). | `turn.rs` ≈440–479, ≈994–1023; `session/context_window.rs` 71–79 |
| Remote compaction | Preferred path is unary `POST /responses/compact` returning a replacement item list. Local summarization is only the no-support fallback. A `TokenBudget` mode installs a fresh window with **no summary**. | `client.rs` `compact_conversation_history` (552–656); dispatch `turn.rs` 1183–1238; `compact_token_budget.rs` |
| Cache key | `prompt_cache_key` = **session_id**, stable for the whole session, **never rotates on compaction**. Warmth is kept by server-side session state (turn-state + `previous_response_id`), not key rotation. | `client.rs` `prompt_cache_key` (484–488) |
| Turn-state | Server issues `x-codex-turn-state` response header at turn start; client replays it on every request within the turn (SSE header, WS `client_metadata`). Sticky routing to a warm backend. | `client.rs` 145, 268–281, 1624–1626 |
| Quota | Preserve prior `credits`/`individual_limit`/`spend_control_reached`/`plan_type` **only when the new snapshot omits them** (absent). An explicit authoritative zero/exhausted **replaces** the old value. Default `limit_id` = `"codex"`. | `state/session.rs` `merge_rate_limit_fields` (338–358) |

---

## 2. Current Goa seams (where the work lands)

| Concern | Goa location | Notes for the tasks |
|---|---|---|
| Codex request body build | `internal/agentic/provider/protocol/openai_responses.go` (`applyCodexBodyFields`, `applyResponsesSessionFields`) | SSE path: sends `prompt_cache_key`, `store=false`, `instructions`; **omits** `previous_response_id` for Codex. |
| WS transport (provider) | `internal/agentic/provider/openai_responses/websocket.go` | Currently **full-history POST-over-WS**, one-shot; parses the event stream. No delta, no reuse. |
| Low-level WS transport | `internal/agentic/provider/transport/websocket.go` (`WebSocketTransport`), `transport.go` (`Transport` interface) | `Do(ctx, req) -> TransportResponse{StatusCode, Headers, Body}` — **one-shot**, not a persistent multi-request connection. Codex reuses one connection across requests; Goa does not yet. |
| SSE/WS event parser | `internal/agentic/provider/openai_responses/provider.go` (`handleResponsesCompleted`, `parseResponsesSSE`) | `response.completed` handler currently reads only `status` + `usage`; it **discards** the server `response.id` and the output items 6b needs for chaining. |
| Assistant message / usage | `internal/agentic/provider/schema/events.go` (`AssistantMessage{Content, Usage, StopReason}`), `schema/types.go` (`Usage`) | No field today carries `response_id` or the added output items. 6b must add one. |
| Cache identity | `internal/agentic/provider/cache_identity.go` (`CacheIdentity`, `NewCacheKey`) | Generation-advancing opaque key. Rotation currently applies on the SSE/full-resend path. |
| Prefix fingerprint / forensics | `internal/agentic/provider/cache_forensics.go`, `cache_miss_log_test.go` | Classifies `exact_append` / `replacement` / `unexpected_divergence`; **reuse as the 6b delta-vs-full trigger**. |
| Compaction engine | `internal/agentic/agent_compression.go` (`compressHistoryWith`, `summarizeHistory`, `dropOldestToFitLocked`, `pruneToolResultsPreCompact`); history replaced at ≈lines 172, 343, 1087 | All strategies are local. No remote `/responses/compact` client exists. |
| Quota plugin | `plugins/bundled/provider-quota/fetchers/codex.js` | JS fetcher; preserve-on-absent semantics. |
| Compaction policy | `internal/agentic/compaction_policy.go` (`DecideCompactionPolicy`) | Pure four-way primitive; 2b adds `remote_compact` / `fresh_window` as strategies, not new decisions. |

---

## 3. Non-negotiable invariants (every task must preserve)

1. **Append-only history** outside explicit compaction — never rewrite, drop, or reorder already-sent messages (Hard Rule 7).
2. **Fresh contexts get isolated identity** — `/clear`, fork, sub-agent, planner, summarizer each get their own cache/session key.
3. **Codex SSE shape unchanged** — `store=false`, `prompt_cache_key` present, **no** `previous_response_id` on the SSE path.
4. **Diagnostics carry bounded hashes only** — never raw session keys, prompts, OAuth tokens, tool arguments, or the turn-state token.
5. **Phases independently shippable & reversible** — each task lands behind its own gate with its own tests.

---

## 4. Common goal-scheduling fields

Apply these to **every** micro-task goal unless the task file overrides them:

- `freshContext: true` — the task file + referenced sections of this file are the whole context.
- `completionCriterion` — copy from the task's **Completion criterion** line.
- `verifyCommand` — copy from the task's **Verify** line.
- Suggested `set_budget`: tokens ≈ `120000`, turns ≈ `25` (tighten for the small 6c/2b.1 tasks).
- `handover` — copy from the task's **Handover** block (State / Decisions / Next steps / Risks).

### Handover template (for the successor goal's `handover`)

```text
State: <what is done + evidence: commits, test names, verify output>
Decisions: <constraints the successor must respect>
Next steps: <the successor task's first actions>
Risks: <what to double-check before proceeding>
```
