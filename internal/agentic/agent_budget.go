// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func (a *Agent) emitBudgetToolSkipped(tc provider.ContentBlock, result string) {
	a.emitEvent(OutputEvent{
		Type: EventToolCall, State: StateToolCall, ToolName: tc.ToolName, ToolInput: tc.ToolArguments, ToolCallID: tc.ToolCallID,
	})

	a.emitEvent(OutputEvent{
		Type:       EventToolResult,
		State:      StateToolResult,
		ToolName:   tc.ToolName,
		ToolResult: result,
		Text:       result,
		ToolCallID: tc.ToolCallID,
	})
}

// toolBudgetMessage is the synthetic tool result returned when an absolute
// per-turn tool-call cap is configured and exceeded. It tells the model to stop
// calling tools and produce a final answer. This is reserved for optional hard
// safety limits; duplicate-window and consecutive-repeat guardrails use the
// messages below instead.
const toolBudgetMessage = "[goa-system] Tool call budget exceeded. Do not call more tools this turn. Answer based on the information you have already gathered."

// toolLoopMessage is the synthetic tool result returned when the exact same
// tool call is repeated too many times consecutively or too many times within
// the rolling budget window. It warns the model that progress has stalled and
// tells it to change approach.
const toolLoopMessage = "[goa-system] Loop guardrail: this exact tool call was repeated too many times without progress. Stop repeating it. Change your approach or produce a final answer."

// ToolBudgetResultPrefix is the prefix shared by every budget-exceeded tool
// result. Callers (e.g. the TUI layer) use it to recognise synthetic budget
// messages without duplicating the full string.
const ToolBudgetResultPrefix = "[goa-system] Tool call budget exceeded"

// ToolRepeatedMessagePrefix is the prefix of the soft duplicate hint returned
// when the exact same tool call appears twice in the same turn. The TUI uses
// this to recognise guardrail messages and render them as warnings rather than
// successful tool results.
const ToolRepeatedMessagePrefix = "[goa-system] This exact tool call"

// ToolLoopMessagePrefix is the prefix of hard loop guardrails. The TUI uses
// this to recognise guardrail messages and render them as warnings.
const ToolLoopMessagePrefix = "[goa-system] Loop guardrail:"

// IsGuardrailResult reports whether a tool result text is a synthetic
// guardrail/budget message rather than a real tool execution result. The TUI
// uses this to render repeated or looped tool calls as warnings instead of
// success.
func IsGuardrailResult(s string) bool {
	trimmed := strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(trimmed, ToolBudgetResultPrefix):
		return true
	case strings.HasPrefix(trimmed, ToolRepeatedMessagePrefix):
		return true
	case strings.HasPrefix(trimmed, ToolLoopMessagePrefix):
		return true
	default:
		return false
	}
}

// defaultThinkingStallWarn is the default duration of pure thinking before a
// stall warning is emitted, used when Config.ThinkingStallWarn is zero.
const defaultThinkingStallWarn = 60 * time.Second

// defaultThinkingStallStop is the default duration of pure thinking before the
// stream is interrupted, used when Config.ThinkingStallStop is zero.
const defaultThinkingStallStop = 120 * time.Second

// synthesizeAssistantBuffer creates an assistant message from accumulated buffers.
func (a *Agent) synthesizeAssistantBuffer() Message {
	content := a.contentBuf.String()
	thinking := a.thinkingBuf.String()
	// If content is empty but thinking was received (e.g., DeepSeek sends
	// response in reasoning_content field with no content), promote thinking
	// to content BUT keep the thinking field populated so the next provider
	// request sends it back as reasoning_content. Without this, the model sees
	// its own reasoning as regular content and attempts to continue, creating
	// an infinite loop detected by the guardrail.
	if content == "" && thinking != "" {
		content = thinking
	}
	msg := Message{
		Type:     Content,
		Role:     Assistant,
		Content:  content,
		Thinking: thinking,
	}
	if content == "" && thinking == "" {
		msg.Delta = true
	}
	return msg
}

// executeTool runs a tool with the given name and input, returning the result.
// shouldBufferToolCall checks whether a tool call from the stream should be
// buffered for concurrent execution. It applies three independent guardrails:
//
//  1. Total-repeat (turn-wide): counts ALL occurrences of the exact same
//     tool+arguments across the entire turn. Controlled by MaxToolRepeatTotal.
//
//  2. Consecutive-repeat: counts CONSECUTIVE identical calls (same tool+args
//     back-to-back). Controlled by MaxToolRepeatConsecutive. A soft warning is
//     emitted at the second consecutive call; a hard loop guard fires at the
//     configured limit.
//
//  3. Rolling-window repeat: counts how many times the same call appears in
//     the last ToolCallLimitResetWindow calls. Controlled by MaxToolCalls.
//     A soft warning is emitted at the second duplicate; a hard loop guard
//     fires at the configured limit.
//
// Tool calls are NEVER rejected entirely (return false) — every call gets
// buffered, keeping the tool_call/tool_result pairing intact for strict
// providers like DeepSeek. Skipped calls are recorded in budgetToolCalls so
// executeBufferedToolCalls substitutes the stored message instead of
// executing, and emitBudgetToolSkipped sends TUI events immediately.
func (a *Agent) shouldBufferToolCall(tc provider.ContentBlock) bool {
	callKey := tc.ToolName + "::" + tc.ToolArguments

	a.mu.Lock()
	a.bufferedToolCallCount++
	windowCount, consecutiveCount, windowExceeded, consecutiveExceeded := a.recordToolCallInBudgetWindow(callKey)
	a.mu.Unlock()

	if a.checkTotalRepeatGuardrail(tc, callKey) {
		return true
	}

	if skipMsg := a.budgetOrRepeatSkipMessage(windowCount, consecutiveCount); skipMsg != "" {
		a.applyToolGuardrail(tc, callKey, skipMsg, windowExceeded, consecutiveExceeded, windowCount, consecutiveCount)
		return true
	}

	// First occurrence: emit tool call event for TUI, then buffer.
	// If a streaming partial was already emitted from handleToolCallPartial,
	// emit a final event (IsDelta=false) so the TUI transitions to running.
	if _, streaming := a.streamingToolCalls[tc.ToolCallID]; streaming {
		a.emitEvent(OutputEvent{
			Type: EventToolCall, State: StateToolCall,
			ToolName: tc.ToolName, ToolInput: tc.ToolArguments, ToolCallID: tc.ToolCallID,
			IsDelta: false,
		})
		delete(a.streamingToolCalls, tc.ToolCallID)
	} else {
		a.emitEvent(OutputEvent{
			Type: EventToolCall, State: StateToolCall,
			ToolName: tc.ToolName, ToolInput: tc.ToolArguments, ToolCallID: tc.ToolCallID,
		})
	}
	a.bufferedToolCalls = append(a.bufferedToolCalls, tc)
	return true
}

// checkTotalRepeatGuardrail returns true and buffers the call (skipped)
// when total identical calls exceed MaxToolRepeatTotal.
func (a *Agent) checkTotalRepeatGuardrail(tc provider.ContentBlock, callKey string) bool {
	if a.cfg.MaxToolRepeatTotal <= 0 {
		return false
	}
	a.mu.Lock()
	a.turnToolCalls[callKey]++
	count := a.turnToolCalls[callKey]
	a.mu.Unlock()
	if count <= a.cfg.MaxToolRepeatTotal {
		return false
	}
	a.cfg.Logger.Log(Warn, "MaxToolRepeatTotal guardrail: tool call %q called %d times total this turn", tc.ToolName, count)
	a.applyToolBudgetSkip(tc, toolLoopMessage)
	a.bufferedToolCalls = append(a.bufferedToolCalls, tc)
	return true
}

// budgetOrRepeatSkipMessage returns the appropriate skip message based on
// budgetOrRepeatSkipMessage returns the skip message when a tool call exceeds a
// CONFIGURED repeat limit, or "" when the call may execute. Only the configured
// limits skip execution; a repeat below them is allowed (a model may legitimately
// re-run a tool — re-read a changed file, re-run a test after a fix, poll a
// build). The previous hardcoded `consecutiveCount >= 2` case skipped the 2nd
// identical call outright (scheduleAndRunToolCalls drops budgetToolCalls entries),
// which broke legitimate re-reads/re-runs — the over-sensitive guard.
//
// Priority: hard-loop (consecutive) > rolling-window. Non-consecutive duplicates
// (A, B, A) only trip the rolling-window / total limits, never the consecutive one.
func (a *Agent) budgetOrRepeatSkipMessage(windowCount, consecutiveCount int) string {
	maxConsecutive := a.cfg.MaxToolRepeatConsecutive
	maxWindow := a.cfg.MaxToolCalls

	switch {
	case maxConsecutive > 0 && consecutiveCount > maxConsecutive:
		return fmt.Sprintf("[goa-system] Loop guardrail: this exact tool call was repeated %d consecutive times (limit: %d). Stop repeating the same call. Change your approach or produce a final answer.", consecutiveCount, maxConsecutive)
	case maxWindow > 0 && windowCount > maxWindow:
		windowSize := a.effectiveToolWindowSize()
		return fmt.Sprintf("[goa-system] Loop guardrail: this exact tool call appeared %d times in the last %d calls (limit: %d). Stop repeating the same call. Use the previous result or change approach.", windowCount, windowSize, maxWindow)
	default:
		return ""
	}
}

// effectiveToolWindowSize returns the configured ToolCallLimitResetWindow, or a
// reasonable default derived from MaxToolCalls when it is unset.
func (a *Agent) effectiveToolWindowSize() int {
	window := a.cfg.ToolCallLimitResetWindow
	if window > 0 {
		return window
	}
	window = a.cfg.MaxToolCalls * 3
	if window < 10 {
		window = 10
	}
	if window > 50 {
		window = 50
	}
	return window
}

// applyToolGuardrail records the skip, logs, emits a TUI event, and buffers
// the call so the model still sees the hint in the tool result.
func (a *Agent) applyToolGuardrail(tc provider.ContentBlock, callKey, skipMsg string, windowExceeded, consecutiveExceeded bool, windowCount, consecutiveCount int) {
	a.applyToolBudgetSkip(tc, skipMsg)
	switch {
	case consecutiveExceeded:
		a.cfg.Logger.Log(Warn, "Hard loop: tool call %q repeated %d times consecutively (limit: %d); substituting hint", tc.ToolName, consecutiveCount, a.cfg.MaxToolRepeatConsecutive)
	case windowExceeded:
		a.cfg.Logger.Log(Warn, "Rolling-window loop: tool call %q appeared %d times in the last %d calls (limit: %d); substituting hint", tc.ToolName, windowCount, a.effectiveToolWindowSize(), a.cfg.MaxToolCalls)
	default:
		a.cfg.Logger.Log(Warn, "Soft repeat: tool call %q repeated %d time(s) in window / %d time(s) consecutively; substituting hint", tc.ToolName, windowCount, consecutiveCount)
	}
	a.emitBudgetToolSkipped(tc, skipMsg)
	a.bufferedToolCalls = append(a.bufferedToolCalls, tc)
}

// applyToolBudgetSkip records a budget-skip message for the tool call ID.
func (a *Agent) applyToolBudgetSkip(tc provider.ContentBlock, msg string) {
	if tc.ToolCallID != "" {
		a.budgetToolCalls[tc.ToolCallID] = msg
	}
}

// recordToolCallInBudgetWindow tracks the rolling-window duplicate count and
// consecutive duplicate count for a tool call. It must be called with a.mu held.
//
// The consecutive counter increments when the current call matches the
// immediately previous call; otherwise it resets to 1. This catches stuck loops
// where the model repeats the same call back-to-back.
//
// The rolling-window counter counts how many times the current call key appears
// in the last ToolCallLimitResetWindow calls (or an effective default window
// when the config is unset). This catches duplicates that are spaced out by a
// few different calls.
//
// Returns (windowCount, consecutiveCount, windowExceeded, consecutiveExceeded).
func (a *Agent) recordToolCallInBudgetWindow(callKey string) (windowCount, consecutiveCount int, windowExceeded, consecutiveExceeded bool) {
	if a.cfg.DisableToolBudget {
		return 0, 0, false, false
	}

	window := a.effectiveToolWindowSize()

	// Consecutive-duplicate tracking.
	if a.lastCallKey == callKey {
		a.consecutiveCount++
	} else {
		a.consecutiveCount = 1
	}
	a.lastCallKey = callKey
	consecutiveCount = a.consecutiveCount

	// Maintain rolling window of recent call keys.
	a.recentToolCalls = append(a.recentToolCalls, callKey)
	if len(a.recentToolCalls) > window {
		a.recentToolCalls = a.recentToolCalls[len(a.recentToolCalls)-window:]
	}

	// Count occurrences of the current call key within the window.
	for _, k := range a.recentToolCalls {
		if k == callKey {
			windowCount++
		}
	}

	// Check limits.
	if a.cfg.MaxToolRepeatConsecutive > 0 && consecutiveCount > a.cfg.MaxToolRepeatConsecutive {
		consecutiveExceeded = true
	}
	if a.cfg.MaxToolCalls > 0 && windowCount > a.cfg.MaxToolCalls {
		windowExceeded = true
	}

	return windowCount, consecutiveCount, windowExceeded, consecutiveExceeded
}

// BufferedToolCallCount returns the number of tool calls buffered for the
// current batch. It resets once executeBufferedToolCalls runs, so it only
// reflects the current in-flight batch.
func (a *Agent) BufferedToolCallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bufferedToolCallCount
}

// BufferedToolCallsSeen returns how many tool calls from the current batch
// have already produced a result. Used alongside BufferedToolCallCount to
// format progress labels such as "tool calling (x/Y)".
func (a *Agent) BufferedToolCallsSeen() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	total := a.bufferedToolCallCount
	remaining := len(a.bufferedToolCalls)
	if total < remaining {
		return 0
	}
	return total - remaining
}

// executeBufferedToolCalls executes all buffered tool calls concurrently via
// the ToolScheduler, adds the assistant message and results to history, and
// emits result events. Called after the stream ends.
//
// Calls recorded in budgetToolCalls are NOT executed — they receive a
// synthetic budget message as their result. They are still appended to
// history (after the shared assistant message) so the tool_calls array and
// tool results stay paired 1:1, which strict OpenAI-style providers require.
func (a *Agent) executeBufferedToolCalls(ctx context.Context) bool {
	tcs := a.bufferedToolCalls
	a.bufferedToolCalls = nil
	a.mu.Lock()
	a.bufferedToolCallCount = 0
	a.mu.Unlock()
	if len(tcs) == 0 {
		return false
	}

	a.appendAssistantToolCallMessage(tcs)
	realResults := a.scheduleAndRunToolCalls(ctx, tcs)
	a.appendToolResults(tcs, realResults)

	a.contentBuf.Reset()
	a.thinkingBuf.Reset()
	a.thinkingDisplayBuf.Reset()

	// If any call was executed for real, continue so the LLM sees results.
	if len(realResults) > 0 {
		return true
	}

	// All calls were synthetic (budget-skipped or loop-skipped).
	// Duplicate guardrails return hints so the model can change approach;
	// only an absolute budget-exceeded message (toolBudgetMessage) should end
	// the turn. Loop/hint messages keep the turn alive so the LLM can respond.
	for _, tc := range tcs {
		if msg := a.budgetToolCalls[tc.ToolCallID]; msg == toolBudgetMessage {
			return false
		}
	}
	return true
}
