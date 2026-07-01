package agentic

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestHandleThinkingDelta_PreservesLeadingSpaces(t *testing.T) {
	a := NewAgent(Config{})

	var got []string
	a.AddObserver(OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventContent && ev.State == StateThinking && ev.Text != "" {
			got = append(got, ev.Text)
		}
	}))

	deltas := []string{"The", " user", " is", " asking"}
	for _, d := range deltas {
		a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: d})
	}

	want := []string{"The", " user", " is", " asking"}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHandleThinkingDelta_ConcatenatedBufferHasSpaces(t *testing.T) {
	a := NewAgent(Config{})

	var full string
	a.AddObserver(OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventContent && ev.State == StateThinking && ev.Text != "" {
			full += ev.Text
		}
	}))

	deltas := []string{"The", " user", " is", " asking", "."}
	for _, d := range deltas {
		a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: d})
	}

	want := "The user is asking."
	if full != want {
		t.Errorf("concatenated thinking = %q, want %q", full, want)
	}
}

func TestHandleThinkingDelta_SuppressesToolXML(t *testing.T) {
	a := NewAgent(Config{})

	var got []string
	a.AddObserver(OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventContent && ev.State == StateThinking && ev.Text != "" {
			got = append(got, ev.Text)
		}
	}))

	deltas := []string{"Plan: use ", "<tool_call>", `{"name":"x"}`, "</tool_call>", " to fix"}
	for _, d := range deltas {
		a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: d})
	}

	if len(got) == 0 {
		t.Fatal("expected at least one emitted thinking event")
	}
	for _, text := range got {
		if strings.Contains(text, "<tool_call>") || strings.Contains(text, "</tool_call>") {
			t.Errorf("emitted thinking still contains tool markup: %q", text)
		}
	}
}
