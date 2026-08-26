// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"

	"github.com/pijalu/goa/internal/spinner"

	"github.com/pijalu/goa/tui"
)

// ConfigCommand manages configuration settings.
type ConfigCommand struct{}

func (c *ConfigCommand) Name() string { return "config" }
func (c *ConfigCommand) Aliases() []string {
	return []string{}
}
func (c *ConfigCommand) IsInternal() bool { return true }
func (c *ConfigCommand) ShortHelp() string {
	return "View or modify configuration settings"
}
func (c *ConfigCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *ConfigCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	// Handle temp subcommand completions first, using the raw prefix so we can
	// detect a trailing space after a complete setting name and offer on/off.
	if comps := configTempArgCompletions(ctx, prefix); comps != nil {
		return comps
	}

	// The router keeps the raw text after the command name in prefix.
	// We return the full argument string after "/config" so the completer can
	// reconstruct "/config:set:key:value" correctly.
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "" {
		return configSubcommandCompletions("")
	}

	// Prefer colon separator; fall back to space for the legacy syntax.
	sep := ":"
	if strings.Contains(trimmed, " ") && !strings.Contains(trimmed, ":") {
		sep = " "
	}
	parts := strings.SplitN(trimmed, sep, 3)
	sub := parts[0]

	if len(parts) == 1 {
		comps := configSubcommandCompletions(sub)
		if sub == "set" || strings.HasPrefix("set", sub) {
			for _, k := range configKeyCompletions("") {
				comps = append(comps, core.ArgCompletion{Value: "set:" + k.Value, Description: k.Description})
			}
		}
		return comps
	}

	if sub != "set" {
		return nil
	}

	key := parts[1]
	if len(parts) == 2 {
		return prefixKeys("set:", key)
	}

	// len == 3: completing the value for a known key.
	valuePrefix := parts[2]
	return prefixValues("set:", key, valuePrefix, ctx)
}

func (c *ConfigCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return newConfigMenu(ctx).showRoot()
	}
	// The router splits only on colons, so the space-separated form
	// /config:set key value arrives as a single arg. Expand it here so both
	// /config:set key value and /config:set:key:value work.
	if len(args) == 1 {
		args = strings.Fields(args[0])
	}
	if len(args) == 0 {
		return newConfigMenu(ctx).showRoot()
	}
	switch args[0] {
	case "set":
		return handleConfigSet(ctx, args[1:])
	case "add":
		return handleConfigAdd(ctx, args[1:])
	case "remove":
		return handleConfigRemove(ctx, args[1:])
	case "reload":
		return handleConfigReload(ctx)
	case "temp":
		return handleConfigTemp(ctx, args[1:])
	default:
		return fmt.Errorf("unknown config subcommand: %s (use 'set', 'add', 'remove', 'temp' or 'reload')", args[0])
	}
}

// configMenu drives the stacked /config interactive menu.
// The menu keeps a history stack so Escape (cancel) returns to the previous
// page; cancelling on the first page closes the menu.
type configMenu struct {
	ctx     core.Context
	current func()
	history []func()
}

func newConfigMenu(ctx core.Context) *configMenu {
	return &configMenu{ctx: ctx}
}

// open pushes the current page onto the history stack and shows next.
func (m *configMenu) open(next func()) {
	if m.current != nil {
		m.history = append(m.history, m.current)
	}
	m.current = next
	next()
}

// back returns to the previous page in the history stack. When there is no
// previous page, the menu is closed.
func (m *configMenu) back() {
	if len(m.history) == 0 {
		m.current = nil
		return
	}
	prev := m.history[len(m.history)-1]
	m.history = m.history[:len(m.history)-1]
	m.current = prev
	prev()
}

// returnTo unwinds the history stack to the page that was current when a page
// started. baseLen is the value of len(m.history) at that time.
func (m *configMenu) returnTo(baseLen int) {
	if baseLen <= 0 || len(m.history) == 0 {
		m.history = nil
		m.current = nil
		_ = m.showRoot()
		return
	}
	parentIdx := baseLen - 1
	if parentIdx >= len(m.history) {
		parentIdx = len(m.history) - 1
	}
	parent := m.history[parentIdx]
	m.history = m.history[:parentIdx]
	m.current = parent
	parent()
}

func (m *configMenu) showRoot() error {
	m.current = func() { m.showRoot() }
	cfg := m.ctx.Config
	// Use the live runtime mode when available (AgentManager) instead of the
	// static config default, so the menu reflects the actual session mode.
	majorMode := string(m.ctx.CurrentMode().Major)
	if majorMode == "" {
		majorMode = cfg.ActiveMajor()
	}
	items := []tui.SelectorItem{
		{Value: "profile", Label: "Mode", Description: majorMode},
		{Value: "model", Label: "Active model", Description: cfg.ActiveModel},
		{Value: "provider", Label: "Provider", Description: cfg.ActiveProvider},
		{Value: "models", Label: "Manage models", Description: "Add, edit, remove, or select models"},
		{Value: "mode", Label: "Execution mode", Description: string(cfg.Execution.Mode)},
		{Value: "retry", Label: "Retry settings", Description: retrySettingsLabel(cfg)},
		{Value: "compression", Label: "Compression", Description: compressionLabel(cfg)},
		{Value: "theme", Label: "Theme", Description: cfg.TUI.Theme},
		{Value: "spinner", Label: "Spinner", Description: spinnerLabel(cfg)},
		{Value: "spinner_location", Label: "Spinner location", Description: spinnerLocationMenuLabel(cfg)},
		{Value: "thinking_level", Label: "Thinking level", Description: string(cfg.GetThinkingLevel("main_agent"))},
		{Value: "thinking_blocks", Label: "Thinking blocks", Description: thinkingBlocksLabel(cfg)},
		{Value: "show_thinking", Label: "Show thinking", Description: boolLabel(cfg.TUI.Transparency.ShowThinking)},
		{Value: "multi_agent", Label: "Multi-agent", Description: multiAgentLabel(cfg, m.ctx.ForegroundOrchestrator)},
		{Value: "orchestrator", Label: "Orchestrator", Description: orchestratorLabel(cfg)},
		{Value: "teams", Label: "Teams", Description: teamsLabel(cfg)},
		{Value: "tools", Label: "Tools", Description: toolsEnabledLabel(cfg)},
		{Value: "bash", Label: "Bash", Description: bashSettingsLabel(cfg)},
		{Value: "mcp", Label: "MCP servers", Description: mcpServersLabel(cfg)},
		{Value: "sandbox", Label: "Sandbox", Description: sandboxLabel(cfg)},
		{Value: "loop_detection", Label: "Loop detection", Description: loopDetectionLabel(cfg)},
		{Value: "skills", Label: "Skills", Description: skillsLabel(m.ctx.SkillRegistry, cfg)},
		{Value: "goals", Label: "Goals", Description: goalsRetentionLabel(cfg.Goals.Retention)},
	}
	m.ctx.SelectOption("Settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.showSubMenu(selected)
	})
	return nil
}

func (m *configMenu) showSubMenu(selected string) {
	handler, ok := m.subMenuHandlers()[selected]
	if !ok {
		return
	}
	handler(m)
}

func (m *configMenu) subMenuHandlers() map[string]func(*configMenu) {
	return map[string]func(*configMenu){
		"profile":          (*configMenu).openMajorMode,
		"model":            (*configMenu).openActiveModel,
		"provider":         (*configMenu).openProvider,
		"models":           (*configMenu).openModels,
		"mode":             (*configMenu).openExecutionMode,
		"retry":            (*configMenu).openRetrySettings,
		"compression":      (*configMenu).openCompression,
		"theme":            (*configMenu).openTheme,
		"spinner":          (*configMenu).openSpinner,
		"spinner_location": (*configMenu).openSpinnerLocation,
		"thinking_level":   (*configMenu).openThinkingLevel,
		"thinking_blocks":  (*configMenu).toggleThinkingBlocks,
		"show_thinking":    (*configMenu).toggleShowThinking,
		"multi_agent":      (*configMenu).openMultiAgent,
		"orchestrator":     (*configMenu).openOrchestratorMenu,
		"teams":            (*configMenu).openTeamsMenu,
		"tools":            (*configMenu).openTools,
		"bash":             (*configMenu).openBash,
		"mcp":              (*configMenu).openMCP,
		"sandbox":          (*configMenu).openSandbox,
		"loop_detection":   (*configMenu).openLoopDetection,
		"skills":           (*configMenu).openSkills,
		"goals":            (*configMenu).openGoalsMenu,
	}
}

func (m *configMenu) openMajorMode()     { m.open(m.settingMajorMode) }
func (m *configMenu) openProvider()      { m.open(m.settingProvider) }
func (m *configMenu) openModels()        { m.open(m.settingModels) }
func (m *configMenu) openExecutionMode() { m.open(m.settingExecutionMode) }
func (m *configMenu) openRetrySettings() { m.open(m.settingRetrySettings) }
func (m *configMenu) openCompression()   { m.open(m.settingCompression) }
func (m *configMenu) openTheme()         { m.open(m.settingTheme) }
func (m *configMenu) openSpinner()       { m.open(m.settingSpinner) }
func (m *configMenu) openThinkingLevel() { m.open(m.settingThinkingLevel) }
func (m *configMenu) openMultiAgent()    { m.open(m.settingMultiAgent) }
func (m *configMenu) openTools()         { m.open(m.settingToolsMenu) }
func (m *configMenu) openBash()          { m.open(m.settingBash) }
func (m *configMenu) openMCP()           { m.open(m.settingMCP) }
func (m *configMenu) openLoopDetection() { m.open(m.settingLoopDetection) }
func (m *configMenu) openSkills()        { m.open(m.settingSkills) }

// Entry wrappers that push the root page onto history so ESC from the
// submenu returns to Settings root instead of closing the menu (bug: Teams /
// Orchestrator / Goals bypassed m.open, so back() exited to the TUI).
func (m *configMenu) openTeamsMenu()        { m.open(m.openTeams) }
func (m *configMenu) openOrchestratorMenu() { m.open(m.openOrchestrator) }
func (m *configMenu) openGoalsMenu()        { m.open(m.openGoalsRetention) }

func (m *configMenu) openActiveModel() {
	m.open(func() {
		m.selectModelPage("Select active model:", m.ctx.Config.ActiveModel, func(modelID string) {
			if modelID != "" {
				m.applySet("active_model", modelID)
			}
		})
	})
}

func (m *configMenu) toggleThinkingBlocks() { m.settingThinkingBlocks() }
func (m *configMenu) toggleShowThinking()   { m.settingShowThinking() }

func (m *configMenu) settingMajorMode() {
	m.current = m.settingMajorMode
	m.ctx.SelectOption("Select mode:", m.modeItems(), m.ctx.Config.ActiveMajor(), func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("mode.default.major", v)
		m.back()
	})
}

// selectModelPage shows a selector of configured models plus an "other" option.
// On confirmation it unwinds to the page that opened it and calls onSelected.
// On cancel it goes back one step, supporting drill-downs.
func (m *configMenu) settingProvider() {
	m.current = m.settingProvider
	items := configuredProviderItems(m.ctx.Config)
	items = append(items, tui.SelectorItem{
		Value:       "__add__",
		Label:       "— add provider —",
		Description: "configure a new provider",
	})
	items = append(items, tui.SelectorItem{
		Value:       "__remove__",
		Label:       "— remove provider —",
		Description: "remove a configured provider",
	})
	m.ctx.SelectOption("Select provider:", items, m.ctx.Config.ActiveProvider, func(v string, ok bool) {
		if !ok || v == "" {
			m.back()
			return
		}
		if v == "__add__" {
			m.open(m.runAddProviderWizard)
			return
		}
		if v == "__remove__" {
			m.open(m.runRemoveProviderSelect)
			return
		}
		m.applySet("active_provider", v)
		m.selectModelPage("Select model for provider:", "", func(modelID string) {
			if modelID != "" {
				m.applySet("active_model", modelID)
			}
		})
	})
}

// runRemoveProviderSelect shows a selector of configured providers for removal.
func (m *configMenu) runRemoveProviderSelect() {
	m.current = m.runRemoveProviderSelect
	items := configuredProviderItems(m.ctx.Config)
	if len(items) == 0 || items[0].Value == "" {
		m.flash("No providers configured.")
		m.back()
		return
	}
	m.ctx.SelectOption("Select provider to remove:", items, "", func(v string, ok bool) {
		if !ok || v == "" {
			m.back()
			return
		}
		m.confirmRemoveProvider(v)
	})
}

// confirmRemoveProvider shows a confirmation dialog before removing a provider.
func (m *configMenu) confirmRemoveProvider(id string) {
	m.current = func() { m.confirmRemoveProvider(id) }
	items := []tui.SelectorItem{
		{Value: "yes", Label: "Yes, remove provider", Description: id},
		{Value: "no", Label: "No, cancel", Description: ""},
	}
	m.ctx.SelectOption("Remove provider "+id+"?", items, "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			m.back()
			return
		}
		m.runRemoveProvider(id)
	})
}

// runRemoveProvider removes a provider from the configuration.
func (m *configMenu) runRemoveProvider(id string) {
	cfg := m.ctx.Config
	for i, p := range cfg.Providers {
		if p.ID != id {
			continue
		}
		cfg.Providers = append(cfg.Providers[:i], cfg.Providers[i+1:]...)
		if cfg.ActiveProvider == id {
			cfg.ActiveProvider = ""
		}
		m.saveConfig()
		m.flash(fmt.Sprintf("Provider %q removed.", id))
		m.settingProvider()
		return
	}
	m.flash(fmt.Sprintf("Provider %q not found.", id))
	m.settingProvider()
}

// runAddProviderWizard guides the user through adding a new provider interactively.
func (m *configMenu) runAddProviderWizard() {
	m.current = m.runAddProviderWizard
	items := buildProviderPresetItems()
	m.ctx.SelectOption("Select provider type:", items, "", m.addProviderWizardHandler)
}

func (m *configMenu) settingExecutionMode() {
	m.current = m.settingExecutionMode
	m.ctx.SelectOption("Select execution mode:", modeItems(), string(m.ctx.Config.Execution.Mode), func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("execution.mode", v)
		m.back()
	})
}

func retrySettingsLabel(cfg *config.Config) string {
	maxRetries := cfg.Execution.Retries
	if maxRetries <= 0 {
		maxRetries = 5
	}
	return fmt.Sprintf("%d retries, 5m cap", maxRetries)
}

// settingRetrySettings shows the retry-settings sub-menu and dispatches the
// selected entry through retrySettingHandlers.
func (m *configMenu) settingRetrySettings() {
	m.current = m.settingRetrySettings
	cfg := m.ctx.Config
	items := []tui.SelectorItem{
		{Value: "retries", Label: "Maximum retries", Description: retrySettingsLabel(cfg)},
		{Value: "provider_delay", Label: "Active provider retry cap", Description: activeProviderRetryDelay(cfg)},
	}
	m.ctx.SelectOption("Retry settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		handler, ok := m.retrySettingHandlers()[selected]
		if !ok {
			return
		}
		handler(m)
	})
}

// retrySettingHandlers maps retry-settings entries to their prompt handlers.
func (m *configMenu) retrySettingHandlers() map[string]func(*configMenu) {
	return map[string]func(*configMenu){
		"retries":        (*configMenu).promptMaxRetries,
		"provider_delay": (*configMenu).promptProviderRetryDelay,
	}
}

// promptMaxRetries asks for the execution.retries value, then returns to the
// retry-settings menu.
func (m *configMenu) promptMaxRetries() {
	m.current = m.settingRetrySettings
	m.ctx.ShowInput("Maximum retries:", fmt.Sprintf("%d", m.ctx.Config.Execution.Retries), func(v string, accepted bool) {
		if accepted && v != "" {
			m.applySet("execution.retries", v)
		}
		m.settingRetrySettings()
	})
}

// promptProviderRetryDelay asks for the active provider's max_retry_delay
// value, then returns to the retry-settings menu.
func (m *configMenu) promptProviderRetryDelay() {
	cfg := m.ctx.Config
	if cfg.ActiveProvider == "" {
		m.flash("Select an active provider first.")
		m.settingRetrySettings()
		return
	}
	m.current = m.settingRetrySettings
	m.ctx.ShowInput("Provider retry cap (duration):", activeProviderRetryDelay(cfg), func(v string, accepted bool) {
		if accepted && v != "" {
			m.applySet("providers."+cfg.ActiveProvider+".max_retry_delay", v)
		}
		m.settingRetrySettings()
	})
}

func activeProviderRetryDelay(cfg *config.Config) string {
	for _, p := range cfg.Providers {
		if p.ID == cfg.ActiveProvider && p.MaxRetryDelay != "" {
			return p.MaxRetryDelay
		}
	}
	return "5m (default)"
}

// toggleBoolLabel returns the string representation of the opposite bool,
// for toggle-style menu entries.
func toggleBoolLabel(v bool) string {
	if v {
		return "false"
	}
	return "true"
}

func (m *configMenu) settingTheme() {
	m.current = m.settingTheme
	m.ctx.SelectOption("Select theme:", themeItems(), m.ctx.Config.TUI.Theme, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("tui.theme", v)
		m.back()
	})
}

// openSpinnerLocation opens the spinner-location (chat|statusbar) picker.
func (m *configMenu) openSpinnerLocation() { m.open(m.settingSpinnerLocation) }

// settingSpinnerLocation lets the user pick where the busy spinner renders:
// the in-chat status line (default) or the status bar next to the model.
func (m *configMenu) settingSpinnerLocation() {
	m.current = m.settingSpinnerLocation
	items := []tui.SelectorItem{
		{Value: "chat", Label: "chat", Description: "in-chat \"Sending request...\" spinner line (default)"},
		{Value: "statusbar", Label: "statusbar", Description: "animated frame next to the model in the status bar only"},
	}
	current := m.ctx.Config.TUI.SpinnerLocation
	if current == "" {
		current = "chat"
	}
	m.ctx.SelectOption("Spinner location:", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("tui.spinner_location", v)
		m.back()
	})
}

// spinnerLocationMenuLabel returns the menu description for spinner_location.
func spinnerLocationMenuLabel(cfg *config.Config) string {
	if cfg.TUI.SpinnerLocation == "statusbar" {
		return "statusbar"
	}
	return "chat (default)"
}

func (m *configMenu) settingSpinner() {
	m.current = m.settingSpinner
	items := spinnerSelectionItems()
	current := m.ctx.Config.TUI.Spinner
	if current == "" {
		current = "arc"
	}
	m.ctx.SelectOption("Select spinner:", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("tui.spinner", v)
		m.applySpinner()
		m.back()
	})
}

func (m *configMenu) applySpinner() {
	name := m.ctx.Config.TUI.Spinner
	if name == "" || name == "none" {
		tui.SetSpinner(spinner.Definition{})
		return
	}
	if def, ok := spinner.Get(name); ok {
		tui.SetSpinner(def)
	}
}

// spinnerLabel returns a display label for the current spinner config.
func spinnerLabel(cfg *config.Config) string {
	name := cfg.TUI.Spinner
	if name == "" {
		return "arc (default)"
	}
	if name == "none" {
		return "none"
	}
	if def, ok := spinner.Get(name); ok && len(def.Frames) > 0 {
		return fmt.Sprintf("%s [%s]", name, def.Frames[0])
	}
	return name
}

// spinnerSelectionItems returns the list of spinner options for the config menu.
func spinnerSelectionItems() []tui.SelectorItem {
	items := []tui.SelectorItem{
		{Value: "none", Label: "(none)", Description: "no spinner animation"},
	}
	for _, name := range spinner.Names() {
		def, _ := spinner.Get(name)
		desc := fmt.Sprintf("%d frames", len(def.Frames))
		if len(def.Frames) > 0 {
			desc = fmt.Sprintf("%d frames  [%s]", len(def.Frames), def.Frames[0])
		}
		interval := time.Duration(def.IntervalMS()) * time.Millisecond
		items = append(items, tui.SelectorItem{
			Value:             name,
			Label:             name,
			Description:       desc,
			AnimationFrames:   def.Frames,
			AnimationInterval: interval,
		})
	}
	return items
}

func (m *configMenu) settingThinkingLevel() {
	m.current = m.settingThinkingLevel
	items := []tui.SelectorItem{
		{Value: "off", Label: "off", Description: "no reasoning"},
		{Value: "minimal", Label: "minimal", Description: "~1k tokens"},
		{Value: "low", Label: "low", Description: "~2k tokens"},
		{Value: "medium", Label: "medium", Description: "~8k tokens"},
		{Value: "high", Label: "high", Description: "~16k tokens"},
		{Value: "xhigh", Label: "xhigh", Description: "~32k tokens"},
	}
	current := string(m.ctx.Config.GetThinkingLevel("main_agent"))
	m.ctx.SelectOption("Select thinking level:", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if m.ctx.AgentManager != nil {
			if err := m.ctx.AgentManager.SetThinkingLevel(v); err != nil {
				m.flash(err.Error())
			}
		}
		m.applySet("thinking_level", v)
		m.back()
	})
}

func (m *configMenu) settingThinkingBlocks() {
	next := "off"
	if m.ctx.Config.TUI.Transparency.ThinkingCollapsed {
		next = "on"
	}
	m.applySet("tui.transparency.thinking_collapsed", next)
	m.showRoot()
}

func (m *configMenu) settingShowThinking() {
	next := "true"
	if m.ctx.Config.TUI.Transparency.ShowThinking {
		next = "false"
	}
	m.applySet("tui.transparency.show_thinking", next)
	m.showRoot()
}

func (m *configMenu) settingMultiAgent() {
	m.current = m.settingMultiAgent
	cfg := m.ctx.Config
	items := []tui.SelectorItem{
		{Value: "companion_model", Label: "Companion model", Description: companionBindingSummary(cfg)},
		{Value: "enabled", Label: "Multi-agent enabled", Description: boolLabel(cfg.MultiAgent.Enabled)},
	}
	m.ctx.SelectOption("Multi-agent settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		switch selected {
		case "companion_model":
			m.open(m.settingCompanionModel)
		case "enabled":
			m.settingMultiAgentEnabled()
		}
	})
}

// companionBindingSummary renders the companion provider+model binding for
// the multi-agent settings row (Bug B: the two are selected together
// like /model — a model cannot be selected without its attached provider).
func companionBindingSummary(cfg *config.Config) string {
	model := cfg.MultiAgent.CompanionModel
	provider := cfg.MultiAgent.CompanionProvider
	switch {
	case provider == "":
		return model
	case model == "":
		return provider
	default:
		return provider + " / " + model
	}
}

// settingCompanionModel picks the companion provider+model as ONE choice:
// the selected model's provider is persisted alongside the model so the two
// can never contradict (Bug B).
func (m *configMenu) settingCompanionModel() {
	m.current = m.settingCompanionModel
	m.selectModelPageFull("Select companion model:", m.ctx.Config.MultiAgent.CompanionModel, func(modelID, providerID string) {
		// Provider first: the model binding is only meaningful with its
		// provider in place.
		if providerID != "" {
			m.applySet("multi_agent.companion_provider", providerID)
		}
		if modelID != "" {
			m.applySet("multi_agent.companion_model", modelID)
		}
	})
}

func (m *configMenu) settingMultiAgentEnabled() {
	next := "true"
	if m.ctx.Config.MultiAgent.Enabled {
		next = "false"
	}
	m.applySet("multi_agent.enabled", next)
	m.settingMultiAgent()
}

// bashSettingsLabel summarizes bash-tool guard settings for the top-level menu.
func bashSettingsLabel(cfg *config.Config) string {
	return "warn on shell file edits: " + boolLabel(warnFileEditsOn(cfg))
}

// warnFileEditsOn reports whether the bash file-edit hint is active (nil = on).
func warnFileEditsOn(cfg *config.Config) bool {
	return cfg.Tools.Bash.WarnFileEdits == nil || *cfg.Tools.Bash.WarnFileEdits
}

func loopDetectionLabel(cfg *config.Config) string {
	if cfg.Execution.DisableToolBudget {
		return "disabled"
	}
	return fmt.Sprintf("warn:%d stop:%d", cfg.Execution.LoopWarning, cfg.Execution.LoopInterrupt)
}

// skillsLabel returns a neutral submenu hint for the top-level Skills row:
// per-source on-counts (embedded / local). Showing the raw execution-mode
// value here ("Skills inline") read like a binary toggle, but the row opens
// a submenu — the mode itself is editable inside it.
func skillsLabel(reg core.SkillRegistry, cfg *config.Config) string {
	if reg == nil {
		return "settings"
	}
	return fmt.Sprintf("embedded %s · local %s",
		skillSourceLabel(reg, "embedded", cfg),
		skillSourceLabel(reg, "local", cfg))
}

// settingLoopDetection is the /config → Loop detection sub-menu.
func (m *configMenu) settingLoopDetection() {
	m.current = m.settingLoopDetection
	items := []tui.SelectorItem{
		{Value: "think_loop", Label: "Thinking-loop detection", Description: loopDetectionStatusLabel(m.ctx.LoopDetector, "think")},
		{Value: "tool_loop", Label: "Tool-loop detection", Description: loopDetectionStatusLabel(m.ctx.LoopDetector, "tool")},
		{Value: "stream_loop", Label: "Stream-loop detection", Description: loopDetectionStatusLabel(m.ctx.LoopDetector, "stream")},
		{Value: "thinking_stall", Label: "Thinking-stall watchdog", Description: loopDetectionStatusLabel(m.ctx.LoopDetector, "stall")},
		{Value: "thresholds", Label: "Threshold settings", Description: "warn/stop/repeat limits"},
	}
	m.ctx.SelectOption("Loop detection settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		switch selected {
		case "think_loop":
			m.chooseLoopDetectionAction("think")
		case "tool_loop":
			m.chooseLoopDetectionAction("tool")
		case "stream_loop":
			m.chooseLoopDetectionAction("stream")
		case "thinking_stall":
			m.chooseLoopDetectionAction("stall")
		case "thresholds":
			m.open(m.settingLoopThresholds)
		}
	})
}

// loopDetectionStatusLabel reports the effective on/off state and how it was
// set (temporary session override vs persisted config).
func loopDetectionStatusLabel(ld *core.LoopDetector, kind string) string {
	if ld == nil {
		return "on"
	}
	if ld.TempOverride(kind) {
		return "off (session)"
	}
	if ld.Disabled(kind) {
		return "off (saved)"
	}
	return "on"
}

// loopDetectionKindLabel returns a human label for the detection kind.
func loopDetectionKindLabel(kind string) string {
	if kind == "think" {
		return "thinking-loop detection"
	}
	if kind == "stream" {
		return "stream-text loop detection"
	}
	if kind == "stall" {
		return "thinking-stall watchdog"
	}
	return "tool-call loop detection"
}

// loopDetectionConfigKey maps the detection kind to its persisted config key.
func loopDetectionConfigKey(kind string) string {
	if kind == "think" {
		return "execution.disable_thinking_loop_detection"
	}
	if kind == "stream" {
		return "execution.disable_stream_loop_detection"
	}
	if kind == "stall" {
		return "execution.disable_thinking_stall_detection"
	}
	return "execution.disable_tool_loop_detection"
}

// chooseLoopDetectionAction offers the choice between a session-only toggle
// and a persistent (config-saved) change, honouring the current state.
func (m *configMenu) chooseLoopDetectionAction(kind string) {
	ld := m.ctx.LoopDetector
	if ld == nil {
		m.flash("Loop detector not available.")
		m.settingLoopDetection()
		return
	}
	label := loopDetectionKindLabel(kind)
	tempOff := ld.TempOverride(kind)
	persistOff := ld.Disabled(kind) && !tempOff

	var items []tui.SelectorItem
	if tempOff {
		items = append(items, tui.SelectorItem{Value: "temp_on", Label: "Re-enable (this session)", Description: "clear session override"})
	} else {
		items = append(items, tui.SelectorItem{Value: "temp_off", Label: "Disable (this session)", Description: "temporary, current session only"})
	}
	if persistOff {
		items = append(items, tui.SelectorItem{Value: "persist_on", Label: "Re-enable (saved)", Description: "clear saved config override"})
	} else {
		items = append(items, tui.SelectorItem{Value: "persist_off", Label: "Disable (saved)", Description: "persist across sessions"})
	}

	m.ctx.SelectOption("Change "+label+":", items, "", func(selected string, ok bool) {
		if !ok {
			m.settingLoopDetection()
			return
		}
		m.applyLoopDetectionAction(kind, selected)
	})
}

// applyLoopDetectionAction executes the chosen temp/persist action.
func (m *configMenu) applyLoopDetectionAction(kind, action string) {
	ld := m.ctx.LoopDetector
	label := loopDetectionKindLabel(kind)
	switch action {
	case "temp_off":
		ld.SetTempOverride(kind, true)
		m.flash(fmt.Sprintf("Temporary: %s disabled (current session only)", label))
	case "temp_on":
		ld.SetTempOverride(kind, false)
		m.flash(fmt.Sprintf("Temporary: %s enabled (current session only)", label))
	case "persist_off":
		m.applySet(loopDetectionConfigKey(kind), "true")
		m.flash(fmt.Sprintf("Saved: %s disabled (persisted across sessions)", label))
	case "persist_on":
		m.applySet(loopDetectionConfigKey(kind), "false")
		m.flash(fmt.Sprintf("Saved: %s enabled (persisted across sessions)", label))
	}
	m.settingLoopDetection()
}

// settingLoopThresholds is the /config → Loop detection → Thresholds sub-menu.
func (m *configMenu) settingLoopThresholds() {
	m.current = m.settingLoopThresholds
	cfg := m.ctx.Config
	// Zero means "use the default"; the numbers below are the effective
	// defaults (embedded configs/default.yaml for the tool-loop thresholds,
	// the runtime defaults documented on config.ExecutionConfig for the
	// stream_loop_* fields).
	items := []tui.SelectorItem{
		{Value: "loop_warning", Label: "Loop warning threshold", Description: thresholdLabel(cfg.Execution.LoopWarning, 3)},
		{Value: "loop_interrupt", Label: "Loop interrupt threshold", Description: thresholdLabel(cfg.Execution.LoopInterrupt, 5)},
		{Value: "tool_repeat_total", Label: "Max tool repeats (total)", Description: disabledThresholdLabel(cfg.Execution.MaxToolRepeatTotal)},
		{Value: "tool_repeat_consecutive", Label: "Max tool repeats (consecutive)", Description: thresholdLabel(cfg.Execution.MaxToolRepeatConsecutive, 2)},
		{Value: "max_tool_calls", Label: "Max tool calls per turn", Description: thresholdLabel(cfg.Execution.MaxToolCalls, 3)},
		{Value: "max_consecutive_tool_rounds", Label: "Max consecutive tool-call rounds", Description: thresholdLabel(cfg.Execution.MaxConsecutiveToolRounds, 15)},
		{Value: "disable_tool_budget", Label: "Disable tool budget", Description: boolLabel(cfg.Execution.DisableToolBudget)},
		{Value: "stream_repeats", Label: "Stream-loop stop repeats", Description: thresholdLabel(cfg.Execution.StreamLoopMaxRepeats, 5)},
		{Value: "stream_min_period", Label: "Stream-loop min unit length (chars)", Description: thresholdLabel(cfg.Execution.StreamLoopMinPeriod, 50)},
		{Value: "stream_strikes", Label: "Stream-loop stop strikes", Description: thresholdLabel(cfg.Execution.StreamLoopMaxStrikes, 3)},
		{Value: "stream_reset_after", Label: "Stream-loop strike reset (clean msgs/tool calls)", Description: thresholdLabel(cfg.Execution.StreamLoopResetAfter, 10)},
		{Value: "runaway_repeats", Label: "Runaway-loop stop repeats", Description: thresholdLabel(cfg.Execution.RunawayLoopMaxRepeats, 2)},
	}
	m.ctx.SelectOption("Loop threshold settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.handleLoopThresholdSetting(selected)
	})
}

func intLabel(v int) string {
	if v <= 0 {
		return "default"
	}
	return fmt.Sprintf("%d", v)
}

// thresholdLabel renders an int loop threshold for the menu. Zero means "use
// the default", so the effective value used is shown with a "(default)"
// annotation instead of the bare word "default".
func thresholdLabel(v, def int) string {
	if v <= 0 {
		return fmt.Sprintf("%d (default)", def)
	}
	return fmt.Sprintf("%d", v)
}

// disabledThresholdLabel renders an int guard value that is OFF at zero (e.g.
// execution.max_tool_repeat_total): zero disables the guard rather than
// falling back to a default, so "off" is shown instead of "default".
func disabledThresholdLabel(v int) string {
	if v <= 0 {
		return "off"
	}
	return fmt.Sprintf("%d", v)
}

func (m *configMenu) handleLoopThresholdSetting(selected string) {
	cfg := m.ctx.Config
	type loopField struct {
		key     string
		prompt  string
		intVal  *int
		isBool  bool
		boolVal *bool
	}
	fields := map[string]loopField{
		"loop_warning":                {key: "execution.loop_warning", prompt: "Loop warning threshold:", intVal: &cfg.Execution.LoopWarning},
		"loop_interrupt":              {key: "execution.loop_interrupt", prompt: "Loop interrupt threshold:", intVal: &cfg.Execution.LoopInterrupt},
		"tool_repeat_total":           {key: "execution.max_tool_repeat_total", prompt: "Max total tool repeats:", intVal: &cfg.Execution.MaxToolRepeatTotal},
		"tool_repeat_consecutive":     {key: "execution.max_tool_repeat_consecutive", prompt: "Max consecutive tool repeats:", intVal: &cfg.Execution.MaxToolRepeatConsecutive},
		"max_tool_calls":              {key: "execution.max_tool_calls", prompt: "Max tool calls per turn:", intVal: &cfg.Execution.MaxToolCalls},
		"max_consecutive_tool_rounds": {key: "execution.max_consecutive_tool_rounds", prompt: "Max consecutive tool-call rounds (0 = unlimited):", intVal: &cfg.Execution.MaxConsecutiveToolRounds},
		"disable_tool_budget":         {key: "execution.disable_tool_budget", isBool: true, boolVal: &cfg.Execution.DisableToolBudget},
		"stream_repeats":              {key: "execution.stream_loop_max_repeats", prompt: "Stream-loop stop repeats (>=2):", intVal: &cfg.Execution.StreamLoopMaxRepeats},
		"stream_min_period":           {key: "execution.stream_loop_min_period", prompt: "Stream-loop min repeated unit length in chars (>=8, 0 = default 50):", intVal: &cfg.Execution.StreamLoopMinPeriod},
		"stream_strikes":              {key: "execution.stream_loop_max_strikes", prompt: "Stream-loop warnings before stop (>=1):", intVal: &cfg.Execution.StreamLoopMaxStrikes},
		"stream_reset_after":          {key: "execution.stream_loop_reset_after", prompt: "Clean messages/tool calls to reset strikes (>=1):", intVal: &cfg.Execution.StreamLoopResetAfter},
		"runaway_repeats":             {key: "execution.runaway_loop_max_repeats", prompt: "Runaway-loop repeats before stop (>=1, 0 = default 2):", intVal: &cfg.Execution.RunawayLoopMaxRepeats},
	}
	f, ok := fields[selected]
	if !ok {
		m.back()
		return
	}
	if f.isBool {
		next := "false"
		if *f.boolVal {
			next = "true"
		}
		m.applySet(f.key, next)
		m.settingLoopThresholds()
		return
	}
	m.ctx.ShowInput(f.prompt, fmt.Sprintf("%d", *f.intVal), func(v string, ok bool) {
		if ok && v != "" {
			m.applySet(f.key, v)
		}
		m.settingLoopThresholds()
	})
}
