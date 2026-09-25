// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"errors"
	"fmt"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// DefaultRemoteCompactRetainedBudget mirrors Codex
// RETAINED_MESSAGE_TOKEN_BUDGET: the server-compacted replacement transcript's
// retained tail is trimmed so its estimated tokens stay under this many
// tokens. Configurable via ContextCompressionConfig.RemoteCompactRetainedBudget.
const DefaultRemoteCompactRetainedBudget = 64_000

// errRemoteCompactFailed wraps any remote-compaction failure so the caller
// (Compact) can distinguish "the remote path was attempted and failed, fall
// back to the local ladder" from a local summarization error. History is never
// partially applied on this path: either the whole replacement lands or none
// of it does.
type errRemoteCompactFailed struct{ cause error }

func (e *errRemoteCompactFailed) Error() string {
	return "remote compaction failed: " + e.cause.Error()
}
func (e *errRemoteCompactFailed) Unwrap() error { return e.cause }

// isRemoteCompactFailure reports whether err is a remote-compaction failure
// (as opposed to a local summarization error), so the fallback ordering can
// branch on it without string matching.
func isRemoteCompactFailure(err error) bool {
	var rc *errRemoteCompactFailed
	return errors.As(err, &rc)
}

// compactRemote attempts server-side conversation compaction via the
// provider's POST /responses/compact endpoint. It returns applied=true when
// the replacement landed (history replaced, generation advanced, provenance
// recorded); on any failure it returns applied=false with a distinct
// errRemoteCompactFailed and leaves history untouched so the caller falls back
// to the local ladder.
//
// The caller (Compact) has already established that remote compaction is
// available (operator gate + provider capability) and opened the compaction
// transaction (txID). This method completes that transaction's
// start → summary → end triple on success. It additionally type-asserts the
// resolved provider to RemoteCompactor; a provider whose capability is
// advertised but which lacks the implementation is reported as unsupported
// (applied=false) rather than panicking.
func (a *Agent) compactRemote(ctx context.Context, txID string) (bool, error) {
	compactor, ok := a.remoteCompactor()
	if !ok {
		return false, &errRemoteCompactFailed{cause: provider.ErrRemoteCompactUnsupported}
	}

	pCtx := a.buildProviderContext(ctx)
	if len(pCtx.Messages) == 0 {
		return false, &errRemoteCompactFailed{cause: errors.New("empty conversation")}
	}

	opts := a.remoteCompactOptions()

	// Before/after estimates for provenance, captured under the lock.
	a.mu.Lock()
	before := a.computeContextStats()
	shadowedEnd := len(a.history)
	a.mu.Unlock()

	resp, err := compactor.Compact(ctx, provider.CompactRequest{
		Model:   a.cfg.Model,
		Context: pCtx,
		Options: opts,
	})
	if err != nil {
		return false, &errRemoteCompactFailed{cause: err}
	}

	// Convert + bound the replacement OFF the lock, then apply atomically.
	replacement := providerMessagesToInternal(resp.Messages)
	replacement = a.boundRemoteCompactReplacement(replacement)
	if len(replacement) == 0 {
		return false, &errRemoteCompactFailed{cause: errors.New("remote compaction returned an empty replacement")}
	}

	a.mu.Lock()
	a.history = replacement
	a.cacheGeneration++
	// History was replaced wholesale (non-prefix rewrite): the recorded
	// provider prompt is stale.
	a.invalidateContextUsageLocked()
	a.mu.Unlock()

	after := a.ContextStats()
	a.emitCompactionTxSummary(&CompactionTx{
		ID:             txID,
		ShadowedStart:  0,
		ShadowedEnd:    shadowedEnd,
		ShadowedCount:  shadowedEnd,
		ShadowedTokens: before.EstimatedTokens,
		FreedTokens:    before.EstimatedTokens - after.EstimatedTokens,
		Provider:       string(a.cfg.Model.Provider),
		Model:          a.cfg.Model.ID,
		Usage:          resp.Usage,
	})
	a.emitCompactionTxEnd(txID, "")
	removed := shadowedEnd - len(replacement)
	if removed < 0 {
		removed = 0
	}
	a.emitCompaction(string(CompressionRemoteCompact), before, after, removed, 0, remoteCompactDetail(replacement))
	return true, nil
}

// remoteCompactor resolves the active model's provider and asserts the
// RemoteCompactor capability. ok=false means the provider does not implement
// server-side compaction.
func (a *Agent) remoteCompactor() (provider.RemoteCompactor, bool) {
	p, ok := provider.GetApiProvider(a.cfg.Model.Api)
	if !ok {
		return nil, false
	}
	c, ok := p.(provider.RemoteCompactor)
	return c, ok
}

// remoteCompactOptions builds the StreamOptions for the compact call: the same
// credentials, sampling, and session/cache identity the conversation turns
// use, with the current cache key stamped on. Purpose is marked compaction so
// transport-level attribution separates it from conversation traffic.
func (a *Agent) remoteCompactOptions() provider.StreamOptions {
	opts := a.cfg.StreamOptions
	if opts.APIKey == "" && a.cfg.APIKey != "" {
		opts.APIKey = a.cfg.APIKey
	}
	opts.PromptCacheKey = a.cacheKey(a.cfg.Model)
	opts.Purpose = provider.PurposeCompaction
	return opts
}

// boundRemoteCompactReplacement trims the retained tail of a remote-compacted
// transcript (newest-first) so its estimated token cost stays under the
// configured retained budget (Codex RETAINED_MESSAGE_TOKEN_BUDGET). It never
// drops below one message: a single-message transcript is returned as-is even
// if it exceeds the budget, so the replacement is never emptied.
func (a *Agent) boundRemoteCompactReplacement(messages []Message) []Message {
	budget := a.remoteCompactRetainedBudget()
	if budget < 0 || len(messages) == 0 {
		return messages
	}
	// Walk newest-first, keeping whole messages until the budget is exhausted.
	kept := make([]Message, 0, len(messages))
	remaining := budget
	for i := len(messages) - 1; i >= 0; i-- {
		tok := messageTokenCount(&messages[i])
		if tok > remaining && len(kept) > 0 {
			break
		}
		kept = append(kept, messages[i])
		remaining -= tok
		if remaining < 0 {
			remaining = 0
		}
	}
	// Reverse to restore oldest-first order.
	for l, r := 0, len(kept)-1; l < r; l, r = l+1, r-1 {
		kept[l], kept[r] = kept[r], kept[l]
	}
	return kept
}

// remoteCompactRetainedBudget resolves the configured retained budget,
// defaulting to DefaultRemoteCompactRetainedBudget when unset (zero). A
// negative value disables the bound.
func (a *Agent) remoteCompactRetainedBudget() int {
	b := a.cfg.ContextCompression.RemoteCompactRetainedBudget
	if b == 0 {
		return DefaultRemoteCompactRetainedBudget
	}
	return b
}

// remoteCompactDetail builds the EventCompact detail string for a remote
// compaction. It carries a bounded summary of the landed transcript (message
// count + first text snippet), never raw session keys or full prompts
// (invariant 4).
func remoteCompactDetail(messages []Message) string {
	snippet := ""
	for i := range messages {
		if messages[i].Content != "" {
			snippet = truncateRunes(messages[i].Content, 120)
			break
		}
	}
	return fmt.Sprintf("server-compacted transcript (%d messages): %s", len(messages), snippet)
}

// providerMessagesToInternal converts a canonical provider transcript (as
// returned by the compact endpoint) into Goa's internal message history.
// Assistant text and tool calls merge into single assistant messages; tool
// results become tool-role messages; user/system text becomes user messages.
func providerMessagesToInternal(pmsgs []provider.Message) []Message {
	b := &remoteHistoryBuilder{}
	for i := range pmsgs {
		b.add(pmsgs[i])
	}
	b.flush()
	return b.messages
}

// remoteHistoryBuilder accumulates canonical provider messages into internal
// messages, merging assistant text + tool calls within a turn.
type remoteHistoryBuilder struct {
	messages []Message
	cur      *Message
}

func (b *remoteHistoryBuilder) add(pm provider.Message) {
	switch pm.Role {
	case provider.RoleUser, provider.RoleSystem:
		b.flush()
		if text := providerTextOf(pm); text != "" {
			b.messages = append(b.messages, Message{Type: Content, Role: User, Content: text})
		}
	case provider.RoleAssistant:
		b.addAssistant(pm)
	case provider.RoleToolResult:
		b.flush()
		b.messages = append(b.messages, toolResultToInternal(pm))
	}
}

func (b *remoteHistoryBuilder) addAssistant(pm provider.Message) {
	b.ensureCur()
	for _, blk := range pm.Content {
		switch blk.Type {
		case provider.ContentBlockText:
			b.cur.Content += blk.Text
		case provider.ContentBlockThinking:
			b.cur.Thinking += blk.Thinking
		case provider.ContentBlockToolCall:
			b.cur.ToolCalls = append(b.cur.ToolCalls, ToolCallInfo{
				ID: blk.ToolCallID, Type: "function", Name: blk.ToolName, Arguments: blk.ToolArguments,
			})
		}
	}
}

func (b *remoteHistoryBuilder) ensureCur() {
	if b.cur == nil {
		b.cur = &Message{Type: Content, Role: Assistant}
	}
}

func (b *remoteHistoryBuilder) flush() {
	if b.cur == nil {
		return
	}
	if b.cur.Content != "" || b.cur.Thinking != "" || len(b.cur.ToolCalls) > 0 {
		b.messages = append(b.messages, *b.cur)
	}
	b.cur = nil
}

// providerTextOf concatenates the text content blocks of a canonical message.
func providerTextOf(pm provider.Message) string {
	var text string
	for _, blk := range pm.Content {
		if blk.Type == provider.ContentBlockText {
			text += blk.Text
		}
	}
	return text
}

// toolResultToInternal converts a canonical tool-result message to internal
// form.
func toolResultToInternal(pm provider.Message) Message {
	for _, blk := range pm.Content {
		if blk.Type == provider.ContentBlockToolResult {
			return Message{
				Type: Content, Role: ToolRole,
				Content: blk.Text, ToolCallID: blk.ToolCallID, ToolName: blk.ToolName,
			}
		}
	}
	return Message{Type: Content, Role: ToolRole}
}

// truncateRunes caps s at n runes, appending an ellipsis when truncated.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
