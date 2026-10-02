// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestClassifyPrematureStop_AnnouncedWorkNeverDelivered pins the guard that
// failed in the 2026-10-02 creaves.project export
// (opencode-go / space-bunny-free, 83 tool rounds).
//
// The session ended every round by announcing work it never performed:
//
//	round 83: "...Let me write the fix plan."   -> detected, auto-continued (attempt 1/3)
//	round 84: "...Now writing the fix plan."   -> MISSED, turn finalized
//
// Round 84 is the last thing the user saw: a promise with no plan behind it,
// with two of the three auto-continue attempts still unused. The budget existed
// for exactly this case and was never spent.
//
// Every case below is text the provider actually produced, or a direct
// paraphrase of that shape.
func TestClassifyPrematureStop_AnnouncedWorkNeverDelivered(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    prematureStopKind
	}{
		// --- verbatim from the failing session ---
		{
			name:    "round 83 wording (was caught)",
			content: "Now I have complete, evidence-backed findings. Let me write the fix plan.",
			want:    prematureStopTruncated,
		},
		{
			name:    "round 84 wording (the observed sudden stop)",
			content: "Confirmed: **0 apply buttons** in `#animalCarePlan` — feeding/observation rows are read-only on the animal Protocol tab. Now writing the fix plan.",
			want:    prematureStopTruncated,
		},
		{
			// Same sentence as round 83 but pushed past the old 40-byte window,
			// which sliced "let me" into "et me" and lost the match.
			name:    "let me pushed past the old 40-byte window",
			content: "The evidence now covers all 44 rows in the table plus both tabs. Let me write the fix plan and finish.",
			want:    prematureStopTruncated,
		},
		{
			name:    "here is the plan",
			content: "Findings are confirmed across both tabs. Here is the fix plan.",
			want:    prematureStopTruncated,
		},
		{
			name:    "here's the plan",
			content: "Findings are confirmed across both tabs. Here's the fix plan.",
			want:    prematureStopTruncated,
		},
		{
			name:    "proceeding with the implementation",
			content: "Findings are confirmed across both tabs. Proceeding with the implementation.",
			want:    prematureStopTruncated,
		},
		{
			name:    "continuing with the plan",
			content: "Root cause is identified. Continuing with the plan.",
			want:    prematureStopTruncated,
		},
		{
			name:    "now applying the fix",
			content: "Root cause is identified and scoped. Now applying the fix.",
			want:    prematureStopTruncated,
		},
		{
			name:    "next step",
			content: "Both call sites are confirmed. Next step: patch the parser.",
			want:    prematureStopTruncated,
		},

		// --- regression guards: genuinely complete answers must still end ---
		{
			name:    "complete answer with period",
			content: "Both fixes applied and verified.",
			want:    prematureStopNone,
		},
		{
			// The documented 2026-08-04 false positive: a terse,
			// minimal-punctuation answer must not be auto-continued.
			name:    "terse minimal-punctuation answer",
			content: "Skill loaded — telegram ready",
			want:    prematureStopNone,
		},
		{
			name:    "complete answer that merely contains 'now'",
			content: "The parser now rejects both malformed shapes.",
			want:    prematureStopNone,
		},
		{
			name:    "complete answer describing a plan already delivered",
			content: "The fix plan was applied: both call sites now share one helper.",
			want:    prematureStopNone,
		},
		{
			// Decimals and abbreviations contain sentence-boundary bytes; the
			// anchor must land on the real closing sentence, not mid-number.
			name:    "announcement after a version number",
			content: "The fix ships in v1.2. Let me continue.",
			want:    prematureStopTruncated,
		},
		{
			name:    "announcement after an abbreviation",
			content: "See the e.g. clause. Now writing the plan.",
			want:    prematureStopTruncated,
		},
		{
			name:    "announcement after a parenthesized aside",
			content: "Result: 44 rows (all planned). Proceeding with the fix.",
			want:    prematureStopTruncated,
		},
		{
			// Cut off mid-thought: no terminal punctuation anywhere, so the
			// scan falls back to the closing clause.
			name:    "unpunctuated reply announcing work",
			content: "All rows are planned, let me finish",
			want:    prematureStopTruncated,
		},
		{
			// Same shape, but the word "now" is mid-sentence on completed work.
			name:    "complete answer with clause boundary",
			content: "The parser rejects malformed shapes, as the tests now confirm",
			want:    prematureStopNone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAgent(Config{SystemPrompt: "sys", Logger: NewLogger(Error)})
			a.contentBuf.WriteString(tc.content)
			// The failing turn ran 83 tool rounds before stopping.
			a.turnHadToolExecution = true
			a.autoContinueCount = 1 // one attempt already spent; two remained
			if got := a.classifyPrematureStop(); got != tc.want {
				t.Errorf("classifyPrematureStop() = %v (%s), want %v",
					got, got.describe(), tc.want)
			}
		})
	}
}

// TestHasTrailingIntent_PhraseNotSlicedByWindow pins the window defect on its
// own: the intent phrase must be matched as a whole, so a window that starts
// mid-phrase cannot silently drop the match.
func TestHasTrailingIntent_PhraseNotSlicedByWindow(t *testing.T) {
	const intent = "Let me write the fix plan and finish it."
	if !hasTrailingIntent(intent) {
		t.Errorf("hasTrailingIntent(%q) = false, want true (intent phrase must not be lost to the scan window)", intent)
	}
}

// announcedWorkProvider replays the failing 2026-10-02 session shape through the
// real agent loop: one round of tool work, then two rounds that each end by
// ANNOUNCING work instead of delivering it, then a real answer.
type announcedWorkProvider struct {
	api      provider.Api
	calls    atomic.Int32
	announce []string
}

func (p *announcedWorkProvider) API() provider.Api { return p.api }

func (p *announcedWorkProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	round := p.calls.Add(1)
	go func() {
		switch {
		case round == 1:
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventToolCallEnd,
				ContentIndex: 0,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    "call_1",
					ToolName:      "mock_tool",
					ToolArguments: `{"arg":"value"}`,
				},
			})
			result.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockToolCall, ToolCallID: "call_1", ToolName: "mock_tool", ToolArguments: `{"arg":"value"}`}},
				StopReason: provider.StopReasonToolCall,
			})
		case round <= int32(1+len(p.announce)):
			// The exact failure mode: a promise, delivered as a finished turn.
			text := p.announce[round-2]
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: text})
			result.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: text}},
				StopReason: provider.StopReasonEndTurn,
			})
		default:
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "Fix plan delivered and verified."})
			result.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "Fix plan delivered and verified."}},
				StopReason: provider.StopReasonEndTurn,
			})
		}
	}()
	return result, nil
}

func (p *announcedWorkProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

func registerAnnouncedWorkProvider(announce []string) *announcedWorkProvider {
	id := prematureStopCounter.Add(1)
	p := &announcedWorkProvider{
		api:      provider.Api(fmt.Sprintf("test-announced-work-%d", id)),
		announce: announce,
	}
	provider.RegisterApiProvider(p)
	return p
}

// TestAgent_AnnouncedWorkAutoContinuesUntilDelivered is the end-to-end guard for
// the reported sudden stop: the user is left staring at "Now writing the fix
// plan." while the model does nothing more. Both announcement wordings from the
// export must be auto-continued until the model actually delivers the work.
func TestAgent_AnnouncedWorkAutoContinuesUntilDelivered(t *testing.T) {
	// Verbatim round 83 and round 84 text from the failing session.
	announce := []string{
		"Confirmed: **0 apply-like buttons** in `#animalCarePlan`. Now I have complete, evidence-backed findings. Let me write the fix plan.",
		"Confirmed: **0 apply buttons** in `#animalCarePlan` — feeding/observation rows are read-only on the animal Protocol tab. Now writing the fix plan.",
	}

	p := registerAnnouncedWorkProvider(announce)
	agent := newAgentWithMockTool(p.API(), 10)
	obs := runAgentCollectingEvents(t, agent, "fix the apply-button bug")

	// The turn must reach the delivered answer rather than ending on a promise.
	assertEventObserved(t, obs.Events(), EventContent, Assistant, "Fix plan delivered and verified.")
	if got := p.calls.Load(); got != int32(2+len(announce)) {
		t.Errorf("provider calls = %d, want %d (tool round, %d announcement rounds, delivery round)",
			got, 2+len(announce), len(announce))
	}
	// The steer note is surfaced so the user can see why goa continued
	// instead of silently stalling on the promise.
	assertEventObserved(t, obs.Events(), EventContent, System,
		"your previous reply was cut off mid-task before completion")
}
