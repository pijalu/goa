// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/tui"
)

func TestTeamCommand_SelectorItems(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var selTitle string
	var selItems []tui.SelectorItem
	var selCurrent string
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		selTitle, selItems, selCurrent = title, options, current
	}
	if err := (&TeamCommand{}).Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if selTitle != "Select team:" {
		t.Errorf("title = %q", selTitle)
	}
	// 2 teams + — add team — + — none — entries.
	if len(selItems) != 4 {
		t.Fatalf("items = %v, want 2 teams + add + none", selItems)
	}
	if selItems[len(selItems)-2].Value != "__add__" {
		t.Errorf("second-to-last item = %v, want — add team —", selItems[len(selItems)-2])
	}
	if selItems[len(selItems)-1].Value != "__none__" {
		t.Errorf("last item = %v, want — none —", selItems[len(selItems)-1])
	}
	if selCurrent != "" {
		t.Errorf("current = %q, want empty (no active team)", selCurrent)
	}
}

func TestTeamCommand_PickerDeleteConfirmsAndRemoves(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var callbacks []func(string, bool)
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		callbacks = append(callbacks, cb)
	}
	if err := (&TeamCommand{}).Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	callbacks[0]("__delete__beta", true)
	if len(callbacks) != 2 {
		t.Fatalf("callbacks = %d, want selector plus confirmation", len(callbacks))
	}
	callbacks[1]("yes", true)
	if _, ok := cfg.Teams.Definitions["beta"]; ok {
		t.Fatal("beta should be removed after picker confirmation")
	}
}

func TestTeamCommand_RemoveConfirmed(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var onSelected func(string, bool)
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, sel func(string, bool)) {
		onSelected = sel
	}
	if err := (&TeamCommand{}).Run(ctx, []string{"remove:beta"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if onSelected == nil {
		t.Fatal("expected remove confirmation selector")
	}
	onSelected("yes", true)
	if _, ok := cfg.Teams.Definitions["beta"]; ok {
		t.Errorf("beta should be removed after confirmation")
	}
	if _, ok := cfg.Teams.Definitions["alpha"]; !ok {
		t.Errorf("alpha must remain")
	}
}

func TestTeamCommand_RemoveActiveRefused(t *testing.T) {
	cfg := teamCmdConfig()
	cfg.Teams.Active = "alpha"
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	if err := (&TeamCommand{}).Run(ctx, []string{"remove:alpha"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, ok := cfg.Teams.Definitions["alpha"]; !ok {
		t.Errorf("active team must not be removed")
	}
	if !strings.Contains(out.String(), "active team") {
		t.Errorf("out = %q, want an active-team refusal", out.String())
	}
}

func TestTeamCommand_RemoveUnknown(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	if err := (&TeamCommand{}).Run(ctx, []string{"remove:ghost"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "not defined") {
		t.Errorf("out = %q, want a not-defined message", out.String())
	}
}

// splitTeamArg must separate a leading sub-command keyword from the team
// name, leaving an unrecognized argument whole (it is a team name). The
// command router splits on ':' only, never whitespace, so a multi-word team
// name arrives as a single trailing token.
func TestSplitTeamArg(t *testing.T) {
	cases := []struct {
		arg  string
		sub  string
		rest string
	}{
		{"", "", ""},
		{"add", "add", ""},
		{"list", "list", ""},
		{"off", "off", ""},
		{"remove:beta", "remove", "beta"},
		{"show:alpha", "show", "alpha"},
		{"use:beta", "use", "beta"},
		{"remove:My Team", "remove", "My Team"},
		{"show:My Team", "show", "My Team"},
		{"alpha", "", "alpha"},         // plain team name → default activation
		{"My Team", "", "My Team"},     // multi-word team name whole
		{"beta-2", "", "beta-2"},       // hyphenated non-keyword is a name
		{"statusquo", "", "statusquo"}, // keyword prefix but not keyword
	}
	for _, tc := range cases {
		sub, rest := splitTeamArg(tc.arg)
		if sub != tc.sub || rest != tc.rest {
			t.Errorf("splitTeamArg(%q) = (%q,%q), want (%q,%q)", tc.arg, sub, rest, tc.sub, tc.rest)
		}
	}
}

// Regression: the production command router splits "/team:remove:beta" into
// args ["remove","beta"] (it splits on every ':'). The command must
// reassemble them so /team:remove:<name>, /team:show:<name> and /team:use:<name>
// reach their handlers — previously only args[0] was read, so these silently
// mis-dispatched. Team names with spaces must also survive.
func TestTeamCommand_RouterSplitArgs(t *testing.T) {
	// remove via router-shaped args ["remove","beta"]
	t.Run("remove", func(t *testing.T) {
		cfg := teamCmdConfig()
		ctx := teamCmdContext(t, cfg)
		var onSelected func(string, bool)
		ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, sel func(string, bool)) {
			onSelected = sel
		}
		if err := (&TeamCommand{}).Run(ctx, []string{"remove", "beta"}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if onSelected == nil {
			t.Fatal("remove via split args did not open the confirmation selector")
		}
		onSelected("yes", true)
		if _, ok := cfg.Teams.Definitions["beta"]; ok {
			t.Errorf("beta should be removed after router-shaped remove")
		}
	})

	// show via router-shaped args ["show","alpha"]
	t.Run("show", func(t *testing.T) {
		cfg := teamCmdConfig()
		ctx := teamCmdContext(t, cfg)
		var out strings.Builder
		ctx.OutputBuffer = &out
		if err := (&TeamCommand{}).Run(ctx, []string{"show", "alpha"}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !strings.Contains(out.String(), "Team alpha") {
			t.Errorf("show via split args out = %q, want the alpha definition", out.String())
		}
	})

	// activate a multi-word team name via router-shaped args ["My Team"]
	t.Run("activate multi-word", func(t *testing.T) {
		cfg := teamCmdConfig()
		cfg.Teams.Definitions["My Team"] = config.TeamDefinition{Main: &config.TeamMember{Model: "m9"}, Review: "off"}
		ctx := teamCmdContext(t, cfg)
		var out strings.Builder
		ctx.OutputBuffer = &out
		if err := (&TeamCommand{}).Run(ctx, []string{"My Team"}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !strings.Contains(out.String(), "My Team") {
			t.Errorf("activate multi-word out = %q, want the team activated", out.String())
		}
	})
}

func TestTeamCommand_CompleteArgs(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := core.Context{Config: cfg}
	comps := (&TeamCommand{}).CompleteArgs(ctx, "")
	var values []string
	for _, c := range comps {
		values = append(values, c.Value)
	}
	for _, want := range []string{"alpha", "beta", "off", "status", "list", "sync", "show:alpha", "use:beta", "add", "remove:alpha"} {
		found := false
		for _, v := range values {
			if v == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("completion missing %q (got %v)", want, values)
		}
	}
	// Prefix filtering.
	comps = (&TeamCommand{}).CompleteArgs(ctx, "al")
	if len(comps) != 1 || comps[0].Value != "alpha" {
		t.Errorf("prefix al = %v", comps)
	}
}

func TestConfigMenu_RootIncludesTeams(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	found := false
	for _, it := range sr.options {
		if it.Value == "teams" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("root items missing teams: %v", sr.options)
	}
}

// Regression (bug: Teams nav never built a history stack): drilling
// root → Teams → team detail → description must push each page so ESC
// (back) walks UP one level instead of exiting the whole menu to the TUI.
// Every other submenu wraps with m.open; Teams (and its sub-pages) bypassed
// it, leaving len(history)==0 so any back() closed the menu.
func TestConfigMenu_TeamsNavigationHistory(t *testing.T) {
	cfg := teamCmdConfig()
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	// Enter via the real root path.
	menu.open(func() { _ = menu.showRoot() })
	menu.showSubMenu("teams")
	if sr.title != "Teams:" {
		t.Fatalf("after teams: title=%q, want Teams:", sr.title)
	}
	// Root must have been pushed so ESC from Teams returns to Settings root.
	if len(menu.history) == 0 {
		t.Fatalf("entering Teams pushed nothing onto history (len=0); ESC would exit the menu")
	}

	// ESC from the Teams list returns to the Settings root, not out of the menu.
	sr.onSel("", false)
	if menu.current == nil {
		t.Fatal("ESC from Teams list closed the menu (current=nil); want Settings root")
	}
	if sr.title != "Settings:" {
		t.Errorf("ESC from Teams list: title=%q, want Settings:", sr.title)
	}
}

// ESC from a team detail must return to the Teams list (one level up), not
// exit the menu.
func TestConfigMenu_TeamDetailEscReturnsToList(t *testing.T) {
	cfg := teamCmdConfig()
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.open(func() { _ = menu.showRoot() })
	menu.showSubMenu("teams")
	sr.onSel("alpha", true) // team detail
	if sr.title != "Edit team alpha:" {
		t.Fatalf("detail title=%q, want 'Edit team alpha:'", sr.title)
	}

	sr.onSel("", false) // ESC from detail
	if menu.current == nil {
		t.Fatal("ESC from team detail closed the menu (current=nil); want Teams list")
	}
	if sr.title != "Teams:" {
		t.Errorf("ESC from team detail: title=%q, want Teams:", sr.title)
	}
}

// ESC from the description input must return to the team detail (one level
// up); submitting a description then ESC must also land back on the detail.
func TestConfigMenu_TeamDescriptionEscReturnsToDetail(t *testing.T) {
	cfg := teamCmdConfig()
	ctx, sr, ir, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.open(func() { _ = menu.showRoot() })
	menu.showSubMenu("teams")
	sr.onSel("alpha", true)       // team detail
	sr.onSel("description", true) // description input shown
	if ir.prompt == "" {
		t.Fatal("expected the description input prompt")
	}

	// ESC on the input -> back to the team detail.
	ir.onSub("", false)
	if menu.current == nil {
		t.Fatal("ESC from description closed the menu (current=nil); want team detail")
	}
	if sr.title != "Edit team alpha:" {
		t.Errorf("ESC from description: title=%q, want 'Edit team alpha:'", sr.title)
	}

	// Submit a description, then ESC from the detail returns to the Teams list.
	sr.onSel("description", true)
	ir.onSub("my desc", true)
	if sr.title != "Edit team alpha:" {
		t.Fatalf("after submit: title=%q, want 'Edit team alpha:'", sr.title)
	}
	if cfg.Teams.Definitions["alpha"].Description != "my desc" {
		t.Errorf("description not persisted: %q", cfg.Teams.Definitions["alpha"].Description)
	}
}

// /config → Teams is wizard-forward (mirrors /config → Models): — add team —
// first, then each defined team (select to edit), then the active-team row.

func TestConfigMenu_TeamsRootIsWizardForward(t *testing.T) {
	cfg := teamCmdConfig()
	cfg.Teams.Active = "alpha"
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.openTeams()
	if sr.title != "Teams:" {
		t.Fatalf("title = %q, want Teams:", sr.title)
	}
	if len(sr.options) != 4 {
		t.Fatalf("options = %v, want add + 2 teams + active row", sr.options)
	}
	if sr.options[0].Value != "__add__" {
		t.Errorf("first option = %q, want — add team — (wizard-forward)", sr.options[0].Value)
	}
	// Selecting a team name opens its detail; the active team is annotated.
	var sawActiveAnnotation bool
	for _, it := range sr.options {
		if it.Value == "alpha" && strings.Contains(it.Description, "active") {
			sawActiveAnnotation = true
		}
	}
	if !sawActiveAnnotation {
		t.Errorf("active team alpha should be annotated in the list: %v", sr.options)
	}
	if sr.options[len(sr.options)-1].Value != "active" {
		t.Errorf("last option = %q, want the Active team row", sr.options[len(sr.options)-1].Value)
	}

	// — add team — opens the wizard.
	sr.onSel("__add__", true)
	// The wizard's first step is the team-name input.
	// (inputRecorder.prompt is checked via the inputRecorder, not sr.)
}

// Regression: deleting a team from the Teams selector emits __delete__<name>.
// The encoded value must be dispatched to the confirmation flow, not treated as
// a literal team name and opened for editing.
func TestConfigMenu_TeamDeleteHotkeyRemovesTeam(t *testing.T) {
	cfg := teamCmdConfig()
	cfg.Teams.Active = "beta"
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.openTeams()
	sr.onSel("__delete__beta", true)
	if sr.title != "Remove team beta?" {
		t.Fatalf("delete title = %q, want Remove team beta?", sr.title)
	}
	sr.onSel("yes", true)
	if _, exists := cfg.Teams.Definitions["beta"]; exists {
		t.Fatal("beta still exists after delete confirmation")
	}
	if cfg.Teams.Active != "" {
		t.Fatalf("active team = %q, want cleared", cfg.Teams.Active)
	}
	if _, exists := cfg.Teams.Definitions["alpha"]; !exists {
		t.Fatal("alpha was unexpectedly deleted")
	}
}

func TestConfigMenu_MemberThinkingEdit(t *testing.T) {
	cfg := teamCmdConfig()
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.openMemberDetail("alpha", "companion")
	// Find the thinking item.
	var found bool
	for _, it := range sr.options {
		if it.Value == "thinking" {
			found = true
		}
	}
	if !found {
		t.Fatalf("member detail missing thinking item: %v", sr.options)
	}
	// Drive the thinking selector.
	for _, it := range sr.options {
		if it.Value == "thinking" {
			sr.onSel(it.Value, true)
			break
		}
	}
	if sr.onSel == nil {
		t.Fatal("expected thinking-level selector")
	}
	sr.onSel("minimal", true)
	def := cfg.Teams.Definitions["alpha"]
	if def.Companion.ThinkingLevel != "minimal" {
		t.Errorf("thinking = %q, want minimal", def.Companion.ThinkingLevel)
	}
}
