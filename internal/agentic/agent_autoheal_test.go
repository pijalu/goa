// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestAutoHealToolCalls(t *testing.T) {
	events := []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "I will call the terminal tool.\n<tool_call>{"},
		{Type: provider.EventTextDelta, Delta: `"name":"terminal","arguments":{"command":"echo hello"}}`},
		{Type: provider.EventTextDelta, Delta: `</tool_call>`},
	}
	p := registerTestProvider("autoheal", events)
	mdl := testModel(p.api)

	called := false
	tool := &autoHealMockTool{
		name: "terminal",
		exec: func(input string) (string, error) {
			called = true
			if input != `{"command":"echo hello"}` {
				t.Errorf("unexpected input: %q", input)
			}
			return "hello", nil
		},
	}

	agent := NewAgent(Config{
		Model:             mdl,
		SystemPrompt:      "test",
		Tools:             []Tool{tool},
		AutoHealToolCalls: true,
	})

	_, err := agent.RunAndCollect(context.Background(), "run echo")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("terminal tool was not executed via auto-heal")
	}
}

func TestAutoHealToolCalls_FromThinkingBuffer(t *testing.T) {
	events := []provider.AssistantMessageEvent{
		{Type: provider.EventThinkingDelta, Delta: "Let me read the docs.\n"},
		{Type: provider.EventThinkingDelta, Delta: `<function=read>`},
		{Type: provider.EventThinkingDelta, Delta: `<parameter=path>docs/COMMANDS.md</parameter>`},
		{Type: provider.EventThinkingDelta, Delta: `</function>`},
	}
	p := registerTestProvider("autoheal-thinking", events)
	mdl := testModel(p.api)

	called := false
	tool := &autoHealMockTool{
		name: "read",
		exec: func(input string) (string, error) {
			called = true
			if input != `{"path":"docs/COMMANDS.md"}` {
				t.Errorf("unexpected input: %q", input)
			}
			return "commands docs", nil
		},
	}

	agent := NewAgent(Config{
		Model:             mdl,
		SystemPrompt:      "test",
		Tools:             []Tool{tool},
		AutoHealToolCalls: true,
	})

	_, err := agent.RunAndCollect(context.Background(), "summarize project")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("read tool was not executed via auto-heal from thinking buffer")
	}

	history := agent.GetHistory()
	for _, m := range history {
		if m.Role == Assistant {
			if strings.Contains(m.Content, "<function=") {
				t.Errorf("assistant history still contains raw tool-call XML: %q", m.Content)
			}
		}
	}
}

func TestAutoHealToolCalls_ThinkingStreamStripsClosedXML(t *testing.T) {
	events := []provider.AssistantMessageEvent{
		{Type: provider.EventThinkingDelta, Delta: "Let me read the docs.\n"},
		{Type: provider.EventThinkingDelta, Delta: `<function=read><parameter=path>docs/COMMANDS.md</parameter></function>`},
	}
	p := registerTestProvider("autoheal-thinking-strip", events)
	mdl := testModel(p.api)

	agent := NewAgent(Config{
		Model:             mdl,
		SystemPrompt:      "test",
		Tools:             []Tool{&autoHealMockTool{name: "read", exec: func(string) (string, error) { return "ok", nil }}},
		AutoHealToolCalls: true,
	})

	var thinkingEvents []string
	agent.AddObserver(OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventContent && ev.State == StateThinking {
			thinkingEvents = append(thinkingEvents, ev.Text)
		}
	}))

	_, err := agent.RunAndCollect(context.Background(), "summarize project")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, text := range thinkingEvents {
		if strings.Contains(text, "<function=") || strings.Contains(text, "<parameter=") {
			t.Errorf("thinking event emitted raw tool-call XML: %q", text)
		}
	}
}

func TestAutoHealToolCalls_ThinkingStreamStripsMultiLineXML(t *testing.T) {
	events := []provider.AssistantMessageEvent{
		{Type: provider.EventThinkingDelta, Delta: "Let me read the files.\n"},
		{Type: provider.EventThinkingDelta, Delta: "<tool_call>\n"},
		{Type: provider.EventThinkingDelta, Delta: "<function=read>\n"},
		{Type: provider.EventThinkingDelta, Delta: "<parameter=path>\n"},
		{Type: provider.EventThinkingDelta, Delta: "gf/presto2/src/main.ts\n"},
		{Type: provider.EventThinkingDelta, Delta: "</parameter>\n"},
		{Type: provider.EventThinkingDelta, Delta: "</function>\n"},
		{Type: provider.EventThinkingDelta, Delta: "</tool_call>\n"},
		{Type: provider.EventThinkingDelta, Delta: "Now compare them."},
	}
	p := registerTestProvider("autoheal-thinking-multiline", events)
	mdl := testModel(p.api)

	agent := NewAgent(Config{
		Model:             mdl,
		SystemPrompt:      "test",
		Tools:             []Tool{&autoHealMockTool{name: "read", exec: func(string) (string, error) { return "ok", nil }}},
		AutoHealToolCalls: true,
	})

	var thinkingEvents []string
	agent.AddObserver(OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventContent && ev.State == StateThinking {
			thinkingEvents = append(thinkingEvents, ev.Text)
		}
	}))

	_, err := agent.RunAndCollect(context.Background(), "summarize project")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var raw strings.Builder
	for _, text := range thinkingEvents {
		raw.WriteString(text)
	}
	combined := raw.String()
	if strings.Contains(combined, "<tool_call>") || strings.Contains(combined, "<function=") || strings.Contains(combined, "<parameter=") {
		t.Errorf("thinking event emitted raw multi-line tool-call XML: %q", combined)
	}
	if !strings.Contains(combined, "Let me read the files.") || !strings.Contains(combined, "Now compare them.") {
		t.Errorf("thinking event was over-stripped: %q", combined)
	}
}

type autoHealMockTool struct {
	name string
	exec func(string) (string, error)
}

func (m *autoHealMockTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        m.name,
		Description: "mock tool",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (m *autoHealMockTool) Execute(input string) (string, error) {
	return m.exec(input)
}

func (m *autoHealMockTool) IsRetryable(err error) bool { return false }
