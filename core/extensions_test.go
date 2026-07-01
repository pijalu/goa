// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"sync"
	"testing"
)

func TestNewExtensionRegistry(t *testing.T) {
	er := NewExtensionRegistry()
	if er == nil {
		t.Fatal("NewExtensionRegistry returned nil")
	}
}

func TestExtensionRegistry_RegisterPane(t *testing.T) {
	er := NewExtensionRegistry()
	factory := func() interface{} { return "pane1" }
	er.RegisterPane("pane1", factory)

	panes := er.PaneFactories()
	if len(panes) != 1 {
		t.Fatalf("PaneFactories = %d, want 1", len(panes))
	}
	if _, ok := panes["pane1"]; !ok {
		t.Errorf("pane1 not found in PaneFactories")
	}
}

func TestExtensionRegistry_RegisterSegment(t *testing.T) {
	er := NewExtensionRegistry()
	factory := func() interface{} { return "segment1" }
	er.RegisterSegment("segment1", factory)

	segs := er.SegmentFactories()
	if len(segs) != 1 {
		t.Fatalf("SegmentFactories = %d, want 1", len(segs))
	}
	if _, ok := segs["segment1"]; !ok {
		t.Errorf("segment1 not found in SegmentFactories")
	}
}

func TestExtensionRegistry_RegisterModal(t *testing.T) {
	er := NewExtensionRegistry()
	factory := func() interface{} { return "modal1" }
	er.RegisterModal("modal1", factory)

	modals := er.ModalFactories()
	if len(modals) != 1 {
		t.Fatalf("ModalFactories = %d, want 1", len(modals))
	}
	if _, ok := modals["modal1"]; !ok {
		t.Errorf("modal1 not found in ModalFactories")
	}
}

func TestExtensionRegistry_RegisterKeyBinding(t *testing.T) {
	er := NewExtensionRegistry()
	binding := KeyBindingDef{Keys: "ctrl+x ctrl+s", Description: "Save session"}
	er.RegisterKeyBinding(binding)

	bindings := er.KeyBindings()
	if len(bindings) != 1 {
		t.Fatalf("KeyBindings = %d, want 1", len(bindings))
	}
	if bindings[0].Keys != "ctrl+x ctrl+s" {
		t.Errorf("Keys = %q, want %q", bindings[0].Keys, "ctrl+x ctrl+s")
	}
}

func TestExtensionRegistry_RegisterEventHandler(t *testing.T) {
	er := NewExtensionRegistry()
	called := false
	handler := func(event interface{}) {
		called = true
	}
	er.RegisterEventHandler("mode.changed", handler)

	er.FireEvent("mode.changed", "test_event")
	if !called {
		t.Error("EventHandler was not called")
	}
}

func TestExtensionRegistry_FireEvent_UnknownEvent(t *testing.T) {
	er := NewExtensionRegistry()
	// Should not panic
	er.FireEvent("unknown.event", "data")
}

func TestExtensionRegistry_RegisterCommand(t *testing.T) {
	er := NewExtensionRegistry()
	cmd := UICommandDef{Name: "save-session", Description: "Save the current session"}
	er.RegisterCommand(cmd)

	cmds := er.UICommands()
	if len(cmds) != 1 {
		t.Fatalf("UICommands = %d, want 1", len(cmds))
	}
	if cmds[0].Name != "save-session" {
		t.Errorf("Name = %q, want %q", cmds[0].Name, "save-session")
	}
}

func TestExtensionRegistry_RegisterThemeToken(t *testing.T) {
	er := NewExtensionRegistry()
	token := ThemeTokenDef{Token: "accent", Color: "#ff6600"}
	er.RegisterThemeToken(token)

	tokens := er.ThemeTokens()
	if len(tokens) != 1 {
		t.Fatalf("ThemeTokens = %d, want 1", len(tokens))
	}
	if tokens[0].Token != "accent" {
		t.Errorf("Token = %q, want %q", tokens[0].Token, "accent")
	}
}

func TestExtensionRegistry_ConcurrentRegistrations(t *testing.T) {
	er := NewExtensionRegistry()
	var wg sync.WaitGroup
	n := 50

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := string(rune('a' + idx%26))
			er.RegisterPane(id, func() interface{} { return idx })
			er.RegisterSegment(id, func() interface{} { return idx })
			er.RegisterKeyBinding(KeyBindingDef{Keys: id, Description: "test"})
		}(i)
	}

	wg.Wait()

	// All registrations should be visible
	panes := er.PaneFactories()
	if len(panes) == 0 {
		t.Error("Expected at least some panes from concurrent registration")
	}
}

func TestExtensionRegistry_FireEvent_MultipleHandlers(t *testing.T) {
	er := NewExtensionRegistry()
	count := 0
	var mu sync.Mutex

	handler1 := func(event interface{}) {
		mu.Lock()
		count++
		mu.Unlock()
	}
	handler2 := func(event interface{}) {
		mu.Lock()
		count++
		mu.Unlock()
	}

	er.RegisterEventHandler("test.event", handler1)
	er.RegisterEventHandler("test.event", handler2)

	er.FireEvent("test.event", "data")

	if count != 2 {
		t.Errorf("Expected 2 handlers to be called, got %d", count)
	}
}
