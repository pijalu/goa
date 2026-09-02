# Muse (OpenAI Responses API) — no thinking shown

**Status:** fixed & validated (2026-09-02).

## Report

**Observed:** With opencode `muse-spark-*` (served via the OpenAI Responses
API, per `model_overrides.yaml`), the TUI never showed any thinking, even on
text turns. Export `frigolite/.goa/exports/goa-export-20260902-220424.zip`
(issue "no thinking"): 20/20 requests succeeded, zero assistant content or
thinking events in `session/events.jsonl`.

**Root cause (validated live against `https://opencode.ai/zen/v1/responses`
with Goa credentials, 2026-09-02):** muse streams reasoning as
`response.output_item.added` (item `type:"reasoning"`) followed by
`response.reasoning_summary_part.added`, `response.reasoning_summary_text.delta`
/`.done`, `response.reasoning_summary_part.done`. Goa's Responses parser
(`internal/agentic/provider/protocol/openai_responses.go`, `parseResponsesSSE`)
dispatched only `output_text.delta`, `output_item.added`,
`function_call_arguments.delta/done`, `output_item.done`, `response.completed` —
every `reasoning_*` event fell through the switch and was silently discarded,
and both `output_item` handlers early-returned unless `type=="function_call"`.
The `EventThinkingStart/Delta/End` + `ContentBlockThinking` contract already
existed (anthropic + openai chat-completions parsers emit it); the Responses
parser never did.

**Expected:** reasoning summary deltas surface as thinking in the TUI and are
recorded as a `ContentBlockThinking` block on the final assistant message.

**Scope note:** tool-call turns stay silent upstream — muse sends `reasoning`
items with empty `summary:[]` and no text on those turns (verbosity:low), so
there is nothing to render; the fix surfaces thinking on text/summary turns.
Encrypted reasoning content is never replayed into input (stateless replay
contract), so no history-roundtrip change is needed.

## Fix plan & test approach

1. Write failing tests in
   `internal/agentic/provider/protocol/openai_responses_reasoning_test.go`
   replaying captured muse SSE streams:
   - text turn with two reasoning-summary parts → expect EventStart,
     EventThinkingStart, deltas, EventThinkingEnd, and a final message whose
     first block is `ContentBlockThinking` ("First thought.\nSecond thought."
     shape) followed by the text block.
   - tool-call turn with `summary:[]` reasoning items → expect no thinking
     events and no thinking block (upstream-silent case preserved).
2. Implement parsing in `openai_responses.go`:
   - `responsesEventContext` gains `thinkingBuf strings.Builder` +
     `thinkingOpen bool`.
   - dispatch adds `response.reasoning_summary_text.delta` →
     `handleResponsesReasoningSummaryDelta` and
     `response.reasoning_summary_part.done` →
     `handleResponsesReasoningSummaryPartDone` (part separator = `\n`).
   - `closeThinkingBlock` emits `EventThinkingEnd` with trailing newlines
     trimmed; `thinkingBlock` prepends `ContentBlockThinking` to the final
     message (both in `handleResponsesCompleted` and the no-`completed`
     fallback path).
   - switch extracted into `dispatchResponsesEvent` to keep
     `parseResponsesSSE` within the gocyclo ≤12 budget.

## Validation

- RED confirmed: reasoning tests failed on pre-fix code (no thinking deltas,
  no thinking blocks); the empty-summary test passed both before and after.
- `go test -count=1 -race -cover ./...` — all packages pass.
- `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
  `gocyclo -over 12 .` — clean (parseResponsesSSE back under budget after
  extracting dispatchResponsesEvent).
- Live probes (curl, Goa credentials) captured the ground-truth event
  sequences used by the tests: text turn = 686 output tokens (635 reasoning),
  2 summary deltas + text; tool turn = 105 output tokens, `summary:[]`
  reasoning items, one function_call.
