// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"fmt"
	"regexp"
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
	if !hasDSMLSignal(combined) && !(a.AutoHealEnabled() && hasToolSignal(combined)) {
		a.reportUnrecoveredTextToolCall(combined, content, thinking)
		return false
	}

	a.emitEvent(OutputEvent{
		Type: EventProgress,
		Text: "Decoding tool calls...",
	})

	// Parse path: full multi-form recovery when auto-heal is on; DSML-only when
	// it is off (generic XML healing stays opt-in, DSML never is).
	var calls []parsedToolCall
	if a.AutoHealEnabled() {
		calls = parseToolCallsFromText(combined, 0, true)
	} else {
		calls = parseDSMLToolCallsFromText(combined, 0, true)
	}
	if len(calls) == 0 {
		// The markup WAS recognized (signal present) but no executable call
		// came out of it — a shape the parser cannot fold (e.g. an unterminated
		// or interleaved block). Report it instead of returning silently: a
		// dropped call that neither executes nor explains itself is the
		// "unexpected stop" failure (export goa-export-20260926-101445).
		a.reportUnrecoveredTextToolCall(combined, content, thinking)
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

// reportUnrecoveredTextToolCall surfaces a tool call that arrived as text and
// was NOT executed, plus the guidance the model needs to re-issue it. It
// replaces the old warnUnrecoveredInvokeCall, whose early return when healing
// was ON made exactly the misconfigured case silent: a recognized-but-
// unrecoverable call (whitespace DSML dialect in export
// goa-export-20260926-101445, an unterminated block, an interleaved dialect)
// used to end the turn with raw markup as the answer and no explanation.
//
// Behavior:
//   - only a CLOSED interactive block that names a REGISTERED tool qualifies,
//     so prose merely discussing the XML shape stays quiet;
//   - the markup is stripped from the stream buffers either way, so it can
//     never be finalized as the assistant's answer;
//   - the notice distinguishes "healing is off" (enable it) from "healing is
//     on but recovery failed" (re-issue as a native call), and the model gets a
//     durable system note so the next round fixes the call instead of the user
//     having to type "continue";
//   - it fires at most once per turn (callDroppedReported).
func (a *Agent) reportUnrecoveredTextToolCall(combined, content, thinking string) {
	if len(a.bufferedToolCalls) > 0 {
		return
	}
	dropped := closedInvokeCallNames(combined, func(name string) bool { return a.registeredToolName(name) })
	if len(dropped) == 0 {
		return
	}
	// Never let unrecovered tool markup become the visible answer.
	a.stripHealedMarkup(content, thinking)

	a.mu.Lock()
	already := a.callDroppedReported
	a.callDroppedReported = true
	a.mu.Unlock()
	if already {
		return
	}

	names := strings.Join(dropped, ", ")
	notice := fmt.Sprintf("warning: model emitted tool call(s) %s as text and was NOT executed — ", names)
	if a.AutoHealEnabled() {
		notice += "text-call recovery could not reconstruct them; re-issue them as native tool calls"
	} else {
		notice += "enable auto_heal_tool_calls to recover text tool calls"
	}
	a.cfg.Logger.Log(Warn, "%s", notice)
	a.emitEvent(OutputEvent{Type: EventProgress, Text: notice})
	// Durable guidance for the model: the turn continues, so the next round can
	// re-issue the call natively instead of the user seeing a dead stop.
	a.InjectEphemeralSystemMessage("[goa-system] Internal control note (never show or mention to the user): your previous reply wrote a tool call as plain text (" + names +
		") instead of emitting a native tool call, so nothing was executed. Re-issue that call now as a native tool call with the same arguments. Do not repeat the markup and do not stop.")
}

// closedInvokeCallNames returns the tool names of every CLOSED invoke-dialect
// block (DSML, whitespace DSML, or Anthropic-legacy) accepted by accept, in
// order of appearance. Complete blocks only: an unterminated block in a
// still-streaming buffer cannot be judged, and prose mentioning the markup does
// not name a registered tool.
func closedInvokeCallNames(text string, accept func(string) bool) []string {
	sc := newToolCallScanner(text, 0, false)
	var names []string
	for _, pc := range sc.allInvokeCalls() {
		if accept(pc.name) {
			names = append(names, pc.name)
		}
	}
	for _, pc := range sc.allDSMLCalls() {
		if accept(pc.name) {
			names = append(names, pc.name)
		}
	}
	return names
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

// hasTrailingIntent reports whether the closing sentence of s announces work
// the model is about to do rather than work it has finished. It is the shared
// intent detector for looksTruncated and for the post-tool-work premature-stop
// path in classifyPrematureStop.
//
// Matching is ANCHORED to the start of the final sentence and matched on whole
// words. Both properties are load-bearing:
//
//   - Anchored. An unanchored substring search fires on any incidental mention
//     ("the parser now rejects both shapes" contains "now i..." boundaries), so
//     a finished answer would be dragged into another round.
//   - Whole-word window. The scan looks at the last intentWindowChars
//     characters, which can cut a phrase in half — "…Let me now write the fix
//     plan and finish." truncated to 40 chars is "et me now write the fix
//     plan and finish.", losing the very phrase being looked for. Word
//     boundaries make that loss impossible (see intentWindow).
//
// The 2026-10-02 creaves.project export is the evidence for both: round 83
// ("…Let me write the fix plan.") tripped the guard and auto-continued, round 84
// ("…Now writing the fix plan.") stated the identical intent in a wording the
// phrase list did not carry, so the turn ended on an undelivered promise with
// two of three auto-continue attempts unspent.
func hasTrailingIntent(s string) bool {
	sentence := strings.ToLower(intentWindow(lastSentence(s)))
	for _, re := range trailingIntentFrames {
		if re.MatchString(sentence) {
			return true
		}
	}
	return false
}

// trailingIntentFrames are the announcement shapes a model uses when it is
// about to start work rather than reporting work it has done. Each pattern
// anchors at the start of the closing sentence (^), so a finished answer that
// merely mentions the same words mid-sentence is not matched.
//
// The set deliberately covers grammatical frames rather than enumerating verbs:
// "now <gerund>", "proceeding/continuing/moving on", "here is / here's", and
// "next step" between them catch the announcement regardless of which verb the
// model picks next, which is what the fixed phrase list could not do.
var trailingIntentFrames = []*regexp.Regexp{
	// "Let me …", "I'll …", "I will …", "I need to …", "I'm going to …",
	// "Let's …" — with an optional leading connective ("Now let me …").
	regexp.MustCompile(`^(?:(?:now|and|but|so|ok|okay|alright|well)[,\s]+)?(?:let me|i'?ll|i will|i need to|i'?m going to|let'?s)\b`),
	// "Now writing the fix plan." / "Next I will check the parser."
	regexp.MustCompile(`^(?:now|next|then)[,\s]+(?:i'?m\s+)?[a-z]+ing\b`),
	// "Proceeding with the implementation.", "Continuing with the plan.",
	// "Moving on to the fix."
	regexp.MustCompile(`^(?:proceeding|continuing|moving|going|starting|heading)\s+(?:on\s+|to\s+|with\s+|into\s+)`),
	// "Here is the fix plan.", "Here's the plan.", "Here are the changes."
	regexp.MustCompile(`^here\s*(?:is|are|'s)\b`),
	// "Next step: patch the parser."
	regexp.MustCompile(`^next\s+steps?\b`),
}

// intentWindowChars bounds how far back the intent scan looks. Sized to cover a
// full announcement sentence including a leading connective.
const intentWindowChars = 120

// intentWindow trims s to its last intentWindowChars, snapped forward to the
// next word boundary so the scan can never begin mid-word (which is how
// "let me" became "et me" and escaped the guard).
func intentWindow(s string) string {
	if len(s) <= intentWindowChars {
		return s
	}
	tail := s[len(s)-intentWindowChars:]
	if i := strings.IndexAny(tail, " \t\n"); i >= 0 {
		tail = tail[i+1:]
	}
	return tail
}

// lastSentence returns the region the intent frames are anchored to: the final
// sentence when the reply has terminal punctuation, otherwise the final clause.
// Anchoring on the closing sentence is what keeps a finished answer from being
// dragged into another round — an unanchored scan over the whole reply matches
// any incidental mention of "let me" in the middle of it.
//
// Boundaries are only accepted when text FOLLOWS them, so a reply that ends on
// its own terminator falls through to the previous boundary rather than scanning
// the empty tail. When no accepted boundary remains, the text is scanned whole
// (a single short sentence, or a reply cut off mid-thought with no punctuation
// at all).
func lastSentence(s string) string {
	s = strings.TrimRight(s, " \t\r\n")
	if frag := trailingFragment(s, sentenceBreaks); frag != "" {
		return frag
	}
	if frag := trailingFragment(s, clauseBreaks); frag != "" {
		return frag
	}
	return s
}

// sentenceBreaks are the terminal punctuation marks that end a sentence;
// clauseBreaks are the weaker intra-sentence boundaries tried only when the
// reply carries no terminal punctuation (a reply cut off mid-thought).
const (
	sentenceBreaks = ".!?"
	clauseBreaks   = ",;:\n"
)

// trailingFragment returns the text following the last boundary byte in cut
// that is itself followed by more text, or "" when no such boundary exists.
func trailingFragment(s, cut string) string {
	for i := strings.LastIndexAny(s, cut); i >= 0; {
		if frag := strings.TrimSpace(s[i+1:]); frag != "" {
			return frag
		}
		// This boundary closes the reply; look for the one before it.
		head := strings.TrimRight(s[:i], " \t\r\n")
		if head == "" {
			return ""
		}
		s = head
		i = strings.LastIndexAny(s, cut)
	}
	return ""
}

// finishStreamTurn handles a stream that ended without an explicit EventDone.
