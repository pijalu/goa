<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug: stall watchdog discards already-complete streams (CRITICAL)

Date: 2026-09-30 · Status: FIXED — commit 60725534, tests RED-before-fix, gates clean.

## Reported

> A provider streams HTTP 200 and the full answer, then holds the SSE connection
> open without ever sending `[DONE]` or a `finish_reason` chunk. Goa waits
> `execution.activity_timeout` (60s in `~/.goa/config/user.yaml`), declares a
> stall, throws the completed answer away, and replays the whole turn.

Measured on export B: last content byte `23:09:57.881` → stall kill
`23:10:57.882` = 60.001s, then `retry scheduled attempt=1/15`. Request 19 shows
`status 200`, `finishReason null`, no `[DONE]`, no `finish_reason`. Export A
reproduces the same signature (requests 19/20/21, 66s/78s/in-flight).

Evidence: `docs/research/zai-connection-review-20260930.md`, sessions
`zai`/`glm-5.3-flash` and `opencode-go`/`space-bunny-free`.

## Root cause

`onStreamStall` (`internal/agentic/agent_stream_stall.go`) treated any silence
past the event-stall window as a stall: it closed the stream with a stall error,
and `handleStreamFailure` undid the last assistant message and re-issued the
turn. For a provider that finished its answer and merely holds the socket open,
that discard is exactly backwards.

## Fix

`roundDeliveredCompleteAnswer` distinguishes the two cases, deliberately
conservatively (a false positive would finalize a genuinely truncated answer and
hide a real stall):

- visible answer text must have been delivered — a thinking-only round is still
  working, so its silence must keep waiting;
- no tool call buffered or still streaming;
- the text must end at a sentence boundary rather than mid-word.

Only then does silence after it end the stream gracefully and finalize the turn
from what was buffered. Genuine silence keeps the stall error and the retry
path.

## Tests

`internal/agentic/agent_complete_open_stream_test.go`

- `TestAgent_CompleteAnswerHeldOpenStream_FinalizesWithoutReplay` — the
  reproduction.
- `TestAgent_ProductiveSilence_StillStalls` — the guard: genuine silence still
  takes the retry path.
- `TestRoundDeliveredCompleteAnswer_Boundaries` — 12 cases: finished sentence,
  finished question, closed code fence, trailing whitespace (accepted);
  truncated mid-word, truncated mid-thought, unclosed bracket, empty,
  whitespace-only, thinking-only, pending buffered tool call, still-streaming
  tool call all stay on the stall path.

## RED-before-fix evidence

`TestAgent_CompleteAnswerHeldOpenStream_FinalizesWithoutReplay` FAILED before the
change with *"LLM connection lost after retries: stream stalled: no events
received from provider for 400ms (provider called 4 times, 4 held-open streams)"*
— matching production (4 calls, replayed answer). PASSES after: 1 provider call,
0.66s, delivered text intact.

## Validation

- `go build ./...` — clean.
- `go vet ./internal/agentic/...`, `staticcheck ./internal/agentic/...` — clean.
- `gocognit -over 15`, `gocyclo -over 12` on `./internal/agentic/` — clean
  (`onStreamStall` was extracted from `consumeStream` to restore the baseline).
- `go test -count=1 -race -timeout 900s ./...` — all packages pass.

## Residual risk

A provider that completes an answer with text ending on a bare alphanumeric (a
lone identifier, a value with no closing punctuation) still stalls and replays —
the conservative boundary trades that rare wasted replay for never finalizing a
truncated answer.