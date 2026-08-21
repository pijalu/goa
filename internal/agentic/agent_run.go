// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (a *Agent) Run(ctx context.Context, input string) error {
	return a.RunWithMetadata(ctx, input, nil)
}

// RunWithImages starts a new conversation turn with the given user input and
// image attachments. Images are file paths; the provider layer encodes them.
func (a *Agent) RunWithImages(ctx context.Context, input string, images []string) error {
	return a.runInternal(ctx, input, images, nil)
}

// RunWithMetadata starts a new conversation turn with the given user input
// and optional metadata. Metadata is attached to the user message and propagated
// through the Output channel and to all observers, but is NOT sent to the LLM.
//
// This is useful for attaching application-level tags (e.g., category, visibility)
// to individual messages without affecting model context.
func (a *Agent) RunWithMetadata(ctx context.Context, input string, metadata map[string]string) error {
	return a.runInternal(ctx, input, nil, metadata)
}

func (a *Agent) runInternal(ctx context.Context, input string, images []string, metadata map[string]string) error {
	a.mu.Lock()

	// Initialize history with system prompt on first call
	if len(a.history) == 0 {
		sysMsg := Message{
			Type:    Content,
			Role:    System,
			Content: a.cfg.SystemPrompt,
		}
		a.history = append(a.history, sysMsg)
		a.mu.Unlock()
		a.emitMessage(sysMsg)
		a.mu.Lock()
	}

	// If processing, queue and return
	if a.processing {
		a.queue = append(a.queue, input)
		a.mu.Unlock()
		return nil
	}

	a.processing = true
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.mu.Unlock()

	// Process current and queued inputs
	currentInput := input
	var err error

	for {
		// One turn per user input; the temporal-context reading (CX6) uses
		// the count in its "turn N" label.
		a.turnCounter++
		// Deliver pre-turn provider content (e.g. due schedule reminders) as
		// user-role messages ahead of the user's actual input. The provider
		// claims what it returns, so each delivery happens exactly once.
		a.deliverPreTurnMessages()
		// Add user message to history and emit event
		userMsg := Message{
			Type:     Content,
			Role:     User,
			Content:  currentInput,
			Images:   images,
			Metadata: metadata,
		}
		a.history = append(a.history, userMsg)
		a.emitMessage(userMsg)

		// Persist the goal context once per turn (kimi-code parity): the
		// reminder becomes ordinary append-only history, so the provider
		// request sequence is strictly append-only and fully prefix-cacheable.
		a.persistGoalReminder()

		// Persist always-on sticky skill instructions under the same
		// contract — deduped, user-role, re-persisted after compression.
		a.persistStickyInstructions()

		// Process one turn
		err = a.processTurn(ctx)
		if err != nil {
			break
		}

		// Check for queued inputs
		a.mu.Lock()
		if len(a.queue) == 0 {
			a.mu.Unlock()
			break
		}
		currentInput = a.queue[0]
		a.queue = a.queue[1:]
		a.mu.Unlock()
	}

	// Cleanup on every exit path (success, error, empty queue). Mark not
	// processing and cancel the per-turn child ctx before discarding the func.
	// Without the cancel() call, every completed turn leaks the cancellable ctx
	// subtree until the *parent* ctx is cancelled (go vet -lostcancel can't see
	// this because cancel is stored in a struct field). The error path
	// previously also left a.processing==true, which made the next Run() queue
	// forever instead of processing.
	a.finishProcessing()

	return err
}

// finishProcessing marks the agent idle and cancels the per-turn child context.
// It must run on every exit path out of runInternal so that the cancellable
// turn ctx (and its subtree) is released and the agent can accept new turns.
// Holding the cancel func without calling it leaks the child ctx tree until the
// caller's parent ctx is cancelled; go vet -lostcancel cannot detect this
// because the func is stored in a struct field rather than a local.
func (a *Agent) finishProcessing() {
	a.mu.Lock()
	a.processing = false
	a.lastTurnEnd = time.Now()
	cancel := a.cancel
	a.cancel = nil
	a.mu.Unlock()
	a.emitEvent(OutputEvent{Type: EventProgress, Text: ""})
	if cancel != nil {
		cancel()
	}
}

// RunAndCollect runs the agent synchronously and collects all text output
// (EventContent) into a single string. Useful for callers that need the
// full response without wiring their own observer, such as sub-agent skill
// execution.
//
// The observer is automatically registered before Run and removed after.
// RunAndCollect runs the agent synchronously and collects all ASSISTANT text
// output (EventContent with Role: Assistant) into a single string.
// System prompt and user messages are excluded. Useful for callers that
// need the full response without wiring their own observer, such as
// sub-agent skill execution or companion testing.
func (a *Agent) RunAndCollect(ctx context.Context, input string) (string, error) {
	var buf strings.Builder
	obs := OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventContent && ev.Role == Assistant && ev.Text != "" {
			buf.WriteString(ev.Text)
		}
	})
	remove := a.AddObserver(obs)
	defer remove()
	err := a.Run(ctx, input)
	return buf.String(), err
}

// Stop cancels any ongoing processing and resets the agent state.
func (a *Agent) Stop() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.processing = false
	a.queue = nil
	a.mu.Unlock()
}

// LastTurnSilentStop reports whether the most recently completed turn ended
// with a "silent stop": the model produced thinking/reasoning tokens but no
// visible answer content and no tool calls (a reasoning-token or output limit
// on the provider side). The goal driver uses this to decide whether to pause
// the goal instead of auto-continuing into the same limit.
func (a *Agent) LastTurnSilentStop() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastTurnSilentStop
}

func (a *Agent) processTurn(ctx context.Context) error {
	if a.cfg.Model.ID == "" && a.cfg.Model.Api == "" {
		return fmt.Errorf("no model configured: set Config.Model")
	}
	if err := a.checkLoopStopped(); err != nil {
		return err
	}
	if err := a.processTurnWithStream(ctx); err != nil {
		return err
	}
	return a.checkProgressLoop()
}

// loopStopCooldown is how long the runaway-loop latch rejects new turns
// before auto-expiring. A guardrail stops a runaway exchange, never the
// session: genuine recovery paths (ResetLoopStop on new user input or goal
// resume) clear it immediately, and this backstop covers driven paths that
// bypass both (runaway-loop bricking).
