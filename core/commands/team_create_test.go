// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tui"
)

// teamCmdConfig builds a config with two shorthand teams.

// /team behaves like /model: the bare picker offers — add team —, /team:add
// opens the creation wizard, and /team:remove:<name> deletes a definition
// after confirmation (active team refused).
func TestTeamCommand_AddOpensWizard(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var inputPrompt string
	ctx.ShowInputFunc = func(prompt, current string, onSubmit func(string, bool)) {
		inputPrompt = prompt
	}
	if err := (&TeamCommand{}).Run(ctx, []string{"add"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(inputPrompt, "team name") {
		t.Errorf("/team:add should open the add-team wizard (name prompt), got prompt %q", inputPrompt)
	}
}

func TestTeamCommand_PickerAddOpensWizard(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var onSelected func(string, bool)
	var inputPrompt string
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, sel func(string, bool)) {
		onSelected = sel
	}
	ctx.ShowInputFunc = func(prompt, current string, onSubmit func(string, bool)) {
		inputPrompt = prompt
	}
	if err := (&TeamCommand{}).Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if onSelected == nil {
		t.Fatal("expected team selector")
	}
	onSelected("__add__", true)
	if !strings.Contains(inputPrompt, "team name") {
		t.Errorf("picker — add team — should open the wizard (name prompt), got %q", inputPrompt)
	}
}

func TestTeamCommand_NoTeamsOffersAdd(t *testing.T) {
	cfg := &config.Config{}
	ctx := teamCmdContext(t, cfg)
	var title string
	var items []tui.SelectorItem
	ctx.SelectOptionFunc = func(ti string, o []tui.SelectorItem, c string, sel func(string, bool)) {
		title, items = ti, o
	}
	if err := (&TeamCommand{}).Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(items) != 1 || items[0].Value != "__add__" {
		t.Fatalf("no-teams picker = %q %v, want a single — add team — entry", title, items)
	}
}

// workflowTeamConfig builds a config with a team that has an ordered
// architect → coder → reviewer⇄coder workflow.
func workflowTeamConfig() *config.Config {
	cfg := teamCmdConfig()
	cfg.Teams.Definitions["crew"] = config.TeamDefinition{
		Members: map[string]config.TeamMember{
			"architect": {Model: "m1", Role: "main"},
			"coder":     {Model: "m2", Role: "worker"},
			"reviewer":  {Model: "m3", Role: "reviewer"},
		},
		Review: "off",
		Workflow: []config.TeamWorkflowStage{
			{Member: "architect", Prompt: "Design {{.UserInput}}"},
			{Member: "coder"},
			{Member: "reviewer", LoopBackTo: "coder", MaxIterations: 2},
		},
	}
	return cfg
}

func TestTeamWorkflowPipeline_Builds(t *testing.T) {
	def := workflowTeamConfig().Teams.Definitions["crew"]
	p, err := teamWorkflowPipeline("crew", def)
	if err != nil {
		t.Fatalf("teamWorkflowPipeline: %v", err)
	}
	if len(p.Stages) != 3 {
		t.Fatalf("stages = %d, want 3", len(p.Stages))
	}
	// Order preserved: architect, coder, reviewer.
	wantOrder := []string{"architect", "coder", "reviewer"}
	for i, w := range wantOrder {
		if p.Stages[i].Agent != w {
			t.Errorf("stage %d agent = %q, want %q", i, p.Stages[i].Agent, w)
		}
	}
	// Loop edge carried over.
	if p.Stages[2].Loop.LoopBackTo != "coder" || p.Stages[2].Loop.MaxIterations != 2 {
		t.Errorf("reviewer loop = %+v, want coder/2", p.Stages[2].Loop)
	}
	// Empty prompt defaulted to the task pass-through.
	if p.Stages[1].Prompt == "" {
		t.Errorf("coder stage prompt was not defaulted")
	}
	// Summary renders the loop annotation.
	if s := teamWorkflowSummary(p); !strings.Contains(s, "reviewer⇄coder") {
		t.Errorf("summary = %q, want it to mark reviewer⇄coder", s)
	}
}

func TestTeamWorkflowPipeline_Invalid(t *testing.T) {
	def := config.TeamDefinition{
		Members:  map[string]config.TeamMember{"a": {Model: "m1", Role: "main"}},
		Workflow: []config.TeamWorkflowStage{{Member: "ghost"}},
	}
	if _, err := teamWorkflowPipeline("x", def); err == nil {
		t.Fatal("expected error for workflow referencing an unknown member")
	}
}

func TestTeamCommand_ShowDisplaysWorkflow(t *testing.T) {
	cfg := workflowTeamConfig()
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	if err := (&TeamCommand{}).Run(ctx, []string{"show:crew"}); err != nil {
		t.Fatalf("Run show: %v", err)
	}
	got := out.String()
	for _, want := range []string{"workflow", "architect", "coder", "reviewer", "⇄ coder", "/team:run:crew"} {
		if !strings.Contains(got, want) {
			t.Errorf("teamShow output missing %q:\n%s", want, got)
		}
	}
}

func TestTeamCommand_RunNoWorkflow(t *testing.T) {
	cfg := teamCmdConfig() // alpha/beta have no workflow
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	if err := (&TeamCommand{}).Run(ctx, []string{"run:alpha"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "no workflow") {
		t.Errorf("expected 'no workflow' message, got:\n%s", out.String())
	}
}

// ── /config → Teams menu CRUD (TEAMS.md §8.3) ────────────────────────────

func TestConfigMenu_TeamWizardCreatesTeam(t *testing.T) {
	cfg := teamCmdConfig()
	ctx, sr, ir, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	// Wizard step 1: name input.
	menu.addTeamWizard()
	if !strings.Contains(ir.prompt, "team name") {
		t.Fatalf("prompt = %q", ir.prompt)
	}
	ir.onSub("new-team", true)

	// Step 2: main model selector (selectModelPage shows configured models).
	if sr.onSel == nil {
		t.Fatal("expected main-model selector")
	}
	sr.onSel("m1", true)

	// Step 3: review policy selector.
	if sr.onSel == nil {
		t.Fatal("expected review-policy selector")
	}
	sr.onSel("agent", true)

	// Step 4: reviewer model selector → save.
	if sr.onSel == nil {
		t.Fatal("expected reviewer-model selector")
	}
	sr.onSel("m2", true)

	def, ok := cfg.Teams.Definitions["new-team"]
	if !ok {
		t.Fatalf("team not created: %v", cfg.Teams.Definitions)
	}
	if def.Main == nil || def.Main.Model != "m1" {
		t.Errorf("main = %+v", def.Main)
	}
	if def.Companion == nil || def.Companion.Model != "m2" {
		t.Errorf("companion = %+v", def.Companion)
	}
	if def.Review != "agent" {
		t.Errorf("review = %q", def.Review)
	}
}

// Team names are user-facing display labels: mixed case and spaces are valid
// (the router splits on ':' only, never whitespace). The wizard must accept
// them and advance to the model selector rather than re-prompting.

// Team names are user-facing display labels: mixed case and spaces are valid
// (the router splits on ':' only, never whitespace). The wizard must accept
// them and advance to the model selector rather than re-prompting.
func TestConfigMenu_TeamWizardAcceptsFriendlyName(t *testing.T) {
	for _, name := range []string{"LocalTeam", "My Team", "local-team"} {
		cfg := teamCmdConfig()
		ctx, sr, ir, _ := newMenuTestContext(t, cfg)
		menu := newConfigMenu(*ctx)

		menu.addTeamWizard()
		sr.onSel = nil // detect advance to a selector (model pick)
		ir.onSub(name, true)

		if sr.onSel == nil {
			t.Fatalf("friendly name %q did not advance to the main-model selector (prompt %q)", name, ir.prompt)
		}
	}
}

// The wizard must reject a genuinely invalid team name at input time instead
// of persisting a definition config validation would refuse on the next
// startup, and it must re-prompt with a normalized suggestion pre-filled.

// The wizard must reject a genuinely invalid team name at input time instead
// of persisting a definition config validation would refuse on the next
// startup, and it must re-prompt with a normalized suggestion pre-filled.
func TestConfigMenu_TeamWizardRejectsInvalidName(t *testing.T) {
	cfg := teamCmdConfig()
	ctx, sr, ir, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.addTeamWizard()
	sr.onSel = nil          // detect any advance to a selector (model pick)
	ir.onSub("-lead", true) // leading dash: must start alphanumeric

	if _, exists := cfg.Teams.Definitions["-lead"]; exists {
		t.Fatalf("invalid team name persisted: %v", cfg.Teams.Definitions)
	}
	// The wizard must re-prompt for a name, not advance to a selector.
	if sr.onSel != nil {
		t.Fatalf("advanced to a selector on an invalid name (title %q); want team-name re-prompt", sr.title)
	}
	if !strings.Contains(ir.prompt, "team name") {
		t.Fatalf("after invalid name prompt = %q, want team-name re-prompt", ir.prompt)
	}

	// A valid name is accepted and advances to the main-model selector.
	menu.addTeamWizard()
	ir.onSub("local-team", true)
	if sr.onSel == nil {
		t.Fatal("valid name did not advance to the main-model selector")
	}
}

func TestConfigMenu_AddMemberRejectsInvalidName(t *testing.T) {
	cfg := teamCmdConfig()
	ctx, _, ir, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.addTeamMember("alpha")
	ir.onSub("Bad Name", true)

	def := cfg.Teams.Definitions["alpha"]
	if _, exists := def.Members["Bad Name"]; exists {
		t.Fatalf("invalid member name persisted: %v", def.Members)
	}
	// Re-prompts for a member name rather than advancing to member detail.
	if !strings.Contains(ir.prompt, "member name") {
		t.Fatalf("after invalid member name prompt = %q, want member-name re-prompt", ir.prompt)
	}
}

func TestConfigMenu_AddMemberUpgradesShorthand(t *testing.T) {
	cfg := teamCmdConfig()
	ctx, _, ir, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	menu.addTeamMember("alpha")
	ir.onSub("sec-review", true)
	def := cfg.Teams.Definitions["alpha"]
	// Shorthand upgraded to canonical map, preserving main+companion.
	if len(def.Members) != 3 {
		t.Fatalf("members = %v, want main+companion+sec-review", def.Members)
	}
	if def.Members["main"].Role != "main" || def.Members["companion"].Role != "reviewer" {
		t.Errorf("upgrade roles = %+v", def.Members)
	}
	if def.Members["sec-review"].Role != "worker" {
		t.Errorf("new member role = %q, want worker", def.Members["sec-review"].Role)
	}
	if def.Main != nil || def.Companion != nil {
		t.Error("shorthand fields should be cleared after upgrade")
	}
}

func TestConfigMenu_RolePromotionDemotesPreviousMain(t *testing.T) {
	cfg := teamCmdConfig()
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	// Promote companion to main.
	menu.selectMemberRole("alpha", "companion", config.TeamMember{Model: "m2", Role: "reviewer"})
	sr.onSel("main", true)
	def := cfg.Teams.Definitions["alpha"]
	if def.Companion.Role != "main" {
		t.Errorf("companion role = %q, want main", def.Companion.Role)
	}
	if def.Main.Role != "worker" {
		t.Errorf("old main role = %q, want worker (demoted)", def.Main.Role)
	}
}
