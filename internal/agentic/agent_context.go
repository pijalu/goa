// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
)

// effectiveMaxTokens returns the context window limit the agent should use for
// compression and ceiling decisions. It prefers the explicit compression config,
// then falls back to the model's advertised context window.
func (a *Agent) effectiveMaxTokens() int {
	if a.cfg.ContextCompression.MaxTokens > 0 {
		return a.cfg.ContextCompression.MaxTokens
	}
	if cw := a.contextWindow.Load(); cw > 0 {
		return int(cw)
	}
	if a.cfg.Model.ContextWindow > 0 {
		return a.cfg.Model.ContextWindow
	}
	return 0
}

// enforceContextCeiling is a last-resort safety net. After proactive compression
// has run, if the estimated context still exceeds the configured maximum it
// drops the oldest non-system messages until usage is back under the ceiling.
// This prevents runaway conversations from growing unbounded when compression
// is disabled, misconfigured, or unable to keep up.
func (a *Agent) enforceContextCeiling() {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return
	}

	const hardCeilingPercent = 95
	hardCeiling := maxTokens * hardCeilingPercent / 100

	for estimateTokensFromHistory(a.history) > hardCeiling && len(a.history) > 1 {
		// Never drop the initial system prompt (index 0).
		removed := a.history[1]
		a.history = append(a.history[:1], a.history[2:]...)
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Warn, "Context ceiling enforced: dropped %s message (len=%d)", removed.Role, len(removed.Content))
		}
	}

	if estimateTokensFromHistory(a.history) > hardCeiling {
		a.cfg.Logger.Log(Error, "Context ceiling cannot be enforced: even minimal history exceeds %d tokens", hardCeiling)
	}
}

// computeContextStatsForMax computes context stats using the supplied max
// instead of the config value. Used by the fallback compression path.
func (a *Agent) computeContextStatsForMax(maxTokens int) ContextStats {
	var chars int
	for _, m := range a.history {
		chars += len(m.Content)
		chars += len(m.Thinking)
		for _, tc := range m.ToolCalls {
			chars += len(tc.Arguments)
		}
	}

	estimated := estimateTokensFromHistory(a.history)
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
		AutoMax:         a.cfg.ContextCompression.MaxTokens == 0 && a.cfg.Model.ContextWindow > 0,
	}
}

// checkContextLimit returns an error when the current context already exceeds
// the hard ceiling before a new turn starts. Callers should refuse to add more
// user input until the conversation is compressed or reset.
func (a *Agent) checkContextLimit() error {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return nil
	}
	const hardCeilingPercent = 95
	hardCeiling := maxTokens * hardCeilingPercent / 100
	if estimateTokensFromHistory(a.history) > hardCeiling {
		return fmt.Errorf("context window full: estimated tokens exceed %d (%d%% of %d); compress or reset the conversation", hardCeiling, hardCeilingPercent, maxTokens)
	}
	return nil
}
