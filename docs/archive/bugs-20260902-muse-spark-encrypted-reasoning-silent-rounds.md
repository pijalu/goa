# opencode muse-spark: no thinking shown on tool-call turns (encrypted reasoning invisible)

**Status:** Fixed (2026-09-02). Approach adjusted mid-fix per maintainer direction
(follow pi/opencode): encrypted reasoning is **not** shown; it is instead
**accounted for** in the consecutive-tool-round limit.

## Observed

With provider `opencode` (OpenCode Zen Responses API) and model
`muse-spark-1.3-contributor-free` (`thinking_level: high`), no thinking block is
ever displayed during tool-calling turns (session export
`goa-export-20260902-224224.zip`, issue "no thinking"). Live probe against
`https://opencode.ai/zen/v1/responses` shows the model DOES reason on tool turns
(`output` carries `reasoning` items, `output_tokens_details.reasoning_tokens` >
0) but the items have **empty summaries** (`"summary":[]`) and the reasoning
content exists only as opaque `encrypted_content` (returned when the request
includes `include: ["reasoning.encrypted_content"]`). Since the Responses parser
only surfaces `reasoning_summary_text.delta` as thinking, these rounds produce
zero thinking events.

**Consequence:** every muse tool round counted as a *silent* tool round in
`Agent.trackToolCallingRound` (no content, no thinking), so the
consecutive-silent-tool-round limit (`execution.max_consecutive_tool_rounds`)
marched toward the forced-answer hint even though the model was actively
reasoning.

## Fix (final behavior)

Encrypted thinking is **not rendered** (matches pi/opencode). Instead the
invisible reasoning is surfaced to the agent via usage metadata so the
silent-streak accounts for it:

1. **Parser** (`internal/agentic/provider/protocol/openai_responses.go`):
   `handleResponsesCompleted` now decodes `output_tokens_details.reasoning_tokens`
   and copies it into `schema.Usage.ReasoningTokens`. No synthesized thinking
   block is emitted; a round whose reasoning exists only as `encrypted_content`
   (empty summary) produces no thinking events, exactly like pi/opencode.
2. **Agent** (`internal/agentic/agent_stream_events.go` `captureStreamResult`):
   when the round's `result.Usage.ReasoningTokens > 0`, set `turnSawThinking =
   true`. `trackToolCallingRound` (run after `consumeStream` → `handleStreamDone`
   → `captureStreamResult` in `runStreamRound`) then treats the round as
   reasoning and resets the silent tool-round streak instead of incrementing it.
   No `StateThinking` event reaches observers, so the TUI shows nothing.

## Validation

Replayed against the REAL captured streams in
`internal/agentic/provider/protocol/testdata/`:

- `muse_spark_tool_turn.sse` (encrypted reasoning items, empty summaries, 27
  reasoning tokens): **no** thinking start/delta/end events, **no**
  `ContentBlockThinking` on the final message, tool call intact
  (`bash`, `{"command":"pwd && ls -la"}`), and `Usage.ReasoningTokens == 27`.
- `muse_spark_text_turn.sse` (visible summaries): real summary text streams as
  thinking, no placeholder, `ReasoningTokens` positive.
- Agent level: replaying the tool turn's stream result sets `turnSawThinking`
  (no visible thinking buffered/shown) so `trackToolCallingRound` reports
  not-silent and resets the streak; a round with **zero** reasoning tokens still
  counts toward the limit (guard not blanket-disabled).

Quality gate (each run separately): `go vet ./...`, `staticcheck ./...`,
`gocognit -over 15`, `gocyclo -over 12`, `go test -count=1 -race -cover ./...`
— all pass.
