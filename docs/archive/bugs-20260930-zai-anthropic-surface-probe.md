<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Investigation: does z.ai's Anthropic surface terminate SSE cleanly? — no migration (RESOLVED — hypothesis rejected)

Date: 2026-09-30 · Status: RESOLVED — live-probed, hypothesis **not** supported, no provider change made.

## Reported

> ZCode talks to z.ai coding plans over `https://api.z.ai/api/anthropic`
> (Anthropic Messages), while goa and pi use the OpenAI-compat
> `.../api/coding/paas/v4`. Both known-bad streams came from the OpenAI-compat
> surface (zai and opencode-go).
>
> **Hypothesis (unverified).** The Anthropic surface terminates SSE cleanly and
> would sidestep the held-open-stream stall
> (`docs/archive/bugs-20260930-stall-held-open-complete-answer.md`) for z.ai.
>
> **Plan.** Live-probe both surfaces with an identical prompt and compare whether
> `[DONE]` / a terminal event is always emitted. Record the result in the review
> doc. Do **not** migrate the provider without that evidence.

## Method

`scripts/repro/zai-sse/` — byte-level SSE probe (`zai_probe.py`) plus two matrix
runners and an Anthropic-surface capability spot-check. The key is read from
`~/.goa/config.yaml` at runtime and never written to disk or printed. Every SSE
line is timestamped off the socket so the "terminator then close" case is
distinguishable from "terminator then silence".

Matrix (69 streaming requests, `glm-5.3-flash`):

- text-only turns, ~400-word long turns, tool-definition turns with and without
  `tool_stream:true`, prompts that force a real tool call, and turns with
  `max_tokens` omitted entirely (the pre-D5 export-A shape);
- reasoning on and off on the OpenAI-compat surface;
- 3 runs per cell.

## Result

| Surface | Endpoint | Terminator | Probes | Clean | Held open |
|---|---|---|---|---|---|
| OpenAI-compat | `POST /api/coding/paas/v4/chat/completions` | `data: [DONE]` + `finish_reason` (`stop`/`tool_calls`) | 42 | 42 | 0 |
| Anthropic | `POST /api/anthropic/v1/messages` | `event: message_stop` + `stop_reason` (`end_turn`/`tool_use`) | 27 | 27 | 0 |

`gap_after_last_event_s == 0.0` in **all 69** probes; the longest stream (64.3 s,
inside the duration band where export A stalled) also closed immediately after
its terminator. The Anthropic surface additionally accepts `glm-5.3-flash`,
`glm-5.3`, `glm-5.2`, `glm-4.6`, both thinking dialects, `tools` +
`tool_choice:{"type":"any"}` (emits `tool_use` with `input_json_delta`),
`system`, non-streaming mode, and `prompt_cache_key`.

**Verdict: hypothesis rejected.** The OpenAI-compat surface terminates cleanly
under every shape the probe exercised, including the pre-fix shapes the exports
used. There is no evidence-backed reason to migrate z.ai to `/api/anthropic`, and
migration would cost a new protocol path, drop `tool_stream`, and require cache
identity rework (`prompt_cache_key` is ignored by the Anthropic surface). Per the
plan's "do not migrate without evidence", **the provider stays on
`…/api/coding/paas/v4`**.

The held-open streams in the exports remain unexplained but are *not* evidence for
a surface change: 42 probes over 20 minutes did not reproduce them, and export B
reproduced the same signature on a different provider and transport. Goa's D1 fix
(`roundDeliveredCompleteAnswer`, `internal/agentic/agent_stream_stall.go`) already
absorbs a complete answer followed by silence, which covers the residual risk
without touching the provider.

## Incidental finding

The coding-plan OpenAI-compat endpoint rejects array/object message content
(`content: [{"type":"text","text":…}]`) with `400 {"code":"1210","message":"Invalid API parameter"}`
— string content is required (verified live, with and without `tools`, and
independent of `max_tokens`). Goa sends string content for text turns
(`buildUserContent`, `openai_completions_messages.go:209`), so no code change is
needed; an image/multimodal turn on that endpoint would fail with `400`/`1210`,
which nothing in goa currently guards or documents.

## Evidence

- Probe report (method, tables, reproduce steps): `docs/research/zai-sse-probe-20260930.md`
- Raw per-probe JSON: `docs/research/zai-sse-probe-20260930/matrix.jsonl`, `…/matrix2.jsonl`
- Review doc §5 updated from "hypothesis" to the verified finding.