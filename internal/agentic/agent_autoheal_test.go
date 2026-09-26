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

// --- whitespace DSML dialect: recovery + never-silent reporting -------------
//
// Export goa-export-20260926-101445: a model emitted its tool call as TEXT in a
// whitespace variant of the DSML dialect ("<｜｜DSML｜｜ invoke name=...>"). Every
// recognizer required the canonical spelling, so the call was neither detected
// nor parsed, healing being ON produced no diagnostics, the markup was shown as
// the answer and the turn just ended ("unexpected stop").

// whitespaceDSMLEvents replays the observed delta sequence: the marker arrives
// split across deltas with a space before the keyword.
func whitespaceDSMLEvents(toolName string) []provider.AssistantMessageEvent {
	return []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Let me create it.\n<｜｜DSML｜｜ invoke name=\"" + toolName + "\">\n"},
		{Type: provider.EventTextDelta, Delta: `<｜｜DSML｜｜ parameter name="command" string="true">echo hello</｜｜DSML｜｜ parameter>` + "\n"},
		{Type: provider.EventTextDelta, Delta: "</｜｜DSML｜｜ invoke>\n</｜｜DSML｜｜"},
	}
}

// TestAutoHeal_WhitespaceDSMLRecoveredWithAutoHealOff pins the export fix: the
// whitespace dialect is recovered by the DSML path (which never needs the
// generic auto-heal opt-in), the tool executes, and the markup never reaches
// the user-visible answer.
func TestAutoHeal_WhitespaceDSMLRecoveredWithAutoHealOff(t *testing.T) {
	p := registerTestProvider("dsml-whitespace-noautoheal", whitespaceDSMLEvents("terminal"))
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
		// AutoHealToolCalls deliberately false: DSML must not need it.
	})

	out, err := agent.RunAndCollect(context.Background(), "create it")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("whitespace DSML tool call was dropped (must be recovered)")
	}
	if strings.Contains(out, "DSML") || strings.Contains(out, "invoke name") {
		t.Errorf("tool markup leaked into user-visible output: %q", out)
	}
}

// TestAutoHeal_WhitespaceDSMLRecoveredWithAutoHealOn covers the export's actual
// configuration (tool call fixing ON) end to end.
func TestAutoHeal_WhitespaceDSMLRecoveredWithAutoHealOn(t *testing.T) {
	p := registerTestProvider("dsml-whitespace-autoheal", whitespaceDSMLEvents("terminal"))
	mdl := testModel(p.api)

	called := false
	tool := &autoHealMockTool{
		name: "terminal",
		exec: func(input string) (string, error) { called = true; return "hello", nil },
	}

	agent := NewAgent(Config{
		Model:             mdl,
		SystemPrompt:      "test",
		Tools:             []Tool{tool},
		AutoHealToolCalls: true,
	})

	if _, err := agent.RunAndCollect(context.Background(), "create it"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("whitespace DSML tool call was dropped with auto-heal on")
	}
}

// newReportTestAgent builds an agent with one registered tool and a content
// buffer holding the given text, for direct reportUnrecoveredTextToolCall
// tests (the heal path itself is covered by the end-to-end tests above).
func newReportTestAgent(t *testing.T, name string, autoHeal bool, buffered string) (*Agent, *mockEventObserver) {
	t.Helper()
	p := registerTestProvider("report-"+name, nil)
	agent := NewAgent(Config{
		Model:             testModel(p.api),
		SystemPrompt:      "test",
		Tools:             []Tool{&autoHealMockTool{name: name, exec: func(string) (string, error) { return "", nil }}},
		AutoHealToolCalls: autoHeal,
	})
	agent.contentBuf.Reset()
	agent.contentBuf.WriteString(buffered)
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	return agent, obs
}

func reportProgress(obs *mockEventObserver) []string {
	var out []string
	for _, e := range obs.Events() {
		if e.Type == EventProgress && strings.Contains(e.Text, "was NOT executed") {
			out = append(out, e.Text)
		}
	}
	return out
}

func guidanceInHistory(a *Agent) string {
	for _, m := range a.GetHistory() {
		if m.Role == System && strings.Contains(m.Content, "Re-issue that call now as a native tool call") {
			return m.Content
		}
	}
	return ""
}

// exportWhitespaceDSMLTool is the tool named by exportWhitespaceDSML.
const exportWhitespaceDSMLTool = "goal"

// TestUnrecoveredInvokeCall_WarnsWithHealingDisabled: the operator-visible notice
// must tell them which switch recovers the call.
func TestUnrecoveredInvokeCall_WarnsWithHealingDisabled(t *testing.T) {
	agent, obs := newReportTestAgent(t, exportWhitespaceDSMLTool, false, exportWhitespaceDSML)
	agent.reportUnrecoveredTextToolCall(agent.contentBuf.String(), agent.contentBuf.String(), "")

	warnings := reportProgress(obs)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d (%v)", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], exportWhitespaceDSMLTool) {
		t.Errorf("warning must name the tool: %q", warnings[0])
	}
	if !strings.Contains(warnings[0], "enable auto_heal_tool_calls") {
		t.Errorf("healing-off warning must point at the switch: %q", warnings[0])
	}
}

// TestUnrecoveredInvokeCall_WarnsWithHealingOn is the export regression: with
// healing ON (the shipped user config) a call that could not be reconstructed
// must STILL be reported — the old code returned silently in exactly this case.
func TestUnrecoveredInvokeCall_WarnsWithHealingOn(t *testing.T) {
	agent, obs := newReportTestAgent(t, exportWhitespaceDSMLTool, true, exportWhitespaceDSML)
	agent.reportUnrecoveredTextToolCall(agent.contentBuf.String(), agent.contentBuf.String(), "")

	warnings := reportProgress(obs)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning with healing on, got %d (%v)", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "could not reconstruct") {
		t.Errorf("healing-on wording must explain recovery failed: %q", warnings[0])
	}
}

// TestUnrecoveredInvokeCall_GuidesModelToReissue: the model must be told to
// re-issue the call natively, otherwise the turn ends on raw markup with no way
// forward.
func TestUnrecoveredInvokeCall_GuidesModelToReissue(t *testing.T) {
	agent, _ := newReportTestAgent(t, exportWhitespaceDSMLTool, true, exportWhitespaceDSML)
	agent.reportUnrecoveredTextToolCall(agent.contentBuf.String(), agent.contentBuf.String(), "")

	guidance := guidanceInHistory(agent)
	if guidance == "" {
		t.Fatal("no re-issue guidance injected into the conversation history")
	}
	if !strings.Contains(guidance, exportWhitespaceDSMLTool) {
		t.Errorf("guidance must name the dropped tool: %q", guidance)
	}
}

// TestUnrecoveredInvokeCall_StripsMarkupFromAnswer: unrecovered markup must
// not survive into the finalized assistant message.
func TestUnrecoveredInvokeCall_StripsMarkupFromAnswer(t *testing.T) {
	agent, _ := newReportTestAgent(t, exportWhitespaceDSMLTool, true, exportWhitespaceDSML)
	content := agent.contentBuf.String()
	agent.reportUnrecoveredTextToolCall(content, content, "")

	if got := agent.contentBuf.String(); strings.Contains(got, "DSML") {
		t.Errorf("markup left in the answer buffer: %q", got)
	}
}

// TestUnrecoveredInvokeCall_ReportsOncePerTurn keeps the notice from
// repeating on every recovery round.
func TestUnrecoveredInvokeCall_ReportsOncePerTurn(t *testing.T) {
	agent, obs := newReportTestAgent(t, exportWhitespaceDSMLTool, false, exportWhitespaceDSML)
	content := agent.contentBuf.String()
	agent.reportUnrecoveredTextToolCall(content, content, "")
	agent.reportUnrecoveredTextToolCall(content, content, "")

	if n := len(reportProgress(obs)); n != 1 {
		t.Fatalf("expected 1 warning for two rounds, got %d", n)
	}
}

// TestUnrecoveredInvokeCall_SilentOnProse: prose discussing the markup, or a
// block naming an unregistered tool, must not produce a warning.
func TestUnrecoveredInvokeCall_SilentOnProse(t *testing.T) {
	prose := "The model wrote <｜｜DSML｜｜ invoke name=\"nonexistent\"> in its reply."
	agent, obs := newReportTestAgent(t, "terminal", true, prose)
	agent.reportUnrecoveredTextToolCall(prose, prose, "")

	if n := len(reportProgress(obs)); n != 0 {
		t.Fatalf("prose/unregistered tool must stay quiet, got %d warnings", n)
	}
}

// TestFinalize_StripsOrphanDSMLMarkup: when a text tool call cannot be
// recovered, its markup must not be finalized as the assistant's answer — the
// user gets the notice instead of a wall of XML (the "unexpected stop" shape
// from export goa-export-20260926-101445). The Anthropic-legacy invoke dialect
// is used here because it is deliberately opt-in (auto-heal OFF => not
// recovered), which forces the report path; the pre-fix code left the markup in
// the output because its warning path never stripped the buffers.
func TestFinalize_StripsOrphanDSMLMarkup(t *testing.T) {
	events := []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Working on it.\n<invoke name=\"terminal\">\n"},
		{Type: provider.EventTextDelta, Delta: "<parameter name=\"command\">echo hi</parameter>\n</invoke>"},
	}
	p := registerTestProvider("orphan-invoke", events)
	mdl := testModel(p.api)

	called := false
	tool := &autoHealMockTool{name: "terminal", exec: func(string) (string, error) { called = true; return "hi", nil }}
	agent := NewAgent(Config{
		Model:        mdl,
		SystemPrompt: "test",
		Tools:        []Tool{tool},
		// Healing off: the invoke dialect is not recovered without the opt-in.
	})
	var warned bool
	agent.AddObserver(OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventProgress && strings.Contains(ev.Text, "was NOT executed") {
			warned = true
		}
	}))

	out, err := agent.RunAndCollect(context.Background(), "run")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Fatal("invoke dialect must not execute with healing off")
	}
	if !warned {
		t.Fatal("unrecovered call must be reported (never a silent drop)")
	}
	if strings.Contains(out, "invoke name") || strings.Contains(out, "parameter name") {
		t.Errorf("orphan tool markup finalized as the answer: %q", out)
	}
	if !strings.Contains(out, "Working on it.") {
		t.Errorf("surrounding answer text lost: %q", out)
	}
}
