// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "time"

func (a *Agent) markGenStart() {
	a.genSawEvent = true
	if a.genStartTime.IsZero() {
		a.genStartTime = time.Now()
	}
}

// startGenTiming opens the output-speed timing window at stream start. The
// window must span the whole generation — including reasoning phases that
// stream as unmapped deltas (e.g. z.ai GLM reasoning_content) — otherwise only
// the short content tail is measured and the derived tok/s is absurd
// (bugs.md z.ai Issue 7). markGenStart remains as a safety net for streams
// that bypass startGenTiming, but is a no-op once the window is open.
func (a *Agent) startGenTiming() {
	a.genStartTime = time.Now()
}

// recordGenDuration captures the wall-clock generation time of the stream that
// just ended (first token → done). Stored for emitTurnStats to derive speed.
func (a *Agent) recordGenDuration() {
	if !a.genStartTime.IsZero() {
		a.genDuration = time.Since(a.genStartTime)
	}
}

// fallbackOutputSpeed returns an estimated output tok/s derived from wall-clock
// generation time. Returns 0 if no generation timing was captured. This is used
// when the provider's usage object carries no timing fields (common for local
// OpenAI-compatible servers like LM Studio, llama.cpp, and Ollama).
func (a *Agent) fallbackOutputSpeed(outputTokens int) float64 {
	if a.genDuration > 0 && outputTokens > 0 {
		if secs := a.genDuration.Seconds(); secs > 0 {
			return float64(outputTokens) / secs
		}
	}
	return 0
}

// emitTurnStats emits estimated token statistics and context usage at the
// end of a turn, but only if the provider did not already emit real stats.
func (a *Agent) emitTurnStats() {
	if a.turnStatsEmitted {
		stats := a.computeContextStats()
		a.emitEvent(OutputEvent{Type: EventContextStats, ContextStats: &stats})
		return
	}

	// If we have provider Usage from stream_options.include_usage, use it.
	// This gives accurate token counts (and cache stats) from the provider
	// instead of character-based estimates.
	a.mu.Lock()
	pu := a.providerUsage
	a.mu.Unlock()
	if pu != nil {
		if pu.InputTokens > 0 || pu.OutputTokens > 0 || pu.CacheReadTokens > 0 {
			a.emitEvent(OutputEvent{
				Type: EventTokenStats,
				Timings: &TokenTimings{
					PromptN:            pu.InputTokens,
					PredictedN:         pu.OutputTokens,
					CacheReadTokens:    pu.CacheReadTokens,
					CacheWriteTokens:   pu.CacheCreationTokens,
					PredictedPerSecond: a.fallbackOutputSpeed(pu.OutputTokens),
				},
			})
			stats := a.computeContextStats()
			a.emitEvent(OutputEvent{Type: EventContextStats, ContextStats: &stats})
			return
		}
	}

	hist := a.copyHistory()
	if len(hist) == 0 {
		return
	}

	promptTokens, predictedTokens := estimateTurnTokens(hist)

	a.emitEvent(OutputEvent{
		Type: EventTokenStats,
		Timings: &TokenTimings{
			PromptN:            promptTokens,
			PredictedN:         predictedTokens,
			PredictedPerSecond: a.fallbackOutputSpeed(predictedTokens),
		},
	})

	stats := a.computeContextStats()
	a.emitEvent(OutputEvent{Type: EventContextStats, ContextStats: &stats})
	a.turnStatsEmitted = true
}

func (a *Agent) copyHistory() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	hist := make([]Message, len(a.history))
	copy(hist, a.history)
	return hist
}

func estimateTurnTokens(hist []Message) (promptTokens, predictedTokens int) {
	last := findLastAssistant(hist)
	if last == nil {
		return estimateTokensFromHistory(hist), 0
	}
	predictedTokens = messageTokenCount(last)
	promptTokens = tokensBefore(hist, last)
	return
}

func findLastAssistant(hist []Message) *Message {
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == Assistant {
			return &hist[i]
		}
	}
	return nil
}

func messageTokenCount(msg *Message) int {
	total := estimateTokens(msg.Content) + estimateTokens(msg.Thinking)
	for _, tc := range msg.ToolCalls {
		total += estimateTokens(tc.Arguments)
	}
	return total
}

func tokensBefore(hist []Message, assistant *Message) int {
	var total int
	for i := range hist {
		if &hist[i] == assistant {
			break
		}
		total += estimateTokens(hist[i].Content)
		total += estimateTokens(hist[i].Thinking)
		for _, tc := range hist[i].ToolCalls {
			total += estimateTokens(tc.Arguments)
		}
	}
	return total
}

// Clear resets the conversation history and cancels any processing.
// Emits an EventClear to all observers.
