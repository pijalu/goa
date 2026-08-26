// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
)

// TurnTokenUsage holds per-turn token usage breakdown.
type TurnTokenUsage struct {
	PromptN         int     // input tokens
	PredictedN      int     // output tokens
	CacheRead       int     // cache read tokens
	CacheWrite      int     // cache creation tokens
	SpeedTokPerSec  float64 // output token/s
	CostUSD         float64 // estimated cost in USD
	ContextEstimate int     // estimated context usage at turn end
	ContextMax      int     // max context window
}

// TurnRecord holds data for one agent turn.
type TurnRecord struct {
	Number             int
	RequestJSON        string // serialized conversation history sent to the LLM
	ResponseJSON       string // serialized assistant response content
	TokensUsed         int
	TokenUsage         TurnTokenUsage // detailed token breakdown
	Timing             TurnTiming
	ToolCalls          []TurnToolCall   // tool calls made during this turn
	ToolResults        []TurnToolResult // tool results received during this turn
	UserInput          string           // the user message that started this turn
	Thinking           []string         // thinking blocks emitted by the model
	AssistantResponses []string         // assistant content blocks emitted by the model
	// AgentRole identifies which agent produced the turn: "" or "main" for
	// the primary session agent, otherwise the multiagent role (companion,
	// workflow stage, team member). Drives the per-agent /stats:cache
	// sections.
	AgentRole string
	// GoalID is the active goal at finalize time ("" = no goal), so the
	// cache view can group turns per goal as well as per agent.
	GoalID string
}

// TurnToolCall records a tool call made during a turn.
type TurnToolCall struct {
	Name   string
	Input  string
	CallID string
}

// TurnToolResult records a tool result received during a turn.
type TurnToolResult struct {
	CallID string
	Name   string
	Result string
}

// TurnTiming holds timing metrics for a turn.
type TurnTiming struct {
	TTFT   float64 // time to first token in seconds
	Total  float64 // total turn duration in seconds
	Phases map[string]float64
}

// agentRunner is the subset of *agentic.Agent used by runAgentTurn. Using an
// interface makes the turn runner testable and keeps AgentManager decoupled
// from the full agent surface.
type agentRunner interface {
	Run(ctx context.Context, input string) error
	RunWithImages(ctx context.Context, input string, images []string) error
}

// steeringSourceAdapter adapts *SteeringQueue (Flush) to the agentic
// SteeringSource interface (Drain) so the agent can poll for mid-turn steering
// between stream rounds.
type steeringSourceAdapter struct{ q *SteeringQueue }

func (a steeringSourceAdapter) Drain() []string { return a.q.Flush() }

func (am *AgentManager) runAgentTurn(ctx context.Context, cancel context.CancelFunc, gen int, runner agentRunner, input string, images []string) {
	// turnEndedCleanly gates the post-turn hook: a panicking turn must not
	// re-drive goals (the agent may panic again). The hook itself runs in the
	// cleanup defer — AFTER am.running is cleared and steering dispatched —
	// because the goal driver started by the hook must never observe the
	// agent as still busy with this turn (Issue 7: a drive started
	// while the agent is mid-turn queue-storms continuation prompts).
	turnEndedCleanly := false
	defer am.recoverTurnPanic()
	defer func() {
		cancel()
		am.mu.Lock()
		if am.cancelGen == gen {
			am.cancel = nil
		}
		am.running = false
		// Capture pending steering while holding the lock so finalizeTurn
		// cannot overwrite it after we release.
		pending := am.pendingSteering
		am.pendingSteering = ""
		am.mu.Unlock()

		// Dispatch steering only after am.running is false, so the
		// alreadyRunning check in SendUserInput does not re-queue.
		if pending != "" {
			am.emitSteeringInjected(pending)
			_ = am.SendUserInput(pending)
		}

		if turnEndedCleanly && am.postTurnHook != nil {
			am.postTurnHook()
		}
	}()

	am.applyPendingMajorMode()
	am.applyPendingThinkingLevel()
	am.mu.Lock()
	am.loopStopReason = ""
	am.mu.Unlock()
	// A genuine new user turn resets the agent's runaway-loop latch and
	// repeat counters: the guardrail stops a runaway exchange, never the
	// session (runaway-loop bricking). Goal continuation turns
	// bypass runAgentTurn and keep their guardrail state so cross-turn
	// driver loops still latch. Optional interface: test runners need not
	// implement it.
	if lr, ok := runner.(interface{ ResetLoopStop() }); ok {
		lr.ResetLoopStop()
	}
	// Defense in depth: a turn that ends without EventEnd (loop-detector
	// interrupt, user Escape, stream error) skips finalizeTurn and would leak
	// its thinking-repeat counters into this turn, instantly re-triggering the
	// detector on the first delta. Start every turn with a clean slate.
	if am.loopDetector != nil {
		am.loopDetector.ResetThinking()
	}
	am.executeRunner(ctx, runner, input, images)

	// After the runner finishes, flush any steering still pending and queue it
	// as the next user turn. Mid-turn steering is normally already woven into
	// the turn by the agent's between-round drain (SetSteeringSource); only
	// leftovers remain — steering typed during the final no-tool round (which
	// has no subsequent round to drain into), or during a turn that errored or
	// was interrupted before draining.
	am.mu.Lock()
	pending := am.steering.Flush()
	if len(pending) > 0 {
		am.pendingSteering = strings.Join(pending, "\n\n")
	}
	am.mu.Unlock()

	turnEndedCleanly = true
}

func (am *AgentManager) emitSteeringInjected(text string) {
	am.emitChat(event.ChatEvent{SteeringInjected: &event.SteeringInput{Text: text}})
}

func (am *AgentManager) emitChat(ev event.ChatEvent) {
	if am.eventsOut == nil {
		return
	}
	select {
	case am.eventsOut.Chat <- ev:
	default:
	}
}

// recoverTurnPanic converts an agent-turn panic into an EventEnd so the UI
// marks the turn complete and the user sees that the agent stopped.
func (am *AgentManager) recoverTurnPanic() {
	if r := recover(); r != nil {
		if am.logger != nil {
			am.logger.Log(agentic.Error, "agent turn panic: %v", r)
		}
		ev := agentic.OutputEvent{
			Type: agentic.EventEnd,
			Text: fmt.Sprintf("agent stopped unexpectedly: %v", r),
		}
		am.emitAgentEvent(ev)
		am.emitInternalEvent(ev)
	}
}

// executeRunner runs the agent and emits an EventEnd on error. It is split
// out of runAgentTurn to keep the turn lifecycle within the complexity budget.
func (am *AgentManager) executeRunner(ctx context.Context, runner agentRunner, input string, images []string) {
	var err error
	if len(images) > 0 {
		err = runner.RunWithImages(ctx, input, images)
	} else {
		err = runner.Run(ctx, input)
	}
	if err != nil {
		ev := agentic.OutputEvent{Type: agentic.EventEnd}
		if errors.Is(err, context.Canceled) {
			am.mu.Lock()
			reason := am.loopStopReason
			am.mu.Unlock()
			if reason != "" {
				// Loop detector cancelled the turn; surface the real reason
				// so the UI does not show the generic "user stopped" message.
				ev.Text = reason
			} else {
				// Distinguish user-initiated cancellation from transport aborts.
				// When the agent's retry logic has already filtered out transport
				// drops (ctx.Err() == nil), a context.Canceled here means the user
				// pressed Escape/Ctrl+C. The metadata "cancelled: user" tells the
				// UI to show "Generation stopped by user." instead of a generic
				// cancellation message.
				ev.Metadata = map[string]string{"cancelled": "true", "cancel_source": "user"}
			}
		} else {
			ev.Text = err.Error()
		}
		// EventEnd must always reach the UI so the turn is marked complete; do
		// not drop it under load (CORE-BUG-3). Block (backpressure) instead.
		am.emitAgentEvent(ev)
		am.emitInternalEvent(ev)
	}
}
