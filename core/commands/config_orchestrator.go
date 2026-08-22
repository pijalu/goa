// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tui"
)

// saveOrchestratorSection persists the orchestrator config section to the
// home config, preserving other home settings (no full-config dump).
func (m *configMenu) saveOrchestratorSection() {
	m.saveHomeSection([]string{"orchestrator"}, m.ctx.Config.Orchestrator)
}

// saveGoalsSection persists the goals config section to the home config,
// preserving other home settings (no full-config dump).
func (m *configMenu) saveGoalsSection() {
	m.saveHomeSection([]string{"goals"}, m.ctx.Config.Goals)
}

// openOrchestratorMenu is the entry point for /config -> Orchestrator.
func (m *configMenu) openOrchestrator() {
	m.current = m.openOrchestrator
	cfg := m.ctx.Config
	items := []tui.SelectorItem{
		{Value: "roles", Label: "Roles", Description: fmt.Sprintf("%d configured", len(cfg.Orchestrator.Roles))},
		{Value: "pool", Label: "Pool", Description: poolLabel(cfg.Orchestrator.Pool)},
		{Value: "defaults", Label: "Defaults", Description: cfg.Orchestrator.Defaults.Topology},
		{Value: "retention", Label: "Retention", Description: retentionLabel(cfg.Orchestrator.Retention)},
	}
	m.ctx.SelectOption("Orchestrator settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		switch selected {
		case "roles":
			m.open(m.openOrchestratorRoles)
		case "pool":
			m.open(m.openOrchestratorPool)
		case "defaults":
			m.open(m.openOrchestratorDefaults)
		case "retention":
			m.open(m.openOrchestratorRetention)
		}
	})
}

func poolLabel(p config.OrchestratorPoolConfig) string {
	if p.MaxTotalAgents <= 0 {
		return "unlimited total"
	}
	return fmt.Sprintf("max %d agents", p.MaxTotalAgents)
}

func retentionLabel(r config.OrchestratorRetentionConfig) string {
	if !r.Enabled || r.Days <= 0 {
		return "never"
	}
	return fmt.Sprintf("%d days", r.Days)
}

func orchestratorLabel(cfg *config.Config) string {
	return fmt.Sprintf("%d roles, %s", len(cfg.Orchestrator.Roles), cfg.Orchestrator.Defaults.Topology)
}

// --- roles -------------------------------------------------------------------

func (m *configMenu) openOrchestratorRoles() {
	m.current = m.openOrchestratorRoles
	items := orchestratorRoleItems(m.ctx.Config)
	items = append(items, tui.SelectorItem{
		Value:       "__add__",
		Label:       "— add role —",
		Description: "configure a new orchestrator role",
	})
	m.ctx.SelectOption("Orchestrator roles:", items, "", func(v string, ok bool) {
		if !ok || v == "" {
			m.back()
			return
		}
		if v == "__add__" {
			m.open(m.addOrchestratorRole)
			return
		}
		m.openRoleDetail(v)
	})
}

func orchestratorRoleItems(cfg *config.Config) []tui.SelectorItem {
	items := make([]tui.SelectorItem, 0, len(cfg.Orchestrator.Roles))
	for name, role := range cfg.Orchestrator.Roles {
		desc := role.Model
		if role.Provider != "" {
			desc += " / " + role.Provider
		}
		if len(role.AllowedTools) > 0 {
			desc += fmt.Sprintf(" (%d tools)", len(role.AllowedTools))
		}
		items = append(items, tui.SelectorItem{Value: name, Label: name, Description: desc})
	}
	return items
}

func (m *configMenu) openRoleDetail(name string) {
	m.current = func() { m.openRoleDetail(name) }
	cfg := m.ctx.Config
	role := cfg.Orchestrator.Roles[name]
	items := []tui.SelectorItem{
		{Value: "model", Label: "Model", Description: role.Model},
		{Value: "provider", Label: "Provider", Description: role.Provider},
		{Value: "tools", Label: "Allowed tools", Description: strings.Join(role.AllowedTools, ", ")},
		{Value: "remove", Label: "Remove role", Description: name},
	}
	m.ctx.SelectOption("Edit role "+name+":", items, "", func(field string, ok bool) {
		if !ok || field == "" {
			m.back()
			return
		}
		switch field {
		case "remove":
			m.confirmRemoveRole(name)
		case "model", "provider":
			m.promptRoleField(name, field)
		case "tools":
			m.promptRoleTools(name)
		}
	})
}

func (m *configMenu) promptRoleField(name, field string) {
	cfg := m.ctx.Config
	role := cfg.Orchestrator.Roles[name]
	current := role.Model
	if field == "provider" {
		current = role.Provider
	}
	m.ctx.ShowInput("Role "+field+":", current, func(value string, ok bool) {
		if !ok {
			m.back()
			return
		}
		updated := cfg.Orchestrator.Roles[name]
		if field == "model" {
			updated.Model = strings.TrimSpace(value)
		} else {
			updated.Provider = strings.TrimSpace(value)
		}
		cfg.Orchestrator.Roles[name] = updated
		m.saveOrchestratorSection()
		m.openRoleDetail(name)
	})
}

func (m *configMenu) promptRoleTools(name string) {
	cfg := m.ctx.Config
	role := cfg.Orchestrator.Roles[name]
	current := strings.Join(role.AllowedTools, ", ")
	m.ctx.ShowInput("Allowed tools (comma-separated):", current, func(value string, ok bool) {
		if !ok {
			m.back()
			return
		}
		updated := cfg.Orchestrator.Roles[name]
		updated.AllowedTools = splitTrim(value, ",")
		cfg.Orchestrator.Roles[name] = updated
		m.saveOrchestratorSection()
		m.openRoleDetail(name)
	})
}

func (m *configMenu) confirmRemoveRole(name string) {
	m.current = func() { m.confirmRemoveRole(name) }
	items := []tui.SelectorItem{
		{Value: "yes", Label: "Yes, remove role", Description: name},
		{Value: "no", Label: "No, cancel", Description: ""},
	}
	m.ctx.SelectOption("Remove role "+name+"?", items, "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			m.back()
			return
		}
		delete(m.ctx.Config.Orchestrator.Roles, name)
		m.saveOrchestratorSection()
		m.flash(fmt.Sprintf("Role %q removed.", name))
		m.openOrchestratorRoles()
	})
}

func (m *configMenu) addOrchestratorRole() {
	m.current = m.addOrchestratorRole
	m.ctx.ShowInput("Role name:", "", func(name string, ok bool) {
		if !ok || name == "" {
			m.back()
			return
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if _, exists := m.ctx.Config.Orchestrator.Roles[name]; exists {
			m.flash(fmt.Sprintf("Role %q already exists.", name))
			m.back()
			return
		}
		m.ctx.ShowInput("Model:", "", func(model string, ok bool) {
			if !ok || model == "" {
				m.back()
				return
			}
			if m.ctx.Config.Orchestrator.Roles == nil {
				m.ctx.Config.Orchestrator.Roles = map[string]config.OrchestratorRole{}
			}
			m.ctx.Config.Orchestrator.Roles[name] = config.OrchestratorRole{Model: strings.TrimSpace(model)}
			m.saveOrchestratorSection()
			m.openRoleDetail(name)
		})
	})
}

// --- pool --------------------------------------------------------------------

func (m *configMenu) openOrchestratorPool() {
	m.current = m.openOrchestratorPool
	cfg := m.ctx.Config.Orchestrator.Pool
	items := []tui.SelectorItem{
		{Value: "max_total", Label: "Max total agents", Description: intLabel(cfg.MaxTotalAgents)},
		{Value: "per_model", Label: "Max agents per model", Description: fmt.Sprintf("%d rules", len(cfg.MaxAgentsPerModel))},
	}
	m.ctx.SelectOption("Orchestrator pool:", items, "", func(field string, ok bool) {
		if !ok || field == "" {
			m.back()
			return
		}
		switch field {
		case "max_total":
			m.promptPoolInt("Max total agents:", &m.ctx.Config.Orchestrator.Pool.MaxTotalAgents, false)
		case "per_model":
			m.openPerModelPool()
		}
	})
}

func (m *configMenu) promptPoolInt(prompt string, target *int, allowZero bool) {
	m.ctx.ShowInput(prompt, fmt.Sprintf("%d", *target), func(value string, ok bool) {
		if !ok {
			m.back()
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			m.flash("Invalid number: " + value)
			m.back()
			return
		}
		if n < 0 || (!allowZero && n == 0) {
			m.flash("Value must be positive")
			m.back()
			return
		}
		*target = n
		m.saveOrchestratorSection()
		m.openOrchestratorPool()
	})
}

func (m *configMenu) openPerModelPool() {
	m.current = m.openPerModelPool
	cfg := m.ctx.Config.Orchestrator.Pool
	items := make([]tui.SelectorItem, 0, len(cfg.MaxAgentsPerModel)+1)
	for model, n := range cfg.MaxAgentsPerModel {
		items = append(items, tui.SelectorItem{Value: model, Label: model, Description: fmt.Sprintf("%d", n)})
	}
	items = append(items, tui.SelectorItem{Value: "__add__", Label: "— add rule —", Description: "model-name=limit"})
	m.ctx.SelectOption("Per-model limits:", items, "", func(v string, ok bool) {
		if !ok || v == "" {
			m.back()
			return
		}
		if v == "__add__" {
			m.addPerModelRule()
			return
		}
		m.editPerModelRule(v)
	})
}

func (m *configMenu) addPerModelRule() {
	m.current = m.addPerModelRule
	m.ctx.ShowInput("Model name:", "", func(model string, ok bool) {
		if !ok || model == "" {
			m.back()
			return
		}
		m.ctx.ShowInput("Agent limit:", "1", func(limit string, ok bool) {
			if !ok {
				m.back()
				return
			}
			n, err := strconv.Atoi(strings.TrimSpace(limit))
			if err != nil || n < 1 {
				m.flash("Invalid limit")
				m.back()
				return
			}
			if m.ctx.Config.Orchestrator.Pool.MaxAgentsPerModel == nil {
				m.ctx.Config.Orchestrator.Pool.MaxAgentsPerModel = map[string]int{}
			}
			m.ctx.Config.Orchestrator.Pool.MaxAgentsPerModel[strings.TrimSpace(model)] = n
			m.saveOrchestratorSection()
			m.openPerModelPool()
		})
	})
}

func (m *configMenu) editPerModelRule(model string) {
	m.current = func() { m.editPerModelRule(model) }
	limit := m.ctx.Config.Orchestrator.Pool.MaxAgentsPerModel[model]
	items := []tui.SelectorItem{
		{Value: "edit", Label: "Edit limit", Description: fmt.Sprintf("%d", limit)},
		{Value: "remove", Label: "Remove rule", Description: model},
	}
	m.ctx.SelectOption("Rule for "+model+":", items, "", func(action string, ok bool) {
		if !ok || action == "" {
			m.back()
			return
		}
		if action == "remove" {
			delete(m.ctx.Config.Orchestrator.Pool.MaxAgentsPerModel, model)
			m.saveOrchestratorSection()
			m.openPerModelPool()
			return
		}
		m.ctx.ShowInput("Agent limit:", fmt.Sprintf("%d", limit), func(value string, ok bool) {
			if !ok {
				m.back()
				return
			}
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 1 {
				m.flash("Invalid limit")
				m.back()
				return
			}
			m.ctx.Config.Orchestrator.Pool.MaxAgentsPerModel[model] = n
			m.saveOrchestratorSection()
			m.openPerModelPool()
		})
	})
}

// --- defaults ----------------------------------------------------------------

func (m *configMenu) openOrchestratorDefaults() {
	m.current = m.openOrchestratorDefaults
	items := []tui.SelectorItem{
		{Value: config.OrchestratorTopologyHub, Label: "hub", Description: "central coordinator role"},
		{Value: config.OrchestratorTopologyFanout, Label: "fanout", Description: "all roles in parallel"},
		{Value: config.OrchestratorTopologyPipeline, Label: "pipeline", Description: "roles in sequence"},
	}
	m.ctx.SelectOption("Default topology:", items, m.ctx.Config.Orchestrator.Defaults.Topology, func(v string, ok bool) {
		if !ok || v == "" {
			m.back()
			return
		}
		m.ctx.Config.Orchestrator.Defaults.Topology = v
		m.saveOrchestratorSection()
		m.back()
	})
}

// --- retention ---------------------------------------------------------------

func (m *configMenu) openOrchestratorRetention() {
	m.current = m.openOrchestratorRetention
	cfg := m.ctx.Config.Orchestrator.Retention
	items := []tui.SelectorItem{
		{Value: "enabled", Label: "Enabled", Description: boolLabel(cfg.Enabled)},
		{Value: "days", Label: "Retention days", Description: retentionLabel(cfg)},
	}
	m.ctx.SelectOption("Orchestrator retention:", items, "", func(field string, ok bool) {
		if !ok || field == "" {
			m.back()
			return
		}
		switch field {
		case "enabled":
			cfg.Enabled = !cfg.Enabled
			m.saveOrchestratorSection()
			m.openOrchestratorRetention()
		case "days":
			m.promptRetentionDays(&cfg.Enabled, &cfg.Days, m.saveOrchestratorSection, m.openOrchestratorRetention)
		}
	})
}

func (m *configMenu) promptRetentionDays(enabled *bool, days *int, save func(), reopen func()) {
	m.ctx.ShowInput("Retention days (0 = keep forever):", fmt.Sprintf("%d", *days), func(value string, ok bool) {
		if !ok {
			m.back()
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			m.flash("Invalid number")
			m.back()
			return
		}
		*days = n
		if n > 0 {
			*enabled = true
		}
		save()
		reopen()
	})
}

// --- goals -------------------------------------------------------------------

func (m *configMenu) openGoalsRetention() {
	m.current = m.openGoalsRetention
	// Edit through a pointer to the LIVE config: saveGoalsSection serializes
	// m.ctx.Config.Goals, so mutations on a copy would be silently dropped.
	g := &m.ctx.Config.Goals
	items := []tui.SelectorItem{
		{Value: "enabled", Label: "Retention enabled", Description: boolLabel(g.Retention.Enabled)},
		{Value: "days", Label: "Retention days", Description: goalsRetentionLabel(g.Retention)},
		{Value: "auto_unblock", Label: "Auto-unblock goals", Description: boolLabel(g.AutoUnblockEnabled())},
		{Value: "fresh_context", Label: "Fresh context for new goals", Description: boolLabel(g.FreshContextEnabled())},
		{Value: "turn_budget", Label: "Default turn budget", Description: goalTurnBudgetLabel(g.DefaultTurnBudget)},
		{Value: "stall_turns", Label: "Stall watchdog", Description: goalStallTurnsLabel(g.StallTurns)},
	}
	m.ctx.SelectOption("Goals:", items, "", func(field string, ok bool) {
		if !ok || field == "" {
			m.back()
			return
		}
		switch field {
		case "enabled":
			g.Retention.Enabled = !g.Retention.Enabled
			m.saveGoalsSection()
			m.openGoalsRetention()
		case "days":
			m.promptRetentionDays(&g.Retention.Enabled, &g.Retention.Days, m.saveGoalsSection, m.openGoalsRetention)
		case "auto_unblock":
			m.toggleGoalAutoUnblock()
		case "fresh_context":
			m.toggleGoalFreshContext()
		case "turn_budget":
			m.promptGoalLimit("Default turn budget for new goals (-1 = unlimited):", g.DefaultTurnBudget,
				func(n int) {
					g.DefaultTurnBudget = n
					m.saveGoalsSection()
					syncGoalLimits(m.ctx)
				}, m.openGoalsRetention)
		case "stall_turns":
			m.promptGoalLimit("Stall watchdog turns (0 or -1 = disabled):", g.StallTurns,
				func(n int) {
					g.StallTurns = n
					m.saveGoalsSection()
					syncGoalLimits(m.ctx)
				}, m.openGoalsRetention)
		}
	})
}

// goalTurnBudgetLabel renders goals.default_turn_budget for the menu
// (-1 = unlimited; 0 = unset, no implicit ceiling).
func goalTurnBudgetLabel(n int) string {
	switch {
	case n < 0:
		return "unlimited"
	case n == 0:
		return "unset"
	default:
		return fmt.Sprintf("%d turns", n)
	}
}

// goalStallTurnsLabel renders goals.stall_turns for the menu (0/-1 = off).
func goalStallTurnsLabel(n int) string {
	if n <= 0 {
		return "disabled"
	}
	return fmt.Sprintf("%d turns", n)
}

// promptGoalLimit asks for an integer limit value and applies it via onSave.
// Values below -1 are rejected (both goal limits share the >= -1 contract:
// -1 = unlimited/disabled). Invalid input flashes an error and reopens the
// page unchanged; cancel unwinds one level like the other prompts.
func (m *configMenu) promptGoalLimit(prompt string, current int, onSave func(int), reopen func()) {
	m.ctx.ShowInput(prompt, strconv.Itoa(current), func(value string, ok bool) {
		if !ok {
			m.back()
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < -1 {
			m.flash("Invalid number (-1 or greater)")
			reopen()
			return
		}
		onSave(n)
		reopen()
	})
}

// toggleGoalAutoUnblock flips goals.auto_unblock and persists it. The field
// is tri-state (*bool); toggling from the default (nil = on) sets an explicit
// false, and back to true, so the choice survives cascade merges.
func (m *configMenu) toggleGoalAutoUnblock() {
	g := &m.ctx.Config.Goals
	next := !g.AutoUnblockEnabled()
	g.AutoUnblock = &next
	m.saveGoalsSection()
	m.flash("Auto-unblock goals " + toggleNextLabel(!next))
	m.openGoalsRetention()
}

// toggleGoalFreshContext flips goals.fresh_context (the default context mode
// for new goals) and persists it. Tri-state (*bool): nil = default true
// (clean context per goal); an explicit false makes new goals reuse the
// current conversation unless overridden per goal.
func (m *configMenu) toggleGoalFreshContext() {
	g := &m.ctx.Config.Goals
	next := !g.FreshContextEnabled()
	g.FreshContext = &next
	m.saveGoalsSection()
	m.flash("Fresh context for new goals " + toggleNextLabel(!next))
	m.openGoalsRetention()
}

func goalsRetentionLabel(r config.GoalsRetentionConfig) string {
	if !r.Enabled || r.Days <= 0 {
		return "never"
	}
	return fmt.Sprintf("%d days", r.Days)
}

func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
