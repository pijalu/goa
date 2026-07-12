// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/hooks"
)

// compactSummaryRequestPrompt is the stable user turn that precedes the
// generated summary in compacted history. Carrying the summary as an assistant
// reply to a user turn keeps the role sequence valid for strict providers
// (DeepSeek, some OpenAI deployments) that reject an assistant-first history,
// and ends on an assistant turn so the next user input alternates correctly.
const compactSummaryRequestPrompt = "Summarize our conversation so far, preserving key facts, decisions, and context."

func (a *Agent) Compact(ctx context.Context) error {
	a.mu.Lock()
	empty := len(a.history) == 0
	a.mu.Unlock()
	if empty {
		return nil
	}

	// Pre-flight: summarizeHistory sends the entire non-system history to the
	// model. If that input is itself near the window, the summarization request
	// returns the same context_length_exceeded and Compact fails exactly when
	// it is needed most. Shrink in-memory (selective) first so the summarize
	// call operates on a smaller input. Reserve headroom for the summarization
	// instruction plus the generated summary output.
	if maxTokens := a.effectiveMaxTokens(); maxTokens > 0 {
		const summarizeHeadroomPercent = 90
		if a.summarizationInputTokens() > maxTokens*summarizeHeadroomPercent/100 {
			if a.cfg.Logger != nil {
				a.cfg.Logger.Log(Info, "Compact: pre-shrinking history (selective) before summarization to avoid self-overflow")
			}
			a.mu.Lock()
			a.compressSelective()
			a.mu.Unlock()
		}
	}

	summary, err := a.summarizeHistory(ctx)
	if err != nil {
		return err
	}

	// Replace history with a valid, cache-stable role sequence. The system
	// prompt is NOT stored here: buildProviderContext sends it via
	// Context.SystemPrompt, so storing it would duplicate it on the next turn.
	// Previously Compact stored [system, assistant] (assistant-first after the
	// index-0 system skip, rejected by strict providers) and obliterated the
	// provider's prompt cache by wholesale prefix replacement.
	a.mu.Lock()
	a.history = []Message{
		{Type: Content, Role: User, Content: compactSummaryRequestPrompt},
		{Type: Content, Role: Assistant, Content: summary},
	}
	a.mu.Unlock()

	a.emitEvent(OutputEvent{Type: EventCompact, Text: summary})
	return nil
}

// summarizationInputTokens estimates the token cost of the input
// summarizeHistory will send to the model (all non-system history), snapshotted
// under the mutex. Used by Compact's pre-flight overflow check.
func (a *Agent) summarizationInputTokens() int {
	a.mu.Lock()
	snapshot := append([]Message(nil), a.history...)
	a.mu.Unlock()
	var total int
	for i := range snapshot {
		if snapshot[i].Role != System {
			total += messageTokenCount(&snapshot[i])
		}
	}
	return total
}

func (a *Agent) summarizeHistory(ctx context.Context) (string, error) {
	// Snapshot history under the mutex, then run the (network) summarization
	// off-lock so a slow provider call does not block off-turn history readers.
	a.mu.Lock()
	snapshot := append([]Message(nil), a.history...)
	a.mu.Unlock()

	var msgs []Message
	for _, m := range snapshot {
		if m.Role != System {
			msgs = append(msgs, m)
		}
	}

	if len(msgs) == 0 {
		return "", nil
	}

	// Use the stream-based path for summarization
	pCtx := provider.Context{
		Context:      ctx,
		SystemPrompt: "Summarize the following conversation concisely, preserving key facts and context:",
		Messages:     migrateMessages(msgs),
	}

	model := a.cfg.Model
	opts := a.cfg.StreamOptions
	if opts.APIKey == "" && a.cfg.APIKey != "" {
		opts.APIKey = a.cfg.APIKey
	}

	stream, err := provider.Stream(model, pCtx, opts)
	if err != nil {
		return "", fmt.Errorf("summarization stream: %w", err)
	}

	var summary strings.Builder
	for event := range stream.Seq() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if event.Type == provider.EventTextDelta {
			summary.WriteString(event.Delta)
		}
		if event.Type == provider.EventError {
			return "", fmt.Errorf("summarization error: %v", event.Error)
		}
	}

	if err := stream.Err(); err != nil {
		return "", fmt.Errorf("summarization failed: %w", err)
	}

	return summary.String(), nil
}

// --- Context Compression ---

// ContextStats returns the current context window usage statistics.

func (a *Agent) MaybeCompress(ctx context.Context) error {
	return a.MaybeCompressWith(ctx, a.cfg.ContextCompression.Strategy, true)
}

// MaybeCompressWith manually triggers context compression using the given
// strategy (empty falls back to configured). When force is true, internal
// per-strategy thresholds are bypassed so manual invocations always perform
// work. No-op if the history is empty.
func (a *Agent) MaybeCompressWith(ctx context.Context, strategy CompressionStrategy, force bool) error {
	a.mu.Lock()
	n := len(a.history)
	a.mu.Unlock()
	if n == 0 {
		return nil
	}
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Manual context compression triggered (strategy=%s force=%v)", strategy, force)
	}
	return a.compressHistoryWith(ctx, strategy, force)
}

// maybeCompress checks context usage and triggers compression if needed.
func (a *Agent) maybeCompress(ctx context.Context) error {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return nil
	}

	cfg := a.cfg.ContextCompression
	strategy := cfg.Strategy
	if strategy == "" {
		strategy = CompressionToolElision
	}

	// Micro compaction uses its own internal threshold checks.
	if strategy != CompressionMicro {
		threshold := cfg.ThresholdPercent
		if threshold == 0 {
			threshold = 90
		}
		a.mu.Lock()
		stats := a.computeContextStatsForMax(maxTokens)
		a.mu.Unlock()
		if stats.UsagePercent < threshold {
			return nil
		}

		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Context compression triggered: %d%% usage (%d / %d tokens)",
				stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
		}
	}

	if err := a.compressHistory(ctx); err != nil {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Error, "Context compression failed: %v", err)
		}
		return err
	}

	// Emit stats after compression (public ContextStats acquires the mutex).
	newStats := a.ContextStats()
	a.emitEvent(OutputEvent{
		Type:         EventContextStats,
		ContextStats: &newStats,
	})

	return nil
}

// compressHistory applies the configured compression strategy.
func (a *Agent) compressHistory(ctx context.Context) error {
	return a.compressHistoryWith(ctx, a.cfg.ContextCompression.Strategy, false)
}

// compressHistoryWith applies a specific strategy. When force is true,
// strategies with their own internal thresholds (micro compaction) bypass
// those thresholds so that a manual /compress invocation always does
// something visible, even when usage is below the configured ratio.
// An empty strategy falls back to the configured one, then to tool_elision.
func (a *Agent) compressHistoryWith(ctx context.Context, strategy CompressionStrategy, force bool) error {
	if strategy == "" {
		strategy = a.cfg.ContextCompression.Strategy
	}
	if strategy == "" {
		strategy = CompressionToolElision
	}

	switch strategy {
	case CompressionToolElision:
		a.mu.Lock()
		a.compressToolElision(force)
		a.mu.Unlock()
	case CompressionSelective:
		a.mu.Lock()
		a.compressSelective()
		a.mu.Unlock()
	case CompressionSummarize:
		return a.Compact(ctx)
	case CompressionHybrid:
		return a.compressHybrid(ctx)
	case CompressionMicro:
		a.mu.Lock()
		a.microCompactForced(force)
		a.mu.Unlock()
	default:
		a.mu.Lock()
		a.compressToolElision(force)
		a.mu.Unlock()
	}
	return nil
}

// compressHybrid applies tool_elision then selective if still over threshold.
func (a *Agent) compressHybrid(ctx context.Context) error {
	a.compressToolElision(true)

	stats := a.computeContextStats()
	threshold := a.cfg.ContextCompression.ThresholdPercent
	if threshold == 0 {
		threshold = 100
	}
	if stats.UsagePercent < threshold {
		return nil
	}

	a.compressSelective()

	stats = a.computeContextStats()
	if stats.UsagePercent < threshold {
		return nil
	}

	return a.Compact(ctx)
}

// compressToolElision replaces old tool arguments and results with placeholders.
// When force is true (manual /compress invocation), the recent-turn preserve
// window is reduced so that small histories still have messages to elide.
func (a *Agent) compressToolElision(force bool) {
	preserve := a.cfg.ContextCompression.PreserveRecentTurns
	if preserve == 0 {
		preserve = 2
	}
	boundary := computeElisionBoundary(len(a.history), preserve)
	// Forced compression must always do visible work. If the standard boundary
	// leaves nothing to elide, keep only the two most recent messages and
	// process everything before them.
	if force && boundary <= 1 {
		boundary = len(a.history) - 2
		if boundary < 1 {
			boundary = 1
		}
	}
	a.elideToolMessages(boundary)
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Applied tool_elision to messages before index %d", boundary)
	}
}

func computeElisionBoundary(histLen, preserve int) int {
	boundary := histLen - preserve*3
	if boundary < 1 {
		boundary = 1
	}
	return boundary
}

func (a *Agent) elideToolMessages(boundary int) {
	for i := 1; i < boundary && i < len(a.history); i++ {
		msg := &a.history[i]
		switch msg.Role {
		case Assistant:
			if len(msg.ToolCalls) > 0 {
				for j := range msg.ToolCalls {
					msg.ToolCalls[j].Arguments = "[elided]"
				}
			}
		case ToolRole:
			// Always replace the tool result body with a compact placeholder,
			// regardless of size, so tool_elision consistently frees tokens.
			msg.Content = "[tool result elided]"
		}
	}
}

// compressSelective drops oldest messages, keeping system + recent turns.
func (a *Agent) compressSelective() {
	preserve := a.cfg.ContextCompression.PreserveRecentTurns
	if preserve == 0 {
		preserve = 2
	}

	var newHistory []Message
	if len(a.history) > 0 && a.history[0].Role == System {
		newHistory = append(newHistory, a.history[0])
	}

	boundary := findCompressionBoundary(a.history, preserve)
	newHistory = append(newHistory, a.history[boundary:]...)

	removed := len(a.history) - len(newHistory)
	a.history = newHistory

	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Applied selective compression: removed %d messages", removed)
	}
}

// findCompressionBoundary finds the oldest message index to keep, ensuring
// tool call chains are never split.
func findCompressionBoundary(history []Message, preserve int) int {
	turnsKept := 0
	boundary := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == User {
			turnsKept++
			if turnsKept >= preserve {
				boundary = i
				break
			}
		}
	}

	// Ensure we don't split tool call chains.
	boundary = widenBoundaryForChains(history, boundary)
	return boundary
}

func widenBoundaryForChains(history []Message, boundary int) int {
	for boundary > 1 {
		prevIdx := boundary - 1
		prevRole := history[prevIdx].Role

		if prevRole == ToolRole {
			boundary--
			for boundary > 1 && history[boundary-1].Role == ToolRole {
				boundary--
			}
			if boundary > 1 && history[boundary-1].Role == Assistant {
				boundary--
			}
			continue
		}

		if prevRole == Assistant && len(history[prevIdx].ToolCalls) > 0 {
			boundary--
			continue
		}

		break
	}
	return boundary
}

// checkSilentOverflow detects providers that silently accept an oversized
// prompt and return a successful response instead of an error (e.g. z.ai,
// Xiaomi MiMo-style truncation).  When the estimated context usage exceeds
// the configured window, it schedules compression for the next turn.
func (a *Agent) checkSilentOverflow() {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return
	}
	stats := a.computeContextStats()
	if stats.UsagePercent < 95 {
		return
	}
	a.cfg.Logger.Log(Warn, "Silent overflow detected: %d%% usage (%d / %d tokens)",
		stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
	a.emitEvent(OutputEvent{
		Type:         EventContextStats,
		ContextStats: &stats,
		Text:         "warning: context usage ≥ 95% without provider error — proactive compression will fire on next turn",
	})
}

// handleContextError checks if the error is a context-length error and, if
// OnContextError is enabled, applies the configured compression strategy
// to free context space.  Unlike compressToolElision (which only elides
// tool calls/results), this uses the full configured strategy — including
// selective (message removal) and summarization — so text-heavy
// conversations are handled too.
//
// When the configured strategy is tool_elision or micro (which may leave
// text content untouched), it escalates to selective as a fallback so the
// retry can make progress.
func (a *Agent) handleContextError(err error) {
	if !isContextLengthError(err) {
		return
	}

	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Context length error detected: %v", err)
	}
	a.emitEvent(OutputEvent{
		Type:     EventContent,
		Role:     System,
		Text:     fmt.Sprintf("Context length exceeded: %v. The conversation is too long for this model's context window.", err),
		Metadata: map[string]string{"category": "system-notification"},
	})

	if !a.cfg.ContextCompression.OnContextError {
		return
	}

	strategy := CompressionStrategy(a.cfg.ContextCompression.Strategy)
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Context length error — applying compression (strategy=%s)", strategy)
	}

	// The reactive compression (compressHistoryWithStrategy + the selective
	// escalation below) is all in-memory; hold the mutex for the whole
	// transaction. Those helpers assume the lock is held.
	a.mu.Lock()
	defer a.mu.Unlock()

	// Apply the configured strategy.  We pass force=true so the strategy
	// runs even when internal thresholds aren't met (overflow is an emergency).
	a.compressHistoryWithStrategy(string(strategy), true)

	// If the configured strategy is tool_elision or micro and context is
	// STILL over the hard ceiling, escalate to selective (remove old turns).
	// This handles text-heavy conversations where eliding tool data leaves
	// all user+assistant messages intact.
	if strategy == "" || strategy == CompressionToolElision || strategy == CompressionMicro {
		stats := a.computeContextStats()
		maxTokens := a.effectiveMaxTokens()
		if maxTokens > 0 && stats.EstimatedTokens > maxTokens*90/100 {
			if a.cfg.Logger != nil {
				a.cfg.Logger.Log(Info, "Tool elision/micro freed insufficient space (%d/%d tokens) — escalating to selective",
					stats.EstimatedTokens, maxTokens)
			}
			a.compressSelective()
		}
	}
}

// compressHistoryWithStrategy applies the named compression strategy
// directly (empty = tool_elision).  The force parameter bypasses internal
// per-strategy thresholds.
func (a *Agent) compressHistoryWithStrategy(strategy string, force bool) {
	// Build a temporary Ctx-free strategy dispatch.  The summarization
	// strategy needs a real context, so we skip it here (it is not a
	// useful emergency strategy anyway since it costs an LLM call).
	switch CompressionStrategy(strategy) {
	case CompressionSelective:
		a.compressSelective()
	case CompressionToolElision:
		a.compressToolElision(force)
	case CompressionMicro:
		a.microCompactForced(force)
	case CompressionHybrid:
		a.compressToolElision(true)
		stats := a.computeContextStats()
		maxTokens := a.effectiveMaxTokens()
		if maxTokens > 0 && stats.EstimatedTokens > maxTokens*90/100 {
			a.compressSelective()
		}
	default:
		a.compressToolElision(force)
	}
}

// isContextLengthError reports whether the error indicates the LLM's context
// window was exceeded. It uses both structured classification (checking
// ProviderError.IsContextOverflow via hooks.IsContextOverflow) and string
// matching so that all provider error formats are recognised.
func isContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	// Structured check first — catches ProviderError where the hook
	// pipeline already classified IsContextOverflow=true.
	if hooks.IsContextOverflow(err) {
		return true
	}
	// Fallback string matching for errors not wrapped as ProviderError.
	msg := strings.ToLower(err.Error())
	patterns := []string{
		"context_length_exceeded",
		"context length",
		"maximum context",
		"token limit",
		"max_tokens",
		"too many tokens",
		"context window",
	}
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// SetBufferedToolCallCountForTest manually sets the buffered tool call
// counter. It is intended only for tests that exercise status labels without
// driving a real stream.
