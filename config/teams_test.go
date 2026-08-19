// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// teamTestConfig returns a config with two providers and three models for
// validation tests. Providers declare openai-compatible endpoints so the
// whole Config.Validate() passes (agentic provider checks included).
func teamTestConfig() *Config {
	return &Config{
		Providers: []ProviderConfig{
			{ID: "p1", Endpoint: "https://p1.example.com/v1/chat/completions", API: "openai-completions"},
			{ID: "p2", Endpoint: "https://p2.example.com/v1/chat/completions", API: "openai-completions"},
		},
		// Model.Provider left empty: validateAgenticModels accepts empty
		// (IsValidAgenticProvider("")), keeping the fixture focused on teams.
		Models: []ModelConfig{
			{ID: "m1"},
			{ID: "m2"},
			{ID: "m3", ThinkingLevel: "high"},
		},
	}
}

func TestTeamsYAML_ParseShorthand(t *testing.T) {
	raw := `
teams:
  active: qa-pair
  definitions:
    qa-pair:
      description: "pair"
      main: { model: m1, thinking_level: medium }
      companion: { model: m2, thinking_level: low }
      review: gated
      review_gates:
        triggers: [goal_complete, file_commit]
        quorum: any
      defaults: { autonomy: solo, turn_budget: 30 }
`
	var c Config
	if err := yaml.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Teams.Active != "qa-pair" {
		t.Errorf("active = %q, want qa-pair", c.Teams.Active)
	}
	def := c.Teams.Definitions["qa-pair"]
	if def.Main == nil || def.Main.Model != "m1" || def.Main.ThinkingLevel != "medium" {
		t.Errorf("main = %+v", def.Main)
	}
	if def.Companion == nil || def.Companion.Model != "m2" {
		t.Errorf("companion = %+v", def.Companion)
	}
	if len(def.ReviewGates.Triggers) != 2 || def.ReviewGates.Quorum != "any" {
		t.Errorf("gates = %+v", def.ReviewGates)
	}
	if def.Defaults.TurnBudget != 30 || def.Defaults.Autonomy != "solo" {
		t.Errorf("defaults = %+v", def.Defaults)
	}
}

func TestTeamsYAML_ParseWorkflow(t *testing.T) {
	raw := `
teams:
  definitions:
    crew:
      members:
        architect: { model: m1, role: main }
        coder:     { model: m2, role: worker }
        reviewer:  { model: m3, role: reviewer }
      workflow:
        - { member: architect, prompt: "Design {{.UserInput}}" }
        - { member: coder, prompt: "Implement the design" }
        - { member: reviewer, prompt: "Review the code", loop_back_to: coder, max_iterations: 3 }
`
	var c Config
	if err := yaml.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	def := c.Teams.Definitions["crew"]
	if !def.HasWorkflow() {
		t.Fatal("workflow not parsed")
	}
	if len(def.Workflow) != 3 {
		t.Fatalf("workflow stages = %d, want 3", len(def.Workflow))
	}
	if def.Workflow[0].Member != "architect" || def.Workflow[1].Member != "coder" || def.Workflow[2].Member != "reviewer" {
		t.Errorf("workflow order = %+v", def.Workflow)
	}
	last := def.Workflow[2]
	if last.LoopBackTo != "coder" || last.MaxIterations != 3 {
		t.Errorf("loop = %+v, want loop_back_to=coder max_iterations=3", last)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for a valid workflow", err)
	}
}

func TestTeamDefinition_ResolvedMembers(t *testing.T) {
	t.Run("shorthand", testResolvedShorthand)
	t.Run("canonical map defaults worker", testResolvedCanonical)
	t.Run("mixing forms is an error", testResolvedMixedForms)
}

func testResolvedShorthand(t *testing.T) {
	members, err := (TeamDefinition{Main: &TeamMember{Model: "m1"}, Companion: &TeamMember{Model: "m2"}}).ResolvedMembers()
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}
	if members[0].Name != "main" || members[0].Member.Role != TeamRoleMain {
		t.Errorf("members[0] = %+v", members[0])
	}
	if members[1].Name != "companion" || members[1].Member.Role != TeamRoleReviewer {
		t.Errorf("members[1] = %+v", members[1])
	}
}

func testResolvedCanonical(t *testing.T) {
	def := TeamDefinition{Members: map[string]TeamMember{"lead": {Model: "m1", Role: "main"}, "reviewer": {Model: "m2", Role: "reviewer"}, "helper": {Model: "m3"}}}
	members, err := def.ResolvedMembers()
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 3 {
		t.Fatalf("got %d members, want 3", len(members))
	}
	if members[0].Name != "helper" || members[0].Member.Role != TeamRoleWorker {
		t.Errorf("members[0] = %+v", members[0])
	}
}

func testResolvedMixedForms(t *testing.T) {
	def := TeamDefinition{Main: &TeamMember{Model: "m1"}, Members: map[string]TeamMember{"x": {Model: "m2", Role: "main"}}}
	if _, err := def.ResolvedMembers(); err == nil {
		t.Fatal("expected error for mixed forms")
	}
}

func TestTeamDefinition_Accessors(t *testing.T) {
	def := TeamDefinition{Members: map[string]TeamMember{
		"lead": {Model: "m1", Role: "main"},
		"r1":   {Model: "m2", Role: "reviewer"},
		"r2":   {Model: "m3", Role: "reviewer"},
		"w":    {Model: "m1"},
	}}
	main, ok := def.MainMember()
	if !ok || main.Name != "lead" {
		t.Errorf("MainMember = %+v, %v", main, ok)
	}
	if got := def.Reviewers(); len(got) != 2 || got[0].Name != "r1" || got[1].Name != "r2" {
		t.Errorf("Reviewers = %+v", got)
	}
	if got := def.Workers(); len(got) != 1 || got[0].Name != "w" {
		t.Errorf("Workers = %+v", got)
	}
}

func TestTeamDefinition_EffectiveDefaults(t *testing.T) {
	d := TeamDefinition{Companion: &TeamMember{Model: "m2"}}
	if got := d.EffectiveReview(); got != TeamReviewAgent {
		t.Errorf("EffectiveReview = %q, want agent", got)
	}
	if got := d.EffectiveQuorum(); got != TeamQuorumAll {
		t.Errorf("EffectiveQuorum = %q, want all", got)
	}
	if got := d.EffectiveDelegation(); got != TeamDelegationAgent {
		t.Errorf("EffectiveDelegation = %q, want agent", got)
	}
	solo := TeamDefinition{Main: &TeamMember{Model: "m1"}, Review: "off"}
	if got := solo.EffectiveReview(); got != "off" {
		t.Errorf("solo EffectiveReview = %q, want off", got)
	}
}

// TestValidateTeams covers every TEAMS.md §3.5 rule with a failing case.
func TestValidateTeams(t *testing.T) {
	validDef := func() TeamDefinition {
		return TeamDefinition{
			Main:      &TeamMember{Model: "m1"},
			Companion: &TeamMember{Model: "m2"},
			Review:    "agent",
		}
	}
	cases := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string // substring; empty = valid
	}{
		{name: "valid pair", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"ok": validDef()}
			c.Teams.Active = "ok"
		}},
		{name: "no teams at all is valid", mutate: func(c *Config) {}},
		{name: "rule1: bad team name", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"-lead": validDef()}
		}, wantErr: "team name must be"},
		{name: "rule2: zero mains", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Members: map[string]TeamMember{"a": {Model: "m1", Role: "reviewer"}},
			}}
		}, wantErr: "exactly one member with role 'main'"},
		{name: "rule2: two mains", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Members: map[string]TeamMember{
					"a": {Model: "m1", Role: "main"},
					"b": {Model: "m2", Role: "main"},
				},
			}}
		}, wantErr: "exactly one member with role 'main'"},
		{name: "rule3: unknown model", mutate: func(c *Config) {
			d := validDef()
			d.Main.Model = "nope"
			c.Teams.Definitions = map[string]TeamDefinition{"t": d}
		}, wantErr: `model "nope" not found`},
		{name: "rule3: unknown provider", mutate: func(c *Config) {
			d := validDef()
			d.Main.Provider = "ghost"
			c.Teams.Definitions = map[string]TeamDefinition{"t": d}
		}, wantErr: `provider "ghost" not found`},
		{name: "rule5: review without reviewer", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Main: &TeamMember{Model: "m1"}, Review: "framework",
			}}
		}, wantErr: "requires at least one reviewer"},
		{name: "rule5: off with reviewer present", mutate: func(c *Config) {
			d := validDef()
			d.Review = "off"
			c.Teams.Definitions = map[string]TeamDefinition{"t": d}
		}, wantErr: "pointless configuration"},
		{name: "rule5: bad policy", mutate: func(c *Config) {
			d := validDef()
			d.Review = "sometimes"
			c.Teams.Definitions = map[string]TeamDefinition{"t": d}
		}, wantErr: "must be one of 'off', 'agent', 'framework', 'gated'"},
		{name: "rule6: gated without triggers", mutate: func(c *Config) {
			d := validDef()
			d.Review = "gated"
			c.Teams.Definitions = map[string]TeamDefinition{"t": d}
		}, wantErr: "'gated' review requires at least one trigger"},
		{name: "rule6: unknown trigger", mutate: func(c *Config) {
			d := validDef()
			d.Review = "gated"
			d.ReviewGates.Triggers = []string{"on_blue_moon"}
			c.Teams.Definitions = map[string]TeamDefinition{"t": d}
		}, wantErr: `unknown trigger "on_blue_moon"`},
		{name: "rule6: bad quorum", mutate: func(c *Config) {
			d := validDef()
			d.ReviewGates.Quorum = "most"
			c.Teams.Definitions = map[string]TeamDefinition{"t": d}
		}, wantErr: "must be 'all' or 'any'"},
		{name: "rule7: bad delegation", mutate: func(c *Config) {
			d := validDef()
			d.Delegation = "boss"
			c.Teams.Definitions = map[string]TeamDefinition{"t": d}
		}, wantErr: "must be 'agent' or 'off'"},
		{name: "rule8: active team undefined", mutate: func(c *Config) {
			c.Teams.Active = "ghost"
		}, wantErr: `team "ghost" not defined`},
		{name: "rule9: mixed forms", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Main:    &TeamMember{Model: "m1"},
				Members: map[string]TeamMember{"b": {Model: "m2", Role: "main"}},
			}}
		}, wantErr: "cannot mix"},
		{name: "rule10: bad thinking level", mutate: func(c *Config) {
			d := validDef()
			d.Main.ThinkingLevel = "galaxy"
			c.Teams.Definitions = map[string]TeamDefinition{"t": d}
		}, wantErr: "thinking_level"},
		{name: "member role enum", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Members: map[string]TeamMember{
					"a": {Model: "m1", Role: "main"},
					"b": {Model: "m2", Role: "boss"},
				},
			}}
		}, wantErr: "must be 'main', 'reviewer', or 'worker'"},
		{name: "member model required", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Members: map[string]TeamMember{"a": {Role: "main"}},
			}}
		}, wantErr: ".model: must be set"},
		{name: "member name charset", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Members: map[string]TeamMember{"Bad One": {Model: "m1", Role: "main"}},
			}}
		}, wantErr: "member name must match"},
		// Workflow (bugs.md: member order / workflow) — architect ⇄ coder ⇄ reviewer.
		{name: "workflow: valid ordered with feedback loop", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Members: map[string]TeamMember{
					"architect": {Model: "m1", Role: "main"},
					"coder":     {Model: "m2", Role: "worker"},
					"reviewer":  {Model: "m3", Role: "reviewer"},
				},
				Workflow: []TeamWorkflowStage{
					{Member: "architect"},
					{Member: "coder"},
					{Member: "reviewer", LoopBackTo: "coder", MaxIterations: 3},
				},
			}}
		}},
		{name: "workflow: stage references unknown member", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Members:  map[string]TeamMember{"a": {Model: "m1", Role: "main"}},
				Workflow: []TeamWorkflowStage{{Member: "ghost"}},
			}}
		}, wantErr: `member: "ghost" is not a member of the team`},
		{name: "workflow: stage missing member", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Members:  map[string]TeamMember{"a": {Model: "m1", Role: "main"}},
				Workflow: []TeamWorkflowStage{{Member: ""}},
			}}
		}, wantErr: ".member: must be set"},
		{name: "workflow: duplicate member stage", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Members: map[string]TeamMember{
					"a": {Model: "m1", Role: "main"},
					"b": {Model: "m2", Role: "worker"},
				},
				Workflow: []TeamWorkflowStage{{Member: "a"}, {Member: "b"}, {Member: "a"}},
			}}
		}, wantErr: "appears more than once"},
		{name: "workflow: loop to unknown stage", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Members: map[string]TeamMember{
					"a": {Model: "m1", Role: "main"},
					"b": {Model: "m2", Role: "reviewer"},
				},
				Workflow: []TeamWorkflowStage{{Member: "a"}, {Member: "b", LoopBackTo: "ghost"}},
			}}
		}, wantErr: "does not match an earlier workflow stage"},
		{name: "workflow: forward loop rejected", mutate: func(c *Config) {
			c.Teams.Definitions = map[string]TeamDefinition{"t": {
				Members: map[string]TeamMember{
					"a": {Model: "m1", Role: "main"},
					"b": {Model: "m2", Role: "reviewer"},
				},
				Workflow: []TeamWorkflowStage{{Member: "a", LoopBackTo: "b"}, {Member: "b"}},
			}}
		}, wantErr: "does not match an earlier workflow stage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := teamTestConfig()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestIsValidTeamName(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
	}{
		{"ok", true},
		{"local-team", true},
		{"a", true},
		{"team-1", true},
		{"0abc", true},
		{"LocalTeam", true},       // mixed case: teams are display labels
		{"My Team", true},         // spaces: teams are display labels
		{"Bad_Name", true},        // underscore allowed in team names
		{"Team Alpha v2.1", true}, // dots allowed
		{"has space", true},
		{"-lead", false},  // must start alphanumeric
		{"trail-", false}, // must end alphanumeric
		{"lead_", false},  // must end alphanumeric
		{"has\ttab", false},
		{"semi:colon", false}, // colon would corrupt /team arg parsing
		{"emoji-\u2728", false},
		{"", false},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
		{strings.Repeat("a ", 32)[:63] + "b", true}, // 64 incl. internal space
	}
	for _, tc := range cases {
		if got := IsValidTeamName(tc.name); got != tc.valid {
			t.Errorf("IsValidTeamName(%q) = %v, want %v", tc.name, got, tc.valid)
		}
	}
}

func TestIsValidMemberName(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
	}{
		{"main", true},
		{"sec-review", true},
		{"0abc", true},
		{"LocalTeam", false}, // camelCase: members are pool roles
		{"Bad Name", false},  // spaces: members are pool roles
		{"Bad_Name", false},
		{"-lead", false},
		{"trail-", true}, // trailing dash allowed by [a-z0-9][a-z0-9-]{0,63}
		{"", false},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
	}
	for _, tc := range cases {
		if got := IsValidMemberName(tc.name); got != tc.valid {
			t.Errorf("IsValidMemberName(%q) = %v, want %v", tc.name, got, tc.valid)
		}
	}
}

func TestNormalizeTeamNameSlug(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"LocalTeam", "localteam"},
		{"My Team", "my-team"},
		{"  Spaced   Out  ", "spaced-out"},
		{"-lead", "lead"},
		{"trail-", "trail"},
		{"a---b___c...d", "a-b-c-d"},
		{"emoji \u2728 star", "emoji-star"},
		{"ok", "ok"},
		{"!!!", ""},
		{"", ""},
		{strings.Repeat("x", 80), strings.Repeat("x", 64)},
	}
	for _, tc := range cases {
		got := NormalizeTeamNameSlug(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeTeamNameSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got != "" && !IsValidTeamName(got) {
			t.Errorf("NormalizeTeamNameSlug(%q) = %q, which is not a valid team name", tc.in, got)
		}
	}
}

func TestValidateTeams_SkipsModelCheckWhenNoModels(t *testing.T) {
	// Early bootstrap: no models configured at all — validate like the
	// orchestrator section does (mirrors validateOrchestrator).
	c := &Config{}
	c.Teams.Definitions = map[string]TeamDefinition{"t": {
		Main:      &TeamMember{Model: "anything"},
		Companion: &TeamMember{Model: "else"},
	}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil (bootstrap skip)", err)
	}
}

func TestMergeTeams(t *testing.T) {
	base := &Config{}
	base.Teams.Definitions = map[string]TeamDefinition{
		"a": {Main: &TeamMember{Model: "m1"}},
	}
	over := &Config{}
	over.Teams.Active = "b"
	over.Teams.Definitions = map[string]TeamDefinition{
		"b": {Main: &TeamMember{Model: "m2"}},
	}
	base.DeepMerge(over)
	if base.Teams.Active != "b" {
		t.Errorf("active = %q, want b", base.Teams.Active)
	}
	if len(base.Teams.Definitions) != 2 {
		t.Errorf("definitions = %v, want a+b merged", base.Teams.Definitions)
	}
	// Scalar last-write-wins only on non-empty: merging an empty override
	// keeps the active team.
	over2 := &Config{}
	base.DeepMerge(over2)
	if base.Teams.Active != "b" {
		t.Errorf("active after empty merge = %q, want b", base.Teams.Active)
	}
}

func TestTeamMemberThinking(t *testing.T) {
	c := teamTestConfig()
	def := TeamDefinition{
		Main:      &TeamMember{Model: "m3"}, // model has saved "high"
		Companion: &TeamMember{Model: "m2"}, // no member level
	}
	main, _ := def.MainMember()
	if got := c.TeamMemberThinking(def, main); got != "high" {
		t.Errorf("main thinking = %q, want high (from model entry)", got)
	}
	reviewers := def.Reviewers()
	if got := c.TeamMemberThinking(def, reviewers[0]); got != "" {
		t.Errorf("reviewer thinking = %q, want empty (no legacy companion level set)", got)
	}
	// Legacy thinking_levels.companion applies to the first reviewer only.
	c.ThinkingLevels.Companion = "low"
	if got := c.TeamMemberThinking(def, reviewers[0]); got != "low" {
		t.Errorf("reviewer thinking = %q, want low (legacy companion)", got)
	}
	// Member override wins over everything.
	def.Companion.ThinkingLevel = "minimal"
	withOverride := def.Reviewers()
	if got := c.TeamMemberThinking(def, withOverride[0]); got != "minimal" {
		t.Errorf("reviewer thinking = %q, want minimal (member override)", got)
	}
}

func TestTeamNames(t *testing.T) {
	c := &Config{}
	c.Teams.Definitions = map[string]TeamDefinition{
		"zeta": {}, "alpha": {},
	}
	got := c.TeamNames()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Errorf("TeamNames = %v", got)
	}
}
