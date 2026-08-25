// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func (a *Agent) tryAutoHealToolCalls() bool {
	if len(a.bufferedToolCalls) > 0 {
		return false
	}

	content := a.contentBuf.String()
	thinking := a.thinkingBuf.String()
	combined := combineContentThinking(content, thinking)
	// DSML is recovered unconditionally; the generic XML forms only when the
	// operator opted in to healing malformed local-model output.
	if !hasDSMLSignal(combined) && !(a.cfg.AutoHealToolCalls && hasToolSignal(combined)) {
		a.warnUnrecoveredInvokeCall(combined)
		return false
	}

	a.emitEvent(OutputEvent{
		Type: EventProgress,
		Text: "Decoding tool calls...",
	})

	// Parse path: full multi-form recovery when auto-heal is on; DSML-only when
	// it is off (generic XML healing stays opt-in, DSML never is).
	var calls []parsedToolCall
	if a.cfg.AutoHealToolCalls {
		calls = parseToolCallsFromText(combined, 0, true)
	} else {
		calls = parseDSMLToolCallsFromText(combined, 0, true)
	}
	if len(calls) == 0 {
		return false
	}

	a.stripHealedMarkup(content, thinking)
	controller := NewToolLoopController(a.reg.Schemas(), a.reg.LoopHints(), true)
	for _, pc := range calls {
		a.dispatchHealedCall(controller, pc)
	}
	return len(a.bufferedToolCalls) > 0 || controller.ForceFinalAnswer()
}

// combineContentThinking joins content and thinking buffers for scanning.
func combineContentThinking(content, thinking string) string {
	if thinking == "" {
		return content
	}
	if content == "" {
		return thinking
	}
	return content + "\n" + thinking
}

// stripHealedMarkup removes healed tool markup from both stream buffers.
func (a *Agent) stripHealedMarkup(content, thinking string) {
	a.contentBuf.Reset()
	a.contentBuf.WriteString(stripToolMarkup(content, true))
	a.thinkingBuf.Reset()
	a.thinkingBuf.WriteString(stripToolMarkup(thinking, true))
	a.thinkingDisplayBuf.Reset()
}

// dispatchHealedCall routes one recovered call through the loop controller;
// executable calls are buffered + emitted, no-op decisions are recorded.
func (a *Agent) dispatchHealedCall(controller *ToolLoopController, pc parsedToolCall) {
	decision := controller.PrepareCall(pc.name, pc.arguments, pc.id)
	if decision.Action != ActionExecute {
		if decision.Action == ActionDuplicate || decision.Action == ActionDisabled || decision.Action == ActionRenderHTMLRepeat {
			controller.RecordNoop(decision)
		}
		return
	}
	a.bufferedToolCallCount++
	a.emitEvent(OutputEvent{
		Type:       EventToolCall,
		State:      StateToolCall,
		ToolName:   decision.ToolName,
		ToolInput:  decision.Arguments,
		ToolCallID: decision.ToolCallID,
	})
	a.bufferedToolCalls = append(a.bufferedToolCalls, provider.ContentBlock{
		Type:          provider.ContentBlockToolCall,
		ToolCallID:    decision.ToolCallID,
		ToolName:      decision.ToolName,
		ToolArguments: decision.Arguments,
	})
}

// warnUnrecoveredInvokeCall surfaces a closed invoke-dialect tool call that
// arrived as text while recovery is disabled (auto_heal_tool_calls off) and
// no native call superseded it. Without this the call is silently rendered
// as content and never executed — the exact loss observed in export
// goa-export-20260819-004622 (a goal create emitted as text after a garbled
// token). Only a closed block naming a REGISTERED tool with parseable
// parameters warns, so prose merely discussing the XML shape stays quiet.
func (a *Agent) warnUnrecoveredInvokeCall(combined string) {
	if a.cfg.AutoHealToolCalls || len(a.bufferedToolCalls) > 0 {
		return
	}
	sc := &toolCallScanner{content: combined, allowIncomplete: false}
	for {
		pc, ok := sc.nextInvokeCall()
		if !ok {
			return
		}
		if a.registeredToolName(pc.name) {
			a.emitEvent(OutputEvent{
				Type: EventProgress,
				Text: fmt.Sprintf("warning: model emitted tool %q as text (<invoke>) and it was NOT executed — enable auto_heal_tool_calls to recover text tool calls", pc.name),
			})
			return
		}
	}
}

// registeredToolName reports whether name matches a tool in the agent's
// current registry. Callers run in the same context as the other registry
// reads in the stream-completion path (see toolListHashLocked).
func (a *Agent) registeredToolName(name string) bool {
	for _, s := range a.reg.Schemas() {
		if s.Name == name {
			return true
		}
	}
	return false
}

// completeStreamTurn finalizes the assistant buffer, executes buffered tool
// calls, and reports whether the agent loop should stream another round
// (true = a real tool executed and the model should be queried again). If a
// tool result requested that the batch stop after this result, the turn ends
// even if the model issued additional tool calls.
//
// When tool calls are present, finalizeStreamTurn is NOT called — the full
// assistant message (content + tool_calls) is assembled in
// executeBufferedToolCalls. Calling finalizeStreamTurn first would append a
// partial assistant message (content only), followed by a second full message
// from appendAssistantToolCallMessage, producing duplicate assistant messages
// that break prompt caching and corrupt the conversation structure.
//
// EventEnd semantics: EventEnd signals the end of the whole conversation
// turn, NOT the end of a single stream round. It is therefore emitted ONLY
// when the turn is actually finishing — either here (when no further round
// will run) or later via finalizeStreamTurn (once the model produces a final
// answer without tool calls). Emitting EventEnd after every tool batch made
// UI consumers (e.g. the status spinner) tear down turn state mid-turn, which
// silently dropped the spinner after the first tool call.

const maxAutoContinuePerTurn = 3

// prematureStopKind classifies why a stream round that ended with
// finish_reason=stop looks premature — i.e. the model quit mid-task and goa
// should auto-continue instead of ending the turn and making the user type
// "continue".
type prematureStopKind int

const (
	// prematureStopNone: no premature-stop signal; the round is a legitimate
	// turn end.
	prematureStopNone prematureStopKind = iota
	// prematureStopThinkingOnly: the round produced reasoning tokens but no
	// visible answer and no tool calls — the model stopped right after its
	// thinking block (reasoning-token/output-limit symptom).
	prematureStopThinkingOnly
	// prematureStopAfterTools: the round followed real tool execution and
	// produced nothing at all — the model stopped immediately after receiving
	// the tool results without an answer or further calls.
	prematureStopAfterTools
	// prematureStopTruncated: the round produced answer text that is clearly
	// truncated mid-task.
	prematureStopTruncated
)

// classifyPrematureStop inspects the finished round's buffers plus per-turn
// state and reports which premature-stop case (if any) applies. Once the
// auto-continue budget is exhausted it always reports none, bounding how often
// a misbehaving provider can extend its own turn; finalizeStreamTurn then ends
// the turn (still surfacing the silent-stop notice for thinking-only rounds).
func (a *Agent) classifyPrematureStop() prematureStopKind {
	if a.autoContinueCount >= maxAutoContinuePerTurn {
		return prematureStopNone // budget exhausted; finalize the turn
	}
	a.mu.Lock()
	content := strings.TrimSpace(a.contentBuf.String())
	thinking := strings.TrimSpace(a.thinkingBuf.String())
	a.mu.Unlock()

	switch {
	case content == "" && thinking != "":
		// Stop after a thinking block: reasoning happened, but no answer text
		// and no tool calls were returned. A model never legitimately ends a
		// turn here, regardless of earlier tool work — continue it.
		return prematureStopThinkingOnly
	case content == "":
		// Nothing streamed this round at all. Without prior tool work this is
		// either the empty-response guard's territory or an opted-in empty
		// answer; after real tool execution a bare stop directly after the
		// results is mid-task (a real "done" would be answer text).
		if a.turnHadToolExecution && !a.cfg.AllowEmptyResponse {
			return prematureStopAfterTools
		}
		return prematureStopNone
	case !a.turnHadToolExecution:
		return prematureStopNone // no tool work → a plain (possibly terse) answer is legitimate
	case looksTruncated(content):
		return prematureStopTruncated
	default:
		// After tool work, an unfulfilled trailing intent is a premature stop even
		// when the text ends with terminal punctuation: looksTruncated's
		// punctuation gate exists to protect no-tool terse answers, but a turn that
		// already executed tools and then announces more work ("…Let me check
		// these.") without emitting the calls stopped mid-task (2026-08-05
		// kimi-code/k3-256k export: silent round end, no tool calls, no error).
		if hasTrailingIntent(content) {
			return prematureStopTruncated
		}
		return prematureStopNone
	}
}

// describe returns the reason fragment used in the premature-stop warn log.
func (k prematureStopKind) describe() string {
	switch k {
	case prematureStopThinkingOnly:
		return "stopped after reasoning without an answer"
	case prematureStopAfterTools:
		return "empty stop right after tool results"
	case prematureStopTruncated:
		return "incomplete output after tool work"
	default:
		return "no premature stop"
	}
}

// steerNote returns the ephemeral system control note injected before the
// continuation round. The [goa-system] prefix makes InjectEphemeralSystemMessage
// surface it as a system notification (users must see why the agent continued);
// the message itself is stripped from history at turn end.
func (k prematureStopKind) steerNote() string {
	head := "[goa-system] Internal control note (never show or mention to the user): "
	tail := " Do not restart, do not re-summarize, and do not stop until the task is fully done."
	switch k {
	case prematureStopThinkingOnly:
		return head + "your previous reply stopped after your reasoning step without producing any answer or tool calls. Pick up exactly where your reasoning left off and produce the result now." + tail
	case prematureStopAfterTools:
		return head + "you stopped immediately after receiving the tool results without producing any reply or further tool calls. Use those results to continue the work and finish it now." + tail
	default:
		return head + "your previous reply was cut off mid-task before completion. Continue the work immediately and finish it now." + tail
	}
}

// looksTruncated reports whether streamed answer text ends mid-task. It
// requires an explicit continuation signal: a trailing continuation marker
// (: , ; - ( …) or a trailing intent phrase ("let me", "I'll", "I will",
// ...). Missing terminal punctuation alone is NOT evidence of truncation:
// terse styles (e.g. minimal-punctuation skill answers like "Skill loaded —
// telegram ready") end complete replies without one, and auto-continuing
// those burns extra rounds and re-nudges the model for no reason
// (2026-08-04 export: false positive triggered the auto-continue that broke
// the session).
func looksTruncated(content string) bool {
	s := strings.TrimRight(content, " \t\r\n")
	if s == "" {
		return false // empty is handled by the empty-response / silent-stop guards
	}
	last := s[len(s)-1]
	// Terminal punctuation or closing markdown = complete.
	if strings.ContainsRune(".!?)`\"']}|>*_", rune(last)) {
		return false
	}
	// Explicit continuation markers.
	if strings.ContainsRune(":;,(-–—…", rune(last)) {
		return true
	}
	// Trailing intent phrase (last 40 chars, case-insensitive).
	return hasTrailingIntent(s)
}

// hasTrailingIntent reports whether the tail of s carries an explicit intent
// to keep working ("let me", "I'll", "I will", ...; last 40 chars,
// case-insensitive). It is the shared intent detector for looksTruncated and
// for the post-tool-work premature-stop path in shouldAutoContinue.
func hasTrailingIntent(s string) bool {
	tail := s
	if len(tail) > 40 {
		tail = tail[len(tail)-40:]
	}
	tail = strings.ToLower(tail)
	for _, phrase := range []string{"let me", "i'll", "i will", "i'm going to", "now i", "next i", "i need to", "let's"} {
		if strings.Contains(tail, phrase) {
			return true
		}
	}
	return false
}

// finishStreamTurn handles a stream that ended without an explicit EventDone.
