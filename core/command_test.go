// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"sync"
	"testing"
)

// testCommand is a simple command for testing.
type testCommand struct {
	name    string
	aliases []string
}

func (c *testCommand) Name() string                         { return c.name }
func (c *testCommand) Aliases() []string                    { return c.aliases }
func (c *testCommand) ShortHelp() string                    { return "test command" }
func (c *testCommand) LongHelp() string                     { return "A test command for unit tests" }
func (c *testCommand) Run(ctx Context, args []string) error { return nil }

// TestRegistryRegisterAndResolve verifies command registration and lookup.
func TestRegistryRegisterAndResolve(t *testing.T) {
	reg := NewCommandRegistry()
	cmd := &testCommand{name: "test", aliases: []string{"t"}}
	if err := reg.Register(cmd); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	got, found := reg.Resolve("test")
	if !found {
		t.Fatal("Resolve('test') should find the command")
	}
	if got.Name() != "test" {
		t.Errorf("Name = %q, want %q", got.Name(), "test")
	}
}

// TestRegistryResolveByAlias verifies alias resolution.
func TestRegistryResolveByAlias(t *testing.T) {
	reg := NewCommandRegistry()
	if err := reg.Register(&testCommand{name: "help", aliases: []string{"h", "?"}}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	got, found := reg.Resolve("h")
	if !found {
		t.Fatal("Resolve('h') should find by alias")
	}
	if got.Name() != "help" {
		t.Errorf("Resolved to %q, want %q", got.Name(), "help")
	}

	got2, found2 := reg.Resolve("?")
	if !found2 {
		t.Fatal("Resolve('?') should find by alias")
	}
	if got2.Name() != "help" {
		t.Errorf("Resolved to %q, want %q", got2.Name(), "help")
	}
}

// TestRegistryResolveUnknown verifies unknown commands return false.
func TestRegistryResolveUnknown(t *testing.T) {
	reg := NewCommandRegistry()
	_, found := reg.Resolve("nonexistent")
	if found {
		t.Error("Resolve('nonexistent') should return false")
	}
}

// TestRegistryAll verifies All returns all registered commands.
func TestRegistryAll(t *testing.T) {
	reg := NewCommandRegistry()
	for _, name := range []string{"beta", "alpha", "gamma"} {
		if err := reg.Register(&testCommand{name: name}); err != nil {
			t.Fatalf("Register %q failed: %v", name, err)
		}
	}

	all := reg.All()
	if len(all) != 3 {
		t.Fatalf("All() = %d commands, want 3", len(all))
	}
	// Should be sorted alphabetically
	if all[0].Name() != "alpha" || all[1].Name() != "beta" || all[2].Name() != "gamma" {
		t.Errorf("All() order: %q, %q, %q — want sorted", all[0].Name(), all[1].Name(), all[2].Name())
	}
}

// TestRegistryDuplicateReturnsError verifies duplicate registration returns an error.
func TestRegistryDuplicateReturnsError(t *testing.T) {
	reg := NewCommandRegistry()
	if err := reg.Register(&testCommand{name: "dup"}); err != nil {
		t.Fatalf("first Register failed: %v", err)
	}
	if err := reg.Register(&testCommand{name: "dup"}); err == nil {
		t.Error("expected error for duplicate registration")
	}
}

// TestIsInternal_CommandWithoutInterface returns false.
func TestIsInternal_CommandWithoutInterface(t *testing.T) {
	cmd := &testCommand{name: "visible"}
	if IsInternal(cmd) {
		t.Error("IsInternal() for plain command should be false")
	}
}

// testInternalCommand is a command that implements InternalCommand.
type testInternalCommand struct {
	name string
}

func (c *testInternalCommand) Name() string                         { return c.name }
func (c *testInternalCommand) Aliases() []string                    { return nil }
func (c *testInternalCommand) ShortHelp() string                    { return "internal test" }
func (c *testInternalCommand) LongHelp() string                     { return "internal test long" }
func (c *testInternalCommand) Run(ctx Context, args []string) error { return nil }
func (c *testInternalCommand) IsInternal() bool                     { return true }

// TestIsInternal_CommandWithInternal returns true.
func TestIsInternal_CommandWithInternal(t *testing.T) {
	cmd := &testInternalCommand{name: "internal"}
	if !IsInternal(cmd) {
		t.Error("IsInternal() for InternalCommand should be true")
	}
}

// TestIsInternal_GlobalRegistryInternalCommands verifies that /config and /setup
// are registered as internal. Tests against core/commands require a separate
// test in that package; here we only test the IsInternal logic.
func TestIsInternal_GlobalRegistryInternalCommands(t *testing.T) {
	// core/commands registers via init() — we cannot test their IsInternal here
	// because they are in a different package. That test lives in
	// core/commands/config_test.go. Here we only verify that non-internal
	// commands in the global registry are not flagged.
	reg := GlobalRegistry()
	for _, name := range []string{"help", "version", "mode"} {
		cmd, found := reg.Resolve(name)
		if !found {
			continue
		}
		if IsInternal(cmd) {
			t.Errorf("/%s should NOT be internal but IsInternal() returned true", name)
		}
	}
}

// TestRegistryGlobalDefault verifies the global default registry works.
func TestRegistryGlobalDefault(t *testing.T) {
	// Clear and re-register through the accessors (not by bypassing the lock).
	ResetGlobalRegistry()

	if err := RegisterCommand(&testCommand{name: "global-test"}); err != nil {
		t.Fatalf("RegisterCommand failed: %v", err)
	}
	reg := GlobalRegistry()
	cmd, found := reg.Resolve("global-test")
	if !found {
		t.Fatal("Global registry should contain registered command")
	}
	if cmd.Name() != "global-test" {
		t.Errorf("Name = %q, want %q", cmd.Name(), "global-test")
	}
}

// TestCommandRegistry_Isolation guards SOLID-4: with explicit *CommandRegistry
// values, registration in one registry must be invisible to another. (The
// package-global defaultRegistry shared state makes this impossible when
// commands are registered via RegisterCommand — hence the deprecation.)
func TestCommandRegistry_Isolation(t *testing.T) {
	a := NewCommandRegistry()
	b := NewCommandRegistry()

	if err := a.Register(&testCommand{name: "only-in-a"}); err != nil {
		t.Fatalf("register A: %v", err)
	}
	if _, found := b.Resolve("only-in-a"); found {
		t.Fatal("registry B should not see a command registered only in A")
	}
	if _, found := a.Resolve("only-in-a"); !found {
		t.Fatal("registry A should resolve its own command")
	}
}

// TestGlobalRegistry_ResetIsRaceFree guards the SOLID-4 race fix: concurrent
// ResetGlobalRegistry / GlobalRegistry must not trigger -race failures.
func TestGlobalRegistry_ResetIsRaceFree(t *testing.T) {
	ResetGlobalRegistry()
	var wg sync.WaitGroup
	const n = 200
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = GlobalRegistry()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = RegisterCommand(&testCommand{name: "race-cmd"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			ResetGlobalRegistry()
		}
	}()
	wg.Wait()
}
