// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/team"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

// teamCmdConfig builds a config with two shorthand teams.
func teamCmdConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Teams.Definitions = map[string]config.TeamDefinition{
		"alpha": {
			Main:      &config.TeamMember{Model: "m1", ThinkingLevel: "high"},
			Companion: &config.TeamMember{Model: "m2", ThinkingLevel: "low"},
			Review:    "agent",
		},
		"beta": {
			Main:   &config.TeamMember{Model: "m3"},
			Review: "off",
		},
	}
	return cfg
}

// teamCmdContext builds a command context with a real TeamManager over fakes
// (headless: nil pool/review, session = in-memory fake via adapters is
// covered in internal/app; here we use a minimal session stub through
// team.NewManager with nil deps — activation reduces to definition state).
func teamCmdContext(t *testing.T, cfg *config.Config) core.Context {
	t.Helper()
	sess := &stubSession{providerID: "p0", modelID: "m0"}
	m := team.NewManager(cfg, sess, nil, nil, nil, nil)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	return core.Context{
		Config:      cfg,
		TeamManager: m,
		ConfigSaver: config.NewCascadeLoader(t.TempDir(), "", nil),
	}
}

// stubSession is a minimal team.SessionController for command tests.
type stubSession struct {
	providerID, modelID string
	mode                internal.ModeState
	thinking            string
}

func (s *stubSession) SwitchModel(pid, mid string) error   { s.providerID, s.modelID = pid, mid; return nil }
func (s *stubSession) CurrentModel() (string, string)      { return s.providerID, s.modelID }
func (s *stubSession) CurrentMode() internal.ModeState     { return s.mode }
func (s *stubSession) SetMode(ms internal.ModeState) error { s.mode = ms; return nil }
func (s *stubSession) SetThinkingLevel(l string) error     { s.thinking = l; return nil }
func (s *stubSession) CurrentThinkingLevel() string        { return s.thinking }

func TestTeamCommand_NoManager(t *testing.T) {
	ctx := core.Context{Config: teamCmdConfig()}
	var out strings.Builder
	ctx.OutputBuffer = &out
	if err := (&TeamCommand{}).Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "unavailable") {
		t.Errorf("out = %q", out.String())
	}
}

func TestTeamCommand_ActivateDirect(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	if err := (&TeamCommand{}).Run(ctx, []string{"alpha"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "Team active: alpha") {
		t.Errorf("out = %q", out.String())
	}
	if cfg.Teams.Active != "alpha" {
		t.Errorf("teams.active = %q, want alpha persisted", cfg.Teams.Active)
	}
}

func TestTeamCommand_ActivateUnknown(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	if err := (&TeamCommand{}).Run(ctx, []string{"ghost"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "ghost") || !strings.Contains(got, "alpha") {
		t.Errorf("out = %q, want missing team + defined list", got)
	}
}

func TestTeamCommand_StatusAndList(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	_ = (&TeamCommand{}).Run(ctx, []string{"list"})
	if !strings.Contains(out.String(), "alpha") || !strings.Contains(out.String(), "beta") {
		t.Errorf("list = %q", out.String())
	}
	out.Reset()
	_ = (&TeamCommand{}).Run(ctx, []string{"alpha"})
	out.Reset()
	_ = (&TeamCommand{}).Run(ctx, []string{"status"})
	if !strings.Contains(out.String(), "Team: alpha") {
		t.Errorf("status = %q", out.String())
	}
}

func TestTeamCommand_Off(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	_ = (&TeamCommand{}).Run(ctx, []string{"alpha"})
	out.Reset()
	_ = (&TeamCommand{}).Run(ctx, []string{"off"})
	if !strings.Contains(out.String(), "deactivated") {
		t.Errorf("off = %q", out.String())
	}
	if cfg.Teams.Active != "" {
		t.Errorf("teams.active = %q after off, want empty", cfg.Teams.Active)
	}
}

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

// ── /config → Teams menu CRUD (TEAMS.md §8.3) ────────────────────────────

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

// The wizard must reject a camelCase/invalid team name at input time instead
// of persisting a definition that config validation would refuse on the next
// startup (e.g. "LocalTeam").
func TestConfigMenu_TeamWizardRejectsInvalidName(t *testing.T) {
	cfg := teamCmdConfig()
	ctx, sr, ir, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.addTeamWizard()
	sr.onSel = nil // detect any advance to a selector (model pick)
	ir.onSub("LocalTeam", true)

	if _, exists := cfg.Teams.Definitions["LocalTeam"]; exists {
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

func TestConfigMenu_RemoveActiveTeamRefused(t *testing.T) {
	cfg := teamCmdConfig()
	cfg.Teams.Active = "alpha"
	ctx, _, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	menu.confirmRemoveTeam("alpha")
	if _, ok := cfg.Teams.Definitions["alpha"]; !ok {
		t.Error("active team must not be removed")
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
