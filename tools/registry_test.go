// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// testTool is a simple tool for testing.
type testTool struct {
	name string
}

func (t *testTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{Name: t.name, Description: "test tool"}
}
func (t *testTool) Execute(input string) (string, error) { return "ok", nil }
func (t *testTool) IsRetryable(err error) bool           { return false }

// testDocTool implements both Tool and Documentable.
type testDocTool struct {
	testTool
	shortDoc string
	longDoc  string
	examples []string
}

func (t *testDocTool) ShortDoc() string   { return t.shortDoc }
func (t *testDocTool) LongDoc() string    { return t.longDoc }
func (t *testDocTool) Examples() []string { return t.examples }

// TestToolRegistryRegisterAndGet verifies basic registration.
func TestToolRegistryRegisterAndGet(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&testTool{name: "read"})

	tool, found := reg.Get("read")
	if !found {
		t.Fatal("Get('read') should find tool")
	}
	if tool.Schema().Name != "read" {
		t.Errorf("Name = %q, want %q", tool.Schema().Name, "read")
	}
}

// TestToolRegistryGetUnknown verifies unknown tool returns false.
func TestToolRegistryGetUnknown(t *testing.T) {
	reg := NewToolRegistry()
	_, found := reg.Get("nonexistent")
	if found {
		t.Error("Get('nonexistent') should return false")
	}
}

// TestToolRegistryAll verifies All returns all tools.
func TestToolRegistryAll(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&testTool{name: "beta"})
	reg.Register(&testTool{name: "alpha"})

	all := reg.All()
	if len(all) != 2 {
		t.Fatalf("All = %d, want 2", len(all))
	}
	// Should be sorted
	if all[0].Schema().Name != "alpha" || all[1].Schema().Name != "beta" {
		t.Errorf("All order: %q, %q — want sorted", all[0].Schema().Name, all[1].Schema().Name)
	}
}

// TestToolRegistryAllDocumented verifies documented tool filtering.
func TestToolRegistryAllDocumented(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&testTool{name: "undocumented"})
	reg.Register(&testDocTool{
		testTool: testTool{name: "documented"},
		shortDoc: "Short doc",
		longDoc:  "Long doc",
		examples: []string{"example 1"},
	})

	doced := reg.AllDocumented()
	if len(doced) != 1 {
		t.Fatalf("AllDocumented = %d, want 1", len(doced))
	}
	if doced[0].ShortDoc != "Short doc" {
		t.Errorf("ShortDoc = %q, want %q", doced[0].ShortDoc, "Short doc")
	}
	if len(doced[0].Examples) != 1 {
		t.Errorf("Examples = %d, want 1", len(doced[0].Examples))
	}
}

// TestToolRegistryUnregister verifies a tool can be removed at runtime.
func TestToolRegistryUnregister(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(&testTool{name: "alpha"})
	reg.Register(&testTool{name: "beta"})

	reg.Unregister("alpha")
	if _, found := reg.Get("alpha"); found {
		t.Error("Get('alpha') should return false after Unregister")
	}
	if len(reg.All()) != 1 {
		t.Fatalf("All = %d, want 1", len(reg.All()))
	}
	// Unregistering unknown tools is a no-op.
	reg.Unregister("gamma")
	if len(reg.All()) != 1 {
		t.Fatalf("All = %d, want 1 after unregistering unknown", len(reg.All()))
	}
}

// TestConfigurableTools_IncludesVerifyAndClarify ensures the runtime-toggleable
// list (which drives the /config → Tools screen and docs) covers verify and
// ask_user_question, the two opt-out tools, in addition to the opt-in extras.
func TestConfigurableTools_IncludesVerifyAndClarify(t *testing.T) {
	names := ConfigurableToolNames()
	want := map[string]bool{"verify": true, "ask_user_question": true, "bg_exec": true, "webfetch": true}
	for n := range want {
		found := false
		for _, got := range names {
			if got == n {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ConfigurableToolNames missing %q: %v", n, names)
		}
	}
}

func TestConfigurableTools_IncludesPython(t *testing.T) {
	names := ConfigurableToolNames()
	found := false
	for _, n := range names {
		if n == "python" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ConfigurableToolNames missing %q: %v", "python", names)
	}

	for _, tool := range ConfigurableTools() {
		if tool.Name == "python" && !tool.Default {
			t.Errorf("python should default to enabled, got Default=%v", tool.Default)
		}
	}
}
