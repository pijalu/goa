# z.ai / Provider Connection Review — 2026-09-30

**Author:** review pass over two Goa diagnostic bundles
**Date:** 2026-09-30
**Status:** analysis complete, fixes scheduled in `bugs.md`

---

## 1. Scope and evidence base

Two diagnostic exports were analysed:

| Export | Workspace | Provider | Model | Window | Issue text |
|---|---|---|---|---|---|
| A | `/Users/muaddib/dev/creaves.project` | `zai` | `glm-5.3-flash` | 22:56:33 → 23:01:37 | "lots of error/disconnect" |
| B | `/Users/muaddib/dev/goa` | `opencode-go` | `space-bunny-free` | 23:08:05 → 23:11:02 | "Connection error also on space-bunny-free" |

Export B is the decisive control: **a completely different provider, on a different
transport, reproduces the identical failure signature.** That separates
provider-specific defects from provider-agnostic ones.

Reference implementations compared against:

- `/Users/muaddib/dev/ZCode` — vendor's own client for z.ai
- `/Users/muaddib/dev/pi` — independent z.ai/OpenAI-completions implementation
- `/Users/muaddib/dev/opencode` — third implementation, documents a z.ai quirk

---

## 2. Headline finding

**The "multitude of errors / disconnects" is not a z.ai problem. It is a goa
stall-watchdog defect that fires on any provider whose SSE stream stops
terminating cleanly — and it fires *after the answer has already been fully
received*.**

Both providers deliver HTTP 200, stream the complete response body, and then
hold the connection open without ever sending `[DONE]` or a `finish_reason`
chunk. Goa waits, declares a stall, **throws the completed answer away**, and
re-issues the whole request.

### 2.1 Export B — line-level proof

`logs/agent.log`:

```
23:09:57.881 [WARN] provider sent unmapped event type "start" ...
23:09:57.881 [TRACE] [delta] content: Evidence
23:09:57.881 [TRACE] [delta] content:  is complete. Writing
23:09:57.881 [TRACE] [delta] content:  the review document.
        <-- 45.001 s of complete silence -->
23:10:42.881 [INFO] provider quiet for 45s — still waiting; will auto-retry after 1m0s of silence
        <-- 15.001 s of silence -->
23:10:57.882 [WARN] Stream stalled: no events received for 1m0s
23:10:57.882 [WARN] stream failure: stream stalled: no events received from provider for 1m0s
23:10:57.882 [WARN] stream error, retrying: stream stalled: ...
23:10:57.882 [INFO] retry scheduled: provider=opencode-go mode=normal attempt=1/15 delay=1s code=TRANSPORT
23:10:58.883 [INFO] retry started: provider=opencode-go mode=normal attempt=1/15
```

`logs/http.jsonl` request 19 confirms the response was already complete:

```
dur 62357  status 200  finishReason null
[DONE]?           False
finish_reason?    False
final chunk:  ..."content":" the review document."}...  (truncated mid-stream, no terminator)
```

The interval from last byte to stall kill is **60.001 s** — exactly the
configured `execution.activity_timeout`.

### 2.2 Export A — same signature, byte-level guard this time

```
22:59:15.926 [WARN] provider sent unmapped event type "start" ...
        <-- silence -->
23:00:00.929 [INFO] provider quiet for 45s — ... after 1m0s of silence
23:00:15.928 [WARN] stream failure: sse stream read failed: stream idle timeout: no data received from LLM
23:00:15.928 [INFO] retry scheduled: provider=zai mode=normal attempt=1/15 delay=1s code=TIMEOUT
23:00:16.929 [INFO] retry started: provider=zai mode=normal attempt=1/15
```

Requests 19, 20 and 21 of export A all show `status 200`, `finishReason null`,
no `[DONE]`, no `finish_reason` chunk, and no `usage` block. Durations
66 s / 78 s / in-flight.

Note that export A trips the **byte-level** reader
(`provider.ErrStreamIdle`, `internal/agentic/provider/idle_timeout.go:17`) while
export B trips the **event-level** watchdog. Both are driven by the *same*
config key, `execution.activity_timeout` (60 s in `config/user.yaml`), wired at
`provider/manager_streamopts.go:38-46`. Two guards, one budget, racing each other.

### 2.3 Consequence

Per stall, goa:

1. discards a complete, correct answer;
2. burns a full 60 s wall-clock of dead time;
3. re-sends the entire conversation (95 k+ prompt tokens on export A) and re-runs
   every tool call in the turn — duplicate side effects;
4. surfaces `Reconnecting (attempt 1/15)…` then `Connection error` to the user.

That is precisely the "lots of error/disconnect" the user reported.

---

## 3. Confirmed defects

### D1 — CRITICAL — stall watchdog discards already-complete streams

**Where**
- `internal/agentic/agent_streaming.go:380-386` — `watchdog := time.AfterFunc(stallTimeout, …)`
- `internal/agentic/agent_streaming.go:405-412` — watchdog re-armed only when `noteStreamEventProgress` returns true
- `internal/agentic/provider/idle_timeout.go:112-134` — byte-level `Read` abort
- `internal/agentic/agent_stream_retry.go:16-24` — `undoLastAssistantMessage()` wipes the received answer

**Defect.** There is no notion of "the model has finished; the socket is merely
still open". A provider that streams the complete answer and then holds the
connection without `[DONE]` is indistinguishable, to goa, from a genuinely hung
provider. The turn is killed and replayed.

**Evidence.** Both exports; see §2.

**Aggravating factor.** `execution.activity_timeout` is `60s` in the user's
`~/.goa/config/user.yaml`, but the shipped default is `2m`
(`provider.DefaultStreamIdleTimeout`, `idle_timeout.go:21`). 60 s is short for a
reasoning model: export A request 17 legitimately ran 80.7 s and produced 4 268
reasoning tokens before its first tool call.

---

### D2 — MEDIUM — `EventStart` is unmapped: no watchdog re-arm + log noise on every request

**Where**
- `internal/agentic/agent_stream_events.go:15-23` — `streamEventHandlers` map
- `internal/agentic/agent_streaming.go:463-472` — `noteStreamEventProgress`
- `internal/agentic/provider/protocol/openai_completions.go:298-303` — `ensureStarted()` emits `EventStart`

**Defect.** `streamAccum.ensureStarted()` pushes `schema.EventStart` as the
first event of every OpenAI-completions stream, but `provider.EventStart` is
absent from `streamEventHandlers`. Consequences:

1. it is classified "unmapped" → **does not re-arm the stall watchdog**;
2. it logs a `WARN` on **every single stream** — 4 occurrences in export A,
   ~40 in export B;
3. the message text blames the provider — *"provider sent unmapped event type"* —
   for what is a goa-internal lifecycle event, which actively misdirects
   diagnosis toward the provider.

**Evidence.** `grep -c 'unmapped event type' logs/agent.log` → non-zero on every
request in both exports, on two unrelated providers.

---

### D3 — MEDIUM — retry storm amplifies cost and cache pressure

**Where** `internal/agentic/agent_stream_retry.go:128-146`, budget `1/15`.

Each stall costs 60 s of silence plus a full-prompt resend. Export A burned
three consecutive 60–78 s requests (19, 20, 21) on a single turn.

On top of that, `logs/cache_miss_requests.json` in export A records one prefix
cache miss:

```
likely_cause: ttl_expiry
prev_cache_read_tokens 96704 → cache_read_tokens 95168
gap_since_prev_response_ms 187088
```

A 187 s gap exceeds the z.ai prefix-cache TTL, so the retry re-paid for ~95 k
prompt tokens instead of reading cache. The stall-and-retry loop is therefore
self-amplifying in cost.

---

### D4 — z.ai HIGH — `tool_stream: true` is never sent

**Where**
- `internal/agentic/provider/compat.go:25` — `ZaiToolStream *bool` (declared)
- `internal/agentic/provider/protocol/openai_completions.go:77` — `ZaiToolStream bool` (declared)
- `internal/agentic/provider/compat_detect.go:118` — `ZaiToolStream: boolPtr(false)` (hardcoded)
- `internal/agentic/provider/protocol/openai_completions.go:104-125` — `buildOpenAIParams` never reads it

**Defect.** The flag is threaded through detection and then **dead**. Nothing
ever writes `tool_stream` into the request body.

`pi` sends it — `packages/ai/src/api/openai-completions.ts:851-853`:

```ts
if (transcriptTools.requestTools.length > 0) {
    params.tools = convertTools(transcriptTools.requestTools, compat);
    if (compat.zaiToolStream) {
        (params as any).tool_stream = true;
    }
}
```

and pi's generated z.ai catalog sets `compat.zaiToolStream: true` for every
GLM-4.6 → GLM-5.3 model (`packages/ai/test/zai-coding-plan-models.test.ts:20`).

**Proof on the wire** — the real request body captured in export A
(`logs/cache_miss_requests.json` → `requests[0].body`) has exactly these keys:

```
messages, model, prompt_cache_key, prompt_cache_retention,
stream, stream_options, thinking, tools
```

No `tool_stream`. Goa speaks the z.ai dialect one version behind `pi`.

---

### D5 — z.ai MEDIUM — no `max_tokens`, so reasoning can consume the whole turn

Same captured body: no `max_tokens` and no `max_completion_tokens`. The user's
model profile (`config/user.yaml`) sets `thinking_level: xhigh` and
`max_tokens: 0`.

`pi` documents precisely this hazard in its compat type
(`packages/ai/src/types.ts:834-840`):

> *"Reasoning and the answer share `max_tokens` on these endpoints, so without a
> budget a reasoning-heavy turn can consume the whole response and emit no
> answer."*

Export A request 17 spent 4 268 reasoning tokens before its first tool call, and
the whole turn ran 80.7 s. Uncapped, a harder turn can simply run until the
stall watchdog kills it — feeding D1.

---

### D6 — z.ai MEDIUM — context overflow is accepted silently

`opencode` records the quirk explicitly
(`packages/opencode/src/provider/error.ts:31`):

```
// Providers not reliably handled in this function:
// - z.ai: can accept overflow silently (needs token-count/context-window checks)
```

Goa's z.ai model profile has `context_window: 0`, so no proactive guard exists,
and `OnContextError` compression recovery only triggers on an actual
context-length error — which z.ai may never raise. Overflow would present as
silent quality degradation instead of a recoverable error.

---

## 4. Feature gap — z.ai Coding Plan quota **reset** API

Not a bug. A missing capability that ZCode ships and goa does not.

`goa` currently fetches only quota consumption, via
`plugins/bundled/provider-quota/fetchers/zai.js`:

```
GET {origin}/api/monitor/usage/quota/limit
```

`ZCode` additionally implements the **Coding Plan reset** surface
(`packages/services/src/usage-stats/providers/bigmodelUsageQuotaProvider.ts`):

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/coding-plan/reset/status` | GET | available 5-hour / week reset tokens, last-used history, unread flag |
| `/api/v1/coding-plan/reset/opportunity` | POST | claim a reset grant (idempotency key) |
| `/api/v1/coding-plan/reset/use` | POST | consume a reset (`FIVE_HOUR` \| `WEEK`) |
| `/api/v1/coding-plan/reset/history/read` | POST | clear the unread-history cursor |

Notable contract details worth preserving:

- business envelope `{ code, msg, data }`; **`code 3301` = throttled**, and its
  `next_try_at` must be passed through verbatim or the client re-polls into the
  rate limit;
- bare HTTP 429 must also map to the same cooldown;
- `idempotency_key` is mandatory, ≤ 64 chars, on both mutating calls;
- auth is dual: `zcodejwttoken` (Bearer) **plus** the family OAuth token
  (`oauth:zai:access_token`), sent raw — no `Bearer` prefix — with **no
  cross-family fallback** (zai vs bigmodel).

> Note: the literal token "coupon" appears only once in the whole ZCode tree, in
> an unrelated bash-command registry. The feature is named **coding-plan reset**,
> not coupon. The user's "coupon" maps onto the reset-opportunity flow.

Implementation shape should follow the existing fetcher convention — a new
`plugins/bundled/provider-quota/fetchers/zai-coding-plan.js` registered in
`plugin.js`, reusing `lib/http-quota.js`, with the reset state surfaced through
`/quota :resets` and the status-bar segment.

---

## 5. Provider comparison — what ZCode / pi / opencode do differently

| Concern | goa | pi | ZCode | opencode |
|---|---|---|---|---|
| z.ai endpoint | `api.z.ai/api/coding/paas/v4` (OpenAI-compat) | same | **`api.z.ai/api/anthropic`** (Anthropic Messages) | OpenAI-compat |
| `tool_stream: true` | **never sent** | sent, `compat.zaiToolStream` | n/a (Anthropic) | not sent |
| `thinking` body | `{type:enabled, clear_thinking:false}` | same | n/a | same |
| `reasoning_effort` | suppressed (correct) | suppressed | n/a | — |
| `max_tokens` cap | **absent** | optional | n/a | — |
| Cache identity | `prompt_cache_key` + `prompt_cache_retention: 24h` | same | — | — |
| Context-overflow handling | error-driven only | token-count driven | — | **documents z.ai silent accept** |

The endpoint divergence (ZCode uses the Anthropic surface for coding plans) is
the most interesting open question: it is plausible that the Anthropic surface
terminates SSE cleanly, which would sidestep D1 entirely for z.ai. That was a
**hypothesis**, and it has now been **live-probed — 69 streaming requests across
both surfaces, all terminated cleanly** (OpenAI-compat emits `[DONE]` +
`finish_reason`, Anthropic emits `message_stop` + `stop_reason`; gap between last
event and close was 0.0 s everywhere, longest stream 64.3 s). The OpenAI-compat
surface did *not* reproduce the held-open signature from the exports, so the
Anthropic surface offers no termination advantage and **no migration was made**.
Evidence and method: `docs/research/zai-sse-probe-20260930.md`.

---

## 6. Fix plan summary

Ordered by severity. Full plans, test approach and validation steps live in
`bugs.md`.

| # | Severity | Fix | Area |
|---|---|---|---|
| 1 | CRITICAL | Treat "content complete, socket still open" as a soft end-of-stream, not a stall. Distinguish *productive* silence from *post-answer* silence. | `internal/agentic` |
| 2 | MEDIUM | Map `provider.EventStart` in `streamEventHandlers`; drop the misleading provider-blaming WARN | `internal/agentic` |
| 3 | MEDIUM | Emit `tool_stream: true` for z.ai when tools are present; wire the already-declared compat flag | `internal/agentic/provider/protocol` |
| 4 | MEDIUM | Send a `max_tokens` cap for reasoning-capable providers | `internal/agentic/provider/protocol` |
| 5 | MEDIUM | Raise the shipped `activity_timeout` default; document the reasoning-model trade-off | `config` |
| 6 | LOW | Guard z.ai context overflow by token count, not only by error | `internal/agentic` |
| 7 | FEATURE | z.ai Coding Plan reset fetcher (`status`/`opportunity`/`use`) | `plugins/bundled/provider-quota` |

---

## 7. Reproduction notes

Defects D1–D3 reproduce from the shipped bundles with no live provider: any
`httptest` SSE server that writes complete content chunks, then holds the
connection open without `[DONE]`, reproduces the exact 60 s stall → undo →
retry cycle. D4–D6 are request-shape assertions against the built payload.