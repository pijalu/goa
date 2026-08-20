# Bug archive — 2026-08-15

## Thinking-stall watchdog fires while thinking deltas are actively streaming

Source: review of `/Users/muaddib/dev/qwentest/.goa/exports/goa-export-20260815-015148.zip`
(issue.md: "No stall but marked as stall"; agent.log: 2603 `[delta] thinking`
traces from 01:45:07.843 through 01:50:07.854, killed at 01:50:07.862 with
`Stopping stream: thinking stalled for 5m0.018967667s without progress` —
exactly `thinking_stall_stop_seconds` after the *first* thinking delta).

### Observed behavior

With a slow local model (LM Studio `qwen3-8-27b`), thinking/reasoning deltas
stream continuously for minutes, yet the thinking-stall watchdog stops the turn
once the *cumulative* thinking-phase duration exceeds
`execution.thinking_stall_stop_seconds` (default 300s). The watchdog measured
from the first thinking delta of the phase (`thinkingStallStart` was set once
and never refreshed on subsequent deltas), so an actively-streaming model was
reported as "stalled without progress".

### Root cause

`handleThinkingDelta` set `a.thinkingStallStart` only when zero (first delta of
the phase) and compared `time.Since(thinkingStallStart)` to the stop threshold.
Every delta that *was* arriving counted as no-progress because the clock was
never reset — the exact inverse of the intended "no deltas arriving" semantics.

### Fix

The stall clock now measures the gap since the **last received** thinking delta:

- `internal/agentic/agent.go`: `thinkingStallStart` is now the last-activity
  timestamp; added `thinkingStallWarnTimer` / `thinkingStallStopTimer`.
- `internal/agentic/agent_streaming.go`:
  - `handleThinkingDelta` refreshes the activity timestamp on every delta and
    (re)arms warn/stop timers (`armThinkingStallTimers`). Only continuous
    *silence* longer than the thresholds trips them — an actively-streaming
    model never stalls, no matter how long the reasoning phase runs.
  - `onThinkingStallWarn` / `onThinkingStallStop` timer callbacks re-check the
    gap under `a.mu` before acting (stale Reset/fire race is suppressed).
  - `markThinkingStalled` sets the sticky `thinkingStalled` flag; the stall
    error still surfaces via `handleStreamEvent`.
  - `resetThinkingStall` (content/tool progress) and `resetStreamRoundState`
    (round boundary) disarm the timers; `processTurnWithStream` defers
    `stopThinkingStallTimers` as a leak safety net on early error returns.

The gap-based stop is required because a true no-delta hang delivers no further
deltas that could re-evaluate a per-delta check — the re-armed timer detects it.

### Tests (would have caught the bug)

`internal/agentic/agent_streamloop_test.go`:

- `TestThinkingStall_ContinuousDeltasNotStalled` — RED before the fix: deltas
  dripping every `stop/4` for ~4× the stop threshold must never stall (this
  failed at delta 4 pre-fix, reproducing the incident exactly).
- `TestThinkingStall_NoDeltaGapStops` — a true hang (no deltas) is caught by
  the re-armed stop timer even with no further deltas.
- `TestThinkingStall_WarnOnSilenceThenStallIsFinal` — warn fires on the silence
  gap; once stalled the decision is final.
- `TestThinkingStall_TimersStopAtRoundBoundary` — timers don't survive a round
  reset.
- `TestThinkingStall_SeparateFlagAndError` / `TestThinkingStall_DisabledByHook`
  — updated to gap semantics; the guard stays independent of the stream-loop
  detector and the disable toggle still works mid-stream.

### Validation

- `go vet ./...`, `staticcheck ./...`, `gocognit -over 15`, `gocyclo -over 12`
  on changed files: clean (no new findings).
- `go test -run TestThinkingStall -race`: all pass.
- Terminal (PTY) against a mock OpenAI-compatible locallm streaming
  `reasoning_content` deltas every 120 ms for ~12 s with
  `thinking_stall_stop_seconds: 3`: the turn streamed `step0…step94`, showed
  **no "thinking stalled" error**, then rendered the final answer — the
  incident scenario fixed. A hang mock (2 deltas then silence) showed the warn
  timer firing ("thinking for over 2s without producing output").
