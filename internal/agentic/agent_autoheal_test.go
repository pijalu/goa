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

func TestAutoHealToolCalls_EmitsDecodingProgress(t *testing.T) {
	events := []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "I will call the terminal tool.\n<tool_call>{"},
		{Type: provider.EventTextDelta, Delta: `"name":"terminal","arguments":{"command":"echo hello"}}`},
		{Type: provider.EventTextDelta, Delta: `</tool_call>`},
	}
	p := registerTestProvider("autoheal-progress", events)
	mdl := testModel(p.api)

	tool := &autoHealMockTool{
		name: "terminal",
		exec: func(input string) (string, error) { return "hello", nil },
	}

	agent := NewAgent(Config{
		Model:             mdl,
		SystemPrompt:      "test",
		Tools:             []Tool{tool},
		AutoHealToolCalls: true,
	})

	var progress []string
	agent.AddObserver(OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventProgress && ev.Text != "" {
			progress = append(progress, ev.Text)
		}
	}))

	_, err := agent.RunAndCollect(context.Background(), "run echo")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	found := false
	for _, text := range progress {
		if strings.Contains(text, "Decoding tool calls") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected progress event with 'Decoding tool calls', got %q", progress)
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

// TestDSMLToolCallRecoveredWithAutoHealOff pins the 2026-08-16 regression:
// deepseek-v4-flash, on a tool_choice:"none" collapse round, emitted its
// native DSML tool-call markup as text. With AutoHealToolCalls disabled (the
// default) the call was silently dropped and the raw markup surfaced to the
// user. DSML is a first-class provider format and must be recovered even when
// the generic XML auto-heal opt-in is off.
func TestDSMLToolCallRecoveredWithAutoHealOff(t *testing.T) {
	events := []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Queue cleared. Now recreate merged batch.\n<｜｜DSML｜｜tool_calls>\n"},
		{Type: provider.EventTextDelta, Delta: `<｜｜DSML｜｜invoke name="terminal">` + "\n"},
		{Type: provider.EventTextDelta, Delta: `<｜｜DSML｜｜parameter name="command" string="true">echo hello</｜｜DSML｜｜parameter>` + "\n"},
		{Type: provider.EventTextDelta, Delta: "</｜｜DSML｜｜invoke>\n</｜｜DSML｜｜tool_calls>"},
	}
	p := registerTestProvider("dsml-noautoheal", events)
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
		Model:        mdl,
		SystemPrompt: "test",
		Tools:        []Tool{tool},
		// AutoHealToolCalls deliberately left false: DSML must not need it.
	})

	out, err := agent.RunAndCollect(context.Background(), "recreate goals")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("DSML tool call was dropped with auto-heal off (must be recovered)")
	}
	if strings.Contains(out, "DSML") {
		t.Errorf("DSML markup leaked into user-visible output: %q", out)
	}
}

// TestInvokeToolCallRecoveredWithAutoHealOn is the regression test for the
// 2026-08-19 export (goa-export-20260819-004622): a GLM turn degraded
// mid-sentence and emitted the next tool call as plain content in the
// Anthropic-legacy <invoke name=...>/<parameter name=...> dialect. With
// auto-heal enabled the call must be recovered and executed like any other
// text dialect; the markup must not leak into user-visible output.
func TestInvokeToolCallRecoveredWithAutoHealOn(t *testing.T) {
	events := []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Creating the goal: learance"},
		{Type: provider.EventTextDelta, Delta: "<invoke name=\"terminal\">\n"},
		{Type: provider.EventTextDelta, Delta: "<parameter name=\"command\">echo hello</parameter>\n"},
		{Type: provider.EventTextDelta, Delta: "</invoke>"},
	}
	p := registerTestProvider("invoke-autoheal", events)
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

	out, err := agent.RunAndCollect(context.Background(), "create goal")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("invoke-dialect tool call was not recovered with auto-heal on")
	}
	if strings.Contains(out, "<invoke") {
		t.Errorf("invoke markup leaked into user-visible output: %q", out)
	}
}

// TestInvokeToolCallWarnsWithAutoHealOff pins the non-silent failure mode:
// recovery disabled (the default) + a closed invoke block naming a REGISTERED
// tool + no native call superseding it → the agent must warn instead of
// silently rendering the call as content (the export's silent-loss shape).
func TestInvokeToolCallWarnsWithAutoHealOff(t *testing.T) {
	events := []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Summary text. <invoke name=\"terminal\">\n"},
		{Type: provider.EventTextDelta, Delta: "<parameter name=\"command\">echo hello</parameter>\n"},
		{Type: provider.EventTextDelta, Delta: "</invoke>"},
	}
	p := registerTestProvider("invoke-noautoheal", events)
	mdl := testModel(p.api)

	called := false
	var warnings []string
	tool := &autoHealMockTool{
		name: "terminal",
		exec: func(input string) (string, error) { called = true; return "hello", nil },
	}

	agent := NewAgent(Config{
		Model:        mdl,
		SystemPrompt: "test",
		Tools:        []Tool{tool},
		// AutoHealToolCalls deliberately false: the invoke dialect is a
		// malformed-fallback shape, recovered only on opt-in (unlike DSML).
	})
	agent.AddObserver(OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventProgress && strings.Contains(ev.Text, "was NOT executed") {
			warnings = append(warnings, ev.Text)
		}
	}))

	_, err := agent.RunAndCollect(context.Background(), "run")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Fatal("tool must NOT execute with auto-heal off")
	}
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 unrecovered-call warning, got %d (%v)", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "terminal") {
		t.Errorf("warning must name the tool: %q", warnings[0])
	}
}
