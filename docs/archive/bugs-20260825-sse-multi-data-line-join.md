<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bugs closed 2026-08-25 — SSE multi-data-line join

Moved from bugs.md per guideline workflow.

---

## SSE parsers drop the newline when an event carries multiple `data:` lines (spec violation)

**Observed behavior.** RFC 9110 / WHATWG SSE spec: when one event carries
several `data:` lines, they must be joined with `\n` to form the event payload.
Three of the four SSE parsers in the repo violated this:

1. `internal/agentic/provider/protocol/anthropic_messages.go`
   (`parseAnthropicEventStream`): consecutive `data:` lines were concatenated
   with no separator — payloads silently merged.
2. `internal/agentic/provider/anthropic/sse.go` (`anthropicSSEState`): same
   missing-`\n` join.
3. `internal/agentic/provider/sse.go` (`ParseSSE`, OpenAI-style): emitted each
   `data:` line as its own payload; a server that split one JSON object
   across multiple `data:` lines fed invalid JSON fragments to callers
   (google/provider.go, bedrock/provider.go, openai_responses/provider.go,
   mistral/provider.go, openai/stream.go).

Not affected: `internal/agentic/provider/transport/sse.go` already joined
multi-line data with `\n` correctly.

**Fix applied.**
1. Both Anthropic parsers now write `'\n'` before appending a `data:` line when
   the builder is non-empty (`anthropicSSEState.appendData`; inline guard in
   `protocol.parseAnthropicEventStream`) — mirrors `transport.appendData`.
2. OpenAI-style `ParseSSE` accumulates consecutive `data:` lines into an event
   buffer and flush-and-emits the joined payload when a non-`data:` line
   arrives (blank line, `event:`, comments), at `[DONE]`, and at clean EOF;
   on mid-stream scanner errors the incomplete buffered tail is dropped and
   the error propagates unchanged. Empty single-line payloads still emit
   (explicit `pending` flag instead of relying on buffer length).
3. Regression tests added per package:
   - `provider`: `TestParseSSEConsecutiveDataLinesJoin`,
     `TestParseSSESplitJSONIsValidJSON`, `TestParseSSEBlankLineStillDispatches`,
     `TestParseSSELenientNoBlankLines`, `TestParseSSEDoneShortCircuitWithCallbacks`,
     `TestParseSSEDoneFlushesBufferedPayload`.
   - `anthropic`: `TestAnthropicSSEMultiDataLineJoin`,
     `TestAnthropicSSEBlankLineDispatchUnchanged`, `TestAnthropicSSEStateAppendData`.
   - `protocol`: `TestParseAnthropicEventStreamMultiDataLineJoin`,
     `TestParseAnthropicEventStreamBlankLineDispatchUnchanged`.
4. Lenient JSON-per-line providers keep working because each payload is
   flushed by the next non-data line / `[DONE]` / EOF; only truly consecutive
   data lines merge.

**Validation** (each gate run separately):
- `go vet ./...` — clean.
- `staticcheck ./...` — clean.
- `gocognit -over 15 .` / `gocyclo -over 12 .` — only pre-existing warnings in
  unrelated files (test/plugin/config/command files); none of the changed SSE
  files appear.
- `go test -count=1 -race ./internal/agentic/...` — all packages green.

**Status**: FIXED + validated.
