// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "time"

func (a *Agent) armThinkingStallTimers(now time.Time, warnAfter, stopAfter time.Duration) {
	a.mu.Lock()
	a.thinkingStallStart = now
	a.mu.Unlock()

	if a.thinkingStallWarnTimer == nil {
		a.thinkingStallWarnTimer = time.AfterFunc(warnAfter, a.onThinkingStallWarn)
	} else {
		a.thinkingStallWarnTimer.Reset(warnAfter)
	}
	if a.thinkingStallStopTimer == nil {
		a.thinkingStallStopTimer = time.AfterFunc(stopAfter, a.onThinkingStallStop)
	} else {
		a.thinkingStallStopTimer.Reset(stopAfter)
	}
}

// onThinkingStallWarn emits the "still thinking" progress warning after
// warnAfter of continuous thinking silence. It re-checks the actual gap under
// the mutex because Reset can race with an in-flight timer callback: a delta
// that landed just before the fire must suppress the stale warning.
func (a *Agent) onThinkingStallWarn() {
	a.mu.Lock()
	if a.thinkingStallStart.IsZero() || a.thinkingStallWarned {
		a.mu.Unlock()
		return
	}
	elapsed := time.Since(a.thinkingStallStart)
	warnAfter := a.cfg.ThinkingStallWarn
	if warnAfter <= 0 {
		warnAfter = defaultThinkingStallWarn
	}
	if elapsed < warnAfter {
		a.mu.Unlock()
		return
	}
	a.thinkingStallWarned = true
	a.mu.Unlock()

	a.emitEvent(OutputEvent{
		Type: EventProgress,
		Text: "The agent has been thinking for over " + warnAfter.Round(time.Second).String() + " without producing output.",
	})
}

// onThinkingStallStop declares the stall after stopAfter of continuous
// thinking silence. The stale-fire guard mirrors onThinkingStallWarn: a
// delta arriving just before the fire invalidates the callback.
func (a *Agent) onThinkingStallStop() {
	a.mu.Lock()
	if a.thinkingStallStart.IsZero() {
		a.mu.Unlock()
		return
	}
	elapsed := time.Since(a.thinkingStallStart)
	stopAfter := a.cfg.ThinkingStallStop
	if stopAfter <= 0 {
		stopAfter = defaultThinkingStallStop
	}
	a.mu.Unlock()
	if elapsed < stopAfter {
		return
	}
	a.markThinkingStalled(elapsed)
}

// markThinkingStalled records the stall: the stream is stopped and the error
// surfaces on the next handled event (see handleStreamEvent).
func (a *Agent) markThinkingStalled(elapsed time.Duration) {
	a.mu.Lock()
	if a.thinkingStalled {
		a.mu.Unlock()
		return
	}
	a.thinkingStalled = true
	a.thinkingStallElapsed = elapsed
	a.mu.Unlock()
	a.cfg.Logger.Log(Warn, "Stopping stream: thinking stalled for %v without progress", elapsed)
}

// resetThinkingStall clears the thinking-stall tracking whenever the model
// produces content or a tool call, indicating forward progress.
func (a *Agent) resetThinkingStall() {
	a.mu.Lock()
	a.resetThinkingStallLocked()
	a.mu.Unlock()
	a.stopThinkingStallTimers()
}

// resetThinkingStallLocked is resetThinkingStall for callers that already
// hold a.mu (e.g. resetStreamRoundState via startStreamRound). Timer Stop is
// safe under the lock, so the timers are disarmed inline.
func (a *Agent) resetThinkingStallLocked() {
	a.thinkingStallStart = time.Time{}
	a.thinkingStallWarned = false
	a.stopThinkingStallTimers()
}

// stopThinkingStallTimers disarms the warn/stop timers. Stale callbacks that
// lose the Stop race are harmless: both re-check thinkingStallStart (zeroed
// by the reset) under the mutex before acting.
func (a *Agent) stopThinkingStallTimers() {
	if a.thinkingStallWarnTimer != nil {
		a.thinkingStallWarnTimer.Stop()
	}
	if a.thinkingStallStopTimer != nil {
		a.thinkingStallStopTimer.Stop()
	}
}

// resetStreamRoundState clears per-round buffers and flags before a re-stream
// or retry. This prevents a failed or truncated assistant response from
// leaking partial tokens or buffered tool calls into the next attempt.
