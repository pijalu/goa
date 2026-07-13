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

	estimated := estimateTokensFromHistory(a.history) + a.fixedCostTokens()

	// The UI should always reflect the model's actual capacity. Prefer the
	// runtime-refreshed context window, then the configured model window, and
	// fall back to the explicit compression limit only when no model window is
	// known. This prevents a stale auto-derived compression limit from hiding
	// a smaller loaded context window (e.g., local models reporting 32k after
	// the default registry advertised 131k).
	maxTokens := int(a.contextWindow.Load())
	autoMax := maxTokens > 0
	if maxTokens == 0 {
		maxTokens = a.cfg.Model.ContextWindow
		autoMax = maxTokens > 0
	}
	if maxTokens == 0 {
		maxTokens = a.cfg.ContextCompression.MaxTokens
		autoMax = false
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
