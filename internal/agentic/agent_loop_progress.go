// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
	"strings"
	"time"
)

const loopStopCooldown = 10 * time.Minute

// checkLoopStopped enforces the runaway-loop latch at turn start. The latch
// auto-expires after loopStopCooldown so no session stays bricked forever.
func (a *Agent) checkLoopStopped() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.loopStopped {
		return nil
	}
	if time.Since(a.loopStoppedAt) >= loopStopCooldown {
		a.cfg.Logger.Log(Info, "Loop guardrail: session stop latch auto-expired after %s", loopStopCooldown)
		a.clearLoopStopLocked()
		return nil
	}
	return fmt.Errorf("session stopped due to a runaway loop%s; please review the conversation and retry", loopEvidenceSuffix(a.loopStoppedSample))
}

// ResetLoopStop clears the runaway-loop latch and repeat counters. It is
// called when a genuine new user message starts a turn (human input, or a
// goal resumed after a runaway pause with a varied recovery prompt): the
// pause/interrupt was the guardrail's stop, and the new input is a deliberate
// attempt to recover — the session must be allowed to proceed.
func (a *Agent) ResetLoopStop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loopStopped || a.assistantRepeatCount > 0 {
		a.cfg.Logger.Log(Info, "Loop guardrail: state reset by new user input (latched=%v, repeats=%d)", a.loopStopped, a.assistantRepeatCount)
	}
	a.clearLoopStopLocked()
}

// clearLoopStopLocked resets all runaway-loop guardrail state. Caller must
// hold a.mu.
func (a *Agent) clearLoopStopLocked() {
	a.loopStopped = false
	a.loopStoppedAt = time.Time{}
	a.loopStoppedSample = ""
	a.assistantRepeatCount = 0
	a.lastAssistantHash = ""
}

// checkProgressLoop detects runaway conversations where the assistant repeats
// the same meaningful message across consecutive turns without progress.
// On the first repeat it injects a warning hint AND surfaces a visible TUI
// warning naming the repeated response; on the second repeat it stops the
// session with an error carrying the same evidence (runaway-loop
// visibility: the user must be able to judge whether the loop was real).
//
// The strike only counts when this turn produced a NEW assistant message:
// when the last assistant message predates turnStartHistoryLen (stream
// error, retry, pause), comparing the stale message against itself would
// score a false strike with zero actual repetition.
func (a *Agent) checkProgressLoop() error {
	warnSample, err := a.scanProgressLoop()
	if warnSample != "" {
		// Emitted after scanProgressLoop released a.mu (emitEvent locks it).
		a.emitEvent(OutputEvent{
			Type: EventContent,
			Role: System,
			Text: fmt.Sprintf("Runaway-loop warning: the assistant repeated the same response as the previous turn%s; if it repeats again the session stops.", loopEvidenceSuffix(warnSample)),
			Metadata: map[string]string{
				"category": "system-notification",
			},
		})
	}
	return err
}

// scanProgressLoop evaluates the repeat counters under a.mu. It returns the
// repeated-response sample when the first strike applies (the caller emits
// the visible warning — emitEvent takes a.mu, so it must run unlocked), or
// the terminal guardrail error when the latch trips.
func (a *Agent) scanProgressLoop() (warnSample string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	idx, msg := a.lastAssistantMessageLocked()
	if idx < 0 || idx < a.turnStartHistoryLen {
		return "", nil
	}
	if !a.isMeaningfulAssistantMessage(msg) {
		return "", nil
	}

	hash := a.hashAssistantMessage(msg)
	if hash != a.lastAssistantHash {
		a.lastAssistantHash = hash
		a.assistantRepeatCount = 0
		return "", nil
	}

	a.assistantRepeatCount++
	a.cfg.Logger.Log(Warn, "Loop guardrail: assistant message repeated %d time(s)", a.assistantRepeatCount)

	sample := progressLoopSample(msg)
	if a.assistantRepeatCount == 1 {
		hint := "[goa-system] Your last response was identical to the previous one. Progress has stalled. Change your approach: use a tool, produce different output, or stop and explain the blocker. Repeating the same text will end the session."
		a.history = append(a.history, Message{Type: Content, Role: System, Content: hint})
		return sample, nil
	}

	a.loopStopped = true
	a.loopStoppedAt = time.Now()
	a.loopStoppedSample = elideLoopSample(sample)
	return "", fmt.Errorf("runaway loop detected: the assistant repeated the same response %d consecutive times without progress%s; session stopped", a.assistantRepeatCount+1, loopEvidenceSuffix(sample))
}

// lastAssistantMessageLocked returns the index and value of the most recent
// assistant message in history, or (-1, Message{}) when there is none.
// Caller must hold a.mu.
func (a *Agent) lastAssistantMessageLocked() (int, Message) {
	for i := len(a.history) - 1; i >= 0; i-- {
		if a.history[i].Role == Assistant {
			return i, a.history[i]
		}
	}
	return -1, Message{}
}

// isMeaningfulAssistantMessage reports whether a message should participate in
// progress-loop detection. Any assistant turn — including an empty one with no
// tool calls — can be a stall signal, because the model is supposed to produce
// content, reasoning, or tool calls. Empty turns are treated as meaningful so
// that repeated no-op turns are caught before the context explodes.
func (a *Agent) isMeaningfulAssistantMessage(msg Message) bool {
	return msg.Role == Assistant
}

// hashAssistantMessage builds a simple fingerprint of an assistant message.
func (a *Agent) hashAssistantMessage(msg Message) string {
	return fmt.Sprintf("%s\x00%s\x00%v", strings.TrimSpace(msg.Content), strings.TrimSpace(msg.Thinking), len(msg.ToolCalls))
}

// withToolResultAsUser returns a copy of model with ToolResultAsUser set on its
// OpenAI completions compat.  Existing compat fields are preserved.
