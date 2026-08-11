// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/pijalu/goa/internal"
)

// Team member role tags (TEAMS.md §2).
const (
	TeamRoleMain     = "main"
	TeamRoleReviewer = "reviewer"
	TeamRoleWorker   = "worker"
)

// Team review policies (TEAMS.md §3.1).
const (
	TeamReviewOff       = "off"
	TeamReviewAgent     = "agent"
	TeamReviewFramework = "framework"
	TeamReviewGated     = "gated"
)

// Team gated-review triggers (TEAMS.md §3.2).
const (
	TeamTriggerTurnEnd      = "turn_end"
	TeamTriggerGoalComplete = "goal_complete"
	TeamTriggerGoalTurn     = "goal_turn"
	TeamTriggerFileCommit   = "file_commit"
	TeamTriggerRunComplete  = "run_complete"
)

// Team multi-reviewer quorum values (TEAMS.md §3.3).
const (
	TeamQuorumAll = "all"
	TeamQuorumAny = "any"
)

// Team worker-delegation values (TEAMS.md §3.4).
const (
	TeamDelegationAgent = "agent"
	TeamDelegationOff   = "off"
)

// teamNamePattern matches team and member names (TEAMS.md §3.5 rule 1–2).
var teamNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// TeamsConfig is the top-level `teams:` section (TEAMS.md §3): the session's
// active team plus the named team definitions.
type TeamsConfig struct {
	Active      string                    `yaml:"active,omitempty"`
	Definitions map[string]TeamDefinition `yaml:"definitions,omitempty"`
}

// TeamDefinition is a named agent team: a set of members (main shorthand +
// companion shorthand, or the canonical Members map), a review policy, and
// defaults applied on activation.
type TeamDefinition struct {
	Description string                `yaml:"description,omitempty"`
	Main        *TeamMember           `yaml:"main,omitempty"`
	Companion   *TeamMember           `yaml:"companion,omitempty"`
	Members     map[string]TeamMember `yaml:"members,omitempty"`
	Review      string                `yaml:"review,omitempty"`
	ReviewGates TeamReviewGates       `yaml:"review_gates,omitempty"`
	Delegation  string                `yaml:"delegation,omitempty"`
	Defaults    TeamDefaults          `yaml:"defaults,omitempty"`
}

// TeamMember is a named model binding: model ID, optional provider override,
// behavioral mode, per-member thinking level, and a role tag (main /
// reviewer / worker — empty means worker in the canonical Members form).
type TeamMember struct {
	Model         string `yaml:"model"`
	Provider      string `yaml:"provider,omitempty"`
	Mode          string `yaml:"mode,omitempty"`
	ThinkingLevel string `yaml:"thinking_level,omitempty"`
	Role          string `yaml:"role,omitempty"`
}

// TeamReviewGates configures the gated review policy (TEAMS.md §3.2–3.3).
type TeamReviewGates struct {
	Triggers []string `yaml:"triggers,omitempty"`
	Quorum   string   `yaml:"quorum,omitempty"`
}

// TeamDefaults holds optional session defaults applied on team activation.
type TeamDefaults struct {
	Autonomy   string `yaml:"autonomy,omitempty"`
	TurnBudget int    `yaml:"turn_budget,omitempty"`
}

// ResolvedMember is a team member after shorthand normalization: a stable
// member name plus the member definition with an explicit role tag.
type ResolvedMember struct {
	Name   string
	Member TeamMember
}

// ResolvedMembers normalizes a team definition into its canonical member
// list (TEAMS.md §3 normalization): Main → member "main" (role main),
// Companion → member "companion" (role reviewer); Members entries pass
// through with empty Role defaulted to worker. The result is sorted by name
// for deterministic iteration. Mixing shorthand with a Members map is an
// error (§3.5 rule 9).
func (d TeamDefinition) ResolvedMembers() ([]ResolvedMember, error) {
	if len(d.Members) > 0 && (d.Main != nil || d.Companion != nil) {
		return nil, errors.New("cannot mix 'members' with 'main'/'companion' shorthand")
	}
	if len(d.Members) > 0 {
		out := make([]ResolvedMember, 0, len(d.Members))
		for name, m := range d.Members {
			if m.Role == "" {
				m.Role = TeamRoleWorker
			}
			out = append(out, ResolvedMember{Name: name, Member: m})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out, nil
	}
	var out []ResolvedMember
	if d.Main != nil {
		m := *d.Main
		m.Role = TeamRoleMain
		out = append(out, ResolvedMember{Name: TeamRoleMain, Member: m})
	}
	if d.Companion != nil {
		m := *d.Companion
		m.Role = TeamRoleReviewer
		out = append(out, ResolvedMember{Name: "companion", Member: m})
	}
	return out, nil
}

// MainMember returns the normalized main member, or false when none exists.
func (d TeamDefinition) MainMember() (ResolvedMember, bool) {
	members, err := d.ResolvedMembers()
	if err != nil {
		return ResolvedMember{}, false
	}
	for _, rm := range members {
		if rm.Member.Role == TeamRoleMain {
			return rm, true
		}
	}
	return ResolvedMember{}, false
}

// Reviewers returns the normalized reviewer members in deterministic order.
func (d TeamDefinition) Reviewers() []ResolvedMember {
	members, err := d.ResolvedMembers()
	if err != nil {
		return nil
	}
	var out []ResolvedMember
	for _, rm := range members {
		if rm.Member.Role == TeamRoleReviewer {
			out = append(out, rm)
		}
	}
	return out
}

// Workers returns the normalized worker members in deterministic order.
func (d TeamDefinition) Workers() []ResolvedMember {
	members, err := d.ResolvedMembers()
	if err != nil {
		return nil
	}
	var out []ResolvedMember
	for _, rm := range members {
		if rm.Member.Role == TeamRoleWorker {
			out = append(out, rm)
		}
	}
	return out
}

// EffectiveReview returns the review policy with the default applied:
// "agent" when a reviewer exists and no policy is set (TEAMS.md §3.5 rule 5).
func (d TeamDefinition) EffectiveReview() string {
	if d.Review != "" {
		return d.Review
	}
	if len(d.Reviewers()) > 0 {
		return TeamReviewAgent
	}
	return ""
}

// EffectiveQuorum returns the review-round quorum with the default "all"
// applied (TEAMS.md §3.3).
func (d TeamDefinition) EffectiveQuorum() string {
	if d.ReviewGates.Quorum != "" {
		return d.ReviewGates.Quorum
	}
	return TeamQuorumAll
}

// EffectiveDelegation returns the worker-delegation setting with the default
// "agent" applied (TEAMS.md §3.4).
func (d TeamDefinition) EffectiveDelegation() string {
	if d.Delegation != "" {
		return d.Delegation
	}
	return TeamDelegationAgent
}

// validateTeams enforces TEAMS.md §3.5 rules 1–10.
func (c *Config) validateTeams(ve *internal.ValidationError) {
	tc := c.Teams
	if tc.Active != "" {
		if _, ok := tc.Definitions[tc.Active]; !ok {
			ve.Add(fmt.Sprintf("teams.active: team %q not defined in teams.definitions", tc.Active))
		}
	}
	// When no models are configured at all (early bootstrap), skip model
	// existence checks so an empty embedded config validates cleanly —
	// mirrors validateOrchestrator.
	skipModelCheck := len(c.Models) == 0
	knownModels := make(map[string]struct{}, len(c.Models))
	for _, m := range c.Models {
		knownModels[m.ID] = struct{}{}
	}
	knownProviders := make(map[string]struct{}, len(c.Providers))
	for _, p := range c.Providers {
		knownProviders[p.ID] = struct{}{}
	}
	for name, def := range tc.Definitions {
		c.validateTeamDefinition(ve, name, def, skipModelCheck, knownModels, knownProviders)
	}
}

// validateTeamDefinition validates one team definition.
func (c *Config) validateTeamDefinition(ve *internal.ValidationError, name string, def TeamDefinition, skipModelCheck bool, knownModels, knownProviders map[string]struct{}) {
	prefix := "teams.definitions." + name
	if !teamNamePattern.MatchString(name) {
		ve.Add(prefix + ": team name must match [a-z0-9][a-z0-9-]{0,63}")
	}
	members, err := def.ResolvedMembers()
	if err != nil {
		ve.Add(prefix + ": " + err.Error())
		return
	}
	validateTeamReview(ve, prefix, def)
	c.validateTeamMembers(ve, prefix, members, skipModelCheck, knownModels, knownProviders)
}

// validateTeamReview checks the review policy, gates, quorum, and delegation.
func validateTeamReview(ve *internal.ValidationError, prefix string, def TeamDefinition) {
	review := def.EffectiveReview()
	switch review {
	case "", TeamReviewOff:
		if len(def.Reviewers()) > 0 {
			ve.Add(prefix + ".review: 'off' with reviewer members present is a pointless configuration")
		}
	case TeamReviewAgent, TeamReviewFramework, TeamReviewGated:
		if len(def.Reviewers()) == 0 {
			ve.Add(fmt.Sprintf("%s.review: %q requires at least one reviewer member", prefix, review))
		}
	default:
		ve.Add(fmt.Sprintf("%s.review: must be one of 'off', 'agent', 'framework', 'gated' (got %q)", prefix, def.Review))
	}
	if review == TeamReviewGated && len(def.ReviewGates.Triggers) == 0 {
		ve.Add(prefix + ".review_gates.triggers: 'gated' review requires at least one trigger")
	}
	for _, t := range def.ReviewGates.Triggers {
		switch t {
		case TeamTriggerTurnEnd, TeamTriggerGoalComplete, TeamTriggerGoalTurn, TeamTriggerFileCommit, TeamTriggerRunComplete:
		default:
			ve.Add(fmt.Sprintf("%s.review_gates.triggers: unknown trigger %q", prefix, t))
		}
	}
	switch def.ReviewGates.Quorum {
	case "", TeamQuorumAll, TeamQuorumAny:
	default:
		ve.Add(fmt.Sprintf("%s.review_gates.quorum: must be 'all' or 'any' (got %q)", prefix, def.ReviewGates.Quorum))
	}
	switch def.Delegation {
	case "", TeamDelegationAgent, TeamDelegationOff:
	default:
		ve.Add(fmt.Sprintf("%s.delegation: must be 'agent' or 'off' (got %q)", prefix, def.Delegation))
	}
}

// validateTeamMembers checks the normalized member list: exactly one main,
// valid member names, resolvable models/providers, valid thinking levels.
// Mode names are NOT validated: custom modes live in prompt directories
// resolved at runtime, outside config's visibility (TEAMS-PLAN Phase 1 note).
func (c *Config) validateTeamMembers(ve *internal.ValidationError, prefix string, members []ResolvedMember, skipModelCheck bool, knownModels, knownProviders map[string]struct{}) {
	mains := 0
	for _, rm := range members {
		if rm.Member.Role == TeamRoleMain {
			mains++
		}
		c.validateTeamMember(ve, prefix+".members."+rm.Name, rm, skipModelCheck, knownModels, knownProviders)
	}
	if mains != 1 {
		ve.Add(fmt.Sprintf("%s: exactly one member with role 'main' is required (got %d)", prefix, mains))
	}
}

// validateTeamMember validates a single normalized member.
func (c *Config) validateTeamMember(ve *internal.ValidationError, mp string, rm ResolvedMember, skipModelCheck bool, knownModels, knownProviders map[string]struct{}) {
	if !teamNamePattern.MatchString(rm.Name) {
		ve.Add(mp + ": member name must match [a-z0-9][a-z0-9-]{0,63}")
	}
	switch rm.Member.Role {
	case TeamRoleMain, TeamRoleReviewer, TeamRoleWorker:
	default:
		ve.Add(fmt.Sprintf("%s.role: must be 'main', 'reviewer', or 'worker' (got %q)", mp, rm.Member.Role))
	}
	if rm.Member.Model == "" {
		ve.Add(mp + ".model: must be set")
	} else if !skipModelCheck {
		if _, ok := knownModels[rm.Member.Model]; !ok {
			ve.Add(fmt.Sprintf("%s.model: model %q not found in models list", mp, rm.Member.Model))
		}
	}
	if rm.Member.Provider != "" {
		if _, ok := knownProviders[rm.Member.Provider]; !ok {
			ve.Add(fmt.Sprintf("%s.provider: provider %q not found in providers list", mp, rm.Member.Provider))
		}
	}
	if rm.Member.ThinkingLevel != "" && !internal.IsValidThinkingLevel(rm.Member.ThinkingLevel) {
		ve.Add(fmt.Sprintf("%s.thinking_level: must be one of off, minimal, low, medium, high, xhigh (got %q)", mp, rm.Member.ThinkingLevel))
	}
}

// mergeTeams merges the teams section: definitions merge per key (map
// last-write-wins like orchestrator roles), the active team adopts any
// non-empty override.
func (c *Config) mergeTeams(other *Config) {
	if other.Teams.Definitions != nil {
		if c.Teams.Definitions == nil {
			c.Teams.Definitions = make(map[string]TeamDefinition)
		}
		for name, def := range other.Teams.Definitions {
			c.Teams.Definitions[name] = def
		}
	}
	if other.Teams.Active != "" {
		c.Teams.Active = other.Teams.Active
	}
}

// TeamNames returns the defined team names in sorted order (completion,
// error messages).
func (c *Config) TeamNames() []string {
	names := make([]string, 0, len(c.Teams.Definitions))
	for name := range c.Teams.Definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveTeamMemberThinking applies TEAMS.md §3.6 resolution for a member:
// member override wins; for the first reviewer the legacy
// thinking_levels.companion applies; then the model's own level; the global
// default/medium fallback is left to GetThinkingLevel callers.
func (c *Config) resolveTeamMemberThinking(m TeamMember, firstReviewer bool) string {
	if m.ThinkingLevel != "" {
		return m.ThinkingLevel
	}
	if firstReviewer && c.ThinkingLevels.Companion != "" {
		return c.ThinkingLevels.Companion
	}
	return c.modelThinkingLevel(m.Model)
}

// TeamMemberThinking exposes §3.6 resolution for the TeamManager: the
// effective thinking level for a normalized member (empty = inherit default
// chain downstream).
func (c *Config) TeamMemberThinking(def TeamDefinition, rm ResolvedMember) string {
	firstReviewer := false
	if rm.Member.Role == TeamRoleReviewer {
		reviewers := def.Reviewers()
		firstReviewer = len(reviewers) > 0 && reviewers[0].Name == rm.Name
	}
	return c.resolveTeamMemberThinking(rm.Member, firstReviewer)
}
