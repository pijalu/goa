# Kimi-code cache/compaction comparison for Goa

Date: 2026-08-19
Reference: `/Users/muaddib/dev/kimi-code`

## Executive conclusion

Goa already has the central cache-affinity mechanism: session-scoped IDs are sent as OpenAI `prompt_cache_key`, fresh contexts rotate IDs, and cache-forensics records the wire body. The Kimi-code reference does not reveal a missing secret Kimi header or a separate cache API. Its stronger behavior is operational: it makes the cache key explicit in the provider/request contract, preserves it through all requests including compaction, detects cache breaks while excluding expected compaction drops, and presents an idle-expiry warning before submitting after a long pause.

The main Goa opportunity is therefore **Kimi-specific policy and observability**, not a blind protocol change.

## Evidence from Kimi-code

### Affinity

- `packages/agent-core/src/session/provider-manager.ts` accepts `promptCacheKey` and passes it to Kimi provider `generationKwargs` as `prompt_cache_key`.
- `packages/node-sdk/src/kimi-code-model-provider.ts` does the same for the SDK provider.
- `packages/agent-core-v2/src/kosong/provider/bases/openai/openai-legacy.ts` maps request `cacheKey` to `prompt_cache_key`, with a hook override.
- Tests in `packages/agent-core-v2/test/kosong/provider/composition.test.ts` assert the exact wire field.
- Kimi's `cacheKey` is session-stable; tests assert it survives compaction on the WebSocket path. The transport may use previous-response chaining where supported, but SSE keeps full history and the cache key.
- No Kimi-specific `affinity` header or alternate cache token protocol was found in source. `prompt_cache_key` is the relevant mechanism.

### Cache-break handling

`apps/kimi-code/src/tui/controllers/cache-hint-controller.ts` records every completed step's cache usage. It flags a break when cache-read tokens fall below 95% of the previous step and by more than 2,000 tokens, including model/effort, before/after usage, ratio, and interval. It deliberately skips all-zero usage and resets the baseline after compaction because a compaction shrink is expected.

It also tracks completed provider activity—not merely turn start—and offers a cache-expiry dialog on resume or idle submit. The dialog avoids repeated prompts per idle cycle and serializes swallowed/released submits.

### Compaction

Kimi's full compaction:

- refuses manual compaction during an active turn;
- blocks the turn at auto-compaction boundaries;
- defers prompts arriving during compaction until summary/reinjection completes;
- refreshes system prompt and reinjects reminders before clearing the compacting state;
- tracks compaction counts and overflow retry limits;
- records compaction lifecycle events and result token counts;
- resets cache-break baseline after compaction.

The older micro-compaction suite is currently skipped/disabled, while full compaction is the supported path. Kimi tests explicitly cover repeated summaries, deferred prompts, failure/cancellation, overflow, and post-compaction invariants.

## Goa comparison

| Area | Goa | Assessment |
|---|---|---|
| Kimi `prompt_cache_key` | Generic OpenAI-completions path derives identity from `PromptCacheKey` or `SessionID`; Kimi catalog currently does not opt into long retention by default. | **Potential gap:** Kimi requests may omit the key when global retention is `none`/short because the generic emission gate is conservative. Verify Kimi endpoint acceptance and default policy. |
| Stable session identity | `AgentManager` creates/rotates IDs; `SetStreamOptions` preserves live ID; sub-agent IDs are dedicated. | Strong; matches reference invariant. |
| Cache-forensics | Captures complete predecessor/miss requests, fingerprints, gaps, affinity presence, and likely cause. | Stronger raw evidence than reference. |
| Cache-break classification | Counts zero-read and significant drops; compaction-aware micro gate exists. | Good, but intentional request-shape changes (e.g. final-step no-tools collapse) are currently classified as ordinary misses. |
| Idle expiry UX | No equivalent Kimi-specific pre-submit cache-expiry dialog found in Goa. | Potential product/diagnostic gap, not correctness requirement. |
| Compaction mutation | Goa has cache-aware micro-compaction deferral, remote compaction, fresh-window, provenance events, and ordered fallback. | More strategy options; must ensure every mutation rotates/re-baselines or is deferred. |
| Compaction lifecycle safety | Goa emits provenance triples and guards cache key generation. | Comparable; add explicit deferred-input and baseline tests where missing. |
| Kimi request parameters | Goa's final-step collapse removes `tools` and sets `tool_choice=none`. | Confirmed cache-risk: this caused all three reviewed misses in the supplied export. |

## Potential Goa updates (prioritized)

### P0 — Kimi wire-policy experiment and regression test

Determine empirically whether `api.kimi.com/coding/v1` accepts and benefits from `prompt_cache_key` and `prompt_cache_retention`. Add a Kimi-specific compatibility/catalog flag rather than assuming all OpenAI-compatible endpoints behave alike. Test two identical append-only sequences with and without the key; record cache reads, request body, and provider response. Do not enable long retention globally without evidence.

### P0 — Keep cache-affinity through all Kimi request variants

Add a wire-capture regression test for Kimi normal turns, tool rounds, final no-tools collapse, retries, recovery rounds, and compaction requests. Assert the same `prompt_cache_key` on every request in one conversation and a new key after fresh-window/context replacement. This directly addresses the reviewed `tools -> none` transition.

### P1 — Classify expected parameter transitions separately

Extend cache-forensics fingerprint classification with an explicit `expected_control_transition` (or `tool_policy_change`) for intentional no-tools/recovery/final-step requests. Keep reporting the token loss, but avoid presenting an intentional safety transition as unexplained provider eviction. Compare report counts separately from provider cache failures.

### P1 — Add compaction-aware Kimi baseline behavior

When Goa applies any history mutation, record the cache generation/key and reset the comparison baseline exactly as Kimi does after compaction. For in-place micro mutation, retain the current hot-cache deferral; for fresh-window/remote replacement, rotate the key. Add tests for compaction followed by first cold request, then a real post-compaction bust.

### P2 — Optional idle cache-expiry hint

Consider an opt-in Kimi UX warning after the provider's measured idle threshold. It should be advisory, never block normal sends permanently, and must not preemptively mutate history. Goa's existing cache-forensics gap data can supply the measured evidence.

## Confirmed missing versus unknown

**Confirmed missing:** no Kimi-specific idle-expiry dialog; no explicit Kimi catalog default enabling affinity/long retention; no distinct classification for intentional tool-policy changes.

**Not confirmed missing:** a special Kimi affinity header, a different cache key field, or a compaction algorithm required for Kimi. Source evidence shows `prompt_cache_key` is the mechanism and Goa already implements it generically.

## Recommended next implementation slice

1. Add Kimi wire tests for prompt-cache-key presence/retention and final-step collapse.
2. Add a Kimi catalog compatibility decision based on live probe/documented endpoint behavior.
3. Add `expected_control_transition` classification for no-tools/recovery requests.
4. Validate with a real Kimi session export before changing defaults.
