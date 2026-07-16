// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tools"
)

func TestRegisterTools_ClarifyDefaultOn(t *testing.T) {
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{}, nil)
	if _, ok := reg.Get("ask_user_question"); !ok {
		t.Fatal("ask_user_question should be registered by default")
	}
}

func TestRegisterTools_ClarifyDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("clarify_disabled", true)
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), cfg, nil)
	if _, ok := reg.Get("ask_user_question"); ok {
		t.Fatal("ask_user_question should NOT be registered when clarify_disabled is true")
	}
}

func TestAttachClarifyTool(t *testing.T) {
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{}, nil)
	called := false
	attachClarifyTool(reg, func(title, summary, question string, options []string, step, total int) (string, bool) {
		called = true
		return "x", true
	})
	tt, ok := reg.Get("ask_user_question")
	if !ok {
		t.Fatal("tool missing")
	}
	// Execute a single question to confirm the callback was wired.
	out, err := tt.Execute(`{"questions":[{"question":"q"}]}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !called {
		t.Error("clarify callback was not invoked")
	}
	if out == "" {
		t.Error("expected non-empty result")
	}
}

func TestAttachClarifyTool_NilSafe(t *testing.T) {
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{}, nil)
	attachClarifyTool(reg, nil) // must not panic
}

func TestRegisterTools_SmartSearchRespectsEnabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.SmartSearch.Enabled = false

	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), cfg, nil)
	if _, ok := reg.Get("smartsearch"); ok {
		t.Fatal("smartsearch should NOT be registered when disabled")
	}

	cfg.Tools.SmartSearch.Enabled = true
	reg = tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), cfg, nil)
	if _, ok := reg.Get("smartsearch"); !ok {
		t.Fatal("smartsearch should be registered when enabled")
	}
}

func TestRuntimeOptions_EmptyPromptImpliesHeadless(t *testing.T) {
	opts := RuntimeOptions{PromptArg: "", PromptGiven: true}
	if !opts.Headless() {
		t.Error("expected headless when --prompt is explicitly empty")
	}
}

func TestRuntimeOptions_PromptArgImpliesHeadless(t *testing.T) {
	opts := RuntimeOptions{PromptArg: "hello"}
	if !opts.Headless() {
		t.Error("expected headless when --prompt has a value")
	}
}

func TestRuntimeOptions_PromptFileImpliesHeadless(t *testing.T) {
	opts := RuntimeOptions{PromptFile: "/tmp/prompt.txt"}
	if !opts.Headless() {
		t.Error("expected headless when --prompt-file is set")
	}
}

func TestRuntimeOptions_DefaultIsTUI(t *testing.T) {
	opts := RuntimeOptions{}
	if opts.Headless() {
		t.Error("expected TUI mode by default")
	}
}
