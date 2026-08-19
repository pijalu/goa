// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseToolCallsJSON(t *testing.T) {
	content := `<tool_call>{"name":"terminal","arguments":{"command":"ls"}}</tool_call>`
	calls := parseToolCallsFromText(content, 0, true)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].name != "terminal" {
		t.Errorf("name = %q, want terminal", calls[0].name)
	}
	if !strings.Contains(calls[0].arguments, `"command":"ls"`) {
		t.Errorf("arguments = %q", calls[0].arguments)
	}
}

func TestParseToolCallsIncomplete(t *testing.T) {
	content := `<tool_call>{"name":"terminal","arguments":{"command":"ls"}}`
	calls := parseToolCallsFromText(content, 0, true)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
}

func TestParseToolCallsFunctionForm(t *testing.T) {
	content := `<function=terminal><parameter=command>ls -la</parameter></function>`
	calls := parseToolCallsFromText(content, 0, true)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].name != "terminal" {
		t.Errorf("name = %q, want terminal", calls[0].name)
	}
	if !strings.Contains(calls[0].arguments, `"command":"ls -la"`) {
		t.Errorf("arguments = %q", calls[0].arguments)
	}
}

func TestParseToolCallsFunctionFormIncomplete(t *testing.T) {
	content := `<function=terminal><parameter=command>ls -la`
	calls := parseToolCallsFromText(content, 0, true)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
}

func TestStripToolMarkup(t *testing.T) {
	content := `Hello <tool_call>{"name":"x"}</tool_call> world`
	got := stripToolMarkup(content, true)
	if got != "Hello  world" {
		t.Errorf("stripToolMarkup = %q", got)
	}
}

func TestStripToolMarkup_PreservesLeadingAndTrailingSpaces(t *testing.T) {
	content := `  Hello <tool_call>{"name":"x"}</tool_call> world  `
	got := stripToolMarkup(content, true)
	want := "  Hello  world  "
	if got != want {
		t.Errorf("stripToolMarkup = %q, want %q", got, want)
	}
}

func TestHasToolSignal(t *testing.T) {
	if !hasToolSignal(`foo <tool_call>{`) {
		t.Error("expected signal for tool_call")
	}
	if !hasToolSignal(`foo <function=`) {
		t.Error("expected signal for function=")
	}
	if hasToolSignal(`foo bar`) {
		t.Error("unexpected signal")
	}
}

func TestParseToolCallsInvokeForm(t *testing.T) {
	// Real newlines, exactly as emitted on the wire (export shape).
	content := "<invoke name=\"terminal\">\n" +
		"<parameter name=\"command\">ls -la</parameter>\n" +
		"</invoke>"
	calls := parseToolCallsFromText(content, 0, true)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].name != "terminal" {
		t.Errorf("name = %q, want terminal", calls[0].name)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(calls[0].arguments), &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v", err)
	}
	if args["command"] != "ls -la" {
		t.Errorf("command = %q, want ls -la", args["command"])
	}
}

func TestParseToolCallsInvokeFormIncomplete(t *testing.T) {
	// Streaming cut before the close tag: recoverable under allowIncomplete.
	content := `<invoke name="terminal">\n<parameter name="command">ls -la`
	calls := parseToolCallsFromText(content, 0, true)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].name != "terminal" {
		t.Errorf("name = %q, want terminal", calls[0].name)
	}
}

func TestParseToolCallsInvokeGarbagePrefix(t *testing.T) {
	// The exact incident shape (export goa-export-20260819-004622): the model
	// degraded mid-sentence and emitted the invoke block as plain content.
	content := "continuing with the CM display: learance<invoke name=\"goal\">\n" +
		"<parameter name=\"action\">create</parameter>\n</invoke>"
	calls := parseToolCallsFromText(content, 0, true)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].name != "goal" {
		t.Errorf("name = %q, want goal", calls[0].name)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(calls[0].arguments), &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v", err)
	}
	if args["action"] != "create" {
		t.Errorf("action = %q, want create", args["action"])
	}
}

func TestParseToolCalls_MultipleInvokes(t *testing.T) {
	content := `<invoke name="bash">\n<parameter name="command">ls</parameter>\n</invoke>\n` +
		`<invoke name="read">\n<parameter name="path">x.go</parameter>\n</invoke>`
	calls := parseToolCallsFromText(content, 0, true)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].name != "bash" || calls[1].name != "read" {
		t.Errorf("names = %q,%q; want bash,read", calls[0].name, calls[1].name)
	}
}

func TestParseToolCallsInvokeRegressionFromExport(t *testing.T) {
	// Shape-faithful replay of the lost goal-create call from export
	// goa-export-20260819-004622: four parameters, long single-line values,
	// close tag on its own line. The parser must recover name + all four
	// parameters with values intact.
	// Real newlines, exactly as emitted on the wire (export shape).
	content := "<invoke name=\"goal\">\n" +
		"<parameter name=\"action\">create</parameter>\n" +
		"<parameter name=\"completionCriterion\">internal/app/stats.go splits tokenCacheMisses into full/partial counters + missed-token total; renderers updated; tests green; committed.</parameter>\n" +
		"<parameter name=\"handover\">bugs.md TODO entry has the full fix plan. Sites: stats.go:1058, 142, 319, 739, 1060, 1132, 1464.</parameter>\n" +
		"<parameter name=\"objective\">Fix bugs.md CM entry: split the CM counter (CM:X|Y rendering, red/orange, hide-when-zero).</parameter>\n" +
		"</invoke>"
	calls := parseToolCallsFromText(content, 0, true)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].name != "goal" {
		t.Errorf("name = %q, want goal", calls[0].name)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(calls[0].arguments), &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v", err)
	}
	for _, k := range []string{"action", "completionCriterion", "handover", "objective"} {
		if _, ok := args[k]; !ok {
			t.Errorf("parameter %q missing from %v", k, args)
		}
	}
	if args["action"] != "create" {
		t.Errorf("action = %q, want create", args["action"])
	}
	want := "Fix bugs.md CM entry: split the CM counter (CM:X|Y rendering, red/orange, hide-when-zero)."
	if args["objective"] != want {
		t.Errorf("objective = %q\nwant      %q", args["objective"], want)
	}
}

func TestParseInvokeParametersEmptyBody(t *testing.T) {
	args, ok := parseInvokeParameters("\n")
	if !ok || args != "{}" {
		t.Errorf("empty body = (%q, %v), want ({}, true)", args, ok)
	}
}

func TestHasToolSignal_Invoke(t *testing.T) {
	if !hasToolSignal("text <invoke name=\"goal\"> more") {
		t.Error("<invoke name= signal not detected")
	}
	if hasToolSignal("plain <invocation of the daemon>") {
		t.Error("<invocation prose must not trip the signal")
	}
}

func TestStripToolMarkup_Invoke(t *testing.T) {
	closed := stripToolMarkup("before <invoke name=\"x\"><parameter name=\"k\">v</parameter></invoke> after", false)
	if closed != "before  after" {
		t.Errorf("closed strip = %q", closed)
	}
	incomplete := stripToolMarkup("before <invoke name=\"x\"><parameter name=\"k\">v", true)
	if incomplete != "before " {
		t.Errorf("incomplete strip = %q", incomplete)
	}
	orphan := stripToolMarkup("text </invoke> tail", true)
	if orphan != "text  tail" {
		t.Errorf("orphan closer strip = %q", orphan)
	}
}

func TestParseToolCalls_MultipleFunctionCalls(t *testing.T) {
	content := `<function=terminal><parameter=command>ls</parameter></function>` +
		`<function=read><parameter=path>/etc/hosts</parameter></function>`
	calls := parseToolCallsFromText(content, 0, false)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].name != "terminal" || calls[1].name != "read" {
		t.Errorf("names = %q, %q", calls[0].name, calls[1].name)
	}
	if calls[0].id != "call_0" || calls[1].id != "call_1" {
		t.Errorf("ids = %q, %q", calls[0].id, calls[1].id)
	}
}

func TestParseToolCalls_MultipleJSONCalls(t *testing.T) {
	content := `<tool_call>{"name":"terminal","arguments":{"command":"ls"}}</tool_call>` +
		`<tool_call>{"name":"read","arguments":{"path":"/x"}}</tool_call>`
	calls := parseToolCallsFromText(content, 0, false)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].name != "terminal" || calls[1].name != "read" {
		t.Errorf("names = %q, %q", calls[0].name, calls[1].name)
	}
}

// TestParseToolCalls_FunctionStartInsideParameterValueIsNotASeparateCall
// guards the regression that the old O(n²) insideOpenParameter check existed
// to prevent: a literal "<function=" embedded in a parameter value must not be
// extracted as a second tool call. The cursor-based scanner consumes the
// value wholesale so the embedded token is never re-examined.
func TestParseToolCalls_FunctionStartInsideParameterValueIsNotASeparateCall(t *testing.T) {
	content := `<function=terminal><parameter=command>echo "<function=evil>"</parameter></function>` +
		`<function=real><parameter=command>id</parameter></function>`
	calls := parseToolCallsFromText(content, 0, false)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls (terminal, real), got %d: %+v", len(calls), calls)
	}
	if calls[0].name != "terminal" {
		t.Errorf("calls[0].name = %q, want terminal", calls[0].name)
	}
	if calls[1].name != "real" {
		t.Errorf("calls[1].name = %q, want real", calls[1].name)
	}
	if !strings.Contains(calls[0].arguments, "function=evil") {
		t.Errorf("embedded token should remain in value, arguments=%q", calls[0].arguments)
	}
}

// TestParseToolCalls_CompleteFunctionRequiresClose verifies that without
// allowIncomplete a missing </function> makes the call invalid.
func TestParseToolCalls_CompleteFunctionRequiresClose(t *testing.T) {
	content := `<function=terminal><parameter=command>ls</parameter>`
	calls := parseToolCallsFromText(content, 0, false)
	if len(calls) != 0 {
		t.Fatalf("expected 0 calls for unclosed function, got %d", len(calls))
	}
}

// TestParseToolCalls_DSMLRecoveredUnconditionally reproduces the 2026-08-16
// deepseek-v4-flash export: on a tool_choice:"none" collapse round the model
// emitted its native DSML markup as text; with no parser the goal-create call
// was silently dropped. DSML must parse regardless of the auto-heal opt-in.
func TestParseToolCalls_DSMLRecoveredUnconditionally(t *testing.T) {
	content := `Queue cleared. Now recreate merged batch — 11 goals from 29.
<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="goal">
<｜｜DSML｜｜parameter name="action" string="true">create</｜｜DSML｜｜parameter>
<｜｜DSML｜｜parameter name="objectives" string="false">["P6.FTS-REST — snippet, ranking", "P6.JSON — JSON1 functions"]</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>`

	calls := parseDSMLToolCallsFromText(content, 0, true)
	if len(calls) != 1 {
		t.Fatalf("expected 1 DSML call, got %d: %+v", len(calls), calls)
	}
	if calls[0].name != "goal" {
		t.Errorf("name = %q, want goal", calls[0].name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].arguments), &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v (%q)", err, calls[0].arguments)
	}
	if args["action"] != "create" {
		t.Errorf("action = %v, want create", args["action"])
	}
	obj, _ := args["objectives"].(string)
	if !strings.Contains(obj, "P6.FTS-REST") || !strings.Contains(obj, "P6.JSON") {
		t.Errorf("objectives mangled: %q", obj)
	}
	// The JSON array value must survive verbatim (string="false").
	var arr []string
	if err := json.Unmarshal([]byte(obj), &arr); err != nil || len(arr) != 2 {
		t.Errorf("objectives should be a 2-element JSON array, got %q (err %v)", obj, err)
	}
}

// TestParseToolCalls_DSMLStripFromDisplay ensures DSML markup is removed from
// the user-visible text once recovered.
func TestParseToolCalls_DSMLStripFromDisplay(t *testing.T) {
	content := `Working. <｜｜DSML｜｜tool_calls><｜｜DSML｜｜invoke name="bash"><｜｜DSML｜｜parameter name="command" string="true">ls</｜｜DSML｜｜parameter></｜｜DSML｜｜invoke></｜｜DSML｜｜tool_calls>`
	stripped := stripToolMarkup(content, true)
	if strings.Contains(stripped, "DSML") {
		t.Errorf("DSML markup not stripped: %q", stripped)
	}
	if !strings.Contains(stripped, "Working.") {
		t.Errorf("surrounding text lost: %q", stripped)
	}
}

// TestParseToolCalls_DSMLIncompleteStreamTolerated verifies a truncated DSML
// block (stream cut mid-call) still yields the call under allowIncomplete.
func TestParseToolCalls_DSMLIncompleteStreamTolerated(t *testing.T) {
	content := `<｜｜DSML｜｜invoke name="read"><｜｜DSML｜｜parameter name="path" string="true">/tmp/x.go</｜｜DSML｜｜parameter>`
	calls := parseDSMLToolCallsFromText(content, 0, true)
	if len(calls) != 1 || calls[0].name != "read" {
		t.Fatalf("incomplete DSML not recovered: %+v", calls)
	}
}
