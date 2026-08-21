# Cache-miss RCA: export 20260819-090231

## Evidence

All three reports use model `k3-256k`, retain the same session key hash across predecessor/miss, and have matching provider/model. In every pair, the miss request's message list is an exact append of the predecessor: no prior message is rewritten, reordered, or removed. The miss appends exactly two messages: an assistant tool call and its tool result.

| Report | Sequences | Messages | Cache read | Gap | Fingerprint |
|---|---:|---:|---:|---:|---|
| 1 | 110 -> 111 | 254 -> 256 | 162560 -> 0 | 5299ms | exact_append -> param_change |
| 2 | 150 -> 151 | 95 -> 97 | 62464 -> 1792 | 2726ms | exact_append -> param_change |
| 3 | 163 -> 164 | 29 -> 31 | 10752 -> 1792 | 111879ms | exact_append -> param_change |

## Confirmed request mutation

The only non-message request-body change common to all three pairs is the final-step tool collapse:

- predecessor has 15 tool definitions and omits `tool_choice`;
- miss request omits the `tools` array and sets `tool_choice: "none"`;
- `model`, `reasoning_effort: "max"`, `stream`, and `stream_options` are unchanged.

The source confirms this is intentional behavior in `internal/agentic/agent_streaming.go` (`NoTools` final-step collapse) and provider protocol builders. It is not an accidental message mutation. However, it is a cache-relevant request-parameter mutation and is the strongest common causal candidate for all three misses.

## Root-cause assessment

1. **No stale-context resend or append-only violation proven.** Prefix messages are byte/structure-identical and only the expected assistant/tool pair is appended.
2. **Conversation identity did not change.** Session key hashes remain identical within each pair; this is not a fresh-context ID bug.
3. **Likely primary cause:** intentional switch from a tool-enabled request to a no-tools request (`tools` removed plus `tool_choice=none`) invalidated or reduced the provider prefix cache. The provider/classifier labels this `param_change`.
4. **Report 3 has a secondary plausible cause:** 111.879 seconds idle may permit provider TTL expiry, but the evidence cannot separate TTL from the parameter change because both occurred on the same request.
5. **No cache-affinity hint was sent**, so the provider had no explicit identity to preserve routing/cache locality.

## Follow-up

Added a bug-tracking follow-up: measure whether final-step collapse should use a cache-compatible request shape, or rotate/re-baseline cache identity when the intentional parameter change is unavoidable. Do not remove the safety behavior without provider compatibility tests.
