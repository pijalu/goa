<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: 429 error during summarize compression does not trigger retries

Closed 2026-08-30. Moved from `bugs.md`. Fixed by commit `3dc8e39`
("fix(agentic): summarize compression retries 429 through the shared
provider retry/backoff path").

Observed: when the provider returned a 429 (rate limit) during
summarize-based compression, the compression failed instead of retrying.
Expected: the model should go into the provider retry/backoff path until
the compression succeeds or retries are exhausted.

## Root cause

`Agent.summarizeHistory` opened the summarize stream and consumed it with
no retry loop — unlike conversation turns, which route every stream
failure through `handleStreamFailure` → `retryStream` with classified
backoff. Any provider failure (429, 5xx, transport) failed the compaction
on the first attempt. The provider layer does not retry at stream-open
time, so there was no safety net anywhere.

A second defect hid mid-stream 429s: `consumeSummarizeStream` wrapped
error events with `%v`, severing the `*hooks.ProviderError` chain, so
even a retry loop could not have classified the failure via `errors.As`.

## Fix

`summarizeHistory` now retries transient failures with the same
primitives the turn path uses (`resolveRetryPlan` →
`shouldRetryStreamError` → `scheduleRetryAttempt`): per-retry agent-log +
EventRateLimit + progress bubble, Retry-After-aware backoff, budget =
policy > MaxRetries > 5, terminal EventRateLimit (will_retry=false) on
give-up. Context-overflow errors stay excluded — `compactOrdered` owns
the once-only shrink-and-retry recovery; user cancel is never retried.
The event-error wrap became `%w`; the single attempt moved into
`summarizeStreamOnce`, rebuilding the provider context per attempt.

## Validation

- RED first: `TestSummarizeHistoryRetriesRateLimitThenSucceeds`,
  `...MidStreamRateLimit`, `...RateLimitRetriesExhausted` failed before
  the fix (1 provider call each), pass after (2 / 2 / 3 calls).
- `TestSummarizeHistoryDoesNotRetryContextOverflow` pins the boundary:
  overflow surfaces to `compactOrdered` after exactly one attempt.
- `TestPreparePath_CeilingOnlyWhenSummarizeCannotRun` updated to
  `registerTestProviderEveryRound` + fast policy: with retries working,
  the ceiling-fallback contract needs a summarize that fails EVERY round.
- Full gates green: `go vet`, `staticcheck`, `gocognit -over 15`,
  `gocyclo -over 12` (one pre-existing unrelated warning:
  TestRetryConfigSetters), `go test -count=1 -race -cover ./...` (87
  packages ok).
