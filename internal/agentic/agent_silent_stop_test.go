// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestAgent_ThinkingOnlyTurnEmitsNotice is the regression test for the
// "session stopped without any message" bug: a reasoning model that finishes
// with finish_reason "stop" after emitting only thinking tokens (no answer
// content, no tool calls) must NOT end the turn silently. The agent must
// surface a non-transient system notification so the user understands why the
// turn produced no reply.
func TestAgent_ThinkingOnlyTurnEmitsNotice(t *testing.T) {
	// Provider streams only thinking deltas, then ends cleanly with no
	// content blocks — the k3 "stopped mid-reasoning" scenario.
	p := registerTestProvider("thinking-only", []provider.AssistantMessageEvent{
		{Type: provider.EventThinkingStart, ContentIndex: 0},
		{Type: provider.EventThinkingDelta, ContentIndex: 0, Delta: "Let me analyze the gocognit results. "},
		{Type: provider.EventThinkingDelta, ContentIndex: 0, Delta: "I need to refactor the two functions..."},
		{Type: provider.EventThinkingEnd, ContentIndex: 0},
	})

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	runAgentToDone(t, agent, "Continue the gate")

	// The turn must still end (EventEnd) — it is not retried as an error.
	if !obs.HasEventType(EventEnd) {
		t.Error("expected EventEnd for a clean stop")
	}

	// A non-transient system notification must explain the silent stop.
	found := false
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == System &&
			strings.Contains(e.Text, "stopped after its reasoning step") {
			found = true
			if e.Metadata["category"] != "system-notification" {
				t.Errorf("notice missing system-notification category, got %v", e.Metadata)
			}
			if e.Metadata["transient"] == "true" {
				t.Error("notice must be non-transient so it survives the turn")
			}
		}
	}
	if !found {
		t.Error("expected a system notification explaining the empty (thinking-only) stop")
	}
}

// TestAgent_ContentTurnNoNotice guards against false positives: a normal turn
// that produces visible answer content must NOT emit the silent-stop notice.
func TestAgent_ContentTurnNoNotice(t *testing.T) {
	p := textEventProvider("Here is the answer.")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	runAgentToDone(t, agent, "Hi")

	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == System &&
			strings.Contains(e.Text, "stopped after its reasoning step") {
			t.Error("silent-stop notice must not fire on a turn that produced answer content")
		}
	}
}

// emptySequenceProvider returns an empty clean stream (no events, clean End
// with no content) for the first `emptyRounds` calls, then a normal text
// response. It simulates a provider under load that returns a truncated/empty
// 200+[DONE] body before recovering.
type emptySequenceProvider struct {
	api         provider.Api
	emptyRounds int
	calls       int
	text        string
}

func (p *emptySequenceProvider) API() provider.Api { return p.api }

func (p *emptySequenceProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	p.calls++
	empty := p.calls <= p.emptyRounds
	go func() {
		if empty {
			// Clean end with NO content/thinking/tool calls — the silent-stop
			// vector under provider load.
			result.End(&provider.AssistantMessage{StopReason: provider.StopReasonEndTurn})
			return
		}
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: p.text})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: p.text}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *emptySequenceProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

func registerEmptySequenceProvider(emptyRounds int, text string) *emptySequenceProvider {
	uniqueID := testProviderCounter.Add(1)
	p := &emptySequenceProvider{
		api:         provider.Api(fmt.Sprintf("test-empty-seq-%d", uniqueID)),
		emptyRounds: emptyRounds,
		text:        text,
	}
	provider.RegisterApiProvider(p)
	return p
}

// TestAgent_EmptyResponseRetried verifies that a clean-but-empty stream (the
// k3-under-load case) is NOT a silent stop: it is classified transient and
// retried, and the turn completes with real content once the provider
// recovers.
func TestAgent_EmptyResponseRetried(t *testing.T) {
	p := registerEmptySequenceProvider(1, "Recovered answer")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	runAgentToDone(t, agent, "Hi")

	// The retried (second) stream produced the answer content.
	assertEventObserved(t, obs.Events(), EventContent, Assistant, "Recovered answer")
	// The user saw a retry notice, not silence.
	foundRetry := false
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == System && strings.Contains(e.Text, "retrying") {
			foundRetry = true
		}
	}
	if !foundRetry {
		t.Error("expected a non-transient retry notification for the empty response")
	}
}

// TestAgent_EmptyResponseExhaustsSurfaced verifies that when the provider
// keeps returning empty streams, the failure is surfaced as a system
// notification (never a silent stop) after retries are exhausted.
func TestAgent_EmptyResponseExhaustsSurfaced(t *testing.T) {
	p := registerEmptySequenceProvider(100, "") // always empty
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	runAgentToDone(t, agent, "Hi")

	// A system notification mentioning the empty response must appear.
	found := false
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == System &&
			strings.Contains(strings.ToLower(e.Text), "empty response") {
			found = true
		}
	}
	if !found {
		t.Error("expected a surfaced system notification for exhausted empty-response retries (no silent stop)")
	}
}

// TestShouldRetryStreamError_EmptyResponse pins the classification: the
// synthesized empty-response error must be retryable (transient), unlike a
// user deadline.
func TestShouldRetryStreamError_EmptyResponse(t *testing.T) {
	if !shouldRetryStreamError(errEmptyResponse) {
		t.Error("errEmptyResponse must be classified retryable")
	}
}
