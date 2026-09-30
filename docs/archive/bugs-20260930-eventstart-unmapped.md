<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug: `EventStart` is unmapped — no watchdog re-arm + misleading WARN on every request (MEDIUM)

Date: 2026-09-30 · Status: FIXED — implemented, tests RED-before-fix, gates clean.

## Reported

> `streamAccum.ensureStarted()` pushes `schema.EventStart` as the first event of
> every OpenAI-completions stream, but `provider.EventStart` is absent from
> `streamEventHandlers` (`internal/agentic/agent_stream_events.go:15-23`). It is
> classified unmapped: it does not re-arm the stall watchdog, and it logs
> `provider sent unmapped event type "start"` on **every request** (4× in export
> A, ~40× in export B, on two unrelated providers). The wording blames the
> provider for a goa-internal lifecycle event.

Evidence: diagnostic bundle `docs/research/zai-connection-review-20260930.md`
(two sessions: `zai`/`glm-5.3-flash` and `opencode-go`/`space-bunny-free`).

## Root cause

`noteStreamEventProgress` (`internal/agentic/agent_streaming.go`) classifies an
event as progress purely by presence in `streamEventHandlers`: mapped → push the
stall-deadline and quiet-warning timers out; unmapped → log one WARN per type per
stream and do not re-arm. `EventStart` — pushed first by *every* protocol
(`openai_completions`, `openai_responses`, `anthropic_messages`,
`google_generative`, plus the bedrock/mistral/google
provider packages) — had no handler, so the guard deliberately ignored it: the
per-request WARN blamed the provider, and the guards treated a live, opening
stream as silence.

## Fix

`internal/agentic/agent_stream_events.go`

- `streamEventHandlers` gains `provider.EventStart: (*Agent).handleStreamStart`.
- `handleStreamStart` is a true no-op returning `(false, false, nil)`. It
  deliberately does **not** call `markGenStart()`: `genSawEvent` is the
  empty-response guard in `agent_turn_lifecycle` ("the stream produced a real
  output event"), and a stream that opens and then carries nothing must still be
  routed to the retry path. Nothing about `EventStart` is renderable either, so
  there is no state for it to touch — mapping it is what fixes both symptoms.

## Tests

`internal/agentic/agent_stream_start_event_test.go`

- `TestEventStart_ReArmsStallWatchdog` — a provider that pushes `EventStart` at
  0.6× the stall window and its first delta at 1.3×. Only a re-arm at
  `EventStart` (deadline 1.6×) keeps the delta alive; otherwise the watchdog kills
  the stream at 1.0× with no answer yet and the turn replays.
- `TestEventStart_NoUnmappedWarning` — a normal `EventStart` + delta + done turn
  logged through a capturing WARN logger contains no unmapped-event warning at
  all, and specifically not the `"start"` variant.
- `TestEventStart_CountsAsMappedProgress` — `noteStreamEventProgress` reports
  `EventStart` as progress and records no unmapped entry (the classification both
  the WARN and the watchdog key on).
- `TestEventStart_AloneIsStillAnEmptyResponse` — the seam the mapping opened: a
  stream that opens, pushes `EventStart`, and ends carrying nothing still takes
  the empty-response retry path and surfaces the notice instead of stopping
  silently.

## RED-before-fix evidence

With the map entry removed:

- `TestEventStart_ReArmsStallWatchdog` FAILs — 3 provider calls instead of 1, and
  the answer never arrives (the stall kill lands before the first delta).
- `TestEventStart_NoUnmappedWarning` FAILs with the captured line
  `[WARN] provider sent unmapped event type "start" — not rendered; it does not
  re-arm the stall watchdog`.
- `TestEventStart_CountsAsMappedProgress` FAILs (reports unmapped).

`TestEventStart_AloneIsStillAnEmptyResponse` passes before and after — it pins
the invariant the no-op must not break, not the defect.

## Validation

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `staticcheck ./internal/agentic/...` — clean.
- `gocognit -over 15`, `gocyclo -over 12` on `./internal/agentic/` — clean.
- `go test -count=1 -race -timeout 600s ./internal/agentic/` — pass.
- `go test -count=1 -race -timeout 900s ./...` — pass.

## Residual risk

None identified: the change adds a no-op handler and touches no timing or
lifecycle decision. The unmapped-event WARN path itself is unchanged and still
covers genuinely unknown provider event types (the F1b keep-alive flood case is
still protected — `TestUnmappedEventFlood_TripsStallWatchdog` passes).