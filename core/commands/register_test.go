// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/core"
)

// TestRegisterAll_RegistersExpectedCommands verifies that RegisterAll populates
// the registry with the standard built-in commands.
func TestRegisterAll_RegistersExpectedCommands(t *testing.T) {
	reg := core.NewCommandRegistry()
	if err := RegisterAll(reg, CommandDependencies{}); err != nil {
		t.Fatalf("RegisterAll failed: %v", err)
	}

	for _, name := range []string{
		"help", "quit",
		"mode", "model", "config", "setup",
		"provider",
		"profile",
		"skill",
		"export",
		"workflows",
		"thinking", "thinking-blocks",
		"ui",
	} {
		if _, found := reg.Resolve(name); !found {
			t.Errorf("RegisterAll did not register /%s", name)
		}
	}

	// Consolidated duplicates must NOT be registered anymore.
	for _, name := range []string{
		"providers", "prs", "models", "profiles", // consolidated into singular forms
		"commands",                    // consolidated into /help
		"skills",                      // plural of /skill
		"save", "restore", "sessions", // consolidated into /session
	} {
		if _, found := reg.Resolve(name); found {
			t.Errorf("RegisterAll should not register /%s (consolidated)", name)
		}
	}
}

// TestRegisterAll_NoDuplicateNames verifies RegisterAll does not panic or error
// when registering the standard command set into a fresh registry.
func TestRegisterAll_NoDuplicateNames(t *testing.T) {
	reg := core.NewCommandRegistry()
	if err := RegisterAll(reg, CommandDependencies{}); err != nil {
		t.Fatalf("RegisterAll failed: %v", err)
	}
	// If any two built-in commands share a primary name, RegisterAll would fail.
	all := reg.All()
	if len(all) == 0 {
		t.Fatal("RegisterAll registered no commands")
	}
	seen := make(map[string]int, len(all))
	for _, cmd := range all {
		seen[cmd.Name()]++
		if seen[cmd.Name()] > 1 {
			t.Errorf("command /%s registered more than once", cmd.Name())
		}
	}
}
