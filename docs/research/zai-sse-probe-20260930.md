# z.ai SSE termination probe — 2026-09-30

Live probe of the two z.ai inference surfaces, run to close the open question in
`bugs.md` §4 and `docs/research/zai-connection-review-20260930.md` §5:

> *Hypothesis (unverified): the Anthropic surface terminates SSE cleanly and
> would sidestep the held-open-stream stall for z.ai.*

## Verdict

**Hypothesis not supported — no provider migration.**

Across **69 instrumented streaming requests** (39 short/long/tool matrix +
30 tool-call + unbounded-`max_tokens` matrix) against both surfaces, **every
single stream ended with a terminal event and a server-side close**:

| Surface | Endpoint | Terminator observed | Probes | Clean terminations | Held open |
|---|---|---|---|---|---|
| OpenAI-compat | `POST /api/coding/paas/v4/chat/completions` | `data: [DONE]` + `finish_reason` (`stop` / `tool_calls`) | 42 | 42 | 0 |
| Anthropic | `POST /api/anthropic/v1/messages` | `event: message_stop` + `stop_reason` (`end_turn` / `tool_use`) | 27 | 27 | 0 |

`gap_after_last_event_s == 0.0` in every probe: the server closes the body in the
same read that delivers the terminator. Longest stream: 64.3 s (the duration band
where export A stalled), still clean.

Because the *current* OpenAI-compat surface already terminates cleanly, moving
z.ai to `/api/anthropic` buys nothing on termination behaviour, while costing a
new protocol path, losing `tool_stream`, and needing cache-identity rework
(`prompt_cache_key` is ignored there). Per `bugs.md` §4 ("do not migrate without
that evidence") the provider stays on `…/api/coding/paas/v4`.

## Why the exports still showed held-open streams

The held-open signature in exports A/B (§2 of the review) is therefore **not a
property of the z.ai OpenAI-compat surface** — it was not reproducible in 42
probes spanning the same model, tools, reasoning and duration range. It remains
a real observation from that session, but it is *not* evidence for a surface
change. Two readings fit the evidence, and neither requires a migration:

1. **Transient upstream behaviour.** The exports were a single ~3-minute window
   on 2026-09-30; both affected a *different* provider (`opencode-go` in export
   B) on a different transport, which points at an endpoint/LB-side event rather
   than a per-surface protocol defect.
2. **Request-shape dependent.** Export A's captured body had **no `max_tokens`**
   (defect D5) and no `tool_stream` (D4). The probe covers the fixed shapes
   (`tool_stream:true`, `max_tokens:4096`) and the pre-fix unbounded shape; all
   terminate. If the hold was triggered by something in the uncapped long-reason
   + tool loop path, it is not reproduced by static single-turn probes.

Goa's own D1 fix (`roundDeliveredCompleteAnswer`, `agent_stream_stall.go`)
already treats a complete answer as a soft end-of-stream, which covers reading
(2) without any provider change.

## Incidental finding — request-shape constraint (not a bug in goa)

The coding-plan OpenAI-compat endpoint rejects the OpenAI array/object content
form for messages with **`400 {"code":"1210","message":"Invalid API parameter"}`**
when `content` is `[{"type":"text","text":…}]` (verified live, with and without
`tools`, and independent of `max_tokens`). String `content` is accepted.

Goa is unaffected for text turns: `buildUserContent`
(`internal/agentic/provider/protocol/openai_completions_messages.go:209`) returns
a plain string unless the message carries an image block. An image/multimodal
turn on the z.ai coding endpoint would therefore fail with `400`/`1210`; recorded
here because nothing else in goa guards or documents that limit.

## Probe details

**Key handling.** The API key is read from `~/.goa/config.yaml` at runtime and
never printed or written to disk by the probe. Raw captures contain request/response
SSE bodies only, no credentials.

**Modes.**

| mode | request shape |
|---|---|
| `plain` | text-only turn, `"Reply with exactly the word OK."` |
| `long` | ~400-word essay turn (exercises 2000+ SSE chunks, 30–60 s) |
| `tools` / `tools-noflag` | tool definitions ± `tool_stream:true` (D4 fixed vs pre-fix shape) |
| `long-tools` / `long-tools-noflag` | long turn + tools |
| `force-tool` | prompt that makes the model actually emit a tool call |
| `unbounded-*` | `max_tokens` omitted entirely (export-A pre-D5 shape) |

`think=on|off` toggles the z.ai `thinking:{type:enabled,clear_thinking:false}`
body field on the OpenAI-compat surface.

**Method.** `zai_probe.py` reads the SSE body one byte at a time off the socket,
timestamps every complete SSE line, and classifies the end of stream:

- `terminated` — a terminal marker was seen (`[DONE]` / `message_stop` /
  `finish_reason` / `stop_reason`), regardless of whether EOF followed;
- `no_terminator` / `held_open_no_terminator` — clean EOF without a marker, or
  no further bytes within the 20 s idle window (connection still open).

`gap_after_last_event_s` is the time from the last SSE line to the end of the
observed stream. Reading byte-at-a-time (rather than relying on curl/EOF) is what
makes the "held open" case distinguishable from a clean close.

**Anthropic-surface capability spot-check** (`round3.py`, all HTTP 200):

- models `glm-5.3-flash`, `glm-5.3`, `glm-5.2`, `glm-4.6` all stream to
  `message_stop` (unknown model → `400 [1211][Unknown Model]`, as expected);
- thinking accepted in both the Anthropic (`budget_tokens`) and z.ai
  (`clear_thinking`) dialects;
- `tools` + `tool_choice:{"type":"any"}` → `tool_use` blocks with
  `input_json_delta` streaming and `stop_reason:"tool_use"`, then `message_stop`;
- `system`, non-streaming mode, and `prompt_cache_key` all accepted.

## Reproduce

```
python3 scripts/repro/zai-sse/run_matrix.py  glm-5.3-flash 3   # 33 probes
python3 scripts/repro/zai-sse/run_matrix2.py                   # 30 probes
python3 scripts/repro/zai-sse/round3.py                        # capability spot-check
```

Raw per-probe JSON: `matrix.jsonl`, `matrix2.jsonl` (this directory).
Wall time ≈ 20 min for the full matrix at the observed latencies.