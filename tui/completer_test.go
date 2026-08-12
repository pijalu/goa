// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
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

	// Typing /mo (2+ chars after /) should expand modifiers for matched
	// commands. Sub-parameter expansion is deferred until 2 chars, so the
	// single-char /m prefix intentionally shows base commands only.
	results := cc.Complete("/mo")
	if len(results) == 0 {
		t.Fatal("expected completions for /mo")
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
		t.Error("expected /mode:coder in completions for partial prefix /mo")
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
	// Complete with a 2-char prefix so the modifier tier is expanded (modifiers
	// are deferred for bare "/" and 1-char prefixes); the Most Used tier must
	// still surface the frequent /mode and /mode:coder entries.
	results := cc.Complete("/mo")
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

// bigCompleter builds a realistic completer: many commands, each with args
// and nested args — the shape that produced hundreds of options for bare "/".
func bigCompleter(numCmds int) *CommandCompleter {
	var cmds []string
	descs := map[string]string{}
	for i := 0; i < numCmds; i++ {
		name := "/" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + "x"
		cmds = append(cmds, name)
		descs[name] = name + " desc"
	}
	cc := NewCommandCompleter(cmds, descs)
	cc.SetArgCompleter(func(cmdName, argPrefix string) []Completion {
		if argPrefix == "" {
			return []Completion{{Value: "a1"}, {Value: "a2"}, {Value: "a3"}, {Value: "a4"}, {Value: "a5"}}
		}
		if !strings.Contains(argPrefix, ":") {
			// nested level
			return []Completion{{Value: argPrefix + ":n1"}, {Value: argPrefix + ":n2"}}
		}
		return nil
	})
	return cc
}

// Bare "/" must NOT expand every command's modifiers — it returns base
// commands (+Most Used) only, bounded in count.
func TestCommandCompleter_BareSlashLimitsAndDefersModifiers(t *testing.T) {
	cc := bigCompleter(40)
	results := cc.Complete("/")

	for _, r := range results {
		if r.Category == CatModifier {
			t.Errorf("bare / must not propose modifiers yet, got %q", r.Value)
		}
		if strings.Count(r.Value, ":") > 0 {
			t.Errorf("bare / must not propose sub-params, got %q", r.Value)
		}
	}
	if len(results) > 60 {
		t.Errorf("bare / returned %d options, want a bounded list (<=60)", len(results))
	}
	if len(results) == 0 {
		t.Fatal("bare / should still propose base commands")
	}
}

// With a single char after '/', still no sub-param expansion.
func TestCommandCompleter_SingleCharDefersModifiers(t *testing.T) {
	cc := bigCompleter(40)
	results := cc.Complete("/a")
	for _, r := range results {
		if strings.Contains(r.Value, ":") {
			t.Errorf("/a (1 char) must not propose sub-params yet, got %q", r.Value)
		}
	}
	if !containsValue(results, "/aax") {
		t.Errorf("/a should still propose matching base commands, got %v", results[:min(3, len(results))])
	}
}

// After 2+ chars, sub-params for the matched commands appear; a near-exact
// command prefix surfaces ITS sub-params (the /goal case).
func TestCommandCompleter_TwoCharsExpandsModifiers(t *testing.T) {
	cc := bigCompleter(40)
	results := cc.Complete("/aax")
	if !containsValue(results, "/aax:a1") {
		t.Errorf("/aax (exact command) should propose its sub-params, got %v", results[:min(5, len(results))])
	}
}

// A frequently-used command must propose its sub-parameters.
// Simulates /goal with its real subcommand set.
func TestCommandCompleter_GoalProposesSubParams(t *testing.T) {
	cc := newGoalCompleter()

	results := cc.Complete("/goal")
	for _, want := range []string{"/goal:status", "/goal:cancel", "/goal:complete", "/goal:pause", "/goal:list"} {
		if !containsValue(results, want) {
			t.Errorf("/goal should propose sub-param %q, got %v", want, results)
		}
	}

	// Nested: /goal:cancel expands to its scopes.
	results = cc.Complete("/goal:cancel")
	if !containsValue(results, "/goal:cancel:current") {
		t.Errorf("/goal:cancel should propose nested scopes, got %v", results)
	}
}

// newGoalCompleter builds a completer whose /goal arg completer faithfully
// replicates the real /goal CompleteArgs router (goal.go): split on the first
// colon; a known sub routes to its scope completer; otherwise a non-nested
// partial filters the level-1 subcommand list.
func newGoalCompleter() *CommandCompleter {
	cmds := []string{"/goal", "/goals", "/help"}
	descs := map[string]string{"/goal": "Manage goal", "/goals": "List goals", "/help": "Help"}
	cc := NewCommandCompleter(cmds, descs)
	goalSubs := []Completion{
		{Value: "status", Description: "show goal status"},
		{Value: "cancel", Description: "cancel a goal"},
		{Value: "complete", Description: "mark complete"},
		{Value: "pause", Description: "pause the goal"},
		{Value: "list", Description: "list goals"},
	}
	cancelScopes := []Completion{{Value: "cancel:current"}, {Value: "cancel:all"}}
	cc.SetArgCompleter(func(cmdName, argPrefix string) []Completion {
		return goalArgCompletions(cmdName, argPrefix, goalSubs, cancelScopes)
	})
	return cc
}

func goalArgCompletions(cmdName, argPrefix string, goalSubs, cancelScopes []Completion) []Completion {
	if cmdName != "/goal" {
		return nil
	}
	if idx := strings.Index(argPrefix, ":"); idx >= 0 {
		return goalNestedCompletions(argPrefix, idx, cancelScopes)
	}
	var out []Completion
	for _, sc := range goalSubs {
		if argPrefix == "" || strings.HasPrefix(sc.Value, argPrefix) {
			out = append(out, sc)
		}
	}
	return out
}

func goalNestedCompletions(argPrefix string, idx int, cancelScopes []Completion) []Completion {
	if argPrefix[:idx] != "cancel" {
		return nil
	}
	rest := argPrefix[idx+1:]
	var out []Completion
	for _, sc := range cancelScopes {
		if rest == "" || strings.HasPrefix(strings.TrimPrefix(sc.Value, "cancel:"), rest) {
			out = append(out, sc)
		}
	}
	return out
}

// The total option count is always bounded, even mid-prefix.
func TestCommandCompleter_OptionCountBounded(t *testing.T) {
	cc := bigCompleter(40)
	for _, prefix := range []string{"/", "/a", "/aa", "/aax"} {
		if n := len(cc.Complete(prefix)); n > 100 {
			t.Errorf("Complete(%q) returned %d options, want <=100", prefix, n)
		}
	}
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
