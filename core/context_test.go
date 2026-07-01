// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

// TestContextImplementsRoleInterfaces verifies that Context satisfies the
// role interfaces defined by ARCH-3. This is primarily a compile-time
// contract, but the test documents the expected surface.
func TestContextImplementsRoleInterfaces(t *testing.T) {
	ctx := &Context{
		EventBus: &event.Bus{
			Chat:    make(chan event.ChatEvent, 1),
			Footer:  make(chan event.FooterEvent, 1),
			Control: make(chan event.ControlEvent, 1),
		},
	}

	// Ensure the role interfaces are satisfied.
	var _ CommandEnv = ctx
	var _ UIHost = ctx
	var _ SessionEnv = ctx
	var _ OutputWriter = ctx
	var _ EventSink = ctx
	var _ Selector = ctx
	var _ InputPrompter = ctx
	var _ ModeController = ctx
	var _ SessionRecorder = ctx
}

// TestOutputWriter_Writef_Buffer captures output when OutputBuffer is set.
func TestOutputWriter_Writef_Buffer(t *testing.T) {
	buf := &strings.Builder{}
	ctx := Context{OutputBuffer: buf}

	ctx.Writef("hello %s", "world")

	if got := buf.String(); got != "hello world" {
		t.Errorf("Writef with buffer = %q, want %q", got, "hello world")
	}
}

// TestEventSink_Flash sends a flash event on the chat channel.
func TestEventSink_Flash(t *testing.T) {
	bus := &event.Bus{
		Chat: make(chan event.ChatEvent, 1),
	}
	ctx := Context{EventBus: bus}

	ctx.Flash("test flash")

	select {
	case ev := <-bus.Chat:
		if ev.Flash == nil || ev.Flash.Text != "test flash" {
			t.Errorf("Flash event = %+v, want Flash{Text: test flash}", ev)
		}
	default:
		t.Error("expected flash event on chat channel")
	}
}

// TestModeController_NoAgentManager is safe when AgentManager is nil.
func TestModeController_NoAgentManager(t *testing.T) {
	ctx := Context{}

	mode := ctx.CurrentMode()
	if !mode.IsZero() {
		t.Errorf("CurrentMode() = %+v, want zero", mode)
	}
	if ctx.GetThinkingLevel() != "" {
		t.Errorf("GetThinkingLevel() = %q, want empty", ctx.GetThinkingLevel())
	}
	if err := ctx.SetThinkingLevel("high"); err != nil {
		t.Errorf("SetThinkingLevel returned error: %v", err)
	}
}

// TestSessionRecorder_NoAgentManager is safe when AgentManager is nil.
func TestSessionRecorder_NoAgentManager(t *testing.T) {
	ctx := Context{}

	if ctx.TurnHistory() != nil {
		t.Error("TurnHistory() should be nil when AgentManager is nil")
	}
	if ctx.LastTurn() != nil {
		t.Error("LastTurn() should be nil when AgentManager is nil")
	}
}

// TestSelector_CallsBack verifies SelectOption invokes the configured callback.
func TestSelector_CallsBack(t *testing.T) {
	var called bool
	ctx := Context{
		SelectOptionFunc: func(string, []tui.SelectorItem, string, func(string, bool)) {
			called = true
		},
	}

	ctx.SelectOption("title", nil, "", nil)

	if !called {
		t.Error("SelectOption did not invoke SelectOptionFunc")
	}
}

// TestInputPrompter_CallsBack verifies ShowInput invokes the configured callback.
func TestInputPrompter_CallsBack(t *testing.T) {
	var called bool
	ctx := Context{
		ShowInputFunc: func(string, string, func(string, bool)) {
			called = true
		},
	}

	ctx.ShowInput("prompt", "", nil)

	if !called {
		t.Error("ShowInput did not invoke ShowInputFunc")
	}
}

// TestSelector_FallbackCancel invokes onSelected with ("", false) when no
// callback is configured.
func TestSelector_FallbackCancel(t *testing.T) {
	ctx := Context{}
	var selected string
	var ok bool
	ctx.SelectOption("title", nil, "", func(s string, b bool) {
		selected = s
		ok = b
	})

	if selected != "" || ok {
		t.Errorf("fallback selected=%q ok=%v, want \"\" false", selected, ok)
	}
}
