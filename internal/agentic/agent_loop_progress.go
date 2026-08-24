// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const loopStopCooldown = 10 * time.Minute

// defaultRunawayLoopMaxRepeats is the number of consecutive identical
// assistant responses without progress tolerated before the runaway-loop
// guardrail stops the session (execution.runaway_loop_max_repeats). Two
// repeats — three identical responses in total — rules out coincidence while
// still giving the model one warned chance to change approach.
const defaultRunawayLoopMaxRepeats = 2

// effectiveRunawayLoopMaxRepeats resolves the repeat limit, defaulting to 2.
func (a *Agent) effectiveRunawayLoopMaxRepeats() int {
	if a.cfg.RunawayLoopMaxRepeats > 0 {
		return a.cfg.RunawayLoopMaxRepeats
	}
	return defaultRunawayLoopMaxRepeats
}

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
// On each repeat below the configured limit it injects a warning hint AND
// surfaces a visible TUI warning naming the repeated response; when the
// repeat count reaches execution.runaway_loop_max_repeats (default 2) it
// stops the session with an error carrying the same evidence
// (runaway-loop visibility: the user must be able to judge whether the loop
// was real).
//
// A turn is NOT a repeat when its tool calls produced non-error results:
// executed tools are observable progress even when assistant text/thinking
// are byte-identical (see turnHadSuccessfulToolResult).
//
// The strike only counts when this turn produced a NEW assistant message:
// when the last assistant message predates turnStartHistoryLen (stream
// error, retry, pause), comparing the stale message against itself would
// score a false strike with zero actual repetition.
func (a *Agent) checkProgressLoop() error {
	maxRepeats := a.effectiveRunawayLoopMaxRepeats()
	warnSample, err := a.scanProgressLoop()
	if warnSample != "" {
		// Emitted after scanProgressLoop released a.mu (emitEvent locks it).
		a.emitEvent(OutputEvent{
			Type: EventContent,
			Role: System,
			Text: fmt.Sprintf("Runaway-loop warning: the assistant repeated the same response as the previous turn%s; the session stops after %d consecutive repeats.", loopEvidenceSuffix(warnSample), maxRepeats),
			Metadata: map[string]string{
				"category": "system-notification",
			},
		})
	}
	return err
}

// scanProgressLoop evaluates the repeat counters under a.mu. It returns the
// repeated-response sample whenever a non-terminal strike applies (the caller
// emits the visible warning — emitEvent takes a.mu, so it must run unlocked),
// or the terminal guardrail error when the latch trips.
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

	// Same fingerprint as the previous turn. Only a true stall may strike:
	// a turn whose tool calls produced non-error results made observable
	// progress — fresh data or world changes — so reset the counter instead
	// of incrementing it. Goal-mode agents legitimately emit little or no
	// prose while running different tools every turn; without this gate such
	// turns score as repeats and three of them kill a healthy session.
	if ok := a.turnHadSuccessfulToolResult(idx, msg); ok {
		a.cfg.Logger.Log(Info, "Loop guardrail: repeated fingerprint but %d tool result(s) succeeded; counting as progress", len(msg.ToolCalls))
		a.assistantRepeatCount = 0
		return "", nil
	}

	a.assistantRepeatCount++
	// Repeats below the configured limit are soft: the model is nudged with
	// an in-history recovery hint and the caller surfaces a visible warning.
	// Reaching the limit latches the session stop.
	maxRepeats := a.effectiveRunawayLoopMaxRepeats()
	a.cfg.Logger.Log(Warn, "Loop guardrail: assistant message repeated %d time(s) (limit %d)", a.assistantRepeatCount, maxRepeats)

	sample := progressLoopSample(msg)
	if a.assistantRepeatCount < maxRepeats {
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
// progress-loop detection. Every assistant turn participates — including
// truly empty ones (no content, no thinking, no tool calls) — because the
// model is supposed to produce one of those; only truly empty turns can ever
// score as "(empty response)" repeats, since turns carrying tool calls are
// either fingerprinted per tool or gated as progress by successful results.
func (a *Agent) isMeaningfulAssistantMessage(msg Message) bool {
	return msg.Role == Assistant
}

// hashAssistantMessage builds a fingerprint of an assistant message for
// repeat detection: text content, thinking, then one entry per tool call
// with its name plus a stable digest of its arguments. Turns running
// different tools — or the same tool with different arguments — therefore
// never score as repeats of each other, unlike the old count-only scheme
// where every tool turn collapsed to the same short hash.
func (a *Agent) hashAssistantMessage(msg Message) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(msg.Content))
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(msg.Thinking))
	b.WriteByte(0)
	for _, tc := range msg.ToolCalls {
		b.WriteString(tc.Name)
		b.WriteByte(':')
		b.WriteString(stableArgsDigest(tc.Arguments))
		b.WriteByte(';')
	}
	return b.String()
}

// stableArgsDigest returns a deterministic short digest of a tool-call
// arguments payload. Semantically equal JSON objects — differing only in key
// order or insignificant whitespace — produce equal digests; payloads that
// are not valid JSON fall back to their trimmed raw text so distinct
// arguments still hash distinctly.
func stableArgsDigest(args string) string {
	trimmed := strings.TrimSpace(args)
	canonical := trimmed
	switch {
	case trimmed == "":
		canonical = "{}" // absent and empty-object arguments are equivalent
	default:
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			// encoding/json marshals maps with sorted keys, normalizing order.
			if norm, err := json.Marshal(parsed); err == nil {
				canonical = string(norm)
			}
		}
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:8])
}

// turnHadSuccessfulToolResult reports whether the assistant turn at idx
// carried at least one tool call whose result landed in history without an
// execution error. Successful tool execution is observable progress, so such
// turns must never accumulate runaway-loop strikes — this is what keeps
// goal-mode sessions (terse prose, different tool every turn) alive.
func (a *Agent) turnHadSuccessfulToolResult(idx int, msg Message) bool {
	called := make(map[string]bool, len(msg.ToolCalls))
	haveIDs := false
	if len(msg.ToolCalls) == 0 {
		return false // no tool calls at all: nothing can count as progress
	}
	for _, tc := range msg.ToolCalls {
		if tc.ID == "" {
			continue // degenerate call: results match positionally below
		}
		called[tc.ID] = true
		haveIDs = true
	}
	for i := idx + 1; i < len(a.history); i++ {
		m := a.history[i]
		if m.Role != ToolRole {
			break // this batch's results end at the next non-tool message
		}
		if haveIDs && !called[m.ToolCallID] {
			continue // result belongs to a different batch
		}
		if !a.isToolErrorMessage(m) {
			return true
		}
	}
	return false
}

// isToolErrorMessage classifies a tool-result message as an execution error.
// Live results carry the authoritative metaToolError marker set by
// appendToolResults (always present, "true" or "false"); history rebuilt
// from persisted sessions loses Metadata, so the conventional "Error:"
// content prefix serves as a best-effort fallback there.
func (a *Agent) isToolErrorMessage(m Message) bool {
	if m.Metadata != nil {
		if marked, ok := m.Metadata[metaToolError]; ok {
			return marked == "true"
		}
	}
	return strings.HasPrefix(m.Content, toolResultErrorPrefix)
}

// withToolResultAsUser returns a copy of model with ToolResultAsUser set on its
// OpenAI completions compat.  Existing compat fields are preserved.
