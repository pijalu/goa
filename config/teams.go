// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

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

// memberNamePattern matches member names (TEAMS.md §3.5 rule 2). Member
// names double as agent-pool roles: they are registered via
// pool.SetConfig(role) and referenced by the main agent's delegation tools
// (delegate_to role:"<member-name>"), so they keep the strict DNS-label
// charset the LLM can reproduce reliably.
var memberNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// teamNamePattern matches team names (TEAMS.md §3.5 rule 1). Team names are
// user-facing display labels: they only serve as config map keys and /team
// command arguments (the command router splits on ':' only, never on
// whitespace), so they allow a friendly charset — any letters, digits,
// spaces, dots, underscores and dashes — as long as they start and end with
// an alphanumeric and stay within 64 bytes.
var teamNamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9 ._-]{0,62}[A-Za-z0-9])?$`)

// maxTeamNameLen bounds team names (bytes), mirroring the member rule.
const maxTeamNameLen = 64

// IsValidTeamName reports whether name satisfies the team naming rule
// (TEAMS.md §3.5 rule 1). Team names are display labels (map keys + /team
// args), so the charset is permissive: letters (either case), digits, spaces,
// dots, underscores, dashes; must start and end alphanumeric; 1–64 bytes.
// Interactive entry points (config menu, /team) must reject a non-conforming
// name at input time instead of persisting a definition that config
// validation would then refuse on the next startup.
func IsValidTeamName(name string) bool {
	return len(name) <= maxTeamNameLen && teamNamePattern.MatchString(name)
}

// IsValidMemberName reports whether name satisfies the member naming rule
// (TEAMS.md §3.5 rule 2): the strict DNS-label charset, because member names
// double as agent-pool roles referenced by the delegation tools.
func IsValidMemberName(name string) bool {
	return memberNamePattern.MatchString(name)
}

// NormalizeTeamNameSlug converts a user-typed team name into a valid team
// name: lowercase, whitespace collapsed to single dashes, unsupported
// characters dropped, leading/trailing dashes trimmed, truncated to 64 bytes.
// It returns "" when nothing usable remains (e.g. input was only symbols).
// Interactive flows use it to suggest a corrected name instead of a bare
// rejection.
func NormalizeTeamNameSlug(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	b.Grow(len(name))
	pendingDash := false
	for _, r := range name {
		lower, ok := slugRune(r)
		if !ok {
			// Separator or unsupported character: collapse any run into one dash,
			// emitted lazily so a trailing separator never yields a trailing dash.
			pendingDash = true
			continue
		}
		if pendingDash && b.Len() > 0 {
			b.WriteByte('-')
		}
		pendingDash = false
		b.WriteRune(lower)
	}
	out := b.String()
	if len(out) > maxTeamNameLen {
		out = strings.TrimRight(out[:maxTeamNameLen], "-")
	}
	return out
}

// slugRune maps r to the lowercase rune kept in a team-name slug, or
// ok=false when r is a separator/unsupported character (collapsed to a dash).
func slugRune(r rune) (lower rune, ok bool) {
	switch {
	case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return r, true
	case r >= 'A' && r <= 'Z':
		return r + ('a' - 'A'), true
	}
	return 0, false
}

// TeamsConfig is the top-level `teams:` section (TEAMS.md §3): the session's
// active team plus the named team definitions.
type TeamsConfig struct {
	Active      string                    `yaml:"active,omitempty"`
	Definitions map[string]TeamDefinition `yaml:"definitions,omitempty"`
}

// TeamDefinition is a named agent team: a set of members (main shorthand +
// companion shorthand, or the canonical Members map), a review policy, an
// optional ordered workflow, and defaults applied on activation.
type TeamDefinition struct {
	Description string                `yaml:"description,omitempty"`
	Main        *TeamMember           `yaml:"main,omitempty"`
	Companion   *TeamMember           `yaml:"companion,omitempty"`
	Members     map[string]TeamMember `yaml:"members,omitempty"`
	Review      string                `yaml:"review,omitempty"`
	ReviewGates TeamReviewGates       `yaml:"review_gates,omitempty"`
	Delegation  string                `yaml:"delegation,omitempty"`
	Workflow    []TeamWorkflowStage   `yaml:"workflow,omitempty"`
	Defaults    TeamDefaults          `yaml:"defaults,omitempty"`
}

// TeamWorkflowStage is one ordered step of a team's workflow (bugs.md "team:
// allow defining member order / workflow"). Stages run in list order; each
// hands its output to the next. A stage with LoopBackTo set forms a feedback
// loop: after it completes, control returns to the named earlier stage (e.g.
// a reviewer sends work back to the coder) and the intervening stages repeat,
// bounded by MaxIterations — this is the architect ⇄ coder ⇄ reviewer cycle.
type TeamWorkflowStage struct {
	// Member is the team member (pool role) that runs this stage. It must
	// reference a defined member (main/reviewer/worker) of the team.
	Member string `yaml:"member"`
	// Prompt is the stage instruction. It is rendered as a Go template with
	// the user's task as {{.UserInput}} and the accumulated prior-stage output
	// prepended as context. Empty falls back to a sensible per-member default.
	Prompt string `yaml:"prompt,omitempty"`
	// LoopBackTo, when set, names an EARLIER stage in the same workflow: after
	// this stage completes the loop returns there. Must point backward
	// (no forward/self loops) so the workflow always has a defined entry.
	LoopBackTo string `yaml:"loop_back_to,omitempty"`
	// MaxIterations caps how many times the loop back-edge may fire (0 = run
	// the loop once, i.e. no repeat). Prevents unbounded reviewer⇄coder cycles.
	MaxIterations int `yaml:"max_iterations,omitempty"`
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

// HasWorkflow reports whether the team defines an ordered member workflow.
func (d TeamDefinition) HasWorkflow() bool { return len(d.Workflow) > 0 }

// ValidateWorkflow checks the team's workflow structurally (member refs,
// uniqueness, backward loop targets) and returns a single error, or nil. It is
// the programmatic counterpart of the config-validation path, used by
// /team:run before building the pipeline.
func (d TeamDefinition) ValidateWorkflow() error {
	if len(d.Workflow) == 0 {
		return nil
	}
	members, err := d.ResolvedMembers()
	if err != nil {
		return err
	}
	ve := &internal.ValidationError{}
	validateTeamWorkflow(ve, "workflow", d, members)
	if ve.HasErrors() {
		return fmt.Errorf("%s", ve.ErrList[0])
	}
	return nil
}

// validateWorkflow checks the team's ordered workflow (bugs.md "team: member
// order / workflow"): every stage must reference a defined member, stage
// members must be unique, and loop_back_to must point to an earlier stage in
// the list (so the workflow has a well-defined entry and loops are backward).
func validateTeamWorkflow(ve *internal.ValidationError, prefix string, def TeamDefinition, members []ResolvedMember) {
	if len(def.Workflow) == 0 {
		return
	}
	known := make(map[string]struct{}, len(members))
	for _, rm := range members {
		known[rm.Name] = struct{}{}
	}
	seen := make(map[string]int, len(def.Workflow)) // stage member -> index
	for i, s := range def.Workflow {
		sp := fmt.Sprintf("%s.workflow[%d]", prefix, i)
		validateWorkflowStage(ve, sp, s, known, seen, i)
		seen[s.Member] = i
	}
}

// validateWorkflowStage validates one workflow stage against the team's
// member set and the set of stages seen so far (for backward loop targets).
func validateWorkflowStage(ve *internal.ValidationError, sp string, s TeamWorkflowStage, known map[string]struct{}, seen map[string]int, idx int) {
	if s.Member == "" {
		ve.Add(sp + ".member: must be set")
		return
	}
	if _, ok := known[s.Member]; !ok {
		ve.Add(fmt.Sprintf("%s.member: %q is not a member of the team", sp, s.Member))
	}
	if _, dup := seen[s.Member]; dup {
		ve.Add(fmt.Sprintf("%s.member: member %q appears more than once in the workflow", sp, s.Member))
	}
	if s.LoopBackTo == "" {
		return
	}
	target, ok := seen[s.LoopBackTo]
	if !ok {
		ve.Add(fmt.Sprintf("%s.loop_back_to: %q does not match an earlier workflow stage", sp, s.LoopBackTo))
		return
	}
	if target >= idx {
		ve.Add(fmt.Sprintf("%s.loop_back_to: must point to an earlier stage (got %q at index %d, current %d)", sp, s.LoopBackTo, target, idx))
	}
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
	if !IsValidTeamName(name) {
		ve.Add(prefix + ": team name must be 1–64 chars of letters, digits, spaces, '.', '_' or '-', starting and ending alphanumeric")
	}
	members, err := def.ResolvedMembers()
	if err != nil {
		ve.Add(prefix + ": " + err.Error())
		return
	}
	validateTeamReview(ve, prefix, def)
	c.validateTeamMembers(ve, prefix, members, skipModelCheck, knownModels, knownProviders)
	validateTeamWorkflow(ve, prefix, def, members)
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
	if !IsValidMemberName(rm.Name) {
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