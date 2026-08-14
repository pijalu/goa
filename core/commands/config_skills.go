// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tui"
	"gopkg.in/yaml.v3"
)

// settingSkills is the /config → Skills sub-menu: execution mode plus
// enable/disable toggles for embedded (global) and local (per-project) skills.
func (m *configMenu) settingSkills() {
	m.current = m.settingSkills
	items := []tui.SelectorItem{
		{Value: "execution_mode", Label: "Execution mode", Description: m.ctx.Config.Skills.ExecutionMode},
		{Value: "embedded", Label: "Embedded skills (global)", Description: skillSourceLabel(m.ctx.SkillRegistry, "embedded", m.ctx.Config)},
		{Value: "local", Label: "Local skills (per project)", Description: skillSourceLabel(m.ctx.SkillRegistry, "local", m.ctx.Config)},
		{Value: "sticky", Label: "Sticky skills (per project)", Description: stickySkillsLabel(m.ctx)},
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
		m.open(func() {
			if selected == "sticky" {
				m.settingStickySkills()
				return
			}
			m.settingSkillSource(selected)
		})
	})
}

// settingStickySkills lists knowledge skills as sticky on/off toggles.
// Sticky state is always persisted at PROJECT level (skills.sticky /
// skills.sticky_off in .goa/config.yaml) so it survives sessions per project.
func (m *configMenu) settingStickySkills() {
	m.current = m.settingStickySkills
	items := buildStickyToggleItems(m.ctx)
	m.ctx.SelectOption("Sticky skills — always-on knowledge skills (toggle on/off):", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		next, err := nextSkillStickyState(m.ctx, selected)
		if err != nil {
			m.flash(err.Error())
		} else if err := setSkillStickyState(m.ctx, selected, next); err != nil {
			m.flash("Failed to save sticky config: " + err.Error())
		} else {
			m.reloadSkillsAfterToggle()
			m.flash(fmt.Sprintf("Sticky %s for %s", boolLabel(next), selected))
		}
		m.settingStickySkills()
	})
}

// buildStickyToggleItems returns one toggle item per loaded knowledge skill,
// with the effective sticky state as the description.
func buildStickyToggleItems(ctx core.Context) []tui.SelectorItem {
	if ctx.SkillRegistry == nil {
		return nil
	}
	var items []tui.SelectorItem
	for _, s := range ctx.SkillRegistry.List() {
		if s.Category != skills.SkillCategoryKnowledge {
			continue
		}
		items = append(items, tui.SelectorItem{
			Value:       s.Name,
			Label:       s.Name,
			Description: boolLabel(skillStickyEffective(ctx, s.Name)),
		})
	}
	return items
}

// stickySkillsLabel summarizes the sticky state for the Skills sub-menu row:
// "N/M sticky" (sticky knowledge skills of all loaded knowledge skills).
func stickySkillsLabel(ctx core.Context) string {
	if ctx.SkillRegistry == nil {
		return "0/0 sticky"
	}
	total, on := 0, 0
	for _, s := range ctx.SkillRegistry.List() {
		if s.Category != skills.SkillCategoryKnowledge {
			continue
		}
		total++
		if skillStickyEffective(ctx, s.Name) {
			on++
		}
	}
	return fmt.Sprintf("%d/%d sticky", on, total)
}

// skillFrontmatterSticky reports the pristine frontmatter sticky flag of a
// loaded skill (before config overrides). ok=false when unknown; for
// non-concrete registries (test fakes) the loaded Meta.Sticky stands in,
// since fakes never carry override records.
func skillFrontmatterSticky(reg core.SkillRegistry, name string) (bool, bool) {
	if r, ok := reg.(*skills.SkillRegistry); ok {
		return r.FrontmatterSticky(name)
	}
	if s, ok := reg.Get(name); ok {
		return s.Meta.Sticky, true
	}
	return false, false
}

// skillStickyEffective reports the effective sticky state of a loaded
// knowledge skill from the config lists plus the frontmatter default:
// sticky_off wins, then sticky, then the frontmatter flag. This mirrors the
// loader's applyStickyOverride so the pre-reload state is already correct.
func skillStickyEffective(ctx core.Context, name string) bool {
	if ctx.Config == nil {
		fm, ok := skillFrontmatterSticky(ctx.SkillRegistry, name)
		return ok && fm
	}
	if stringInSlice(ctx.Config.Skills.StickyOff, name) {
		return false
	}
	if stringInSlice(ctx.Config.Skills.Sticky, name) {
		return true
	}
	fm, ok := skillFrontmatterSticky(ctx.SkillRegistry, name)
	return ok && fm
}

// nextSkillStickyState validates that the named skill is a loaded knowledge
// skill and returns the inverted sticky state for a toggle.
func nextSkillStickyState(ctx core.Context, name string) (bool, error) {
	if ctx.SkillRegistry == nil {
		return false, fmt.Errorf("skill registry not available")
	}
	skill, ok := ctx.SkillRegistry.Get(name)
	if !ok {
		return false, fmt.Errorf("skill not found: %s", name)
	}
	if skill.Meta.Category != skills.SkillCategoryKnowledge {
		return false, fmt.Errorf("sticky applies to knowledge skills only: %s is not a knowledge skill", name)
	}
	if skill.Meta.Hidden {
		return false, fmt.Errorf("sticky applies to non-hidden skills only: %s is hidden", name)
	}
	return !skillStickyEffective(ctx, name), nil
}

// setSkillStickyState turns the sticky (always-on) state of a knowledge skill
// on or off, persists BOTH override lists at PROJECT level, and reloads the
// skill registry so the running session picks the change up. The minimal
// entry is written: a frontmatter-sticky skill needs sticky_off to turn off
// and nothing to turn on; a plain skill needs sticky to turn on and nothing
// to turn off.
func setSkillStickyState(ctx core.Context, name string, sticky bool) error {
	if ctx.Config == nil {
		return fmt.Errorf("configuration not available")
	}
	fm, fmKnown := skillFrontmatterSticky(ctx.SkillRegistry, name)
	if sticky {
		ctx.Config.Skills.StickyOff = removeString(ctx.Config.Skills.StickyOff, name)
		if fmKnown && fm {
			// Frontmatter already grants sticky; an explicit on entry is inert.
			ctx.Config.Skills.Sticky = removeString(ctx.Config.Skills.Sticky, name)
		} else {
			ctx.Config.Skills.Sticky = appendUnique(ctx.Config.Skills.Sticky, name)
		}
	} else {
		ctx.Config.Skills.Sticky = removeString(ctx.Config.Skills.Sticky, name)
		if fmKnown && fm {
			ctx.Config.Skills.StickyOff = appendUnique(ctx.Config.Skills.StickyOff, name)
		} else {
			ctx.Config.Skills.StickyOff = removeString(ctx.Config.Skills.StickyOff, name)
		}
	}
	if err := persistSkillSticky(ctx); err != nil {
		return err
	}
	reloadSkillsFor(ctx)
	return nil
}

// persistSkillSticky writes the skills.sticky / skills.sticky_off lists to
// the PROJECT config layer only (sticky state is per-project by design,
// including embedded skills like telegram), deleting keys when empty.
func persistSkillSticky(ctx core.Context) error {
	if ctx.ConfigSaver == nil {
		return nil
	}
	if err := saveSkillListField(ctx, false, "sticky", ctx.Config.Skills.Sticky); err != nil {
		return err
	}
	return saveSkillListField(ctx, false, "sticky_off", ctx.Config.Skills.StickyOff)
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
			Description: boolLabel(skillEnabled(cfg, s.Name, ctx.SkillRegistry)),
		})
	}
	return items
}

// skillSummariesForSource filters the registry's skills by origin for the
// toggle menu. For source == "embedded" it enumerates EVERY discoverable
// embedded skill (including default-off ones the loader skipped,
// embedded default-off except telegram) so the user can re-enable them from
// the menu; the agent still never sees them until enabled. For file sources
// it keeps the loaded file-based skills.
func skillSummariesForSource(reg core.SkillRegistry, source string) []skills.SkillSummary {
	if reg == nil {
		return nil
	}
	// Real registry: enumerate EVERY discoverable embedded skill (including
	// default-off ones the loader skipped) so the menu can re-enable them.
	if source == "embedded" {
		if r, ok := reg.(*skills.SkillRegistry); ok {
			return r.ListEmbeddedDiscoverable()
		}
	}
	// Fakes / non-concrete registries (tests) and the file-source branch:
	// filter the loaded skills by origin.
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
		if skillEnabled(cfg, s.Name, reg) {
			on++
		}
	}
	return fmt.Sprintf("%d/%d on", on, total)
}

// skillEnabled reports whether a skill is currently on, mirroring
// SkillRegistry.allowed PLUS the embedded default-off policy: not disabled,
// (no allowlist OR in allowlist), AND — for embedded skills — not
// default-off unless explicitly opted back in via skills.embedded_enabled.
// reg may be nil (no embedded-default info → fall back to the list logic).
func skillEnabled(cfg *config.Config, name string, reg core.SkillRegistry) bool {
	if stringInSlice(cfg.Skills.Disabled, name) {
		return false
	}
	if len(cfg.Skills.Enabled) > 0 {
		return stringInSlice(cfg.Skills.Enabled, name)
	}
	// Embedded default-off: an embedded skill suppressed by default is off
	// unless the user opted it back in via the embedded-scoped list.
	if r, ok := reg.(*skills.SkillRegistry); ok && r.EmbeddedDefaultDisabled(name) {
		return stringInSlice(cfg.Skills.EmbeddedEnabled, name)
	}
	return true
}

// setSkillEnabled updates the in-memory skills lists for a toggle.
//
// Embedded skills (default-off except telegram) are routed to the
// embedded-scoped skills.embedded_enabled list: enabling opts the skill back
// in WITHOUT activating the global Enabled allowlist (which would suppress
// file-based skills), and disabling removes it from that opt-in list so it
// returns to default-off. isEmbedded marks embedded skills; defaultOff marks
// an embedded skill that is OFF by default (all except telegram) so disabling
// a default-ON one (telegram) still writes an explicit Disabled entry.
//
// Non-embedded (file) skills keep the legacy semantics: enabling removes the
// name from Disabled and adds it to Enabled when an allowlist is active (so
// the default all-on state stays empty); disabling removes it from Enabled
// and adds it to Disabled — except when the name is the last allowlist
// member: removing it would collapse the allowlist to empty, which loads as
// "all on" and silently destroys the user's allowlist mode (a
// disable/re-enable round trip flipped every other skill on). A name in both
// lists is disabled (explicit off wins), so keeping the membership is inert
// until the skill is re-enabled.
func setSkillEnabled(cfg *config.Config, name string, enabled bool, allowListActive bool, isEmbedded bool, defaultOff bool) {
	if isEmbedded {
		if enabled {
			cfg.Skills.Disabled = removeString(cfg.Skills.Disabled, name)
			// A default-off skill needs the opt-in to come back on; a
			// default-ON one (telegram) is on as soon as Disabled is cleared.
			if defaultOff {
				cfg.Skills.EmbeddedEnabled = appendUnique(cfg.Skills.EmbeddedEnabled, name)
			}
			return
		}
		// Disabling: a default-off embedded skill needs nothing (it is off
		// already) beyond dropping any opt-in; a default-ON one (telegram)
		// needs an explicit Disabled entry. defaultOff distinguishes the two.
		cfg.Skills.EmbeddedEnabled = removeString(cfg.Skills.EmbeddedEnabled, name)
		if !defaultOff {
			cfg.Skills.Disabled = appendUnique(cfg.Skills.Disabled, name)
		}
		return
	}
	if enabled {
		cfg.Skills.Disabled = removeString(cfg.Skills.Disabled, name)
		if allowListActive {
			cfg.Skills.Enabled = appendUnique(cfg.Skills.Enabled, name)
		}
		return
	}
	cfg.Skills.Disabled = appendUnique(cfg.Skills.Disabled, name)
	if len(cfg.Skills.Enabled) > 1 || !stringInSlice(cfg.Skills.Enabled, name) {
		cfg.Skills.Enabled = removeString(cfg.Skills.Enabled, name)
	}
}

// toggleSkill flips a skill's enabled state, persists it to the config layer
// owning its source (embedded → home/global, local → project), and reloads the
// skill registry so the change applies to the running session.
func (m *configMenu) toggleSkill(name, source string) {
	cfg := m.ctx.Config
	enabled := skillEnabled(cfg, name, m.ctx.SkillRegistry)
	isEmbedded, defaultOff := embeddedToggleInfo(m.ctx.SkillRegistry, name)
	setSkillEnabled(cfg, name, !enabled, skillAllowListActive(m.ctx, name, source, !enabled), isEmbedded, defaultOff)
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
	source := skillSourceForToggle(ctx, name)
	isEmbedded, defaultOff := embeddedToggleInfo(ctx.SkillRegistry, name)
	setSkillEnabled(ctx.Config, name, enabled, skillAllowListActive(ctx, name, source, enabled), isEmbedded, defaultOff)
	if err := persistSkillToggle(ctx, source, enabled); err != nil {
		return err
	}
	reloadSkillsFor(ctx)
	return nil
}

// embeddedToggleInfo resolves whether a skill is an embedded built-in and, if
// so, whether it is OFF by default (all except telegram). reg may be nil or a
// non-concrete implementation (both → false, false, the file-skill path).
func embeddedToggleInfo(reg core.SkillRegistry, name string) (isEmbedded, defaultOff bool) {
	r, ok := reg.(*skills.SkillRegistry)
	if !ok {
		return false, false
	}
	src, ok := r.SourceOf(name)
	if !ok || src != "embedded" {
		return false, false
	}
	return true, r.IsEmbeddedDefaultOff(name)
}

// skillAllowListActive reports whether a skills allowlist is in effect for
// the layer owning source, so enabling a skill re-joins the allowlist instead
// of leaving the merged config in the default all-on mode. It checks the live
// merged list first, then — for the re-enable path where the just-emptied
// in-memory list hides the mode — the persisted layer on disk. enabling is
// the state being applied (true = re-enable).
func skillAllowListActive(ctx core.Context, name, source string, enabling bool) bool {
	if ctx.Config == nil {
		return false
	}
	if len(ctx.Config.Skills.Enabled) > 0 {
		return true
	}
	if !enabling {
		// Disabling never needs the disk check: an empty live list means no
		// allowlist is active (or it just became empty, in which case the
		// disabled entry now being written preserves the off state).
		return false
	}
	// Re-enabling with an empty live list: consult the persisted layers. The
	// owning layer for embedded skills is home, for file skills project;
	// unknown sources check both.
	home, project := false, false
	switch source {
	case "embedded":
		home = true
	case "local", "file":
		project = true
	default:
		home, project = true, true
	}
	if home && skillEnabledKeyOnDisk(ctx, true, name) {
		return true
	}
	return project && skillEnabledKeyOnDisk(ctx, false, name)
}

// skillEnabledKeyOnDisk reports whether the given config layer persists a
// non-empty skills.enabled list, or one that still contains name (the layer
// may hold the stale membership from before the disable emptied the live
// list). A read failure is conservative: no allowlist is assumed.
func skillEnabledKeyOnDisk(ctx core.Context, homeLayer bool, name string) bool {
	path := skillLayerConfigPath(ctx, homeLayer)
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var raw struct {
		Skills struct {
			Enabled []string `yaml:"enabled"`
		} `yaml:"skills"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return false
	}
	return len(raw.Skills.Enabled) > 0 || stringInSlice(raw.Skills.Enabled, name)
}

// skillLayerConfigPath resolves the on-disk config path for a layer.
func skillLayerConfigPath(ctx core.Context, homeLayer bool) string {
	if homeLayer {
		if home, ok := internal.GoaHome(); ok {
			return filepath.Join(home, ".goa", "config.yaml")
		}
		return ""
	}
	if ctx.ProjectDir == "" {
		return ""
	}
	return filepath.Join(ctx.ProjectDir, ".goa", "config.yaml")
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
	embeddedEnabled := cfg.Skills.EmbeddedEnabled
	switch source {
	case "embedded", "file":
		home := source == "embedded"
		filteredE, filteredD := skillNamesForLayer(ctx, enabled, disabled, home)
		if err := saveSkillListsToLayer(ctx, home, filteredE, filteredD, embeddedEnabled); err != nil {
			return err
		}
		if enabling {
			otherE, otherD := skillNamesForLayer(ctx, enabled, disabled, !home)
			return saveSkillListsToLayer(ctx, !home, otherE, otherD, embeddedEnabled)
		}
		return nil
	default: // unknown source — write to both layers
		homeE, homeD := skillNamesForLayer(ctx, enabled, disabled, true)
		if err := saveSkillListsToLayer(ctx, true, homeE, homeD, embeddedEnabled); err != nil {
			return err
		}
		projE, projD := skillNamesForLayer(ctx, enabled, disabled, false)
		return saveSkillListsToLayer(ctx, false, projE, projD, embeddedEnabled)
	}
}

// saveSkillListsToLayer writes the skills lists to the home (global) or
// project config layer, deleting keys when the lists are empty. The
// embedded_enabled list (embedded-scoped opt-in) is written ONLY to
// the home layer: embedded skills are global, so their opt-in never belongs
// to a project config.
func saveSkillListsToLayer(ctx core.Context, home bool, enabled, disabled, embeddedEnabled []string) error {
	if err := saveSkillListField(ctx, home, "enabled", enabled); err != nil {
		return err
	}
	if err := saveSkillListField(ctx, home, "disabled", disabled); err != nil {
		return err
	}
	if home {
		return saveSkillListField(ctx, true, "embedded_enabled", embeddedEnabled)
	}
	return nil
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
