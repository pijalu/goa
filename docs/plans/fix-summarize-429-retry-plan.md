<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# Fix plan: 429 error during summarize compression does not trigger retries

Bug: bugs.md — "429 error during summarize compression does not trigger retries".

## Root cause

`Agent.summarizeHistory` (internal/agentic/agent_compress_micro.go) opens the
summarize stream and consumes it with `consumeSummarizeStream`. Unlike the
conversation turn path (`runStreamRound` → `handleStreamFailure` →
`retryStream`), there is NO retry loop: any provider failure — including a
classified 429 rate limit — fails the compaction immediately.

Two defects:

1. No retry loop around the summarize stream open + consume.
2. `consumeSummarizeStream` wraps a mid-stream error event with `%v`
   (destroying the `*hooks.ProviderError` chain), so 429 classification via
   `errors.As`/`shouldRetryStreamError` would fail even with a retry loop.

The provider layer does not retry at stream-open time (`provider.Stream` opens
once); all retry policy lives in the agent.

## Fix

File: `internal/agentic/agent_compress_micro.go` only.

1. `consumeSummarizeStream`: wrap the event error with `%w` so the provider
   error chain survives for classification.
2. Extract the single attempt (build request → open → consume → empty-check →
   usage capture) into `summarizeStreamOnce`, rebuilding the provider context
   per attempt (parity with `executeRetryAttempt`).
3. Add a retry loop in `summarizeHistory` reusing the shared primitives:
   - `resolveRetryPlan(opts)` — budget (policy > MaxRetries > 5).
   - `shouldRetryStreamError(ctx, err, plan.policy)` — eligibility
     (429 ProviderError is retryable).
   - `a.scheduleRetryAttempt(...)` — shared per-retry event emission
     (agent-log, EventRateLimit willRetry=true, progress bubble) + backoff
     wait honoring Retry-After, ctx-aware.
   - Terminal `emitRateLimit(..., willRetry=false)` on give-up (non-retryable
     or budget exhausted), mirroring handleStreamFailure.
   - Context-overflow errors are NOT retried here: `compactOrdered` owns the
     once-only shrink-and-retry recovery (micro + shrink). User cancel
     (`ctx.Err() != nil`) breaks immediately.

## Test approach (RED first)

New file `internal/agentic/agent_summarize_retry_test.go` using the existing
`scriptedStreamProvider` and the fast jitter-free policy pattern from
rate_limit_event_test.go (InitialDelay 1ms, MaxDelay 5ms, Jitter 0):

1. `TestSummarizeHistoryRetriesRateLimitThenSucceeds` — step 0: open fails
   with a classified 429 (`rateLimitErrWithRetry`), step 1: text events.
   Expect success, summary non-empty, provider called twice. RED today: fails
   after 1 call.
2. `TestSummarizeHistoryRetriesMidStreamRateLimit` — step 0: stream opens,
   pushes `EventError` carrying the 429 provider error; step 1: clean text.
   Proves the `%w` chain fix. RED today.
3. `TestSummarizeHistoryRateLimitRetriesExhausted` — all steps 429, MaxRetries
   2 → error returned after exactly 3 calls.
4. `TestSummarizeHistoryDoesNotRetryContextOverflow` — step 0: context-length
   error, step 1: would-be success → exactly 1 call, error returned (caller
   owns overflow recovery).

## Validation steps

- `go test ./internal/agentic/ -run 'SummarizeHistory' -count=1 -race` green.
- Full quality gates run separately: `go vet ./...`, `staticcheck ./...`,
  `gocognit -over 15 .`, `gocyclo -over 12 .`,
  `go test -count=1 -race -cover ./...`.
- Compact-path regression: existing summarize/compaction suite stays green
  (`go test ./internal/agentic/ -run 'Compact|Compress' -count=1`).
- Commit with a descriptive message; archive the bug entry to docs/archive.
