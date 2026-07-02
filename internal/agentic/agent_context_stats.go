// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

func (a *Agent) ContextStats() ContextStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.computeContextStats()
}

func (a *Agent) computeContextStats() ContextStats {
	var chars int
	for _, m := range a.history {
		chars += len(m.Content)
		chars += len(m.Thinking)
		for _, tc := range m.ToolCalls {
			chars += len(tc.Arguments)
		}
	}

	estimated := estimateTokensFromHistory(a.history)
	maxTokens := a.cfg.ContextCompression.MaxTokens
	autoMax := false
	if maxTokens == 0 {
		// Fall back to the model's advertised context window so the UI can
		// show usage even when the user has not configured compression.
		maxTokens = a.cfg.Model.ContextWindow
		autoMax = maxTokens > 0
	} else if a.cfg.Model.ContextWindow > maxTokens {
		// Compression is configured with a smaller limit than the model's
		// actual context window. The smaller limit still drives proactive
		// compression (see maybeCompress), but the displayed total should
		// reflect what the model can actually hold. Mark as auto so the UI
		// hints that the value comes from model metadata.
		maxTokens = a.cfg.Model.ContextWindow
		autoMax = true
	}
	usagePercent := 0
	if maxTokens > 0 {
		usagePercent = estimated * 100 / maxTokens
	}

	return ContextStats{
		Messages:        len(a.history),
		Characters:      chars,
		EstimatedTokens: estimated,
		MaxTokens:       maxTokens,
		UsagePercent:    usagePercent,
		AutoMax:         autoMax,
	}
}

// estimateTokensFromHistory returns a rough token count for a message slice
// using a language-aware heuristic: CJK ≈ 1 token, ASCII ≈ 0.25 tokens.
func estimateTokensFromHistory(msgs []Message) int {
	var total int
	for _, m := range msgs {
		total += estimateTokens(m.Content)
		total += estimateTokens(m.Thinking)
		for _, tc := range m.ToolCalls {
			total += estimateTokens(tc.Arguments)
		}
	}
	return total
}

func estimateTokens(text string) int {
	cjkCount := 0
	asciiCount := 0
	for _, r := range text {
		switch {
		case r >= '\u4e00' && r <= '\u9fff',
			r >= '\u3040' && r <= '\u309f',
			r >= '\u30a0' && r <= '\u30ff',
			r >= '\uac00' && r <= '\ud7af':
			cjkCount++
		case r < 128:
			asciiCount++
		}
	}
	others := len([]rune(text)) - cjkCount - asciiCount
	return cjkCount + asciiCount/4 + others/2
}

// MaybeCompress manually triggers context compression regardless of thresholds.
// Returns the compression result. No-op if the context is empty.
