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

// TestToolSummaryLine_StripsSPDXHeader is a regression test for the bug where
// /tools listed the goal tool's raw SPDX license header instead of a summary.
func TestToolSummaryLine_StripsSPDXHeader(t *testing.T) {
	withHeader := "<!--\nSPDX-License-Identifier: GPL-3.0-or-later\n\nCopyright (C) 2026 Pierre Poissinger\n-->\n\nManage the current goal: create one.\n\nMore detail follows."
	got := toolSummaryLine(withHeader)
	if strings.Contains(got, "SPDX") || strings.Contains(got, "<!--") || strings.Contains(got, "Copyright") {
		t.Fatalf("summary leaked license header: %q", got)
	}
	if !strings.HasPrefix(got, "Manage the current goal") {
		t.Fatalf("summary should start with the real description, got %q", got)
	}

	// Plain single-line descriptions are unchanged.
	if got := toolSummaryLine("Run a shell command."); got != "Run a shell command." {
		t.Fatalf("plain description changed: %q", got)
	}

	// Multi-paragraph docs collapse to the first content line.
	if got := toolSummaryLine("First line.\n\nSecond paragraph."); got != "First line." {
		t.Fatalf("expected first line only, got %q", got)
	}
}

// fakeDeferredTool is a tool whose schema is withheld from the prompt and
// loaded on demand via tool_search (implements agentic.Deferred).
type fakeDeferredTool struct {
	fakeTool
}

func (f *fakeDeferredTool) Deferred() bool { return true }

// descTool is an eager tool with a controllable name/description.
type descTool struct {
	name string
	desc string
}

func (f *descTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{Name: f.name, Description: f.desc}
}
func (f *descTool) Execute(string) (string, error) { return "ok", nil }
func (f *descTool) IsRetryable(error) bool         { return false }

// TestListTools_PartitionsEagerAndDeferred verifies /tools groups tools into
// "Default part of prompt" and "Must use search", marks deferred tools with
// the 🔍 icon and a "via tool_search" label, and does not truncate
// descriptions.
func TestListTools_PartitionsEagerAndDeferred(t *testing.T) {
	reg := tools.NewToolRegistry()
	longDesc := "This is a deliberately long tool description that exceeds sixty characters to prove the list view no longer truncates it."
	reg.Register(&descTool{name: "read", desc: "Read a file."})
	reg.Register(&descTool{name: "bash", desc: longDesc})
	reg.Register(&fakeDeferredTool{fakeTool{name: "webfetch"}})
	reg.Register(&fakeDeferredTool{fakeTool{name: "memento"}})

	w := newWriter()
	if err := listTools(w, reg); err != nil {
		t.Fatalf("listTools: %v", err)
	}
	text := w.Text()

	if !strings.Contains(text, "Default part of prompt") {
		t.Errorf("expected eager section header, got:\n%s", text)
	}
	if !strings.Contains(text, "Must use search") {
		t.Errorf("expected deferred section header, got:\n%s", text)
	}
	if !strings.Contains(text, "🔍 via tool_search") {
		t.Errorf("expected deferred tools marked with 🔍 via tool_search, got:\n%s", text)
	}
	if !strings.Contains(text, longDesc) {
		t.Errorf("description was truncated; expected full text, got:\n%s", text)
	}
	if strings.Contains(text, "...") {
		t.Errorf("found truncation ellipsis in output:\n%s", text)
	}
	// Counts line reports the split.
	if !strings.Contains(text, "4 tool(s) available (2 in prompt, 2 via tool_search)") {
		t.Errorf("expected eager/deferred counts, got:\n%s", text)
	}
}

// TestListTools_AllEager verifies the deferred section is omitted when no
// tool is deferred.
func TestListTools_AllEager(t *testing.T) {
	reg := tools.NewToolRegistry()
	reg.Register(&descTool{name: "read", desc: "Read a file."})

	w := newWriter()
	if err := listTools(w, reg); err != nil {
		t.Fatalf("listTools: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "Default part of prompt") {
		t.Errorf("expected eager section header, got:\n%s", text)
	}
	if strings.Contains(text, "Must use search") {
		t.Errorf("did not expect a deferred section, got:\n%s", text)
	}
	if !strings.Contains(text, "1 tool(s) available (1 in prompt, 0 via tool_search)") {
		t.Errorf("expected counts, got:\n%s", text)
	}
}

// TestShowToolDetail_DeferredAvailability verifies the detail view announces
// whether a tool is in the prompt or must be loaded via tool_search.
func TestShowToolDetail_DeferredAvailability(t *testing.T) {
	reg := tools.NewToolRegistry()
	reg.Register(&descTool{name: "read", desc: "Read a file."})
	reg.Register(&fakeDeferredTool{fakeTool{name: "webfetch"}})

	w := newWriter()
	if err := showToolDetail(w, reg, "webfetch"); err != nil {
		t.Fatalf("showToolDetail: %v", err)
	}
	if !strings.Contains(w.Text(), "🔍 Must use search") {
		t.Errorf("expected deferred availability line, got:\n%s", w.Text())
	}

	w2 := newWriter()
	if err := showToolDetail(w2, reg, "read"); err != nil {
		t.Fatalf("showToolDetail: %v", err)
	}
	if !strings.Contains(w2.Text(), "Default part of prompt") {
		t.Errorf("expected eager availability line, got:\n%s", w2.Text())
	}
}

// TestGetToolEnabled_AskUserQuestionDefault verifies ask_user_question is
// enabled by default (opt-out): a zero-value config (clarify_disabled unset)
// reports enabled.
func TestGetToolEnabled_AskUserQuestionDefault(t *testing.T) {
	if !getToolEnabled(&config.Config{}, "ask_user_question") {
		t.Error("ask_user_question should be enabled by default")
	}
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("clarify_disabled", true)
	if getToolEnabled(cfg, "ask_user_question") {
		t.Error("ask_user_question should be disabled when clarify_disabled=true")
	}
}

// TestConfigurableTools_AskUserQuestionDefault verifies the toggle catalog
// marks ask_user_question as enabled by default (opt-out).
func TestConfigurableTools_AskUserQuestionDefault(t *testing.T) {
	for _, ct := range tools.ConfigurableTools() {
		if ct.Name == "ask_user_question" {
			if !ct.Default {
				t.Error("ask_user_question catalog Default should be true (enabled by default)")
			}
			return
		}
	}
	// If we get here the tool is missing from the catalog entirely.
	t.Error("ask_user_question missing from ConfigurableTools()")
}

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
