// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tui"
)

// settingSkills is the /config → Skills sub-menu: execution mode plus
// enable/disable toggles for embedded (global) and local (per-project) skills.
func (m *configMenu) settingSkills() {
	m.current = m.settingSkills
	items := []tui.SelectorItem{
		{Value: "execution_mode", Label: "Execution mode", Description: m.ctx.Config.Skills.ExecutionMode},
		{Value: "embedded", Label: "Embedded skills (global)", Description: skillSourceLabel(m.ctx.SkillRegistry, "embedded", m.ctx.Config)},
		{Value: "local", Label: "Local skills (per project)", Description: skillSourceLabel(m.ctx.SkillRegistry, "local", m.ctx.Config)},
	}
	m.ctx.SelectOption("Skills settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if selected == "execution_mode" {
			m.handleSkillsSetting("execution_mode")
			return
		}
		m.open(func() { m.settingSkillSource(selected) })
	})
}

// handleSkillsSetting handles the execution-mode entry of the Skills sub-menu.
func (m *configMenu) handleSkillsSetting(selected string) {
	switch selected {
	case "execution_mode":
		items := []tui.SelectorItem{
			{Value: "inline", Label: "inline", Description: "Run skills inline in the conversation"},
			{Value: "subagent", Label: "sub-agent", Description: "Delegate skills to sub-agents"},
		}
		m.ctx.SelectOption("Skill execution mode:", items, m.ctx.Config.Skills.ExecutionMode, func(v string, ok bool) {
			if ok && v != "" {
				m.applySet("skills.execution_mode", v)
			}
			m.settingSkills()
		})
	}
}

// settingSkillSource lists the skills of one origin ("embedded" or "local")
// as on/off toggles, mirroring the Tools sub-menu.
func (m *configMenu) settingSkillSource(source string) {
	m.current = func() { m.settingSkillSource(source) }
	items := buildSkillToggleItems(m.ctx, source)
	title := "Embedded skills (toggle on/off):"
	if source == "local" {
		title = "Local skills (toggle on/off):"
	}
	m.ctx.SelectOption(title, items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.toggleSkill(selected, source)
		m.settingSkillSource(source)
	})
}

// buildSkillToggleItems returns one toggle item per loaded skill of the given
// source, with the current enabled state as the description.
func buildSkillToggleItems(ctx core.Context, source string) []tui.SelectorItem {
	cfg := ctx.Config
	var items []tui.SelectorItem
	for _, s := range skillSummariesForSource(ctx.SkillRegistry, source) {
		items = append(items, tui.SelectorItem{
			Value:       s.Name,
			Label:       s.Name,
			Description: boolLabel(skillEnabled(cfg, s.Name)),
		})
	}
	return items
}

// skillSummariesForSource filters the registry's loaded skills by origin:
// source == "embedded" keeps embedded skills, otherwise file-based skills.
func skillSummariesForSource(reg core.SkillRegistry, source string) []skills.SkillSummary {
	if reg == nil {
		return nil
	}
	wantEmbedded := source == "embedded"
	var out []skills.SkillSummary
	for _, s := range reg.List() {
		if (s.Source == "embedded") == wantEmbedded {
			out = append(out, s)
		}
	}
	return out
}

// skillSourceLabel summarizes the on/off state of a skill origin for the
// Skills sub-menu: "<on>/<total> on".
func skillSourceLabel(reg core.SkillRegistry, source string, cfg *config.Config) string {
	summaries := skillSummariesForSource(reg, source)
	total := len(summaries)
	on := 0
	for _, s := range summaries {
		if skillEnabled(cfg, s.Name) {
			on++
		}
	}
	return fmt.Sprintf("%d/%d on", on, total)
}

// skillEnabled reports whether a skill is currently on, mirroring
// SkillRegistry.allowed: not disabled AND (no allowlist OR in allowlist).
func skillEnabled(cfg *config.Config, name string) bool {
	if stringInSlice(cfg.Skills.Disabled, name) {
		return false
	}
	if len(cfg.Skills.Enabled) > 0 {
		return stringInSlice(cfg.Skills.Enabled, name)
	}
	return true
}

// setSkillEnabled updates the in-memory skills lists for a toggle. Enabling
// removes the name from Disabled and adds it to Enabled only when an allowlist
// is active (so the default all-on state stays empty); disabling removes it
// from Enabled and adds it to Disabled.
func setSkillEnabled(cfg *config.Config, name string, enabled bool) {
	if enabled {
		cfg.Skills.Disabled = removeString(cfg.Skills.Disabled, name)
		if len(cfg.Skills.Enabled) > 0 {
			cfg.Skills.Enabled = appendUnique(cfg.Skills.Enabled, name)
		}
		return
	}
	cfg.Skills.Enabled = removeString(cfg.Skills.Enabled, name)
	cfg.Skills.Disabled = appendUnique(cfg.Skills.Disabled, name)
}

// toggleSkill flips a skill's enabled state, persists it to the config layer
// owning its source (embedded → home/global, local → project), and reloads the
// skill registry so the change applies to the running session.
func (m *configMenu) toggleSkill(name, source string) {
	cfg := m.ctx.Config
	enabled := skillEnabled(cfg, name)
	setSkillEnabled(cfg, name, !enabled)
	if err := persistSkillToggle(m.ctx, source, !enabled); err != nil {
		m.flash("Failed to save skill config: " + err.Error())
	}
	m.reloadSkillsAfterToggle()
	m.flash(fmt.Sprintf("Skill %s %s", name, toggleNextLabel(enabled)))
}

// setSkillEnabledState enables or disables a skill by name and persists the
// change. Shared by the /skill:enable and /skill:disable commands.
func setSkillEnabledState(ctx core.Context, name string, enabled bool) error {
	if ctx.Config == nil {
		return fmt.Errorf("configuration not available")
	}
	setSkillEnabled(ctx.Config, name, enabled)
	source := skillSourceForToggle(ctx, name)
	if err := persistSkillToggle(ctx, source, enabled); err != nil {
		return err
	}
	reloadSkillsFor(ctx)
	return nil
}

// skillSourceForToggle resolves the config layer a skill toggle belongs to.
// Loaded skills report their source from the registry; a disabled skill is not
// loaded, so a scan (SourceOf) is used to find it among candidate locations.
// "" means the source is unknown → the toggle is persisted to both layers.
func skillSourceForToggle(ctx core.Context, name string) string {
	if ctx.SkillRegistry == nil {
		return ""
	}
	if s, ok := ctx.SkillRegistry.Get(name); ok && s.Source != "" {
		return s.Source
	}
	if reg, ok := ctx.SkillRegistry.(*skills.SkillRegistry); ok {
		if src, ok := reg.SourceOf(name); ok {
			return src
		}
	}
	return ""
}

// persistSkillToggle writes the skills enabled/disabled lists to the config
// layer owning the source per the gold rules: embedded skills are global (home
// config), all other loaded skills are per-project (project config). Only
// names whose discoverable origin matches the layer are written (unknown-origin
// names are kept conservatively), so entries belonging to the other layer are
// never duplicated. Enabling additionally clears the name from the OTHER layer
// (it may be pinned there by manual config or legacy duplication). An empty
// list deletes the key so configs stay minimal. When the source is unknown the
// lists are written to both layers so the toggle is effective regardless of
// which layer pinned the skill.
func persistSkillToggle(ctx core.Context, source string, enabling bool) error {
	if ctx.ConfigSaver == nil {
		return nil
	}
	if source == "local" {
		source = "file"
	}
	cfg := ctx.Config
	enabled, disabled := cfg.Skills.Enabled, cfg.Skills.Disabled
	switch source {
	case "embedded", "file":
		home := source == "embedded"
		filteredE, filteredD := skillNamesForLayer(ctx, enabled, disabled, home)
		if err := saveSkillListsToLayer(ctx, home, filteredE, filteredD); err != nil {
			return err
		}
		if enabling {
			otherE, otherD := skillNamesForLayer(ctx, enabled, disabled, !home)
			return saveSkillListsToLayer(ctx, !home, otherE, otherD)
		}
		return nil
	default: // unknown source — write to both layers
		homeE, homeD := skillNamesForLayer(ctx, enabled, disabled, true)
		if err := saveSkillListsToLayer(ctx, true, homeE, homeD); err != nil {
			return err
		}
		projE, projD := skillNamesForLayer(ctx, enabled, disabled, false)
		return saveSkillListsToLayer(ctx, false, projE, projD)
	}
}

// saveSkillListsToLayer writes the given enabled/disabled lists to the home
// (global) or project config layer, deleting keys when the lists are empty.
func saveSkillListsToLayer(ctx core.Context, home bool, enabled, disabled []string) error {
	if err := saveSkillListField(ctx, home, "enabled", enabled); err != nil {
		return err
	}
	return saveSkillListField(ctx, home, "disabled", disabled)
}

// skillNamesForLayer filters merged skill names to those whose discoverable
// origin belongs to the layer: embedded → home (homeLayer=true), file →
// project (homeLayer=false). Names with an unknown origin are kept in both
// layers so they remain effective.
func skillNamesForLayer(ctx core.Context, enabled, disabled []string, homeLayer bool) ([]string, []string) {
	filter := func(names []string) []string {
		var out []string
		for _, n := range names {
			embedded, ok := skillSourceIsEmbedded(ctx, n)
			if !ok || embedded == homeLayer {
				out = append(out, n)
			}
		}
		return out
	}
	return filter(enabled), filter(disabled)
}

// skillSourceIsEmbedded reports whether the skill's discoverable origin is
// embedded; ok=false when the origin cannot be resolved.
func skillSourceIsEmbedded(ctx core.Context, name string) (bool, bool) {
	if ctx.SkillRegistry != nil {
		if s, ok := ctx.SkillRegistry.Get(name); ok && s.Source != "" {
			return s.Source == "embedded", true
		}
		if reg, ok := ctx.SkillRegistry.(*skills.SkillRegistry); ok {
			if src, ok := reg.SourceOf(name); ok {
				return src == "embedded", true
			}
		}
	}
	return false, false
}

// saveSkillListField writes a skills list to the home (global) or project
// config layer, deleting the key when the list is empty.
func saveSkillListField(ctx core.Context, home bool, kind string, list []string) error {
	path := []string{"skills", kind}
	if home {
		if len(list) == 0 {
			return ctx.ConfigSaver.DeleteHomeField(path)
		}
		return ctx.ConfigSaver.SaveHomeFieldValue(path, list)
	}
	if len(list) == 0 {
		return ctx.ConfigSaver.DeleteProjectField(path)
	}
	return ctx.ConfigSaver.SaveProjectFieldValue(path, list)
}

// reloadSkillsAfterToggle re-scans and re-loads the skill registry so a
// toggle takes effect in the running session. The system prompt itself is not
// rebuilt mid-session (documented load-time behavior).
func (m *configMenu) reloadSkillsAfterToggle() {
	if m.ctx.ReloadHandler == nil {
		return
	}
	if _, err := m.ctx.ReloadHandler.ReloadSkills(); err != nil {
		m.flash("Skill reload failed: " + err.Error())
	}
}

// reloadSkillsFor re-scans and re-loads the skill registry after a toggle
// initiated by a slash command.
func reloadSkillsFor(ctx core.Context) {
	if ctx.ReloadHandler == nil {
		return
	}
	if _, err := ctx.ReloadHandler.ReloadSkills(); err != nil {
		writeFmt(ctx, "Skill reload failed: %v\n", err)
	}
}

// stringInSlice reports whether s is present in the slice.
func stringInSlice(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// removeString returns the slice without the first occurrence of s.
func removeString(slice []string, s string) []string {
	out := make([]string, 0, len(slice))
	for _, v := range slice {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// appendUnique returns the slice with s appended when not already present.
func appendUnique(slice []string, s string) []string {
	if stringInSlice(slice, s) {
		return slice
	}
	return append(slice, s)
}
