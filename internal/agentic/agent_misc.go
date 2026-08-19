// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/perms"
)

func (a *Agent) withToolResultAsUser(model provider.Model, value bool) provider.Model {
	compat, ok := model.Compat.(provider.OpenAICompletionsCompat)
	if !ok {
		compat = provider.OpenAICompletionsCompat{}
	}
	compat.ToolResultAsUser = &value
	model.Compat = compat
	return model
}

func (a *Agent) undoLastAssistantMessage() {
	a.mu.Lock()
	defer a.mu.Unlock()

	start := a.turnStartHistoryLen
	if start < 0 {
		start = 0
		for i := len(a.history) - 1; i >= 0; i-- {
			if a.history[i].Role == User {
				start = i + 1
				break
			}
		}
	}

	for i := len(a.history) - 1; i >= start; i-- {
		if a.history[i].Role == Assistant {
			a.history = a.history[:i]
			return
		}
	}
}

// consumeStream reads events from a stream, buffers tool calls, and
// executes them concurrently after the stream ends.
// Returns true if tool calls were encountered (caller should re-stream).
// a fallback for providers that omit timing fields (LM Studio, llama.cpp, Ollama).
func (a *Agent) Clear() {
	a.mu.Lock()

	if a.cancel != nil {
		a.cancel()
	}

	a.history = nil
	a.cacheGeneration++
	a.queue = nil
	a.processing = false
	a.lastRoundActivity = time.Time{}
	a.lastCacheReadTokens = 0
	a.lastPersistedSticky = ""
	a.clearLoopStopLocked()
	a.invalidateContextUsageLocked()
	a.mu.Unlock()

	// Re-arm provider cache-miss forensics: the post-clear cold start must
	// not be reported as a bust against the cleared conversation's cache.
	provider.ResetCacheForensicsBaseline()

	a.emitEvent(OutputEvent{Type: EventClear})
}

// Compact summarizes the conversation history using the LLM provider
// and replaces it with a condensed version. This is useful for managing
// context window limits in long conversations.
//
// Emits an EventCompact with the summary text.
func (a *Agent) SetBufferedToolCallCountForTest(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.bufferedToolCallCount = n
}

// PolicyConfigForTest exposes the safety-gating fields a sub-agent was built
// with (autonomy, guard, confirm, project dir) so tests can assert policy
// inheritance without reaching into unexported state. Test-only; not part of
// the runtime API.
func (a *Agent) PolicyConfigForTest() (getAutonomy func() internal.AutonomyLevel, getGuard func() perms.GuardConfig, confirm func(context.Context, string, string) (bool, error), projectDir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.GetAutonomy, a.cfg.GetGuardConfig, a.cfg.ConfirmTool, a.cfg.ProjectDir
}
