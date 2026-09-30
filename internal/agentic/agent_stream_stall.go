// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// roundDeliveredCompleteAnswer reports whether the current stream round has
// already delivered a finished answer, so that silence afterwards means "the
// provider is holding the socket open" rather than "the model is producing
// nothing" (docs/research/zai-connection-review-20260930.md §2).
//
// It is deliberately conservative — a false positive here would finalize a
// genuinely truncated answer and hide a real stall, so every condition must
// hold:
//
//   - visible answer text was actually delivered (thinking-only rounds do not
//     count: a model that streamed reasoning and then went quiet is still
//     working, and that silence must keep waiting);
//   - no tool call is buffered or still streaming — a pending tool call means
//     the round is mid-work, not finished, and dropping it would lose the
//     turn's whole purpose;
//   - the buffered text looks like a finished sentence rather than a fragment
//     cut mid-word, since a trailing dangling token is the signature of a
//     truncated stream rather than a completed one.
//
// Everything is read under a.mu because the watchdog timer fires on its own
// goroutine, concurrently with the event loop appending deltas.
func (a *Agent) roundDeliveredCompleteAnswer() bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.bufferedToolCalls) > 0 || len(a.streamingToolCalls) > 0 || len(a.streamingToolCallsByIndex) > 0 {
		return false
	}
	content := strings.TrimSpace(a.contentBuf.String())
	if content == "" {
		return false
	}
	return endsLikeFinishedSentence(content)
}

// endsLikeFinishedSentence reports whether text ends at a sentence boundary.
// Providers that terminate cleanly stop after punctuation, a closing fence, or
// trailing whitespace; a stream truncated mid-thought ends on a partial word or
// an unclosed construct.
func endsLikeFinishedSentence(content string) bool {
	switch content[len(content)-1] {
	case '.', '!', '?', ':', ';', ')', ']', '}', '"', '\'', '`':
		return true
	}
	// A trailing newline is whitespace, already trimmed above, so reaching
	// here means the text ends on an alphanumeric or an opening bracket:
	// treat it as an unfinished fragment.
	return false
}

// onStreamStall is the event-stall watchdog body: it runs on its own goroutine
// after stallTimeout of silence and decides how to terminate the stream.
//
// A provider that finished its answer but never closed the socket is NOT
// stalled — it is done. z.ai (glm-5.3-flash) and opencode-go both deliver
// HTTP 200 plus the complete response, then hold the SSE connection open
// without ever sending [DONE] or a finish_reason chunk
// (docs/research/zai-connection-review-20260930.md §2). Treating that silence
// as a stall threw away a finished answer and replayed the whole turn — four
// provider calls for one reply in the captured session.
//
// So silence after a complete answer ends the stream gracefully and the turn
// finalizes from the buffered content; silence with no finished answer stays a
// stall error, which handleStreamFailure treats as transient and retries.
func (a *Agent) onStreamStall(stream *provider.AssistantMessageEventStream, stallTimeout time.Duration) {
	if a.roundDeliveredCompleteAnswer() {
		a.cfg.Logger.Log(Warn, "Stream held open %v after a complete answer; finalizing without the provider's terminator ([DONE]/finish_reason)", stallTimeout)
		stream.End(nil)
		return
	}
	a.cfg.Logger.Log(Warn, "Stream stalled: no events received for %v", stallTimeout)
	stream.CloseWithError(fmt.Errorf("stream stalled: no events received from provider for %v", stallTimeout))
}

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
