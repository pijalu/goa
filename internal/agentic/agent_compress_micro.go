// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
)

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

// summarizeHistory runs the compaction summarize call and returns the
// summary text plus the provider-reported usage of the summarize request
// (nil when the provider emitted none — CX4 compaction_summary.usage).
//
// Transient failures retry through the SAME provider retry/backoff path the
// conversation turns use (resolveRetryPlan → shouldRetryStreamError →
// scheduleRetryAttempt): a 429 rate limit during summarize must send the
// compaction into retries, not fail it (bugs.md summarize-429). The budget
// and backoff come from the same policy resolution as the turn path.
// Context-overflow errors are deliberately excluded from this loop:
// compactOrdered owns the once-only shrink-and-retry recovery for them.
func (a *Agent) summarizeHistory(ctx context.Context) (string, *provider.Usage, error) {
	// Diagnosability: elided tool calls in the history used to reach the
	// provider verbatim and 400 the summarize request (invalid JSON
	// function.arguments). migrateMessages now serializes them as text notes.
	// Counted on the internal history because migration erases the marker.
	a.mu.Lock()
	elidedCount := countElidedToolCalls(a.history)
	a.mu.Unlock()
	if elidedCount > 0 && a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "summarizeHistory: snapshot carries %d elided tool-call block(s), serialized as text notes", elidedCount)
	}

	model := a.cfg.Model
	opts := a.cfg.StreamOptions
	if opts.APIKey == "" && a.cfg.APIKey != "" {
		opts.APIKey = a.cfg.APIKey
	}
	// P13 (CA2/CA3): mark the summarize call as compaction so DeepSeek-compat
	// routes carry x-goa-compact: 1, letting hosts separate compaction
	// traffic from conversation requests (dsh GenerateOptions.purpose).
	opts.Purpose = provider.PurposeCompaction

	plan := resolveRetryPlan(opts)
	summary, usage, err := a.summarizeStreamOnce(ctx, model, opts)
	for retry := 0; err != nil; retry++ {
		if isContextLengthError(err) {
			break
		}
		// Same classification as conversation turns: 429/5xx/transport retry,
		// non-retryable 4xx and a dead parent context (user cancel) surface
		// immediately. Terminal episode: classified, no retry scheduled.
		if !shouldRetryStreamError(ctx, err, plan.policy) {
			a.emitRateLimit(model, err, 0, false, 0)
			break
		}
		if !plan.always && retry >= plan.maxRetries {
			// Terminal episode: the retry budget is exhausted.
			a.emitRateLimit(model, err, 0, false, 0)
			break
		}
		// Shared per-retry step: agent-log + EventRateLimit + progress bubble,
		// then the Retry-After-aware backoff wait (false when canceled).
		if !a.scheduleRetryAttempt(ctx, err, model, plan, retry) {
			break
		}
		summary, usage, err = a.summarizeStreamOnce(ctx, model, opts)
	}
	if err != nil {
		return "", nil, err
	}
	return summary, usage, nil
}

// summarizeStreamOnce performs ONE summarize attempt: it rebuilds the
// provider context from the live history (parity with the conversation retry
// path — executeRetryAttempt rebuilds per attempt too), opens the stream, and
// drains it into the summary text.
func (a *Agent) summarizeStreamOnce(ctx context.Context, model provider.Model, opts provider.StreamOptions) (string, *provider.Usage, error) {
	// Build the request from the SAME prefix the conversation turns use —
	// cfg.SystemPrompt, registered tool schemas, and buildProviderHistory's
	// migration (leading-system skip, elision notes, orphan-result pairing) —
	// then append the instruction as the final user message. Prefix parity
	// with conversation requests is by construction (one shared builder), the
	// deepseek-harness compaction-basic design ("replays the conversation's
	// own system prompt, tools, and shadowed-region messages verbatim").
	pCtx := a.buildProviderContext(ctx)
	if len(pCtx.Messages) == 0 {
		return "", nil, nil
	}
	pCtx.Messages = append(pCtx.Messages, provider.Message{
		Role: provider.RoleUser,
		Content: []provider.ContentBlock{
			{Type: provider.ContentBlockText, Text: compactSummaryInstruction},
		},
	})

	stream, err := a.stream(model, pCtx, opts)
	if err != nil {
		return "", nil, fmt.Errorf("summarization stream: %w", err)
	}

	summary, err := consumeSummarizeStream(ctx, stream)
	if err != nil {
		return "", nil, err
	}

	// The request now carries tool schemas, so the model may answer the
	// instruction with only a tool call and no text. An empty summary must
	// fail here rather than let Compact wipe the history with blank content.
	if strings.TrimSpace(summary) == "" {
		return "", nil, fmt.Errorf("summarization produced no text (model may have answered with a tool call only)")
	}

	// Capture the provider-reported usage of the summarize call for the
	// compaction_summary provenance event (the usage chunk rides the stream
	// result via stream_options.include_usage). Nil when absent.
	var usage *provider.Usage
	if res := stream.Result(); res != nil {
		usage = res.Usage
	}

	return summary, usage, nil
}

// consumeSummarizeStream drains the summarize stream into the summary text.
// It returns on context cancellation, on an in-stream provider error, and on
// a terminal stream failure; a clean close yields the accumulated text.
func consumeSummarizeStream(ctx context.Context, stream *provider.AssistantMessageEventStream) (string, error) {
	var summary strings.Builder
	for event := range stream.Seq() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if event.Type == provider.EventTextDelta {
			summary.WriteString(event.Delta)
		}
		if event.Type == provider.EventError {
			// %w keeps the *hooks.ProviderError chain intact: the retry loop
			// in summarizeHistory classifies mid-stream failures (429 rate
			// limit, 5xx) through errors.As — a %v wrap would strand them as
			// unclassifiable text and skip the retry.
			return "", fmt.Errorf("summarization error: %w", event.Error)
		}
	}

	if err := stream.Err(); err != nil {
		return "", fmt.Errorf("summarization failed: %w", err)
	}
	return summary.String(), nil
}

// --- Pre-compaction tool-result pruning (CX1) ---

// pruneToolResultsPreCompact runs the model-free tool-result pruning passes
// ahead of summarizeHistory (CX1 + P4):
//
//  1. Stale-token pruning (P4): old large tool-result bodies outside the
//     protected recent window are replaced with a placeholder (gate ≥20K
//     reclaimable). This reclaims the bulk of a dump-heavy session at zero
//     token/latency cost.
//  2. Threshold pruning (CX1): every remaining over-budget ToolRole message is
//     rewritten in place to head + PruneMarker + tail (Unicode code points),
//     preserving the tool-call/result pairing (ToolCallID, ToolName and every
//     field except Content are untouched).
//
// The pass then re-measures the context estimate: when pruning dropped usage
// under the escalation level, the caller SKIPS the summarize LLM call (the
// pruned history already fits). It returns skip=true only in that case, plus
// the pre-pass stats and work record for the caller's EventCompact. Both
// passes are idempotent: a second invocation prunes nothing.
func (a *Agent) pruneToolResultsPreCompact() (skip bool, before ContextStats, res compactionResult) {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 {
		return false, before, res
	}
	cfg := a.cfg.ContextCompression.ToolResultPruning.resolve()

	a.mu.Lock()
	before = a.computeContextStats()
	staleChanged, staleReclaimed := pruneStaleToolOutput(a.history)
	changed := a.pruneToolResultsLocked(cfg)
	changed += staleChanged
	if staleChanged > 0 {
		// Tool payloads were replaced in place by the stale pass: the recorded
		// provider prompt no longer matches the conversation.
		a.invalidateContextUsageLocked()
	}
	skip = changed > 0 &&
		a.computeContextStatsForMax(maxTokens).UsagePercent < a.cfg.ContextCompression.resolveThresholds().effectiveHard()
	a.mu.Unlock()

	if changed > 0 && a.cfg.Logger != nil {
		note := ""
		if skip {
			note = "; pressure resolved, skipping summarize"
		}
		a.cfg.Logger.Log(Info, "tool-result pruner: pruned %d over-budget tool result(s) (threshold=%d chars, stale=%d tokens reclaimed)%s",
			changed, cfg.ThresholdChars, staleReclaimed, note)
	}
	return skip, before, compactionResult{changed: changed}
}

// pruneToolResultsLocked rewrites every over-budget tool result in history to
// head + PruneMarker + tail under a.mu. It returns the number of results
// pruned. A result already within the threshold (including one pruned by an
// earlier pass) is left untouched, making the pass idempotent.
func (a *Agent) pruneToolResultsLocked(cfg ToolResultPruningConfig) int {
	changed := 0
	for i := range a.history {
		msg := &a.history[i]
		if msg.Role != ToolRole {
			continue
		}
		pruned, ok := pruneToolResultContent(msg.Content, cfg)
		if !ok {
			continue
		}
		msg.Content = pruned
		changed++
	}
	if changed > 0 {
		// Tool payloads were replaced in place: the recorded provider prompt
		// no longer matches the conversation.
		a.invalidateContextUsageLocked()
	}
	return changed
}

// pruneToolResultContent returns the bounded replacement for an over-budget
// tool result body: head + PruneMarker + tail in Unicode code points (runes),
// or ok=false when the content is within the threshold. Slicing by rune
// never splits a UTF-8 multi-byte sequence (the dsh code-point rule). A valid
// configuration keeps head + marker + tail within the threshold, so the
// replacement is at most ThresholdChars runes and strictly smaller than the
// triggering input.
func pruneToolResultContent(content string, cfg ToolResultPruningConfig) (string, bool) {
	runes := []rune(content)
	if len(runes) <= cfg.ThresholdChars {
		return "", false
	}
	head := cfg.HeadChars
	if head > len(runes) {
		head = len(runes)
	}
	tail := cfg.TailChars
	if tail > len(runes)-head {
		tail = len(runes) - head
	}
	return string(runes[:head]) + PruneMarker + string(runes[len(runes)-tail:]), true
}

// --- Context Compression ---

// ContextStats returns the current context window usage statistics.
