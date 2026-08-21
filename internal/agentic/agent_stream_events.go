// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

type streamEventHandler func(*Agent, context.Context, *provider.AssistantMessageEventStream, provider.AssistantMessageEvent) (done bool, toolCallsEncountered bool, err error)

var streamEventHandlers = map[provider.EventType]streamEventHandler{
	provider.EventTextDelta:     (*Agent).handleStreamTextDelta,
	provider.EventThinkingDelta: (*Agent).handleStreamThinkingDelta,
	provider.EventToolCallEnd:   (*Agent).handleStreamToolCallEnd,
	provider.EventToolCallStart: (*Agent).handleStreamToolCallStart,
	provider.EventToolCallDelta: (*Agent).handleStreamToolCallDelta,
	provider.EventDone:          (*Agent).handleStreamDone,
	provider.EventError:         (*Agent).handleStreamError,
}

func (a *Agent) handleStreamTextDelta(_ context.Context, _ *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	a.markGenStart()
	a.handleTextDelta(event)
	return false, false, nil
}

func (a *Agent) handleStreamThinkingDelta(_ context.Context, _ *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	a.markGenStart()
	a.handleThinkingDelta(event)
	return false, false, nil
}

func (a *Agent) handleStreamToolCallEnd(_ context.Context, _ *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	if event.ToolCall == nil {
		return false, false, nil
	}
	a.markGenStart()
	a.resetThinkingStall()
	a.shouldBufferToolCall(*event.ToolCall)
	return false, false, nil
}

func (a *Agent) handleStreamToolCallStart(_ context.Context, _ *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	if event.Partial == nil || len(event.Partial.Content) == 0 {
		return false, false, nil
	}
	a.markGenStart()
	a.resetThinkingStall()
	a.handleToolCallPartial(event.Partial.Content[0], event.ContentIndex)
	return false, false, nil
}

func (a *Agent) handleStreamToolCallDelta(_ context.Context, _ *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	a.mu.Lock()
	a.toolCallDeltasThisRound++
	a.mu.Unlock()
	// OpenAI-style delta: a full Partial snapshot is attached.
	if event.Partial != nil && len(event.Partial.Content) > 0 {
		a.markGenStart()
		a.resetThinkingStall()
		a.handleToolCallPartial(event.Partial.Content[0], event.ContentIndex)
		return false, false, nil
	}
	// Anthropic-style delta: Partial is nil but Delta carries incremental JSON,
	// correlated by ContentIndex. Without this the streamed arguments never
	// reach the TUI until the whole call completes.
	if event.Delta != "" {
		a.markGenStart()
		a.resetThinkingStall()
		a.handleToolCallDeltaByIndex(event.ContentIndex, event.Delta)
	}
	return false, false, nil
}

func (a *Agent) handleStreamDone(ctx context.Context, stream *provider.AssistantMessageEventStream, _ provider.AssistantMessageEvent) (bool, bool, error) {
	// P0 diagnostic: record whether this provider streamed tool-call args at
	// all. A zero count means tool widgets can only appear at call completion
	// (no live arg streaming) for this provider/model combination.
	a.mu.Lock()
	deltas := a.toolCallDeltasThisRound
	a.mu.Unlock()
	a.cfg.Logger.Log(Debug, "stream round done: tool_call deltas=%d", deltas)
	// Capture provider Usage from the stream result.
	// The usage chunk (stream_options.include_usage) is attached to
	// the stream result via End() or UpdateResult().
	a.captureStreamResult(stream)
	a.recordGenDuration()
	return true, a.completeStreamTurn(ctx), nil
}

// captureStreamResult records provider Usage and StopReason from a finished
// stream's result (the usage chunk arrives via stream_options.include_usage
// and is attached through End() or UpdateResult()).
func (a *Agent) captureStreamResult(stream *provider.AssistantMessageEventStream) {
	result := stream.Result()
	if result == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// Per-round provider contact: keeps the compaction cache gates hot during
	// long single turns where lastTurnEnd (turn END only) would go stale.
	a.lastRoundActivity = time.Now()
	if result.Usage != nil {
		if a.cfg.Logger != nil {
			// round-17 anomaly forensics: correlate cache_read drops
			// with request-shape changes (tool-set re-registration changes
			// tools_hash) vs provider-side eviction (hash stable).
			a.cfg.Logger.Log(Debug, "request usage: cache_read %d -> %d, tools_hash=%08x",
				a.lastCacheReadTokens, result.Usage.CacheReadTokens, a.toolListHashLocked())
		}
		a.lastCacheReadTokens = result.Usage.CacheReadTokens
	}
	if result.Usage != nil && !a.turnStatsEmitted {
		a.providerUsage = result.Usage
	}
	if result.Usage != nil {
		if result.Usage.CacheReadTokens > 0 {
			// Direct evidence the provider prefix cache is hot: expires the
			// first-turn cold presumption of the compaction cache gate.
			a.cacheWarmObserved = true
		}
		a.recordContextUsageLocked(result.Usage)
	}
	if result.StopReason != "" {
		a.lastStopReason = result.StopReason
	}
	// Flush cache-miss notices: the provider journal flags misses as streams
	// complete; logging here keeps the notice next to the round that missed.
	// mu is held here — use the locked variant (sync.Mutex is not reentrant).
	a.drainCacheMissNoticesLocked()
}

// toolListHashLocked returns a cheap fingerprint of the tool schemas actually
// shipped with each request (the Schemas() view) so cache-drop forensics can
// discriminate provider-side partial eviction from request-shape changes: a
// deferred-tool load grows the loaded-tail and alters the provider request
// shape exactly like a tool-set re-registration busts the prefix cache
// (round-17 anomaly). Hashing the exposed view (rather than the full
// registered set) makes the fingerprint track the real wire shape. The caller
// must hold a.mu (reads a.reg, replaced under mu by SetTools).
func (a *Agent) toolListHashLocked() uint32 {
	h := fnv.New32a()
	for _, s := range a.reg.Schemas() {
		h.Write([]byte(s.Name))
		h.Write([]byte{0})
	}
	return h.Sum32()
}

func (a *Agent) handleStreamError(ctx context.Context, stream *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	return true, false, a.resolveStreamError(ctx, stream, event.Error)
}

// tryAutoHealToolCalls recovers tool calls the model emitted as text instead
// of as structured tool_calls, when no native tool calls were buffered.
//
// DSML (DeepSeek's native markup) is ALWAYS recovered: DeepSeek-family models
// fall back to it precisely when the request suppresses structured tool calls
// (tool_choice "none", e.g. the post-StopTurn collapse round), so a well-formed
// DSML call would otherwise be silently dropped — losing the user's work with
