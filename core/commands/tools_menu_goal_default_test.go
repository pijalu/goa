// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

// shippedDefaultConfig loads the embedded defaults through the real cascade
// with an isolated HOME (the developer's ~/.goa is a legitimate override and
// must not leak into an assertion about what a fresh install ships).
func shippedDefaultConfig(t *testing.T) *config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	loader := config.NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("cascade Load: %v", err)
	}
	return cfg
}

// TestToolsMenu_GoalShowsOnWithShippedDefaults is the rendered-output check for
// the `goal` row of /config → Tools: every row's Description is the user-visible
// on/off label derived from the live config, so a fresh install must read "on"
// now that the embedded default ships tools.enabled.goal: true.
func TestToolsMenu_GoalShowsOnWithShippedDefaults(t *testing.T) {
	cfg := shippedDefaultConfig(t)

	var goalRow *string
	for _, item := range buildToolItems(cfg) {
		if item.Value == "goal" {
			desc := item.Description
			goalRow = &desc
			break
		}
	}
	if goalRow == nil {
		t.Fatal("/config → Tools has no `goal` row")
	}
	if *goalRow != "on" {
		t.Errorf("/config → Tools goal row = %q, want \"on\" with the shipped defaults", *goalRow)
	}
}

// TestConfigKeyCompletions_GoalDefaultWording pins the /config:set help text for
// tools.enabled.goal: it must state the shipped default (true), not the old
// "default false" that disagreed with the embedded config.
func TestConfigKeyCompletions_GoalDefaultWording(t *testing.T) {
	comps := configKeyCompletions("tools.enabled.goal")
	var goal *core.ArgCompletion
	for i := range comps {
		if comps[i].Value == "tools.enabled.goal" {
			goal = &comps[i]
			break
		}
	}
	if goal == nil {
		t.Fatal("configKeyCompletions has no tools.enabled.goal entry")
	}
	if strings.Contains(goal.Description, "default false") {
		t.Errorf("help text still claims the old default: %q", goal.Description)
	}
	if !strings.Contains(goal.Description, "default true") {
		t.Errorf("help text = %q, want it to state the shipped default ("+
			"default true)", goal.Description)
	}
}

// TestToolsGoalToggleOfferedWithShippedDefaults checks the /tools:goal
// completion the user actually sees: with goal enabled by default the offered
// next action is ":off / disable goal" (not ":on / enable goal" as before).
func TestToolsGoalToggleOfferedWithShippedDefaults(t *testing.T) {
	cfg := shippedDefaultConfig(t)
	ctx := core.Context{Config: cfg}

	comps := completeToolToggleSuffix(ctx, "goal", "")
	if len(comps) != 1 {
		t.Fatalf("completeToolToggleSuffix(goal) returned %d completions, want 1: %+v", len(comps), comps)
	}
	if comps[0].Value != "goal:off" {
		t.Errorf("offered completion = %q, want \"goal:off\" (goal ships enabled)", comps[0].Value)
	}

	// And the tool list the completion of `/tools:` shows must label goal
	// "enabled" in the same shipped-default state.
	var goalStatus string
	for _, comp := range completeToolNames(ctx, "") {
		if comp.Value == "goal" {
			goalStatus = comp.Description
			break
		}
	}
	if goalStatus != "enabled" {
		t.Errorf("/tools: completion for goal = %q, want \"enabled\"", goalStatus)
	}
}
