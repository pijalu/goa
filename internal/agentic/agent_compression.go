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
// plain-text note during provider-bound serialization (models imitate
// the placeholder as call arguments, and strict providers 400 requests whose
// function.arguments are not valid JSON — which broke /compress:summarize).
const elidedToolCallArguments = "[elided]"

// elidedToolResultContent is the placeholder written by elideToolMessages in
// place of elided tool result bodies.
const elidedToolResultContent = "[tool result elided]"

func (a *Agent) Compact(ctx context.Context) error {
	a.mu.Lock()
	empty := len(a.history) == 0
	before := a.computeContextStats()
	a.mu.Unlock()
	if empty {
		return nil
	}

	// Micro compaction is an OPT-IN step, disabled by default so summarize is
	// always the default compaction path on a full window.
	//
	// Micro must NEVER fire as a first pass. When enabled it is run FIRST only
	// as a DRY-RUN (estimation only, no mutation) to validate whether it could
	// meet the required shrink — but the dry-run never mutates history and never
	// short-circuits the summarize. The summarize below ALWAYS runs on the
	// ORIGINAL, untouched history so the provider prefix cache is preserved.
	//
	// ONLY if that summarize fails with a context-overflow error (the input to
	// summarize was itself too big for the window) do we apply micro compaction
	// for real — to create enough room — then retry summarize on the shrunk
	// input. That summarize-overflow path is the single place micro mutates.
	if a.cfg.ContextCompression.MicroCompaction.Enabled {
		a.microDryRunValidate()
	}

	summary, err := a.summarizeHistory(ctx)
	if err != nil && isContextLengthError(err) && a.cfg.ContextCompression.MicroCompaction.Enabled {
		// Summarize self-overflowed: the history to summarize was too big for
		// the window. Now — and only now — apply micro compaction to create
		// enough space, then retry summarize on the reduced input.
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Compact: summarize overflowed (%v); applying micro compaction to make room and retrying", err)
		}
		a.applyMicroForSummarize()
		summary, err = a.summarizeHistory(ctx)
	}
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
	removed := len(a.history) - 2
	if removed < 0 {
		removed = 0
	}
	a.history = []Message{
		{Type: Content, Role: User, Content: compactSummaryRequestPrompt},
		{Type: Content, Role: Assistant, Content: summary},
	}
	// History was replaced wholesale: the recorded provider prompt is stale.
	a.invalidateContextUsageLocked()
	a.mu.Unlock()

	// Compact keeps its internal emission (public API contract +
	// TestAgent_CompactEmitsCompactEvent), now enriched with the structured
	// payload. Text stays the summary so existing consumers keep working.
	a.emitCompaction("summarize", before, a.ContextStats(), removed, 0, summary)
	return nil
}

// microDryRunValidate runs the micro-compaction dry-run (estimation only — it
// never mutates history) and logs whether micro could meet the required shrink
// on its own. This is purely diagnostic: it validates micro's feasibility up
// front WITHOUT letting micro become the first-pass action. The summarize that
// follows always runs on the original history; micro is only ever applied for
// real on the summarize-overflow fallback (applyMicroForSummarize).
func (a *Agent) microDryRunValidate() {
	cfg := a.cfg.ContextCompression.MicroCompaction
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 || a.cfg.Logger == nil {
		return
	}
	// The required shrink target: micro would need to bring estimated usage
	// under the escalation level (effectiveHard−5), the same headroom the
	// reactive paths reserve so the next request goes out with margin.
	target := maxTokens * a.cfg.ContextCompression.resolveThresholds().escalationPercent() / 100

	a.mu.Lock()
	before := a.computeContextStats()
	changed, freed := a.microCompactionDryRun(cfg, true)
	meets := changed > 0 && before.EstimatedTokens-freed <= target
	a.mu.Unlock()

	if meets {
		a.cfg.Logger.Log(Info, "Compact: micro dry-run COULD meet required shrink (would free ~%d tokens to reach %d); still summarizing original history to preserve cache", freed, target)
	} else {
		a.cfg.Logger.Log(Info, "Compact: micro dry-run cannot meet required shrink (would free ~%d tokens, need to reach %d); summarizing original history", freed, target)
	}
}

// applyMicroForSummarize applies micro compaction unconditionally (the
// summarize request just overflowed, so we need room regardless of any gate)
// and emits the resulting EventCompact. This is the ONLY path on which micro
// compaction mutates history during Compact — a last resort after summarize
// itself failed with a context-overflow error.
func (a *Agent) applyMicroForSummarize() {
	cfg := a.cfg.ContextCompression.MicroCompaction
	a.mu.Lock()
	before := a.computeContextStats()
	res := a.applyMicroLocked(cfg)
	a.mu.Unlock()
	a.emitCompactionResult("micro", before, res, "summarize overflow fallback")
}

// applyMicroLocked performs the real in-place micro compaction pass under a.mu
// (forced: bypasses the ratio/cache gates — Compact already decided to shrink).
// Returns the work record for the caller's EventCompact. The caller must hold
// a.mu.
func (a *Agent) applyMicroLocked(cfg MicroCompactionConfig) compactionResult {
	if len(a.history) == 0 {
		return compactionResult{}
	}
	keepIdx := computeKeepIdx(a.history, cfg.KeepRecentMessages, true)
	changed := a.truncateToolResults(a.history, keepIdx, cfg)
	if changed > 0 && a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Applied micro compaction: truncated %d tool results (keepIdx=%d)", changed, keepIdx)
	}
	return compactionResult{changed: changed}
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
	// thresholds, so skip the tier gate except at the emergency ceiling. Uses
	// effectiveHard so a disabled proactive hard layer (0) still leaves the
	// emergency branch reachable for the reactive paths.
	stats := a.ContextStats()
	if rt.triggerStrategy == CompressionMicro && stats.UsagePercent < rt.effectiveHard() {
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
		before, res := a.runElision(force)
		a.emitCompactionResult("elision", before, res, "")
	case CompressionSelective:
		before, res := a.runSelective()
		a.emitCompactionResult("selective", before, res, "")
	case CompressionSummarize:
		return a.Compact(ctx)
	case CompressionHybrid:
		return a.compressHybrid(ctx)
	case CompressionMicro:
		before, res := a.microCompactForced(force)
		a.emitCompactionResult("micro", before, res, "")
	default:
		before, res := a.runElision(force)
		a.emitCompactionResult("elision", before, res, "")
	}
	return nil
}

// compactionResult is the outcome of a single in-memory compression step,
// captured while the step held a.mu so callers can emit an accurate
// EventCompact after unlock (emitEvent re-acquires a.mu, so it must never
// run under the lock).
type compactionResult struct {
	removed     int  // messages dropped from history
	freedTokens int  // estimated tokens freed (0 = unknown)
	changed     int  // messages mutated in place (elision / micro truncation)
	escalated   bool // a cheaper step escalated into a harder one (elision→selective)
}

// didWork reports whether the pass changed history in any observable way.
func (r compactionResult) didWork() bool {
	return r.removed > 0 || r.changed > 0 || r.freedTokens > 0
}

// emitCompactionResult emits a single structured EventCompact after a
// compression pass, measuring the post-pass usage off-lock. It emits nothing
// when the pass did no work so observers never see a phantom compaction.
func (a *Agent) emitCompactionResult(strategy string, before ContextStats, res compactionResult, detail string) {
	if !res.didWork() {
		return
	}
	a.emitCompaction(strategy, before, a.ContextStats(), res.removed, res.freedTokens, detail)
}

// emitCompaction emits the structured EventCompact shared by every
// compression path. The Text label mirrors Compaction.Strategy so legacy
// consumers keyed on Text (session JSONL greps, the footer classifier) keep
// working while new consumers read the structured payload.
func (a *Agent) emitCompaction(strategy string, before, after ContextStats, removed, freed int, detail string) {
	a.emitEvent(OutputEvent{
		Type: EventCompact,
		Text: strategy,
		Compaction: &CompactionInfo{
			Strategy:    strategy,
			BeforePct:   before.UsagePercent,
			AfterPct:    after.UsagePercent,
			FreedTokens: freed,
			Removed:     removed,
			Detail:      detail,
		},
	})
}

// runElision executes compressToolElision under a.mu and returns the
// pre-pass stats plus the work done. The emission happens after unlock in
// the caller.
func (a *Agent) runElision(force bool) (ContextStats, compactionResult) {
	a.mu.Lock()
	before := a.computeContextStats()
	res := a.compressToolElision(force)
	a.mu.Unlock()
	return before, res
}

// runSelective executes compressSelective under a.mu and returns the
// pre-pass stats plus the work done.
func (a *Agent) runSelective() (ContextStats, compactionResult) {
	a.mu.Lock()
	before := a.computeContextStats()
	res := a.compressSelective()
	a.mu.Unlock()
	return before, res
}

// compressHybrid applies tool_elision then selective if still over threshold.
// The in-memory steps run under a.mu; Compact (network) runs off-lock.
// The "still too full" gate uses the escalation level (effectiveHard−5), not
// the proactive trigger — so the last-resort summarize only fires when the
// window is genuinely near full, independent of any opt-in trigger level.
//
// Emission: exactly one EventCompact per invocation. When the cheap steps
// free enough, a single "hybrid" event fires; when they escalate to Compact,
// Compact emits its own "summarize" event and no "hybrid" event fires (the
// elision/selective work is subsumed by the summarize).
func (a *Agent) compressHybrid(ctx context.Context) error {
	threshold := a.cfg.ContextCompression.resolveThresholds().escalationPercent()

	a.mu.Lock()
	before := a.computeContextStats()
	res := a.compressToolElision(true)
	stats := a.computeContextStats()
	needMore := stats.UsagePercent >= threshold
	if needMore {
		sel := a.compressSelective()
		res.removed += sel.removed
		res.changed += sel.changed
		res.freedTokens += sel.freedTokens
		stats = a.computeContextStats()
		needMore = stats.UsagePercent >= threshold
	}
	a.mu.Unlock()
	if !needMore {
		a.emitCompactionResult("hybrid", before, res, "")
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
//
// It returns the work done (messages mutated in place + estimated tokens
// freed, plus any selective escalation) so the caller can emit one EventCompact
// after unlock. The caller must hold a.mu.
func (a *Agent) compressToolElision(force bool) compactionResult {
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
	changed, freed := a.elideToolMessages(boundary)
	if boundary > 1 {
		// Tool payloads were replaced in place: the recorded provider prompt
		// no longer matches the conversation.
		a.invalidateContextUsageLocked()
	}
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Applied tool_elision to messages before index %d", boundary)
	}
	res := compactionResult{changed: changed, freedTokens: freed}
	if escalate {
		// The elidable payload could not meet the hysteresis budget, so this
		// hot-cache bust would repeat next round. Drop old turns instead so
		// the bust buys real headroom (prefix-cache bust loop).
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "tool_elision budget unmet: escalating to selective compression")
		}
		sel := a.compressSelective()
		res.removed += sel.removed
		res.changed += sel.changed
		res.freedTokens += sel.freedTokens
		res.escalated = true
	}
	return res
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
// history growth (prefix-cache bust loop: the count boundary frees
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

// elideToolMessages replaces tool payloads before boundary with placeholders.
// It returns the number of messages mutated and the estimated tokens freed
// (sum of the per-message reclaim). The caller must hold a.mu.
func (a *Agent) elideToolMessages(boundary int) (changed, freed int) {
	for i := 1; i < boundary && i < len(a.history); i++ {
		msg := &a.history[i]
		reclaim := 0
		switch msg.Role {
		case Assistant:
			if len(msg.ToolCalls) > 0 {
				for j := range msg.ToolCalls {
					reclaim += estimateTokens(msg.ToolCalls[j].Arguments) - estimateTokens(elidedToolCallArguments)
					msg.ToolCalls[j].Arguments = elidedToolCallArguments
				}
			}
		case ToolRole:
			// Always replace the tool result body with a compact placeholder,
			// regardless of size, so tool_elision consistently frees tokens.
			reclaim += estimateTokens(msg.Content) - estimateTokens(elidedToolResultContent)
			msg.Content = elidedToolResultContent
		}
		if reclaim > 0 {
			changed++
			freed += reclaim
		}
	}
	return changed, freed
}

// compressSelective drops oldest messages, keeping system + recent turns.
// It returns the number of messages removed so the caller can emit one
// EventCompact after unlock. The caller must hold a.mu.
func (a *Agent) compressSelective() compactionResult {
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
	return compactionResult{removed: removed}
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
	// Use effectiveHard (not the raw configured hard): with proactive thresholds
	// disabled (0) the reactive ceiling still guards against silent truncation.
	hard := a.cfg.ContextCompression.resolveThresholds().effectiveHard()
	stats := a.computeContextStats()
	if stats.UsagePercent < hard {
		return
	}
	a.cfg.Logger.Log(Warn, "Silent overflow detected: %d%% usage (%d / %d tokens)",
		stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
	a.emitEvent(OutputEvent{
		Type:         EventContextStats,
		ContextStats: &stats,
		Text:         fmt.Sprintf("warning: context usage ≥ %d%% without provider error — compression will fire on next turn", hard),
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
	// enough at the window edge. With the default (empty) strategy this reduces
	// to tool_elision + selective; the explicit summarize/hybrid paths skip the
	// LLM call here (no ctx) and run the same free-space steps directly. Micro
	// compaction needs a lock-free emission boundary, so it runs off-lock via
	// compressHistoryWithStrategy (which emits its own "micro" event); the
	// selective follow-up then reports as "truncation".
	strategy := a.cfg.ContextCompression.Strategy
	if strategy == CompressionMicro {
		a.compressHistoryWithStrategy(string(strategy), true)
		a.mu.Lock()
		before := a.computeContextStats()
		res := a.compressSelective()
		a.mu.Unlock()
		a.emitCompactionResult("truncation", before, res, "")
		a.emitContextStats()
		return
	}
	a.mu.Lock()
	before := a.computeContextStats()
	var res compactionResult
	if strategy == "" {
		e := a.compressToolElision(true)
		s := a.compressSelective()
		res = mergeCompaction(e, s)
	} else {
		res = a.compressHistoryWithStrategyLocked(string(strategy), true)
		s := a.compressSelective()
		res = mergeCompaction(res, s)
	}
	a.mu.Unlock()
	a.emitCompactionResult("truncation", before, res, "")
	a.emitContextStats()
}

// mergeCompaction folds the work of two in-memory steps into one record so a
// multi-step pass emits a single EventCompact.
func mergeCompaction(x, y compactionResult) compactionResult {
	return compactionResult{
		removed:     x.removed + y.removed,
		freedTokens: x.freedTokens + y.freedTokens,
		changed:     x.changed + y.changed,
		escalated:   x.escalated || y.escalated,
	}
}

// handleContextError checks if the error is a context-length error and, if
// OnContextError is enabled, applies the hybrid compression strategy to free
// context space: tool_elision → selective (message removal) → summarize as a
// last resort (pi-style). This is the reactive safety net that stays on when
// proactive threshold compression is disabled (the default): cheap steps run
// first, and only if the window is still near full does it escalate — ending
// in a Compact (LLM summarize) when nothing cheaper freed enough.
func (a *Agent) handleContextError(ctx context.Context, err error) {
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

	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Context length error — applying hybrid compression (elision → selective → summarize)")
	}
	a.compressOverflowRecovery(ctx)
}

// compressOverflowRecovery applies the hybrid strategy for a PROVEN context
// overflow: tool_elision → selective (unconditional) → summarize (only if the
// estimate still sits at the window edge). Unlike compressHybrid (which gates
// selective behind the escalation level), a provider rejection proves the
// request exceeded the window — and the local estimate provably under-counts
// (a deepseek-v4 session overflowed at 84% estimated / 100% actual, and
// gating selective behind the estimate made the retry fail identically). So
// selective message removal ALWAYS runs here to buy real headroom.
func (a *Agent) compressOverflowRecovery(ctx context.Context) {
	a.mu.Lock()
	before := a.computeContextStats()
	e := a.compressToolElision(true)
	s := a.compressSelective()
	res := mergeCompaction(e, s)
	stats := a.computeContextStats()
	a.mu.Unlock()

	// If the estimate still sits at the window edge, escalate to a summarize as
	// the last resort (pi-style): the cheaper steps could not free enough.
	// When it escalates, Compact emits its own "summarize" event, so the
	// pre-escalation elision/selective work is NOT separately reported (it is
	// subsumed by the summarize) — exactly one EventCompact per recovery.
	threshold := a.cfg.ContextCompression.resolveThresholds().escalationPercent()
	if stats.UsagePercent >= threshold {
		if err := a.Compact(ctx); err != nil && a.cfg.Logger != nil {
			a.cfg.Logger.Log(Error, "Context-overflow summarize failed: %v", err)
		}
	} else {
		a.emitCompactionResult("overflow", before, res, "")
	}
	a.emitContextStats()
}

// compressHistoryWithStrategy applies the named compression strategy
// directly (empty = tool_elision).  The force parameter bypasses internal
// per-strategy thresholds. Micro compaction self-manages its own lock (and
// now reports through the caller's emission point).
func (a *Agent) compressHistoryWithStrategy(strategy string, force bool) {
	// Build a temporary Ctx-free strategy dispatch.  The summarization
	// strategy needs a real context, so we skip it here (it is not a
	// useful emergency strategy anyway since it costs an LLM call).
	if CompressionStrategy(strategy) == CompressionMicro {
		before, res := a.microCompactForced(force)
		a.emitCompactionResult("micro", before, res, "")
		return
	}
	a.mu.Lock()
	a.compressHistoryWithStrategyLocked(strategy, force)
	a.mu.Unlock()
}

// compressHistoryWithStrategyLocked is the a.mu-held core of
// compressHistoryWithStrategy: in-memory strategies only (selective /
// elision / hybrid's free-space steps). It returns the work done so the
// caller can emit a single EventCompact after unlock. Micro compaction is
// NOT handled here (it needs the lock-free emission boundary); the caller
// must route CompressionMicro to microCompactForced before acquiring a.mu.
func (a *Agent) compressHistoryWithStrategyLocked(strategy string, force bool) compactionResult {
	switch CompressionStrategy(strategy) {
	case CompressionSelective:
		return a.compressSelective()
	case CompressionHybrid:
		res := a.compressToolElision(true)
		stats := a.computeContextStats()
		maxTokens := a.effectiveMaxTokens()
		escalation := a.cfg.ContextCompression.resolveThresholds().escalationPercent()
		if maxTokens > 0 && stats.EstimatedTokens > maxTokens*escalation/100 {
			s := a.compressSelective()
			res = mergeCompaction(res, s)
		}
		return res
	case CompressionToolElision:
		return a.compressToolElision(force)
	default:
		return a.compressToolElision(force)
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
