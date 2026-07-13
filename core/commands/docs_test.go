// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools"
)

func TestToolsDocCommand_ToggleCompletionStateAware(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.WebFetch = true
	ctx := core.Context{Config: cfg}
	cmd := &ToolsDocCommand{}

	vals := cmd.CompleteArgs(ctx, "webfetch:")
	if len(vals) != 1 || vals[0].Value != "webfetch:off" {
		t.Errorf("enabled tool: got %v, want [webfetch:off]", vals)
	}

	cfg.Tools.Enabled.WebFetch = false
	vals = cmd.CompleteArgs(ctx, "webfetch:")
	if len(vals) != 1 || vals[0].Value != "webfetch:on" {
		t.Errorf("disabled tool: got %v, want [webfetch:on]", vals)
	}

	vals = cmd.CompleteArgs(ctx, "webfetch:of")
	if len(vals) != 0 {
		t.Errorf("no-action prefix: got %v, want []", vals)
	}
}

func TestListDocs_WithProvider(t *testing.T) {
	w := newWriter()
	dp := &fakeDocsProvider{
		list: []core.DocInfo{
			{Name: "ARCHITECTURE", Description: "System architecture"},
			{Name: "COMMANDS", Description: "Commands reference"},
		},
	}

	err := listDocs(w, dp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "ARCHITECTURE") || !strings.Contains(text, "COMMANDS") {
		t.Errorf("expected docs listing, got: %s", text)
	}
	if !strings.Contains(text, "Goa Documentation") {
		t.Errorf("expected header, got: %s", text)
	}
}

func TestListBuiltinDocs(t *testing.T) {
	w := newWriter()
	err := listBuiltinDocs(w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	for _, name := range []string{"ARCHITECTURE", "COMMANDS", "TOOLS", "TUI"} {
		if !strings.Contains(text, name) {
			t.Errorf("expected %s in output, got: %s", name, text)
		}
	}
}

func TestShowDoc_Found(t *testing.T) {
	w := newWriter()
	dp := &fakeDocsProvider{
		list: []core.DocInfo{
			{Name: "ARCHITECTURE", Path: "docs/ARCHITECTURE.md"},
		},
	}

	err := showDoc(w, dp, "ARCHITECTURE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "content for ARCHITECTURE") {
		t.Errorf("expected doc content, got: %s", text)
	}
}

func TestShowDoc_NotFound(t *testing.T) {
	notFoundErr := fmt.Errorf("doc not found")
	w := newWriter()
	dp := &fakeDocsProvider{
		list:    []core.DocInfo{{Name: "ARCHITECTURE"}},
		findErr: notFoundErr,
	}

	err := showDoc(w, dp, "NONEXISTENT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "not found") {
		t.Errorf("expected not found message, got: %s", text)
	}
}

func TestShowBuiltinDoc_Known(t *testing.T) {
	w := newWriter()
	err := showBuiltinDoc(w, "ARCHITECTURE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "Goa Architecture") {
		t.Errorf("expected known doc title, got: %s", text)
	}
}

func TestShowBuiltinDoc_Unknown(t *testing.T) {
	w := newWriter()
	err := showBuiltinDoc(w, "UNKNOWN")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "not found") {
		t.Errorf("expected not found message, got: %s", text)
	}
}

func TestRunDocsCommand_Delegates(t *testing.T) {
	w := newWriter()
	dp := &fakeDocsProvider{
		list: []core.DocInfo{{Name: "ARCHITECTURE", Description: "docs"}},
	}

	// When DocsProvider is nil, falls back to builtins
	err := runDocsCommand(w, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Goa Documentation") {
		t.Errorf("expected builtin fallback, got: %s", w.Text())
	}

	// With provider, no args = list
	w2 := newWriter()
	err = runDocsCommand(w2, dp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w2.Text(), "ARCHITECTURE") {
		t.Errorf("expected listing, got: %s", w2.Text())
	}

	// With provider + arg = show
	w3 := newWriter()
	err = runDocsCommand(w3, dp, []string{"ARCHITECTURE"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w3.Text(), "content for ARCHITECTURE") {
		t.Errorf("expected doc content, got: %s", w3.Text())
	}
}

func TestPrintHelpTools_WithRegistry(t *testing.T) {
	w := newWriter()
	// With nil registry = fallback to known tools
	printHelpTools(w, nil)
	text := w.Text()
	if !strings.Contains(text, "read") || !strings.Contains(text, "bash") {
		t.Errorf("expected known tools, got: %s", text)
	}
}

func TestPrintHelpDocs_WithRegistry(t *testing.T) {
	w := newWriter()
	dp := &fakeDocsProvider{
		list: []core.DocInfo{{Name: "ARCHITECTURE", Description: "Architecture"}},
	}
	printHelpDocs(w, dp)
	text := w.Text()
	if !strings.Contains(text, "ARCHITECTURE") {
		t.Errorf("expected doc listing, got: %s", text)
	}

	// Nil = fallback
	w2 := newWriter()
	printHelpDocs(w2, nil)
	if !strings.Contains(w2.Text(), "ARCHITECTURE") {
		t.Errorf("expected fallback docs, got: %s", w2.Text())
	}
}

func TestShowHelpFor_Unknown(t *testing.T) {
	w := newWriter()
	err := showHelpFor(w, nil, nil, nil, "zzz_nonexistent_command")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "Unknown") {
		t.Errorf("expected unknown message, got: %s", text)
	}
}

func TestShowFullHelp(t *testing.T) {
	w := newWriter()
	err := showFullHelp(w, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "Goa — terminal-native") {
		t.Errorf("expected Goa header, got: %s", text)
	}
}

func TestParseToolToggleArgs(t *testing.T) {
	tests := []struct {
		args      []string
		wantName  string
		wantState string
		wantOK    bool
	}{
		{[]string{"memento:on"}, "memento", "on", true},
		{[]string{"bg_exec", "off"}, "bg_exec", "off", true},
		{[]string{"read"}, "", "", false},
		{[]string{"unknown:on"}, "", "", false},
		{[]string{"memento", "maybe"}, "", "", false},
	}
	for _, tt := range tests {
		name, state, ok := parseToolToggleArgs(tt.args)
		if ok != tt.wantOK {
			t.Errorf("parseToolToggleArgs(%q) ok=%v want %v", tt.args, ok, tt.wantOK)
			continue
		}
		if name != tt.wantName || state != tt.wantState {
			t.Errorf("parseToolToggleArgs(%q) = (%q, %q, %v) want (%q, %q, %v)",
				tt.args, name, state, ok, tt.wantName, tt.wantState, tt.wantOK)
		}
	}
}

func TestToggleTool_DisablesAndUnregisters(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("bg_exec", true)
	reg := tools.NewToolRegistry()
	reg.Register(tools.NewBGExecTool())

	ctx := core.Context{Config: cfg, ToolRegistry: reg}
	if err := toggleTool(ctx, "bg_exec", "off"); err != nil {
		t.Fatalf("toggleTool: %v", err)
	}
	if cfg.Tools.Enabled.BGExec {
		t.Error("BGExec should be disabled")
	}
	if _, ok := reg.Get("bg_exec"); ok {
		t.Error("bg_exec should be unregistered")
	}
}

func TestToggleTool_EnablesAndRegisters(t *testing.T) {
	cfg := &config.Config{}
	reg := tools.NewToolRegistry()
	am := core.NewAgentManager(cfg, nil, nil, core.NewSessionState(internal.ModeState{Major: internal.MajorCoder}), nil, "")

	factoryCalled := false
	factory := func(name string) (agentic.Tool, bool) {
		if name == "bg_exec" {
			factoryCalled = true
			return tools.NewBGExecTool(), true
		}
		return nil, false
	}

	ctx := core.Context{Config: cfg, ToolRegistry: reg, ToolFactory: factory, AgentManager: am}
	if err := toggleTool(ctx, "bg_exec", "on"); err != nil {
		t.Fatalf("toggleTool: %v", err)
	}
	if !cfg.Tools.Enabled.BGExec {
		t.Error("BGExec should be enabled")
	}
	if !factoryCalled {
		t.Error("ToolFactory should have been called")
	}
	if _, ok := reg.Get("bg_exec"); !ok {
		t.Error("bg_exec should be registered")
	}
}

func TestToggleTool_AlreadyInState(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("memento", false)
	ctx := core.Context{Config: cfg}
	if err := toggleTool(ctx, "memento", "off"); err != nil {
		t.Fatalf("toggleTool: %v", err)
	}
	if cfg.Tools.Enabled.Memento {
		t.Error("Memento should remain disabled")
	}
}

func TestToolsDocCommand_TogglePythonCompletionStateAware(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("python", true)
	ctx := core.Context{Config: cfg}
	cmd := &ToolsDocCommand{}

	vals := cmd.CompleteArgs(ctx, "python:")
	if len(vals) != 1 || vals[0].Value != "python:off" {
		t.Errorf("enabled python: got %v, want [python:off]", vals)
	}

	cfg.Tools.Enabled.SetEnabled("python", false)
	vals = cmd.CompleteArgs(ctx, "python:")
	if len(vals) != 1 || vals[0].Value != "python:on" {
		t.Errorf("disabled python: got %v, want [python:on]", vals)
	}
}
