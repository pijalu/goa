// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// HookPoint identifies one interception point in the agent loop. The values
// are stable wire identifiers shared with the plugin layer ("message:pre-send",
// "tool-call:pre", …); renaming one must break the point-map test in M2, not
// production silently.
type HookPoint string

const (
	// HookMessagePreSend fires before a user input becomes a history message.
	// Intercept may rewrite text/metadata/images or deny the input outright;
	// a denial appends nothing (interception sits strictly upstream of the
	// append — append-only rule).
	HookMessagePreSend HookPoint = "message:pre-send"
	// HookToolCallPre fires before a tool executes and BEFORE the shell-hook
	// engine sees the call: shell vetoes validate the final (possibly
	// plugin-mutated) input.
	HookToolCallPre HookPoint = "tool-call:pre"
	// HookToolCallPost fires after a tool executed, before the result reaches
	// history or observers. Intercept may rewrite output/error/stop_turn.
	HookToolCallPost HookPoint = "tool-call:post"
	// HookReplyPre fires once the assistant reply is complete, before it is
	// appended to history. Intercept may rewrite the text. Denial is NOT
	// supported here (a completed assistant turn cannot be unsaid): denials
	// are logged and treated as pass-through.
	HookReplyPre HookPoint = "reply:pre"
	// HookReplyDelta fires per streamed delta. Content deltas may be
	// rewritten by intercept handlers. Thinking deltas are notify-only:
	// rewriting reasoning risks breaking providers' reasoning-signature
	// verification.
	HookReplyDelta HookPoint = "reply:delta"
	// HookLLMError fires when an LLM request or stream fails. Notify-only
	// semantics in v1 even for intercept-mode registrations (mutating retry
	// policy is deferred); the sink wrapper downgrades intercept handlers.
	HookLLMError HookPoint = "llm:error"
)

// HookDecision reports what an Intercept handler decided for a payload.
type HookDecision int

const (
	// HookPass means no handler changed the payload.
	HookPass HookDecision = iota
	// HookModified means a handler rewrote part of the payload; the result
	// map carries the replacement fields.
	HookModified
	// HookDenied means a handler vetoed the action; denyReason explains why.
	HookDenied
)

// PluginHookSink receives interception points from the agent loop.
// Implementations MUST be safe for concurrent use (tool execution runs on
// scheduler goroutines) and MUST treat payload maps as owned by the caller
// after the call returns — copy anything retained. result may be nil when
// decision != HookModified. Modified results carry replacement fields keyed
// like the payload ("text" for message:pre-send/reply:pre, "delta" for
// reply:delta, "input"/"output" for tool points); unrecognized keys and types
// are ignored by the agent side — the plugin adapter layer owns coercing wire
// values into these documented Go types.
type PluginHookSink interface {
	Intercept(ctx context.Context, point HookPoint, payload map[string]any) (decision HookDecision, result map[string]any, denyReason string)
	Notify(point HookPoint, payload map[string]any) // async, never blocks
}

// pluginSink returns the configured sink, or nil when no plugin hooks are
// registered. Every seam helper short-circuits on this single nil check so a
// hook-free session pays nothing beyond it.
func (a *Agent) pluginSink() PluginHookSink {
	return a.cfg.PluginHookSink
}

// interceptMessagePreSend applies the message:pre-send seam to one pending
// user input, strictly before any history mutation for the turn (denial must
// leave zero trace). On HookModified it rewrites input/images/metadata in
// place. It reports whether the input was denied; the caller skips the input
// and advances to the next queued one without appending anything.
func (a *Agent) interceptMessagePreSend(ctx context.Context, input *string, images *[]string, metadata *map[string]string) bool {
	sink := a.pluginSink()
	if sink == nil {
		return false
	}
	payload := map[string]any{
		"point":      string(HookMessagePreSend),
		"role":       "user",
		"text":       *input,
		"session_id": a.cfg.SessionID,
		"turn":       a.turnCounter,
	}
	if len(*images) > 0 {
		imgs := make([]string, len(*images))
		copy(imgs, *images)
		payload["images"] = imgs
	}
	if len(*metadata) > 0 {
		md := make(map[string]string, len(*metadata))
		for k, v := range *metadata {
			md[k] = v
		}
		payload["metadata"] = md
	}
	decision, result, reason := sink.Intercept(ctx, HookMessagePreSend, payload)
	switch decision {
	case HookDenied:
		a.emitEvent(OutputEvent{
			Type: EventContent,
			Role: System,
			Text: fmt.Sprintf("Input blocked by plugin hook: %s", reason),
			Metadata: map[string]string{
				"category": "system-notification",
			},
		})
		return true
	case HookModified:
		if v, ok := payloadString(result, "text"); ok {
			*input = v
		}
		if imgs, ok := result["images"].([]string); ok {
			*images = imgs
		}
		if md, ok := result["metadata"].(map[string]string); ok {
			*metadata = md
		}
	}
	return false
}

// interceptToolCallPre applies the tool-call:pre seam BEFORE fireBeforeToolHook
// runs, so shell hooks see (and veto against) the possibly-mutated final input.
// On denial it returns the model-facing veto message matching the existing
// shell-veto style so the renderer surfaces the reason to the model.
func (a *Agent) interceptToolCallPre(ctx context.Context, name string, input *string, callID string) (veto string, denied bool) {
	sink := a.pluginSink()
	if sink == nil {
		return "", false
	}
	payload := map[string]any{
		"point":      string(HookToolCallPre),
		"tool":       name,
		"input":      *input,
		"call_id":    callID,
		"session_id": a.cfg.SessionID,
	}
	decision, result, reason := sink.Intercept(ctx, HookToolCallPre, payload)
	switch decision {
	case HookDenied:
		return fmt.Sprintf("tool %q blocked by plugin hook: %s", name, reason), true
	case HookModified:
		if v, ok := payloadString(result, "input"); ok {
			*input = v
		}
	}
	return "", false
}

// interceptToolCallPost applies the tool-call:post seam AFTER runTool returns
// and BEFORE the caller appends the result to history, so rewrites reach
// history and observers exactly like a real execution outcome. The flattened
// ToolResult fields actually consumed downstream (output/error/stop_turn) are
// what plugins see and may replace.
func (a *Agent) interceptToolCallPost(ctx context.Context, name, input, callID string, result ToolResult, runErr error) (ToolResult, error) {
	sink := a.pluginSink()
	if sink == nil {
		return result, runErr
	}
	errStr := ""
	if runErr != nil {
		errStr = runErr.Error()
	}
	decision, res, _ := sink.Intercept(ctx, HookToolCallPost, map[string]any{
		"point":      string(HookToolCallPost),
		"tool":       name,
		"input":      input,
		"call_id":    callID,
		"output":     result.Output,
		"error":      errStr,
		"stop_turn":  result.StopTurn,
		"session_id": a.cfg.SessionID,
	})
	if decision != HookModified || res == nil {
		return result, runErr
	}
	// Rebuild the ToolResult from the returned map. A non-empty "error"
	// becomes a plain error so downstream renderers surface it identically
	// ("Error: …") to a tool-produced failure.
	out := ToolResult{}
	if v, ok := payloadString(res, "output"); ok {
		out.Output = v
	}
	if v, ok := res["stop_turn"].(bool); ok {
		out.StopTurn = v
	}
	if e, ok := payloadString(res, "error"); ok && e != "" {
		return out, fmt.Errorf("%s", e)
	}
	return out, nil
}

// rewriteReplyDelta applies the reply:delta seam to one content delta on the
// hot streaming path. With no sink this costs a single nil-check (M2 adds a
// per-turn circuit breaker for registered interceptors). Denials are treated
// as pass-through: a mid-stream delta cannot meaningfully be vetoed — the
// redaction use case is rewriting, not suppression. The rewritten delta flows
// through buffering AND display so history and UI stay consistent.
func (a *Agent) rewriteReplyDelta(ctx context.Context, delta string) string {
	sink := a.pluginSink()
	if sink == nil || delta == "" {
		return delta
	}
	decision, result, _ := sink.Intercept(ctx, HookReplyDelta, map[string]any{
		"point":   string(HookReplyDelta),
		"delta":   delta,
		"is_delta": true,
		"state":   "content",
	})
	if decision == HookModified {
		if v, ok := payloadString(result, "delta"); ok {
			return v
		}
	}
	return delta
}

// notifyThinkingDelta notifies the reply:delta point about a thinking delta.
// Notify-only by design (see HookReplyDelta): interception of reasoning is
// deliberately refused because rewriting thinking risks breaking providers'
// reasoning-signature verification.
func (a *Agent) notifyThinkingDelta(delta string) {
	if sink := a.pluginSink(); sink != nil && delta != "" {
		sink.Notify(HookReplyDelta, map[string]any{
			"point":    string(HookReplyDelta),
			"delta":    delta,
			"is_delta": true,
			"state":    "thinking",
		})
	}
}

// applyReplyPreHook applies the reply:pre seam to the finalized assistant
// message just before the single history append in finalizeStreamTurn.
// Denial is not supported here (an assistant turn cannot be unsaid once the
// stream completed): it is logged and treated as pass-through.
func (a *Agent) applyReplyPreHook(ctx context.Context, msg Message) Message {
	sink := a.pluginSink()
	if sink == nil {
		return msg
	}
	decision, result, reason := sink.Intercept(ctx, HookReplyPre, map[string]any{
		"point":      string(HookReplyPre),
		"text":       msg.Content,
		"thinking":   msg.Thinking,
		"tool_calls": len(a.bufferedToolCalls),
		"session_id": a.cfg.SessionID,
	})
	switch decision {
	case HookModified:
		if v, ok := payloadString(result, "text"); ok {
			msg.Content = v
		}
	case HookDenied:
		a.cfg.Logger.Log(Warn, "plugin hook denied reply:pre (%s): an assistant turn cannot be unsaid — treating as pass-through", reason)
	}
	return msg
}

// notifyLLMError emits the llm:error notification for one stream-failure
// episode. Notify-only semantics in v1 even for intercept-mode registrations
// (mutating retry policy is deferred). It lives on handleStreamFailure alone
// because every LLM failure funnels there exactly once — including stream
// error events resolved by handleStreamError/resolveStreamError — which keeps
// the notification free of double-fires by construction. next_delay_ms is the
// computed backoff for the first retry attempt; jitter makes it approximate.
func (a *Agent) notifyLLMError(streamErr error, model provider.Model, opts provider.StreamOptions, willRetry bool) {
	sink := a.pluginSink()
	if sink == nil {
		return
	}
	payload := map[string]any{
		"point":      string(HookLLMError),
		"error":      streamErr.Error(),
		"attempt":    0, // failed attempts earlier in THIS episode (v1 reports episode entry only)
		"model":      model.ID,
		"classified": classifyLLMError(streamErr),
		"will_retry": willRetry,
		"session_id": a.cfg.SessionID,
	}
	if willRetry {
		plan := resolveRetryPlan(opts)
		payload["next_delay_ms"] = retryBackoff(streamErr, 0, plan.policy).Milliseconds()
	}
	sink.Notify(HookLLMError, payload)
}

// classifyLLMError maps an error onto the canonical lowercased retry-code
// vocabulary surfaced to plugins (rate_limit, server, timeout, transport,
// empty_response); "unknown" for unrecognized failures.
func classifyLLMError(err error) string {
	if code := retryCodeOf(err); code != "" {
		return strings.ToLower(code)
	}
	return "unknown"
}

// payloadString extracts a string field from a hook result map. A nil map
// yields ("" , false), letting Modified handlers omit optional fields.
func payloadString(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key].(string)
	return v, ok
}
