// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
)

func TestCommandCompleter_ExpandsArgVariants(t *testing.T) {
	cmdNames := []string{"/mode", "/models", "/memory"}
	descs := map[string]string{
		"/mode":   "Set or display the agent's mode",
		"/models": "List available models",
		"/memory": "Manage memory",
	}
	cc := NewCommandCompleter(cmdNames, descs)
	cc.SetArgCompleter(func(cmdName, argPrefix string) []Completion {
		// Only /mode has args
		if cmdName != "/mode" {
			return nil
		}
		return []Completion{
			{Value: "coder", Description: "switch to coder mode"},
			{Value: "minor", Description: "configure minor modes"},
			{Value: "list", Description: "list all registered modes"},
		}
	})

	// Typing /mode should show /mode plus its arg variants
	results := cc.Complete("/mode")
	if len(results) == 0 {
		t.Fatal("expected completions for /mode")
	}

	// Check base command is present
	found := false
	for _, r := range results {
		if r.Value == "/mode" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected /mode in completions")
	}

	// Check arg variants are expanded
	variants := []string{"/mode:coder", "/mode:minor", "/mode:list"}
	for _, v := range variants {
		found := false
		for _, r := range results {
			if r.Value == v {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected arg variant %q in completions for /mode", v)
		}
	}
}

func TestCommandCompleter_ExpandsModifiersForPartial(t *testing.T) {
	cmdNames := []string{"/mode", "/models", "/memory"}
	descs := map[string]string{
		"/mode":   "Set or display the agent's mode",
		"/models": "List available models",
		"/memory": "Manage memory",
	}
	cc := NewCommandCompleter(cmdNames, descs)
	cc.SetArgCompleter(func(cmdName, argPrefix string) []Completion {
		if cmdName == "/mode" {
			return []Completion{
				{Value: "coder", Description: "switch to coder mode"},
				{Value: "list", Description: "list modes"},
			}
		}
		return nil
	})

	// Typing /m should expand modifiers for matched commands
	results := cc.Complete("/m")
	if len(results) == 0 {
		t.Fatal("expected completions for /m")
	}

	// Should contain base commands AND modifier variants
	foundMode := false
	foundModeCoder := false
	for _, r := range results {
		if r.Value == "/mode" {
			foundMode = true
		}
		if r.Value == "/mode:coder" {
			foundModeCoder = true
		}
	}
	if !foundMode {
		t.Error("expected /mode in completions")
	}
	if !foundModeCoder {
		t.Error("expected /mode:coder in completions for partial prefix /m")
	}
}

func TestCommandCompleter_ColonTriggersArgCompletion(t *testing.T) {
	cmdNames := []string{"/mode", "/models"}
	descs := map[string]string{
		"/mode":   "Set or display the agent's mode",
		"/models": "List available models",
	}
	cc := NewCommandCompleter(cmdNames, descs)
	cc.SetArgCompleter(func(cmdName, argPrefix string) []Completion {
		if cmdName == "/mode" && argPrefix == "" {
			return []Completion{
				{Value: "coder", Description: "switch to coder"},
				{Value: "minor", Description: "minor mode"},
			}
		}
		if cmdName == "/mode" && argPrefix == "m" {
			return []Completion{
				{Value: "minor", Description: "minor mode"},
			}
		}
		return nil
	})

	// Typing /mode: should show all arg variants
	results := cc.Complete("/mode:")
	if len(results) != 2 {
		t.Fatalf("expected 2 completions for /mode:, got %d: %v", len(results), results)
	}

	// Typing /mode:m should filter to matching variants
	results = cc.Complete("/mode:m")
	if len(results) != 1 || results[0].Value != "/mode:minor" {
		t.Fatalf("expected /mode:minor for /mode:m, got %v", results)
	}
}

func TestCommandCompleter_NoColonCompletionWithoutSlash(t *testing.T) {
	cmdNames := []string{"/mode", "/help"}
	descs := map[string]string{
		"/mode": "Set mode",
		"/help": "Help",
	}
	cc := NewCommandCompleter(cmdNames, descs)
	// arg completer that returns something for any command
	cc.SetArgCompleter(func(cmdName, argPrefix string) []Completion {
		return []Completion{{Value: "something", Description: "test"}}
	})

	// Non-command prefix with colon should NOT trigger arg completion
	results := cc.Complete("text:withcolon")
	if len(results) != 0 {
		t.Errorf("expected 0 completions for non-command colon prefix, got %d: %v", len(results), results)
	}

	// /command: should still work
	results = cc.Complete("/mode:")
	if len(results) == 0 {
		t.Error("expected completions for /mode: to still work")
	}
}

func TestCommandCompleter_BasePresence(t *testing.T) {
	cmdNames := []string{"/help", "/mode"}
	descs := map[string]string{
		"/help": "Show help",
		"/mode": "Set mode",
	}
	cc := NewCommandCompleter(cmdNames, descs)

	// No arg completer set
	results := cc.Complete("/mode")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Value != "/mode" {
		t.Errorf("expected /mode, got %q", results[0].Value)
	}
}

func TestCommandCompleter_MostUsedTier(t *testing.T) {
	cc := newMostUsedCompleter()
	results := cc.Complete("/")
	if len(results) == 0 {
		t.Fatal("expected completions")
	}

	mostUsedCount, cmdCount, modCount := countCategories(results)
	assertCategoryCounts(t, mostUsedCount, cmdCount, modCount)
	assertMostUsedSorted(t, results, mostUsedCount)
	assertMostUsedNotDuplicated(t, results, mostUsedCount)
}

func newMostUsedCompleter() *CommandCompleter {
	cmdNames := []string{"/mode", "/memory"}
	descs := map[string]string{"/mode": "mode", "/memory": "memory"}
	cc := NewCommandCompleter(cmdNames, descs)
	cc.SetArgCompleter(func(cmdName, argPrefix string) []Completion {
		if cmdName == "/mode" && argPrefix == "" {
			return []Completion{{Value: "coder", Description: "coder"}}
		}
		if cmdName == "/memory" && argPrefix == "" {
			return []Completion{{Value: "clear", Description: "clear"}}
		}
		return nil
	})
	cc.SetMinThreshold(5)
	cc.SetMaxMostUsed(2)
	cc.SetFreqOrder(map[string]int{
		"/mode":       10,
		"/mode:coder": 8,
		"/memory":     3,
	})
	return cc
}

func countCategories(results []Completion) (mostUsed, cmd, mod int) {
	for _, r := range results {
		switch r.Category {
		case CatMostUsed:
			mostUsed++
		case CatCommand:
			cmd++
		case CatModifier:
			mod++
		}
	}
	return mostUsed, cmd, mod
}

func assertCategoryCounts(t *testing.T, mostUsed, cmd, mod int) {
	if mostUsed != 2 {
		t.Errorf("expected 2 MostUsed items, got %d", mostUsed)
	}
	if cmd != 1 {
		t.Errorf("expected 1 Command item, got %d", cmd)
	}
	if mod != 1 {
		t.Errorf("expected 1 Modifier item (/memory:clear), got %d", mod)
	}
}

func assertMostUsedSorted(t *testing.T, results []Completion, mostUsedCount int) {
	mu := results[:mostUsedCount]
	if mu[0].Score < mu[1].Score {
		t.Error("MostUsed items not sorted by score descending")
	}
}

func assertMostUsedNotDuplicated(t *testing.T, results []Completion, mostUsedCount int) {
	for _, r := range results[mostUsedCount:] {
		if r.Value == "/mode" || r.Value == "/mode:coder" {
			t.Errorf("MostUsed item %q appeared in lower tier", r.Value)
		}
	}
}

func TestCommandCompleter_MostUsed_Disabled(t *testing.T) {
	cmdNames := []string{"/mode"}
	descs := map[string]string{"/mode": "mode"}
	cc := NewCommandCompleter(cmdNames, descs)
	cc.SetMinThreshold(0)
	cc.SetFreqOrder(map[string]int{"/mode": 100})

	results := cc.Complete("/")
	for _, r := range results {
		if r.Category == CatMostUsed {
			t.Error("MostUsed tier should be disabled when threshold is 0")
		}
	}
}

func TestCommandCompleter_MostUsed_RespectsMaxCap(t *testing.T) {
	cmdNames := []string{"/a", "/b", "/c", "/d"}
	descs := map[string]string{"/a": "a", "/b": "b", "/c": "c", "/d": "d"}
	cc := NewCommandCompleter(cmdNames, descs)
	cc.SetMinThreshold(1)
	cc.SetMaxMostUsed(2)
	cc.SetFreqOrder(map[string]int{"/a": 5, "/b": 4, "/c": 3, "/d": 2})

	results := cc.Complete("/")
	var muCount int
	for _, r := range results {
		if r.Category == CatMostUsed {
			muCount++
		}
	}
	if muCount != 2 {
		t.Errorf("expected max 2 MostUsed items, got %d", muCount)
	}
}

// Test that after accepting a partial completion, further modifiers are
// still available (simulating Tab-fill-then-recomplete behaviour).
func TestCommandCompleter_RecompleteAfterAccept(t *testing.T) {
	cc := newCompanionCompleter()

	t.Run("partial_prefix", func(t *testing.T) { assertPartialCompanionCompletions(t, cc) })
	t.Run("after_accept_base", func(t *testing.T) { assertAfterAcceptBase(t, cc) })
	t.Run("after_accept_colon", func(t *testing.T) { assertAfterAcceptColon(t, cc) })
}

func newCompanionCompleter() *CommandCompleter {
	cmdNames := []string{"/mode", "/companion"}
	descs := map[string]string{"/mode": "Set mode", "/companion": "Toggle companion"}
	cc := NewCommandCompleter(cmdNames, descs)
	cc.SetArgCompleter(func(cmdName, argPrefix string) []Completion {
		switch cmdName {
		case "/mode":
			if argPrefix == "" {
				return []Completion{
					{Value: "coder", Description: "coder mode"},
					{Value: "list", Description: "list modes"},
				}
			}
		case "/companion":
			if argPrefix == "" {
				return []Completion{
					{Value: "on", Description: "enable companion"},
					{Value: "off", Description: "disable companion"},
				}
			}
		}
		return nil
	})
	return cc
}

func assertPartialCompanionCompletions(t *testing.T, cc *CommandCompleter) {
	results := cc.Complete("/comp")
	if !containsValue(results, "/companion") {
		t.Fatal("expected /companion in completions")
	}
	if !containsValue(results, "/companion:on") {
		t.Fatal("expected /companion:on in completions")
	}
}

func assertAfterAcceptBase(t *testing.T, cc *CommandCompleter) {
	results := cc.Complete("/companion")
	if !containsValue(results, "/companion") {
		t.Error("expected /companion still present after re-complete")
	}
	if !containsValue(results, "/companion:on") {
		t.Error("expected /companion:on still present after re-complete")
	}
}

func assertAfterAcceptColon(t *testing.T, cc *CommandCompleter) {
	results := cc.Complete("/companion:")
	if len(results) != 2 {
		t.Fatalf("expected 2 nested completions, got %d", len(results))
	}
	if !containsValue(results, "/companion:on") {
		t.Error("expected /companion:on")
	}
	if !containsValue(results, "/companion:off") {
		t.Error("expected /companion:off")
	}
}

func containsValue(results []Completion, value string) bool {
	for _, r := range results {
		if r.Value == value {
			return true
		}
	}
	return false
}

// TestCommandCompleter_SetCommandsLateRegistration reproduces the /quota bug:
// plugin commands register in the shared registry AFTER the completer
// snapshotted command names at TUI build time. Without SetCommands, the
// late-registered command resolves on execute but is never proposed.
func TestCommandCompleter_SetCommandsLateRegistration(t *testing.T) {
	cc := NewCommandCompleter([]string{"/help"}, map[string]string{"/help": "Help"})

	if containsValue(cc.Complete("/q"), "/quota") {
		t.Fatal("/quota unexpectedly present before registration")
	}

	// Simulate the async plugin load landing: registry now has /quota,
	// completer gets re-snapshotted.
	names, descs := collectNamesForTest(
		[][2]string{{"/help", "Help"}, {"/quota", "Show provider quota"}},
	)
	cc.SetCommands(names, descs)

	results := cc.Complete("/q")
	if !containsValue(results, "/quota") {
		t.Fatalf("expected /quota after SetCommands, got %v", results)
	}
	for _, r := range results {
		if r.Value == "/quota" && r.Description != "Show provider quota" {
			t.Errorf("expected description to follow SetCommands, got %q", r.Description)
		}
	}

	// Nil descriptions must not panic and must clear stale descriptions.
	cc.SetCommands([]string{"/help"}, nil)
	if containsValue(cc.Complete("/q"), "/quota") {
		t.Fatal("/quota should be gone after re-snapshot without it")
	}
}

func collectNamesForTest(cmds [][2]string) ([]string, map[string]string) {
	names := make([]string, 0, len(cmds))
	descs := make(map[string]string, len(cmds))
	for _, c := range cmds {
		names = append(names, c[0])
		descs[c[0]] = c[1]
	}
	return names, descs
}
