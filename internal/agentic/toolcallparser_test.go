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
