// SPDX-License-Identifier: GPL-3.0-or-later

package openairesponses

import (
	"io"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// parseSync runs a parse function and blocks until it (and any baseline
// recording) has fully finished, draining the stream meanwhile.
func parseSync(stream *provider.AssistantMessageEventStream, parse func()) {
	done := make(chan struct{})
	go func() {
		parse()
		close(done)
	}()
	for range stream.Seq() {
	}
	<-done
}

func sseBody(events ...string) io.ReadCloser {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	return io.NopCloser(strings.NewReader(b.String()))
}

func TestBaselineCapturedOnSuccessfulCompleted(t *testing.T) {
	resetWSBaselines()
	defer resetWSBaselines()

	lastInput := []provider.Message{provider.NewUserMessage("hello")}
	completed := `{"type":"response.completed","response":{"id":"resp-123","status":"completed","usage":{"input_tokens":3,"output_tokens":2},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi there"}]}]}}`
	stream := provider.NewAssistantMessageEventStream(8)
	parseSync(stream, func() {
		parseResponsesSSEWithBaseline(sseBody(
			`{"type":"response.output_text.delta","delta":"hi there"}`,
			completed,
		), stream, "sess-1", lastInput, requestFingerprint{})
	})

	b := wsBaseline("sess-1")
	if b == nil {
		t.Fatal("expected baseline recorded for sess-1")
	}
	if b.ResponseID != "resp-123" {
		t.Errorf("ResponseID = %q, want resp-123", b.ResponseID)
	}
	if len(b.LastInput) != 1 || b.LastInput[0].Content[0].Text != "hello" {
		t.Errorf("LastInput not captured: %+v", b.LastInput)
	}
	if len(b.AddedItems) != 1 || b.AddedItems[0].Content[0].Text != "hi there" {
		t.Errorf("AddedItems not captured: %+v", b.AddedItems)
	}
}

func TestBaselineDeepCopyIsolated(t *testing.T) {
	resetWSBaselines()
	defer resetWSBaselines()

	lastInput := []provider.Message{provider.NewUserMessage("hello")}
	completed := `{"type":"response.completed","response":{"id":"resp-1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"x"}]}]}}`
	stream := provider.NewAssistantMessageEventStream(8)
	parseSync(stream, func() { parseResponsesSSEWithBaseline(sseBody(completed), stream, "sess-dc", lastInput, requestFingerprint{}) })

	// Mutate the source input; the recorded baseline must be unaffected.
	lastInput[0].Content[0].Text = "MUTATED"
	// Mutate a fetched copy; the store must be unaffected either.
	got := wsBaseline("sess-dc")
	got.AddedItems[0].Content[0].Text = "MUTATED2"
	got.LastInput[0].Content[0].Text = "MUTATED3"

	fresh := wsBaseline("sess-dc")
	if fresh.LastInput[0].Content[0].Text != "hello" {
		t.Errorf("LastInput shared with caller: %q", fresh.LastInput[0].Content[0].Text)
	}
	if fresh.AddedItems[0].Content[0].Text != "x" {
		t.Errorf("AddedItems shared with caller: %q", fresh.AddedItems[0].Content[0].Text)
	}
}

func TestBaselineNotAdvancedOnFailure(t *testing.T) {
	resetWSBaselines()
	defer resetWSBaselines()

	// Seed a good baseline.
	lastInput := []provider.Message{provider.NewUserMessage("first")}
	good := `{"type":"response.completed","response":{"id":"resp-good","status":"completed","output":[]}}`
	s1 := provider.NewAssistantMessageEventStream(8)
	parseSync(s1, func() { parseResponsesSSEWithBaseline(sseBody(good), s1, "sess-f", lastInput, requestFingerprint{}) })
	if wsBaseline("sess-f") == nil {
		t.Fatal("seed baseline not recorded")
	}

	// A stream that dies mid-flight with no completed event must not advance.
	bad := provider.NewAssistantMessageEventStream(8)
	parseSync(bad, func() {
		parseResponsesSSEWithBaseline(sseBody(
			`{"type":"response.output_text.delta","delta":"partial"}`,
		), bad, "sess-f", []provider.Message{provider.NewUserMessage("second")}, requestFingerprint{})
	})

	b := wsBaseline("sess-f")
	if b.ResponseID != "resp-good" {
		t.Errorf("baseline advanced on failure: ResponseID = %q, want resp-good", b.ResponseID)
	}
	if b.LastInput[0].Content[0].Text != "first" {
		t.Errorf("baseline input overwritten on failure: %q", b.LastInput[0].Content[0].Text)
	}
}

func TestBaselineNotRecordedForNonCodex(t *testing.T) {
	resetWSBaselines()
	defer resetWSBaselines()

	// Plain parse (non-Codex WS / SSE path) must never touch the registry.
	completed := `{"type":"response.completed","response":{"id":"resp-x","status":"completed","output":[]}}`
	stream := provider.NewAssistantMessageEventStream(8)
	parseSync(stream, func() { parseResponsesSSE(sseBody(completed), stream) })

	if wsBaseline("sess-noncodex") != nil {
		t.Error("baseline recorded on non-Codex path")
	}
}

func TestBaselineSessionKeyPrefersCacheKey(t *testing.T) {
	opts := provider.StreamOptions{PromptCacheKey: "pck", SessionID: "sid"}
	if got := wsBaselineSessionKey(opts); got != "pck" {
		t.Errorf("session key = %q, want pck", got)
	}
	if got := wsBaselineSessionKey(provider.StreamOptions{SessionID: "sid"}); got != "sid" {
		t.Errorf("fallback key = %q, want sid", got)
	}
	if got := wsBaselineSessionKey(provider.StreamOptions{}); got != "" {
		t.Errorf("empty key = %q, want empty", got)
	}
}

func TestBaselineEmptySessionKeySkipped(t *testing.T) {
	resetWSBaselines()
	defer resetWSBaselines()
	completed := `{"type":"response.completed","response":{"id":"resp-e","status":"completed","output":[]}}`
	stream := provider.NewAssistantMessageEventStream(8)
	parseSync(stream, func() {
		parseResponsesSSEWithBaseline(sseBody(completed), stream, "", []provider.Message{provider.NewUserMessage("x")}, requestFingerprint{})
	})
	wsBaselines.mu.Lock()
	n := len(wsBaselines.bySession)
	wsBaselines.mu.Unlock()
	if n != 0 {
		t.Errorf("registry should stay empty for empty session key, has %d", n)
	}
}
