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
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{})
	if _, ok := reg.Get("ask_user_question"); !ok {
		t.Fatal("ask_user_question should be registered by default")
	}
}

func TestRegisterTools_ClarifyDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("clarify_disabled", true)
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), cfg)
	if _, ok := reg.Get("ask_user_question"); ok {
		t.Fatal("ask_user_question should NOT be registered when clarify_disabled is true")
	}
}

func TestAttachClarifyTool(t *testing.T) {
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{})
	called := false
	attachClarifyTool(reg, func(title, summary, question string, options []string) (string, bool) {
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
	registerTools(reg, nil, nil, t.TempDir(), &config.Config{})
	attachClarifyTool(reg, nil) // must not panic
}
