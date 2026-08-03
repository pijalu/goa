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

// elidedToolCallArguments is the in-history marker written by elideToolMessages
// into the arguments of elided assistant tool calls. It is NOT valid JSON and
// must never reach a provider: migrateMessage converts elided calls to a
// plain-text note during provider-bound serialization (bugs.md: models imitate
// the placeholder as call arguments, and strict providers 400 requests whose
// function.arguments are not valid JSON — which broke /compress:summarize).
const elidedToolCallArguments = "[elided]"

// elidedToolResultContent is the placeholder written by elideToolMessages in
// place of elided tool result bodies.
const elidedToolResultContent = "[tool result elided]"

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
	// History was replaced wholesale: the recorded provider prompt is stale.
	a.invalidateContextUsageLocked()
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

	// Diagnosability: elided tool calls in the snapshot used to reach the
	// provider verbatim and 400 the summarize request (invalid JSON
	// function.arguments). migrateMessages now serializes them as text notes.
	a.logElidedSnapshotCount(msgs)

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
// The escalation layer (soft/trigger/hard/none) is selected from the
// configured thresholds and the cache gate; each layer runs its own resolved
// strategy (the soft layer only zero-LLM strategies).
func (a *Agent) maybeCompress(ctx context.Context) error {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return nil
	}

	rt := a.cfg.ContextCompression.resolveThresholds()

	// Legacy whole-strategy micro: micro compaction self-manages its internal
	// thresholds, so skip the tier gate except at the emergency ceiling.
	stats := a.ContextStats()
	if rt.triggerStrategy == CompressionMicro && stats.UsagePercent < rt.hard {
		return a.compressAndReport(ctx)
	}

	tier := a.proactiveTier(rt, maxTokens)
	switch tier {
	case tierHard:
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Hard-layer context compression: %d%% usage (%d / %d tokens)",
				stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
		}
		return a.compressAndReportWith(ctx, rt.hardStrategy)
	case tierTrigger:
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Context compression triggered: %d%% usage (%d / %d tokens)",
				stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
		}
		return a.compressAndReportWith(ctx, rt.triggerStrategy)
	case tierSoft:
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Soft-tier context maintenance: %d%% usage (%d / %d tokens)",
				stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
		}
		return a.compressSoftAndReport(ctx, rt.softStrategy)
	default:
		return nil
	}
}

// compressAndReport applies the configured strategy and emits fresh stats.
func (a *Agent) compressAndReport(ctx context.Context) error {
	return a.compressAndReportWith(ctx, a.cfg.ContextCompression.Strategy)
}

// compressAndReportWith applies the given strategy and emits fresh stats.
func (a *Agent) compressAndReportWith(ctx context.Context, strategy CompressionStrategy) error {
	if err := a.compressHistoryWith(ctx, strategy, false); err != nil {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Error, "Context compression failed: %v", err)
		}
		return err
	}
	a.emitContextStats()
	return nil
}

// compressSoftAndReport applies the soft-layer (zero-LLM) strategy and emits
// fresh stats. It never calls the LLM and never drops messages.
func (a *Agent) compressSoftAndReport(ctx context.Context, strategy CompressionStrategy) error {
	if err := a.compressHistoryWith(ctx, strategy, false); err != nil {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Error, "Soft-tier context compression failed: %v", err)
		}
		return err
	}
	a.emitContextStats()
	return nil
}

// emitContextStats emits the post-compression context stats event.
func (a *Agent) emitContextStats() {
	newStats := a.ContextStats()
	a.emitEvent(OutputEvent{
		Type:         EventContextStats,
		ContextStats: &newStats,
	})
}

// proactiveTier resolves the current usage and selects the compression tier.
func (a *Agent) proactiveTier(rt resolvedThresholds, maxTokens int) compressionTier {
	a.mu.Lock()
	stats := a.computeContextStatsForMax(maxTokens)
	tier := a.proactiveTierLocked(stats.UsagePercent, rt)
	a.mu.Unlock()
	return tier
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
		// microCompactForced self-manages a.mu (it emits after unlock), so do
		// not wrap it in a held lock here.
		a.microCompactForced(force)
	default:
		a.mu.Lock()
		a.compressToolElision(force)
		a.mu.Unlock()
	}
	return nil
}

// compressHybrid applies tool_elision then selective if still over threshold.
// The in-memory steps run under a.mu; Compact (network) runs off-lock.
func (a *Agent) compressHybrid(ctx context.Context) error {
	threshold := a.cfg.ContextCompression.resolveThresholds().trigger

	a.mu.Lock()
	a.compressToolElision(true)
	stats := a.computeContextStats()
	needMore := stats.UsagePercent >= threshold
	if needMore {
		a.compressSelective()
		stats = a.computeContextStats()
		needMore = stats.UsagePercent >= threshold
	}
	a.mu.Unlock()
	if !needMore {
		return nil
	}
	return a.Compact(ctx)
}

// compressToolElision replaces old tool arguments and results with placeholders.
// When force is true (manual /compress invocation), the recent-turn preserve
// window is reduced so that small histories still have messages to elide.
// The proactive path (force=false) picks its boundary via
// proactiveElisionBoundary: eager with a cold cache, token-budgeted with
// hysteresis when the pass must bust a hot provider prefix cache.
func (a *Agent) compressToolElision(force bool) {
	preserve := a.cfg.ContextCompression.PreserveRecentTurns
	if preserve == 0 {
		preserve = 2
	}
	boundary := computeElisionBoundary(len(a.history), preserve)
	escalate := false
	if force {
		// Forced compression must always do visible work. If the standard
		// boundary leaves nothing to elide, keep only the two most recent
		// messages and process everything before them.
		boundary = forcedElisionBoundary(boundary, len(a.history))
	} else {
		boundary, escalate = a.proactiveElisionBoundary(boundary)
	}
	a.elideToolMessages(boundary)
	if boundary > 1 {
		// Tool payloads were replaced in place: the recorded provider prompt
		// no longer matches the conversation.
		a.invalidateContextUsageLocked()
	}
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Applied tool_elision to messages before index %d", boundary)
	}
	if escalate {
		// The elidable payload could not meet the hysteresis budget, so this
		// hot-cache bust would repeat next round. Drop old turns instead so
		// the bust buys real headroom (bugs.md prefix-cache bust loop).
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "tool_elision budget unmet: escalating to selective compression")
		}
		a.compressSelective()
	}
}

// forcedElisionBoundary applies the forced-pass floor: keep only the two most
// recent messages when the count-based boundary leaves nothing to elide.
func forcedElisionBoundary(boundary, histLen int) int {
	if boundary > 1 {
		return boundary
	}
	boundary = histLen - 2
	if boundary < 1 {
		boundary = 1
	}
	return boundary
}

// proactiveElisionBoundary returns the elision boundary for a threshold-
// triggered pass, and whether the caller must escalate to selective
// compression afterwards. With a cold (or cache-gate-disabled) cache the
// legacy count-based boundary is used: mutation is free, so elide eagerly.
//
// With a HOT cache (reachable only at/above the deferral ceiling, where the
// gate overrides cache protection) the pass must buy hysteresis: elide
// oldest-first by token budget until the estimated usage drops to the
// elision target (hard−20), so one cache bust buys many rounds of headroom
// instead of re-busting every round as the count boundary advances with
// history growth (bugs.md prefix-cache bust loop: the count boundary frees
// only the ~2 messages that crossed it per round while usage stays at the
// ceiling, so the bust repeats every round and never converges). The budget
// walk may extend past the count boundary into the preserve window but never
// touches the in-flight tail (the last two messages). When the elidable
// payload cannot meet the budget, escalate is set: nibbling again next round
// is strictly worse than one headroom-buying selective pass.
//
// The caller must hold a.mu (cache gate and history reads).
func (a *Agent) proactiveElisionBoundary(countBoundary int) (boundary int, escalate bool) {
	if a.cfg.ContextCompression.DisableCacheGate || a.cacheAssumedColdForProactive() {
		return countBoundary, false
	}
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 {
		return countBoundary, false
	}
	rt := a.cfg.ContextCompression.resolveThresholds()
	need := a.computeContextStatsForMax(maxTokens).EstimatedTokens - maxTokens*rt.elisionTargetPercent()/100
	if need <= 0 {
		return countBoundary, false
	}
	freed := 0
	boundary = 1
	tailCap := len(a.history) - 2
	for i := 1; i < tailCap && freed < need; i++ {
		freed += elisionReclaim(&a.history[i])
		boundary = i + 1
	}
	return boundary, freed < need
}

// elisionReclaim estimates the tokens elideToolMessages would free on msg:
// tool-call arguments collapse to the elision marker and tool results to the
// result placeholder. Messages elideToolMessages would not touch (and already
// elided ones) reclaim zero or less; the caller sums the raw deltas.
func elisionReclaim(msg *Message) int {
	reclaim := 0
	switch msg.Role {
	case Assistant:
		for j := range msg.ToolCalls {
			reclaim += estimateTokens(msg.ToolCalls[j].Arguments) - estimateTokens(elidedToolCallArguments)
		}
	case ToolRole:
		reclaim += estimateTokens(msg.Content) - estimateTokens(elidedToolResultContent)
	}
	return reclaim
}

// logElidedSnapshotCount logs how many elided tool-call blocks a
// provider-bound snapshot carries, so future summarize-request rejections
// are diagnosable from agent.log alone.
func (a *Agent) logElidedSnapshotCount(msgs []Message) {
	if a.cfg.Logger == nil {
		return
	}
	if n := countElidedToolCalls(msgs); n > 0 {
		a.cfg.Logger.Log(Info, "summarizeHistory: snapshot carries %d elided tool-call block(s), serialized as text notes", n)
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
					msg.ToolCalls[j].Arguments = elidedToolCallArguments
				}
			}
		case ToolRole:
			// Always replace the tool result body with a compact placeholder,
			// regardless of size, so tool_elision consistently frees tokens.
			msg.Content = elidedToolResultContent
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
	if removed > 0 {
		// Messages were dropped: the recorded provider prompt is stale.
		a.invalidateContextUsageLocked()
	}

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
// the hard ceiling, it schedules compression for the next turn.
func (a *Agent) checkSilentOverflow() {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return
	}
	hard := a.cfg.ContextCompression.resolveThresholds().hard
	stats := a.computeContextStats()
	if stats.UsagePercent < hard {
		return
	}
	a.cfg.Logger.Log(Warn, "Silent overflow detected: %d%% usage (%d / %d tokens)",
		stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
	a.emitEvent(OutputEvent{
		Type:         EventContextStats,
		ContextStats: &stats,
		Text:         fmt.Sprintf("warning: context usage ≥ %d%% without provider error — proactive compression will fire on next turn", hard),
	})
}

// maybeCompressAfterLengthTruncation forces compression when the provider
// truncated the round at the edge of the context window: finish_reason=length
// with the reported total (gross prompt + completion) at ≥99% of the
// effective window. That truncation is the last warning before the next
// request is rejected with context_length_exceeded — a deepseek-v4 session
// ended its penultimate round at exactly total_tokens=window (finish_reason
// "length"), appended another tool result, and the next request came back
// HTTP 400. A plain max_tokens output cap does NOT trigger this: the total
// must actually be at the window.
func (a *Agent) maybeCompressAfterLengthTruncation() {
	a.mu.Lock()
	stopReason := a.lastStopReason
	total := a.lastGrossInputTokens + a.lastUsageOutputTokens
	a.mu.Unlock()
	if stopReason != provider.StopReasonMaxTokens {
		return
	}
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 || total < maxTokens*99/100 {
		return
	}
	a.cfg.Logger.Log(Warn, "provider truncated output at the context window edge (%d/%d tokens, finish_reason=length); forcing compression before the next round",
		total, maxTokens)
	// Cheap strategies only touch tool payloads and cannot be trusted to free
	// enough at the window edge; follow with selective turn removal, same as
	// the overflow-recovery path.
	a.compressHistoryWithStrategy(string(a.cfg.ContextCompression.Strategy), true)
	a.mu.Lock()
	a.compressSelective()
	a.mu.Unlock()
	a.emitContextStats()
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

	// compressHistoryWithStrategy self-manages a.mu per strategy branch (micro
	// emits after unlock; the pure strategies take the lock around their
	// mutation), so do not hold the lock across it here.
	a.compressHistoryWithStrategy(string(strategy), true)

	// Cheap strategies (tool_elision, micro) touch only tool payloads and can
	// leave the context nearly full — the bulk is often assistant text,
	// tool-call arguments and sheer message count. The provider just PROVED
	// the request exceeds the window, so the local estimate must not veto
	// escalation: it under-reads real usage (a deepseek-v4 session overflowed
	// at 84% estimated / 100% actual, the 90% escalation gate never opened,
	// and the retry failed identically). Always escalate to selective message
	// removal so the retry goes out with real headroom.
	if strategy == "" || strategy == CompressionToolElision || strategy == CompressionMicro {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Provider rejected the request for context size — escalating to selective compression (strategy=%s)", strategy)
		}
		a.mu.Lock()
		a.compressSelective()
		a.mu.Unlock()
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
		a.mu.Lock()
		a.compressSelective()
		a.mu.Unlock()
	case CompressionToolElision:
		a.mu.Lock()
		a.compressToolElision(force)
		a.mu.Unlock()
	case CompressionMicro:
		// microCompactForced self-manages a.mu (it emits after unlock).
		a.microCompactForced(force)
	case CompressionHybrid:
		a.mu.Lock()
		a.compressToolElision(true)
		stats := a.computeContextStats()
		maxTokens := a.effectiveMaxTokens()
		escalation := a.cfg.ContextCompression.resolveThresholds().escalationPercent()
		if maxTokens > 0 && stats.EstimatedTokens > maxTokens*escalation/100 {
			a.compressSelective()
		}
		a.mu.Unlock()
	default:
		a.mu.Lock()
		a.compressToolElision(force)
		a.mu.Unlock()
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