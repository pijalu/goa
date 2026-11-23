<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# Archived: summarize live telemetry and compression follow-up queueing

Closed 2026-11-23. Moved from `bugs.md`; fixed in the associated commit.

> Follow-up UX correction: summarize and other compression passes no longer
> update the status bar or footer while sending, processing, or compacting.
> `EventCompactionProgress` remains available as SDK telemetry, but the TUI
> consumes it without displaying it or adding it to session token totals.
> Compression uses the async busy/steering queue through `SilentAsyncCommand`
> without a spinner. The compaction bubble is the sole completion activity.
> The UI-rendering steps below record the superseded initial implementation.

## Observed and expected behavior

`/compress:summarize` drained its provider stream without observer updates, so
the TUI showed only `Compressing (summarize)…` until completion. Users could not
see generated tokens, generation speed, or elapsed generation time. Only the
default, summarize, and hybrid compression commands used the async busy gate;
the other strategies bypassed its follow-up-message queue.

The intended contract was live, transient summarize telemetry without leaking
checkpoint text into chat or adding auxiliary tokens to normal session totals.
Every compression command must hold the follow-up gate until compression has
returned, while automatic compression must remain synchronous inside its agent
turn.

## Root cause

`consumeSummarizeStream` accumulated text locally and exposed nothing until it
returned. The normal token-stats path could not be reused for live updates:
`EventTokenStats` feeds cumulative session totals and the persistent usage
store, which would incorrectly charge the same summarize call as ordinary turn
output.

`CompressCommand.AsyncHint` also special-cased only LLM-backed strategies, even
though the existing async command lifecycle already supplied the required busy
and steering-queue behavior for every strategy.

## Fix

- Added dedicated `EventCompactionProgress` events with estimated output tokens,
  elapsed milliseconds, and derived output tokens/second. Updates are emitted at
  stream start, on the first delta, at most every 100 ms thereafter, and once at
  clean completion. Each retry gets fresh counters.
- Added TUI rendering as
  `Summarizing… ~N tokens · S tok/s · T s` in the chat status and footer model
  activity. `compaction_end` clears the transient status and busy indicator.
- Kept checkpoint text out of `EventContent`, and kept estimated live metrics
  out of `EventTokenStats`; exact provider usage remains on the durable
  `compaction_summary` record.
- Routed every valid `/compress` strategy through the existing async command
  busy/queue lifecycle. No history, cache-key, or compression-ordering semantics
  changed.
- Added an automatic-compression regression proving a queued input cannot open
  a conversation stream while the compaction stream is blocked, then runs after
  release.

## Test and validation record

RED was observed before implementation:

- `TestSummarizeHistory_EmitsLiveTokenProgress` and
  `TestSummarizeHistory_RetryProgressResetsFailedAttempt`: no progress events.
- `TestHandleProgressEvent_SummarizeShowsLiveTelemetry`: static text, no footer
  activity, no model-busy state (the final version exercises the dedicated event
  through `handleAgentOutputEvent`).
- `TestCompactionEnd_ClearsSummarizeTelemetry`: status remained visible.
- `TestCompressCommand_AsyncHint`: tool-elision, selective, and micro returned
  no async label.

GREEN targeted suites passed for `internal/agentic`, `internal/app`, and
`core/commands`. The queue test initially used a 100-token fixture and correctly
failed because fixed request cost could not fit; the fixture was corrected to
1000 tokens, leaving production code unchanged.

`TestSummarizeProgress_FilmstripShowsAndClearsTelemetry` rendered the full
production component tree to a test terminal. Actual live output contained:

```text
◆ Summarizing… ~42 tokens · 25.5 tok/s · 1.6s
```

The completed frame no longer contained the telemetry.

Required quality commands were run separately:

- `go vet ./...` — pass.
- `staticcheck ./...` — pass.
- `go test -count=1 -race -cover -timeout 180s ./...` — pass across all packages.
- `gocognit -over 15 .` — only unrelated pre-existing warnings in
  `TestAgentMCPCloseRemovesExtractedArtifacts`, `TestResolveStartPlanAPIKey`,
  `TestMakeToolFactory_BuildsEveryConfigurableTool`,
  `TestConfigurableTools_AllDefaultsMirrorEmbeddedConfig`, `initGoalSystem`, and
  `AgentManager.executeRunner`; no changed function was reported.
- `gocyclo -over 12 .` — only unrelated pre-existing warning in test helper
  `alignOneMessage`; no changed function was reported.
