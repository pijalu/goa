# Bug: ReplayRunner.Submit can block forever (deadlock) under concurrent Submits

**Date:** 2026-08-25 · **Status:** IMPLEMENTED — tested, validated, archived.

## Observed

`go test -count=1 -race ./...` timed out after 10m in
`tui/agentctx.TestReplayRunner_ConcurrentSubmitClose`: Submit goroutines
stuck on chan send (Submit's blocking fallback), the runner goroutine stuck
delivering a result into the cap-4 `results` channel that nobody drains
during the submit storm, and the test blocked on WaitGroup.

## Root cause

`Submit`'s drain-then-send over the cap-1 `reqCh` channel is not atomic
(TOCTOU): concurrent Submits interleave between draining the slot and the
blocking fallback send. When the loop goroutine cannot return to receive —
its `run()` is blocked pushing into the full `results` buffer, which only
Close/Cancel unblocks and those only run after WaitGroup — every submitter
blocks forever.

## Fix

Replace the cap-1 request channel with a mutex-guarded `pending
*ReplayRequest` slot plus a cap-1 wake signal:

- `Submit` cancels the in-flight run (supersede), stores pending atomically
  under mu (latest-wins), and signals wake non-blockingly — it can never
  block.
- The loop wakes on the token and drains latest-wins until the slot is empty,
  so a burst of tab switches still collapses to the newest target per pass.
- `Cancel` clears pending under the same mutex; `Close` semantics unchanged.

No lost-wakeup: a wake token buffered while the loop is busy makes it re-drain
after the current run; pending persists until consumed.

Regression: the existing `TestReplayRunner_ConcurrentSubmitClose`
(4×25 concurrent Submits, no results consumer, then Cancel+Close) deadlocked
pre-fix; it now passes repeatedly.

## Validation

- `go test -race -count=10 -run TestReplayRunner_ConcurrentSubmitClose
  ./tui/agentctx/` — ok.
- Full gates separately: `go vet ./...`, `staticcheck ./...`, `gocognit
  -over 15 .`, `gocyclo -over 12 .` (pre-existing test-file warnings unchanged
  from HEAD, unrelated to this change), `go test -count=1 -race -cover ./...`
  — 87 packages ok, no failures, no races.
