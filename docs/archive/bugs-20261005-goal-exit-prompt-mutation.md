# Goal exit mutated the cached prompt → full-prompt cache miss (kimi-code)

**Status:** fixed & validated (2026-10-05). Fix commit `8a9176cd`.

## Report

**Observed.** Export `goa-export-20261005-135830.zip` (provider `kimi-code`,
model `k3-256k`) carried two provider cache misses, both classified
`tool_policy_transition`. Raw provider frames (`logs/http.jsonl`):

| request | msgs | prompt_tokens | cached_tokens | tools in body |
|---|---:|---:|---:|---|
| 18 | 52 | 42,811 | **41,728** | present (9 schemas) |
| 19 | 54 | 41,086 | **1,792** | **absent**, `tool_choice:"none"` |

Request 19's message array is a byte-exact append of 18's (`sha256` of the
message prefix matches), so the only body delta was the removed tool surface.
39,294 tokens were recomputed at full price; the same pair earlier in the
session went 115,200 → 0. Latency: 12.3 s for the miss vs 8–9 s for its cached
siblings.

The warning reached the user at the moment of the goal *switch*, which is why it
read as reset-related. The reset itself is clean: the cache identity rotated
(`goa_3333e5f8…` → `goa_cd2c6252…`, Hard Rule #7 respected) and the fresh
chain's first request still landed a 4,608-token prefix hit. Both misses belong
to the goal that was *exiting*.

## Root cause

1. `tools/goal/goal.go:881` returns `StopTurn: true` for `Goal marked complete.`
   (same for goal pause `:792`, plan outcome `tools/plan/task_outcome.go:157`,
   plan exit `tools/plan/plan.go:310`).
2. `internal/agentic/agent_turn_lifecycle.go:55` arms `toolCollapseNextRound`.
3. `internal/agentic/agent_streaming.go:203` sets `pCtx.NoTools` on that round.
4. The protocol builders translated `NoTools` into "omit the tools array"
   (`openai_completions_messages.go:applyToolChoice` and the equivalents in
   anthropic/mistral/google/responses).

The tool schemas are serialized into the prompt, so removing them for one round
moved the provider's prefix-cache divergence point from the tail of the
conversation to right after the system prompt — the textbook way to convert an
append-only cache hit into a full re-read.

The same class of bug existed on the Codex WebSocket path, whose incremental
reuse fingerprint compares `tools`, `tool_choice` and `parallel_tool_calls`
(`internal/agentic/provider/openai_responses/ws_incremental.go:24-104`): the
collapse broke all three and forced a full replay.

## Fix

The invariant now enforced across all protocol builders: **while the context is
the same, the request is an append-only continuation of its predecessor — the
prompt-bearing surface (tool schemas, system prompt, history) never moves.** A
text-only collapse may only toggle a prompt-neutral control field:

- `tool_choice` on the OpenAI-completions, Anthropic and Mistral flavors, and
  Google's `toolConfig.functionCallingConfig.mode = NONE`.
- Where the upstream rejects `tool_choice:"none"` — strict Responses upstreams
  (opencode Zen / muse "Console", 2026-09-02, `bugs-20260902-responses-force-stop-tool-choice.md`)
  — the collapse round repeats the previous round's `tool_choice` verbatim, so
  the body is byte-append-only in *every* field. This is the default for the
  Responses flavors and the escape hatch for any other model:

```yaml
# per model, overrides the flavor default
compat:
  supports_tool_choice_none: false
```

- `parallel_tool_calls` is no longer dropped: it is fingerprint-compared by the
  Codex reuse path and cannot yield a tool call on its own.
- Cache forensics gained the `tool_choice_collapse` classification for the
  (cache-neutral) control-field-only shape; `tool_policy_transition` now means
  the buggy/legacy shape only, so a reintroduction is still reported with an
  actionable cause instead of being excused.
- `docs/PROVIDER-CACHE.md` §2 now states the contract for prompt-bearing
  fields, not just for messages.

Residual, documented: on upstreams that refuse `tool_choice:"none"` the
text-only intent is not enforced server-side, so a collapse round there can in
principle return a tool call; the turn guards (tool-round budget, recovery
rounds) bound it. Trading that soft guarantee for an append-only context is
deliberate — a mutation re-bills the whole prompt on every goal exit.

## Validation

- **Live wire probe** against `api.kimi.com/coding/v1` (`k3-256k`, ~3k-token
  prompt, 1 tool):

  | shape | prompt | cached | uncached |
  |---|---:|---:|---:|
  | append pair, tools kept + `tool_choice:"none"` | 3,111 | **2,816** | 295 |
  | append pair, tools removed + `tool_choice:"none"` (pre-fix) | 3,034 | **0** | 3,034 |

  Both HTTP 200; the tools-kept round returned `finish_reason: stop` with no
  tool call even when the prompt demanded one.
- `TestCollapse_PreservesCachedPromptSurface` (per flavor) and
  `TestCollapse_RealKimiProfileStaysAppendOnly` fail if any prompt-bearing
  field moves between a normal and a collapse round.
- `go vet ./...`, `gocognit -over 15`, `gocyclo -over 12` clean;
  `staticcheck ./...` reports only two pre-existing findings in `plugins/`,
  untouched by this change; `go test -count=1 -race ./...` green (88 packages).

## Prior art

`docs/archive/bugs-20260819-cache-rca.md` reached the same conclusion on
2026-08-19 for three misses in another export and left an unimplemented
follow-up ("measure whether final-step collapse should use a cache-compatible
request shape"). Only the classification was implemented
(`LikelyCauseToolPolicyTransition`, 2026-09-02), so goa labelled the miss
instead of avoiding it. This fix closes that follow-up.
