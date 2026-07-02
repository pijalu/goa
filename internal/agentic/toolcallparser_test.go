// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
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
