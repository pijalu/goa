// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

func TestNewConsoleObserverDefaults(t *testing.T) {
	obs := NewConsoleObserver()
	if obs == nil {
		t.Fatal("NewConsoleObserver returned nil")
	}
	if obs.writer == nil {
		t.Fatal("default writer should not be nil")
	}
	if obs.format == nil {
		t.Fatal("default format should not be nil")
	}
}

func TestConsoleObserver_Content(t *testing.T) {
	var buf bytes.Buffer
	obs := NewConsoleObserver(WithConsoleWriter(&buf))

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "Hello"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	got := buf.String()
	if !strings.Contains(got, "[content]") {
		t.Errorf("missing [content] prefix: %q", got)
	}
	if !strings.Contains(got, "Hello") {
		t.Errorf("missing content: %q", got)
	}
	if strings.Count(got, "[content]") != 1 {
		t.Errorf("[content] should appear exactly once: %q", got)
	}
}

func TestConsoleObserver_StreamingContent(t *testing.T) {
	var buf bytes.Buffer
	obs := NewConsoleObserver(WithConsoleWriter(&buf))

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "The "})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "answer "})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "is 27"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	got := buf.String()
	if strings.Count(got, "[content]") != 1 {
		t.Errorf("[content] should appear exactly once: %q", got)
	}
	if !strings.Contains(got, "The answer is 27") {
		t.Errorf("missing streaming content: %q", got)
	}
}

func TestConsoleObserver_ThinkingToContent(t *testing.T) {
	var buf bytes.Buffer
	obs := NewConsoleObserver(WithConsoleWriter(&buf))

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "Let me think..."})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "Result: 42"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	got := buf.String()
	if !strings.Contains(got, "[thinking]") {
		t.Errorf("missing [thinking]: %q", got)
	}
	if !strings.Contains(got, "[content]") {
		t.Errorf("missing [content]: %q", got)
	}
	if !strings.Contains(got, "Let me think...") {
		t.Errorf("missing thinking text: %q", got)
	}
	if !strings.Contains(got, "Result: 42") {
		t.Errorf("missing content text: %q", got)
	}
}

func TestConsoleObserver_ToolCall(t *testing.T) {
	var buf bytes.Buffer
	obs := NewConsoleObserver(WithConsoleWriter(&buf))

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateToolCall})
	obs.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		ToolName:   "calculator",
		ToolInput:  `{"a":10,"b":5,"op":"+"}`,
		ToolCallID: "call_123",
	})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	got := buf.String()
	if !strings.Contains(got, "[tool_call] calculator") {
		t.Errorf("missing tool call: %q", got)
	}
	if !strings.Contains(got, `input: {"a":10,"b":5,"op":"+"}`) {
		t.Errorf("missing tool input: %q", got)
	}
	if !strings.Contains(got, "call_id: call_123") {
		t.Errorf("missing call_id: %q", got)
	}
}

func TestConsoleObserver_ToolResult(t *testing.T) {
	var buf bytes.Buffer
	obs := NewConsoleObserver(WithConsoleWriter(&buf))

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateToolResult})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult, Text: "15"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	got := buf.String()
	if !strings.Contains(got, "[tool_result]") {
		t.Errorf("missing [tool_result]: %q", got)
	}
	if !strings.Contains(got, "15") {
		t.Errorf("missing result: %q", got)
	}
}

func TestConsoleObserver_End(t *testing.T) {
	var buf bytes.Buffer
	obs := NewConsoleObserver(WithConsoleWriter(&buf))

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "Done"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventEnd})

	got := buf.String()
	if !strings.Contains(got, "[end]") {
		t.Errorf("missing [end]: %q", got)
	}
}

func TestConsoleObserver_ContentToToolCallToContent(t *testing.T) {
	var buf bytes.Buffer
	obs := NewConsoleObserver(WithConsoleWriter(&buf))

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "Let me calculate"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateToolCall})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "calculator", ToolInput: `{}`})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "Result: 15"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	got := buf.String()
	if strings.Count(got, "[content]") != 2 {
		t.Errorf("[content] appeared %d times, want 2: %q", strings.Count(got, "[content]"), got)
	}
	if !strings.Contains(got, "[tool_call] calculator") {
		t.Errorf("missing tool_call: %q", got)
	}
}

func TestConsoleObserver_CustomFormat(t *testing.T) {
	var buf bytes.Buffer
	customFormat := func(state agentic.OutputState) string {
		if state == agentic.StateContent {
			return ">> "
		}
		return ""
	}
	obs := NewConsoleObserver(WithConsoleWriter(&buf), WithConsoleFormat(customFormat))

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "hello"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	got := buf.String()
	if !strings.HasPrefix(got, ">> hello") {
		t.Errorf("custom format not applied: %q", got)
	}
	if strings.Contains(got, "[content]") {
		t.Errorf("default prefix should not appear: %q", got)
	}
}

func TestConsoleObserver_Clear(t *testing.T) {
	var buf bytes.Buffer
	obs := NewConsoleObserver(WithConsoleWriter(&buf))

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "Hello"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventClear})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "New"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	got := buf.String()
	if !strings.Contains(got, "Hello") {
		t.Errorf("missing first content: %q", got)
	}
	if !strings.Contains(got, "New") {
		t.Errorf("missing second content: %q", got)
	}
}

func TestConsoleObserver_TokenStats(t *testing.T) {
	var buf bytes.Buffer
	obs := NewConsoleObserver(WithConsoleWriter(&buf))

	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{
			PromptN:            18,
			PredictedN:         62,
			PromptMs:           459.502,
			PredictedMs:        1444.654,
			PredictedPerSecond: 42.91,
		},
	})

	got := buf.String()
	if !strings.Contains(got, "[stats]") {
		t.Errorf("missing [stats] prefix: %q", got)
	}
	if !strings.Contains(got, "80 tokens") {
		t.Errorf("missing total tokens (80): %q", got)
	}
	if !strings.Contains(got, "42.91 t/s") {
		t.Errorf("missing speed: %q", got)
	}
}

func TestConsoleObserver_Progress(t *testing.T) {
	var buf bytes.Buffer
	obs := NewConsoleObserver(WithConsoleWriter(&buf))

	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventProgress,
		PromptProgress: &agentic.PromptProgress{
			Total:     24,
			Cache:     6,
			Processed: 12,
			TimeMs:    399,
		},
	})

	got := buf.String()
	if !strings.Contains(got, "[progress]") {
		t.Errorf("missing [progress] prefix: %q", got)
	}
	if !strings.Contains(got, "12/24 processed") {
		t.Errorf("missing progress count: %q", got)
	}
	if !strings.Contains(got, "cache: 6") {
		t.Errorf("missing cache info: %q", got)
	}
}

func TestConsoleObserver_TokenStatsWithContent(t *testing.T) {
	var buf bytes.Buffer
	obs := NewConsoleObserver(WithConsoleWriter(&buf))

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "Hello"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{
			PromptN:            10,
			PredictedN:         5,
			PredictedPerSecond: 25.0,
		},
	})

	got := buf.String()
	if !strings.Contains(got, "[content]") {
		t.Errorf("missing [content]: %q", got)
	}
	if !strings.Contains(got, "[stats]") {
		t.Errorf("missing [stats]: %q", got)
	}
	if !strings.Contains(got, "15 tokens") {
		t.Errorf("missing token count: %q", got)
	}
}
