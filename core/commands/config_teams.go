// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

// /config → Teams: full team-definition CRUD (TEAMS.md §8.3), mirroring the
// orchestrator-roles editor (config_orchestrator.go). All mutations persist
// via saveHomeSection on the "teams" section and are revalidated per §3.5.

func teamsLabel(cfg *config.Config) string {
	n := len(cfg.Teams.Definitions)
	if cfg.Teams.Active != "" {
		return fmt.Sprintf("%s (%d defined)", cfg.Teams.Active, n)
	}
	return fmt.Sprintf("%d defined", n)
}

// saveTeamsSection persists the teams config section to the home config.
func (m *configMenu) saveTeamsSection() {
	m.saveHomeSection([]string{"teams"}, m.ctx.Config.Teams)
	if err := m.ctx.Config.Validate(); err != nil {
		m.flash("Saved with validation warning: " + err.Error())
	}
}

// openTeams is the entry point for /config → Teams.
func (m *configMenu) openTeams() {
	m.current = m.openTeams
	cfg := m.ctx.Config
	items := []tui.SelectorItem{
		{Value: "active", Label: "Active team", Description: orNone(cfg.Teams.Active)},
		{Value: "definitions", Label: "Team definitions", Description: fmt.Sprintf("%d defined", len(cfg.Teams.Definitions))},
	}
	m.ctx.SelectOption("Teams settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		switch selected {
		case "active":
			m.open(m.openTeamsActive)
		case "definitions":
			m.open(m.openTeamsDefinitions)
		}
	})
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// openTeamsActive selects the session's active team (same set as /team).
func (m *configMenu) openTeamsActive() {
	m.current = m.openTeamsActive
	cfg := m.ctx.Config
	items := []tui.SelectorItem{{Value: "", Label: "(none)", Description: "no active team"}}
	for _, name := range cfg.TeamNames() {
		def := cfg.Teams.Definitions[name]
		items = append(items, tui.SelectorItem{Value: name, Label: name, Description: teamOneLiner(def)})
	}
	m.ctx.SelectOption("Active team:", items, cfg.Teams.Active, func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		cfg.Teams.Active = selected
		m.saveTeamsSection()
		m.openTeams()
	})
}

// openTeamsDefinitions lists defined teams with add entry point.
func (m *configMenu) openTeamsDefinitions() {
	m.current = m.openTeamsDefinitions
	cfg := m.ctx.Config
	var items []tui.SelectorItem
	for _, name := range cfg.TeamNames() {
		def := cfg.Teams.Definitions[name]
		items = append(items, tui.SelectorItem{Value: name, Label: name, Description: teamOneLiner(def)})
	}
	items = append(items, tui.SelectorItem{Value: "__add__", Label: "— add team —", Description: "define a new team"})
	m.ctx.SelectOption("Team definitions:", items, "", func(v string, ok bool) {
		if !ok || v == "" {
			m.back()
			return
		}
		if v == "__add__" {
			m.open(m.addTeamWizard)
			return
		}
		m.openTeamDetail(v)
	})
}

// openTeamDetail edits one team definition.
func (m *configMenu) openTeamDetail(name string) {
	m.current = func() { m.openTeamDetail(name) }
	cfg := m.ctx.Config
	def := cfg.Teams.Definitions[name]
	items := []tui.SelectorItem{
		{Value: "description", Label: "Description", Description: orNone(def.Description)},
		{Value: "review", Label: "Review policy", Description: def.EffectiveReview()},
		{Value: "members", Label: "Members", Description: teamMembersSummary(def)},
		{Value: "remove", Label: "Remove team", Description: name},
	}
	if def.EffectiveReview() == config.TeamReviewGated {
		items = insertSelectorItem(items, 2, tui.SelectorItem{
			Value: "gates", Label: "Gated triggers", Description: fmt.Sprintf("%s (quorum: %s)", strings.Join(def.ReviewGates.Triggers, ","), def.EffectiveQuorum()),
		})
	}
	m.ctx.SelectOption("Edit team "+name+":", items, "", func(field string, ok bool) {
		if !ok || field == "" {
			m.back()
			return
		}
		switch field {
		case "description":
			m.promptTeamField(name, "description")
		case "review":
			m.openTeamReviewPolicy(name)
		case "gates":
			m.openTeamGates(name)
		case "members":
			m.openTeamMembers(name)
		case "remove":
			m.confirmRemoveTeam(name)
		}
	})
}

func insertSelectorItem(items []tui.SelectorItem, at int, item tui.SelectorItem) []tui.SelectorItem {
	if at > len(items) {
		at = len(items)
	}
	return append(items[:at], append([]tui.SelectorItem{item}, items[at:]...)...)
}

// teamMembersSummary renders the member list compactly.
func teamMembersSummary(def config.TeamDefinition) string {
	members, err := def.ResolvedMembers()
	if err != nil {
		return "<invalid>"
	}
	parts := make([]string, 0, len(members))
	for _, rm := range members {
		parts = append(parts, rm.Name+":"+rm.Member.Role)
	}
	return strings.Join(parts, ", ")
}

// promptTeamField edits a scalar team field (description).
func (m *configMenu) promptTeamField(name, field string) {
	cfg := m.ctx.Config
	def := cfg.Teams.Definitions[name]
	m.ctx.ShowInput("Team "+field+":", def.Description, func(value string, ok bool) {
		if !ok {
			m.back()
			return
		}
		def.Description = strings.TrimSpace(value)
		cfg.Teams.Definitions[name] = def
		m.saveTeamsSection()
		m.openTeamDetail(name)
	})
}

// openTeamReviewPolicy selects the review policy.
func (m *configMenu) openTeamReviewPolicy(name string) {
	m.current = func() { m.openTeamReviewPolicy(name) }
	cfg := m.ctx.Config
	def := cfg.Teams.Definitions[name]
	items := []tui.SelectorItem{
		{Value: config.TeamReviewOff, Label: "off", Description: "no reviewer"},
		{Value: config.TeamReviewAgent, Label: "agent", Description: "main agent requests reviews"},
		{Value: config.TeamReviewFramework, Label: "framework", Description: "review after every main turn"},
		{Value: config.TeamReviewGated, Label: "gated", Description: "review only on defined triggers"},
	}
	m.ctx.SelectOption("Review policy:", items, def.EffectiveReview(), func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		def.Review = v
		cfg.Teams.Definitions[name] = def
		m.saveTeamsSection()
		m.openTeamDetail(name)
	})
}

// openTeamGates edits gated triggers (comma list) and quorum.
func (m *configMenu) openTeamGates(name string) {
	m.current = func() { m.openTeamGates(name) }
	cfg := m.ctx.Config
	def := cfg.Teams.Definitions[name]
	items := []tui.SelectorItem{
		{Value: "triggers", Label: "Triggers", Description: strings.Join(def.ReviewGates.Triggers, ",")},
		{Value: "quorum", Label: "Quorum", Description: def.EffectiveQuorum()},
	}
	m.ctx.SelectOption("Gated review:", items, "", func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		switch v {
		case "triggers":
			m.ctx.ShowInput("Triggers (comma-separated: turn_end,goal_complete,goal_turn,file_commit,run_complete):", strings.Join(def.ReviewGates.Triggers, ","), func(value string, ok bool) {
				if !ok {
					m.back()
					return
				}
				def.ReviewGates.Triggers = splitTrim(value, ",")
				cfg.Teams.Definitions[name] = def
				m.saveTeamsSection()
				m.openTeamDetail(name)
			})
		case "quorum":
			m.ctx.SelectOption("Quorum:", []tui.SelectorItem{
				{Value: config.TeamQuorumAll, Label: "all", Description: "every reviewer must PASS"},
				{Value: config.TeamQuorumAny, Label: "any", Description: "first PASS wins"},
			}, def.EffectiveQuorum(), func(q string, ok bool) {
				if !ok {
					m.back()
					return
				}
				def.ReviewGates.Quorum = q
				cfg.Teams.Definitions[name] = def
				m.saveTeamsSection()
				m.openTeamDetail(name)
			})
		}
	})
}

// openTeamMembers lists members with add entry point.
func (m *configMenu) openTeamMembers(name string) {
	m.current = func() { m.openTeamMembers(name) }
	cfg := m.ctx.Config
	def := cfg.Teams.Definitions[name]
	members, _ := def.ResolvedMembers()
	var items []tui.SelectorItem
	for _, rm := range members {
		items = append(items, tui.SelectorItem{
			Value:       rm.Name,
			Label:       rm.Name,
			Description: fmt.Sprintf("%s · %s%s", rm.Member.Role, rm.Member.Model, memberThinkingSuffix(rm.Member)),
		})
	}
	items = append(items, tui.SelectorItem{Value: "__add__", Label: "— add member —", Description: "add a member to this team"})
	m.ctx.SelectOption("Members of "+name+":", items, "", func(v string, ok bool) {
		if !ok || v == "" {
			m.back()
			return
		}
		if v == "__add__" {
			m.addTeamMember(name)
			return
		}
		m.openMemberDetail(name, v)
	})
}

func memberThinkingSuffix(mem config.TeamMember) string {
	if mem.ThinkingLevel == "" {
		return ""
	}
	return "/" + mem.ThinkingLevel
}

// teamMemberByName returns a pointer-style accessor to a member (canonical
// Members map or shorthand) for editing.
func teamMemberByName(def config.TeamDefinition, member string) (config.TeamMember, bool) {
	if len(def.Members) > 0 {
		mem, ok := def.Members[member]
		return mem, ok
	}
	switch member {
	case "main":
		if def.Main != nil {
			return *def.Main, true
		}
	case "companion":
		if def.Companion != nil {
			return *def.Companion, true
		}
	}
	return config.TeamMember{}, false
}

// setTeamMemberByName writes a member back (canonical or shorthand form).
func setTeamMemberByName(def *config.TeamDefinition, member string, mem config.TeamMember) {
	if len(def.Members) > 0 {
		def.Members[member] = mem
		return
	}
	switch member {
	case "main":
		def.Main = &mem
	case "companion":
		def.Companion = &mem
	}
}

// removeTeamMemberByName deletes a member (canonical or shorthand form).
func removeTeamMemberByName(def *config.TeamDefinition, member string) {
	if len(def.Members) > 0 {
		delete(def.Members, member)
		return
	}
	switch member {
	case "main":
		def.Main = nil
	case "companion":
		def.Companion = nil
	}
}

// openMemberDetail edits one member: model, mode, thinking, role, remove.
func (m *configMenu) openMemberDetail(teamName, member string) {
	m.current = func() { m.openMemberDetail(teamName, member) }
	cfg := m.ctx.Config
	def := cfg.Teams.Definitions[teamName]
	mem, ok := teamMemberByName(def, member)
	if !ok {
		m.flash("Member " + member + " not found")
		m.back()
		return
	}
	role := mem.Role
	if role == "" {
		role = config.TeamRoleWorker
	}
	items := []tui.SelectorItem{
		{Value: "model", Label: "Model", Description: orNone(mem.Model)},
		{Value: "mode", Label: "Mode", Description: orNone(mem.Mode)},
		{Value: "thinking", Label: "Thinking level", Description: orNone(mem.ThinkingLevel)},
		{Value: "role", Label: "Role", Description: role},
		{Value: "remove", Label: "Remove member", Description: member},
	}
	m.ctx.SelectOption(fmt.Sprintf("Edit %s.%s:", teamName, member), items, "", func(field string, ok bool) {
		if !ok || field == "" {
			m.back()
			return
		}
		switch field {
		case "model":
			m.selectModelPage("Member model:", mem.Model, func(modelID string) {
				mem.Model = modelID
				m.saveMember(teamName, member, mem)
			})
		case "mode":
			m.selectMemberMode(teamName, member, mem)
		case "thinking":
			m.selectMemberThinking(teamName, member, mem)
		case "role":
			m.selectMemberRole(teamName, member, mem)
		case "remove":
			m.confirmRemoveMember(teamName, member)
		}
	})
}

// saveMember writes a member back, persists, marks drift when the edited
// team is active, and returns to the member detail.
func (m *configMenu) saveMember(teamName, member string, mem config.TeamMember) {
	cfg := m.ctx.Config
	def := cfg.Teams.Definitions[teamName]
	setTeamMemberByName(&def, member, mem)
	cfg.Teams.Definitions[teamName] = def
	m.saveTeamsSection()
	m.markActiveTeamDrift(teamName)
	m.openMemberDetail(teamName, member)
}

// markActiveTeamDrift flags the active team drifted after a definition edit
// (TEAMS.md §8.3 live-effect semantics).
func (m *configMenu) markActiveTeamDrift(teamName string) {
	if m.ctx.Config.Teams.Active != teamName {
		return
	}
	if tm := teamManager(m.ctx); tm != nil && tm.Active() == teamName {
		tm.MarkDrift()
		m.ctx.FooterRefresh()
	}
}

// selectMemberMode picks a behavioral mode from the mode registry.
func (m *configMenu) selectMemberMode(teamName, member string, mem config.TeamMember) {
	items := []tui.SelectorItem{{Value: "", Label: "(inherit)", Description: "no mode override"}}
	if m.ctx.ModeRegistry != nil {
		majors := m.ctx.ModeRegistry.Majors()
		sort.Slice(majors, func(i, j int) bool { return majors[i] < majors[j] })
		for _, mj := range majors {
			items = append(items, tui.SelectorItem{Value: string(mj), Label: string(mj)})
		}
	}
	m.ctx.SelectOption("Member mode:", items, mem.Mode, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		mem.Mode = v
		m.saveMember(teamName, member, mem)
	})
}

// selectMemberThinking picks a thinking level (or inherit).
func (m *configMenu) selectMemberThinking(teamName, member string, mem config.TeamMember) {
	items := []tui.SelectorItem{{Value: "", Label: "(inherit)", Description: "model/role default (§3.6)"}}
	for _, lvl := range internal.AllThinkingLevels() {
		items = append(items, tui.SelectorItem{Value: string(lvl), Label: string(lvl)})
	}
	m.ctx.SelectOption("Member thinking level:", items, mem.ThinkingLevel, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		mem.ThinkingLevel = v
		m.saveMember(teamName, member, mem)
	})
}

// selectMemberRole picks the role tag; promoting to main demotes the
// previous main to worker after the write (validation keeps exactly one).
func (m *configMenu) selectMemberRole(teamName, member string, mem config.TeamMember) {
	role := mem.Role
	if role == "" {
		role = config.TeamRoleWorker
	}
	items := []tui.SelectorItem{
		{Value: config.TeamRoleMain, Label: "main", Description: "drives turns (exactly one per team)"},
		{Value: config.TeamRoleReviewer, Label: "reviewer", Description: "reviews the main member's output"},
		{Value: config.TeamRoleWorker, Label: "worker", Description: "delegatable specialist"},
	}
	m.ctx.SelectOption("Member role:", items, role, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if v == config.TeamRoleMain {
			m.demoteExistingMain(teamName, member)
		}
		mem.Role = v
		m.saveMember(teamName, member, mem)
	})
}

// demoteExistingMain sets any current main member (other than keepMember)
// to worker, so promoting a new main keeps exactly one.
func (m *configMenu) demoteExistingMain(teamName, keepMember string) {
	cfg := m.ctx.Config
	def := cfg.Teams.Definitions[teamName]
	members, err := def.ResolvedMembers()
	if err != nil {
		return
	}
	for _, rm := range members {
		if rm.Member.Role == config.TeamRoleMain && rm.Name != keepMember {
			demoted := rm.Member
			demoted.Role = config.TeamRoleWorker
			setTeamMemberByName(&def, rm.Name, demoted)
		}
	}
	cfg.Teams.Definitions[teamName] = def
}

// addTeamMember prompts for a member name then opens its detail for editing.
func (m *configMenu) addTeamMember(teamName string) {
	m.current = func() { m.addTeamMember(teamName) }
	m.ctx.ShowInput("New member name (e.g. architect, sec-review):", "", func(value string, ok bool) {
		if !ok {
			m.back()
			return
		}
		name := strings.TrimSpace(value)
		cfg := m.ctx.Config
		def := cfg.Teams.Definitions[teamName]
		if _, exists := teamMemberByName(def, name); exists {
			m.flash("Member " + name + " already exists")
			m.openTeamMembers(teamName)
			return
		}
		// Shorthand teams upgrade to the canonical members map on first add
		// (the shorthand only holds main+companion).
		if len(def.Members) == 0 {
			def.Members = map[string]config.TeamMember{}
			if def.Main != nil {
				main := *def.Main
				main.Role = config.TeamRoleMain
				def.Members["main"] = main
				def.Main = nil
			}
			if def.Companion != nil {
				comp := *def.Companion
				comp.Role = config.TeamRoleReviewer
				def.Members["companion"] = comp
				def.Companion = nil
			}
		}
		def.Members[name] = config.TeamMember{Role: config.TeamRoleWorker}
		cfg.Teams.Definitions[teamName] = def
		m.saveTeamsSection()
		m.openMemberDetail(teamName, name)
	})
}

// confirmRemoveMember deletes a member after confirmation; removing the last
// main is refused (validation would reject the team).
func (m *configMenu) confirmRemoveMember(teamName, member string) {
	m.current = func() { m.confirmRemoveMember(teamName, member) }
	cfg := m.ctx.Config
	def := cfg.Teams.Definitions[teamName]
	mem, _ := teamMemberByName(def, member)
	if mem.Role == config.TeamRoleMain {
		m.flash("Cannot remove the main member — promote another member to main first")
		m.openTeamMembers(teamName)
		return
	}
	items := []tui.SelectorItem{
		{Value: "yes", Label: "Remove " + member},
		{Value: "no", Label: "Cancel"},
	}
	m.ctx.SelectOption("Remove member "+member+"?", items, "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			m.openTeamMembers(teamName)
			return
		}
		removeTeamMemberByName(&def, member)
		cfg.Teams.Definitions[teamName] = def
		m.saveTeamsSection()
		m.openTeamMembers(teamName)
	})
}

// confirmRemoveTeam deletes a team; refused while the team is teams.active
// or bound to a queued goal (§8.3).
func (m *configMenu) confirmRemoveTeam(name string) {
	m.current = func() { m.confirmRemoveTeam(name) }
	cfg := m.ctx.Config
	if cfg.Teams.Active == name {
		m.flash("Cannot remove the active team — deactivate it first (/team:off)")
		m.openTeamDetail(name)
		return
	}
	items := []tui.SelectorItem{
		{Value: "yes", Label: "Remove " + name},
		{Value: "no", Label: "Cancel"},
	}
	m.ctx.SelectOption("Remove team "+name+"?", items, "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			m.openTeamDetail(name)
			return
		}
		delete(cfg.Teams.Definitions, name)
		m.saveTeamsSection()
		m.openTeamsDefinitions()
	})
}

// addTeamWizard creates a team: name → main model → review policy → reviewer
// model (when policy ≠ off) → save (§8.3). N-member teams are reachable by
// adding members afterwards in the detail view.
func (m *configMenu) addTeamWizard() {
	m.current = m.addTeamWizard
	m.ctx.ShowInput("New team name ([a-z0-9-]):", "", func(name string, ok bool) {
		if !ok {
			m.back()
			return
		}
		name = strings.TrimSpace(name)
		cfg := m.ctx.Config
		if _, exists := cfg.Teams.Definitions[name]; exists {
			m.flash("Team " + name + " already exists")
			m.openTeamsDefinitions()
			return
		}
		m.wizardMainModel(name)
	})
}

func (m *configMenu) wizardMainModel(teamName string) {
	m.selectModelPage("Main member model:", m.ctx.Config.ActiveModel, func(modelID string) {
		m.wizardReviewPolicy(teamName, modelID)
	})
}

func (m *configMenu) wizardReviewPolicy(teamName, mainModel string) {
	items := []tui.SelectorItem{
		{Value: config.TeamReviewAgent, Label: "agent", Description: "main agent requests reviews"},
		{Value: config.TeamReviewFramework, Label: "framework", Description: "review every turn"},
		{Value: config.TeamReviewGated, Label: "gated", Description: "review on triggers only"},
		{Value: config.TeamReviewOff, Label: "off", Description: "solo team (no reviewer)"},
	}
	m.ctx.SelectOption("Review policy:", items, config.TeamReviewAgent, func(policy string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if policy == config.TeamReviewOff {
			m.wizardSave(teamName, mainModel, policy, "")
			return
		}
		m.wizardReviewerModel(teamName, mainModel, policy)
	})
}

func (m *configMenu) wizardReviewerModel(teamName, mainModel, policy string) {
	m.selectModelPage("Reviewer member model:", "", func(modelID string) {
		m.wizardSave(teamName, mainModel, policy, modelID)
	})
}

// wizardSave writes the shorthand-shaped team, persists, and opens detail.
func (m *configMenu) wizardSave(teamName, mainModel, policy, reviewerModel string) {
	cfg := m.ctx.Config
	if cfg.Teams.Definitions == nil {
		cfg.Teams.Definitions = map[string]config.TeamDefinition{}
	}
	def := config.TeamDefinition{
		Main:   &config.TeamMember{Model: mainModel},
		Review: policy,
	}
	if reviewerModel != "" {
		def.Companion = &config.TeamMember{Model: reviewerModel}
	}
	if policy == config.TeamReviewGated {
		def.ReviewGates = config.TeamReviewGates{Triggers: []string{config.TeamTriggerGoalComplete}}
	}
	cfg.Teams.Definitions[teamName] = def
	m.saveTeamsSection()
	m.openTeamDetail(teamName)
}
