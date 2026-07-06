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
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tools"
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
		{Value: "compression", Label: "Compression", Description: compressionLabel(cfg)},
		{Value: "theme", Label: "Theme", Description: cfg.TUI.Theme},
		{Value: "spinner", Label: "Spinner", Description: spinnerLabel(cfg)},
		{Value: "thinking_level", Label: "Thinking level", Description: string(cfg.GetThinkingLevel("main_agent"))},
		{Value: "thinking_blocks", Label: "Thinking blocks", Description: thinkingBlocksLabel(cfg)},
		{Value: "show_thinking", Label: "Show thinking", Description: boolLabel(cfg.TUI.Transparency.ShowThinking)},
		{Value: "multi_agent", Label: "Multi-agent", Description: multiAgentLabel(cfg, m.ctx.ForegroundOrchestrator)},
		{Value: "orchestrator", Label: "Orchestrator", Description: orchestratorLabel(cfg)},
		{Value: "tools", Label: "Tools", Description: toolsEnabledLabel(cfg)},
		{Value: "loop_detection", Label: "Loop detection", Description: loopDetectionLabel(cfg)},
		{Value: "skills", Label: "Skills", Description: skillsLabel(cfg)},
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
		"profile":         (*configMenu).openMajorMode,
		"model":           (*configMenu).openActiveModel,
		"provider":        (*configMenu).openProvider,
		"models":          (*configMenu).openModels,
		"mode":            (*configMenu).openExecutionMode,
		"compression":     (*configMenu).openCompression,
		"theme":           (*configMenu).openTheme,
		"spinner":         (*configMenu).openSpinner,
		"thinking_level":  (*configMenu).openThinkingLevel,
		"thinking_blocks": (*configMenu).toggleThinkingBlocks,
		"show_thinking":   (*configMenu).toggleShowThinking,
		"multi_agent":     (*configMenu).openMultiAgent,
		"orchestrator":    (*configMenu).openOrchestrator,
		"tools":           (*configMenu).openTools,
		"loop_detection":  (*configMenu).openLoopDetection,
		"skills":          (*configMenu).openSkills,
		"goals":           (*configMenu).openGoalsRetention,
	}
}

func (m *configMenu) openMajorMode()     { m.open(m.settingMajorMode) }
func (m *configMenu) openProvider()      { m.open(m.settingProvider) }
func (m *configMenu) openModels()        { m.open(m.settingModels) }
func (m *configMenu) openExecutionMode() { m.open(m.settingExecutionMode) }
func (m *configMenu) openCompression()   { m.open(m.settingCompression) }
func (m *configMenu) openTheme()         { m.open(m.settingTheme) }
func (m *configMenu) openSpinner()       { m.open(m.settingSpinner) }
func (m *configMenu) openThinkingLevel() { m.open(m.settingThinkingLevel) }
func (m *configMenu) openMultiAgent()    { m.open(m.settingMultiAgent) }
func (m *configMenu) openTools()         { m.open(m.settingTools) }
func (m *configMenu) openLoopDetection() { m.open(m.settingLoopDetection) }
func (m *configMenu) openSkills()        { m.open(m.settingSkills) }

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

func buildProviderPresetItems() []tui.SelectorItem {
	presets := config.PresetProviders()
	items := make([]tui.SelectorItem, 0, len(presets)+1)
	for _, p := range presets {
		items = append(items, tui.SelectorItem{
			Value:       p.ID,
			Label:       p.Name,
			Description: p.Endpoint,
		})
	}
	items = append(items, tui.SelectorItem{
		Value:       "__custom__",
		Label:       "— custom provider —",
		Description: "enter endpoint and API key manually",
	})
	return items
}

func (m *configMenu) addProviderWizardHandler(v string, ok bool) {
	if !ok || v == "" {
		m.back()
		return
	}
	if v == "__custom__" {
		m.promptProviderEndpoint(func(endpoint, apiKey string) {
			m.promptProviderID(endpoint, apiKey)
		})
		return
	}
	preset, found := findPresetProvider(v)
	if !found {
		m.back()
		return
	}
	m.finalizePresetProvider(preset)
}

func findPresetProvider(id string) (config.ProviderPreset, bool) {
	for _, p := range config.PresetProviders() {
		if p.ID == id {
			return p, true
		}
	}
	return config.ProviderPreset{}, false
}

func (m *configMenu) finalizePresetProvider(preset config.ProviderPreset) {
	if !preset.NeedsAPIKey {
		m.finalizeAddProvider(preset.ID, preset.Name, preset.Endpoint, "")
		return
	}
	m.ctx.ShowInput("API key for "+preset.Name+":", "", func(apiKey string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.finalizeAddProvider(preset.ID, preset.Name, preset.Endpoint, apiKey)
	})
}

// promptProviderEndpoint prompts for a custom provider endpoint.
func (m *configMenu) promptProviderEndpoint(onDone func(endpoint, apiKey string)) {
	m.ctx.ShowInput("Provider endpoint (e.g. https://api.example.com/v1):", "", func(endpoint string, ok bool) {
		if !ok || endpoint == "" {
			m.back()
			return
		}
		// Ask for API key
		m.ctx.ShowInput("API key (optional, press Enter to skip):", "", func(apiKey string, ok bool) {
			if !ok {
				m.back()
				return
			}
			onDone(endpoint, apiKey)
		})
	})
}

// promptProviderID asks the user for a provider ID (short identifier).
func (m *configMenu) promptProviderID(endpoint, apiKey string) {
	// Derive a default ID from the endpoint
	defaultID := config.DeriveProviderID(endpoint)
	m.ctx.ShowInput("Provider ID (short identifier, e.g. 'my-provider'):", defaultID, func(id string, ok bool) {
		if !ok || id == "" {
			m.back()
			return
		}
		m.finalizeAddProvider(id, id, endpoint, apiKey)
	})
}

// finalizeAddProvider saves the provider config and optionally prompts for a model.
func (m *configMenu) finalizeAddProvider(id, name, endpoint, apiKey string) {
	cfg := m.ctx.Config
	saver := m.ctx.ConfigSaver

	upsertProviderConfig(cfg, id, name, endpoint, apiKey)
	if saver != nil {
		if err := saver.SaveHomeProvidersAndModels(cfg); err != nil {
			m.flash("Failed to save: " + err.Error())
			m.back()
			return
		}
	}
	m.flash("Provider '" + id + "' added.")
	m.promptAddModelForProvider(id)
}

func upsertProviderConfig(cfg *config.Config, id, name, endpoint, apiKey string) {
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == id {
			cfg.Providers[i].Endpoint = endpoint
			cfg.Providers[i].APIKey = apiKey
			cfg.Providers[i].Name = name
			return
		}
	}
	cfg.Providers = append(cfg.Providers, config.ProviderConfig{
		ID:       id,
		Name:     name,
		Endpoint: endpoint,
		APIKey:   apiKey,
	})
}

func (m *configMenu) promptAddModelForProvider(providerID string) {
	m.ctx.ShowInput("Add a model? Enter model ID (or press Enter to skip):", "", func(modelID string, ok bool) {
		if !ok || modelID == "" {
			m.back()
			return
		}
		cfg := m.ctx.Config
		saver := m.ctx.ConfigSaver
		upsertModelConfig(cfg, modelID, providerID)
		if saver != nil {
			if err := saver.SaveHomeProvidersAndModels(cfg); err != nil {
				m.flash("Failed to save model: " + err.Error())
				m.back()
				return
			}
		}
		m.flash("Model '" + modelID + "' added for '" + providerID + "'.")
		m.back()
	})
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

// settingCompression is the /config → Compression sub-menu. It exposes the
// strategy, trigger threshold, and max-tokens so the user can tune context
// compression without editing YAML by hand.
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
		{Value: "companion_model", Label: "Companion model", Description: cfg.MultiAgent.CompanionModel},
		{Value: "companion_provider", Label: "Companion provider", Description: cfg.MultiAgent.CompanionProvider},
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
		case "companion_provider":
			m.open(m.settingCompanionProvider)
		case "enabled":
			m.settingMultiAgentEnabled()
		}
	})
}

func (m *configMenu) settingCompanionModel() {
	m.current = m.settingCompanionModel
	m.selectModelPage("Select companion model:", m.ctx.Config.MultiAgent.CompanionModel, func(modelID string) {
		if modelID != "" {
			m.applySet("multi_agent.companion_model", modelID)
		}
	})
}

func (m *configMenu) settingCompanionProvider() {
	m.current = m.settingCompanionProvider
	items := configuredProviderItems(m.ctx.Config)
	m.ctx.SelectOption("Select companion provider:", items, m.ctx.Config.MultiAgent.CompanionProvider, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if v != "" {
			m.applySet("multi_agent.companion_provider", v)
		}
		m.back()
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

// settingTools is the /config → Tools sub-menu for toggling optional tools.
func (m *configMenu) settingTools() {
	m.current = m.settingTools
	cfg := m.ctx.Config
	items := buildToolItems(cfg)
	m.ctx.SelectOption("Toggle optional tools:", items, "", m.toolToggleHandler)
}

func buildToolItems(cfg *config.Config) []tui.SelectorItem {
	toolList := []struct {
		name string
		desc string
	}{
		{"bg_exec", "Background process execution"},
		{"delegate_to", "Delegate tasks to sub-agents"},
		{"memento", "Persistent memory files"},
		{"pty_exec", "Pseudo-terminal sessions"},
		{"request_review", "Request companion review"},
		{"smartsearch", "BM25 code search (needs restart to enable)"},
		{"ssh_bash", "Remote SSH command execution"},
		{"webfetch", "URL content fetching"},
	}
	items := make([]tui.SelectorItem, len(toolList))
	for i, t := range toolList {
		items[i] = tui.SelectorItem{
			Value:       t.name,
			Label:       t.name,
			Description: boolLabel(getToolEnabled(cfg, t.name)),
		}
	}
	return items
}

func (m *configMenu) toolToggleHandler(selected string, ok bool) {
	if !ok {
		m.back()
		return
	}
	if !isConfigurableTool(selected) {
		m.back()
		return
	}
	cfg := m.ctx.Config
	enabled := getToolEnabled(cfg, selected)
	m.applyToolToggle(selected, enabled)
	setToolEnabled(cfg, selected, !enabled)
	m.saveToolToggle(selected, !enabled)
	m.flash(fmt.Sprintf("Tool %s %s", selected, toggleNextLabel(enabled)))
	m.settingTools()
}

func toggleNextLabel(enabled bool) string {
	if enabled {
		return "off"
	}
	return "on"
}

func (m *configMenu) applyToolToggle(toolName string, currentlyEnabled bool) {
	if !currentlyEnabled {
		return
	}
	if tr, ok := m.ctx.ToolRegistry.(*tools.ToolRegistry); ok {
		tr.Unregister(toolName)
	}
	if m.ctx.AgentManager != nil {
		_ = m.ctx.AgentManager.SetTools(m.ctx.ToolRegistry.All())
	}
}

func (m *configMenu) saveToolToggle(toolName string, enabled bool) {
	if m.ctx.ConfigSaver == nil {
		return
	}
	path := []string{"tools", "enabled", toolName}
	if toolName == "smartsearch" {
		path = []string{"tools", "smartsearch", "enabled"}
	}
	if err := m.ctx.ConfigSaver.SaveHomeField(path, enabled); err != nil {
		m.flash("Failed to save config: " + err.Error())
	}
}

// toolsEnabledLabel returns a short summary of how many optional tools are on.
func toolsEnabledLabel(cfg *config.Config) string {
	names := tools.ConfigurableToolNames()
	on := 0
	for _, n := range names {
		if getToolEnabled(cfg, n) {
			on++
		}
	}
	return fmt.Sprintf("%d/%d enabled", on, len(names))
}

// loopDetectionLabel returns a short summary of loop detection settings.
func loopDetectionLabel(cfg *config.Config) string {
	if cfg.Execution.DisableToolBudget {
		return "disabled"
	}
	return fmt.Sprintf("warn:%d stop:%d", cfg.Execution.LoopWarning, cfg.Execution.LoopInterrupt)
}

// skillsLabel returns a short summary of skills configuration.
func skillsLabel(cfg *config.Config) string {
	if cfg.Skills.ExecutionMode != "" {
		return cfg.Skills.ExecutionMode
	}
	return "inline"
}

// settingLoopDetection is the /config → Loop detection sub-menu.
func (m *configMenu) settingLoopDetection() {
	m.current = m.settingLoopDetection
	items := []tui.SelectorItem{
		{Value: "think_loop", Label: "Thinking-loop detection", Description: loopDetectionToggleLabel(m.ctx.LoopDetector, "think")},
		{Value: "tool_loop", Label: "Tool-loop detection", Description: loopDetectionToggleLabel(m.ctx.LoopDetector, "tool")},
		{Value: "thresholds", Label: "Threshold settings", Description: "warn/stop/repeat limits"},
	}
	m.ctx.SelectOption("Loop detection settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		switch selected {
		case "think_loop":
			m.toggleLoopDetection("think")
		case "tool_loop":
			m.toggleLoopDetection("tool")
		case "thresholds":
			m.open(m.settingLoopThresholds)
		}
	})
}

// loopDetectionToggleLabel returns the display label for a temp loop-detection
// override. The detection is on unless the loop detector reports it disabled.
func loopDetectionToggleLabel(ld *core.LoopDetector, kind string) string {
	if ld == nil {
		return "on"
	}
	if !ld.TempOverride(kind) {
		return "on"
	}
	return "off"
}

// toggleLoopDetection flips the session-level temp override for the given kind.
func (m *configMenu) toggleLoopDetection(kind string) {
	ld := m.ctx.LoopDetector
	if ld == nil {
		m.flash("Loop detector not available.")
		m.settingLoopDetection()
		return
	}
	disabled := ld.TempOverride(kind)
	ld.SetTempOverride(kind, !disabled)
	m.flash(loopDetectionToggleFlash(kind, !disabled))
	m.settingLoopDetection()
}

func loopDetectionToggleFlash(kind string, disabled bool) string {
	state := "enabled"
	if disabled {
		state = "disabled"
	}
	switch kind {
	case "think":
		return fmt.Sprintf("Temporary: thinking-loop detection %s (current session only)", state)
	case "tool":
		return fmt.Sprintf("Temporary: tool-call loop detection %s (current session only)", state)
	}
	return ""
}

// settingLoopThresholds is the /config → Loop detection → Thresholds sub-menu.
func (m *configMenu) settingLoopThresholds() {
	m.current = m.settingLoopThresholds
	cfg := m.ctx.Config
	items := []tui.SelectorItem{
		{Value: "loop_warning", Label: "Loop warning threshold", Description: intLabel(cfg.Execution.LoopWarning)},
		{Value: "loop_interrupt", Label: "Loop interrupt threshold", Description: intLabel(cfg.Execution.LoopInterrupt)},
		{Value: "tool_repeat_total", Label: "Max tool repeats (total)", Description: intLabel(cfg.Execution.MaxToolRepeatTotal)},
		{Value: "tool_repeat_consecutive", Label: "Max tool repeats (consecutive)", Description: intLabel(cfg.Execution.MaxToolRepeatConsecutive)},
		{Value: "max_tool_calls", Label: "Max tool calls per turn", Description: intLabel(cfg.Execution.MaxToolCalls)},
		{Value: "disable_tool_budget", Label: "Disable tool budget", Description: boolLabel(cfg.Execution.DisableToolBudget)},
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
		"loop_warning":            {key: "execution.loop_warning", prompt: "Loop warning threshold:", intVal: &cfg.Execution.LoopWarning},
		"loop_interrupt":          {key: "execution.loop_interrupt", prompt: "Loop interrupt threshold:", intVal: &cfg.Execution.LoopInterrupt},
		"tool_repeat_total":       {key: "execution.max_tool_repeat_total", prompt: "Max total tool repeats:", intVal: &cfg.Execution.MaxToolRepeatTotal},
		"tool_repeat_consecutive": {key: "execution.max_tool_repeat_consecutive", prompt: "Max consecutive tool repeats:", intVal: &cfg.Execution.MaxToolRepeatConsecutive},
		"max_tool_calls":          {key: "execution.max_tool_calls", prompt: "Max tool calls per turn:", intVal: &cfg.Execution.MaxToolCalls},
		"disable_tool_budget":     {key: "execution.disable_tool_budget", isBool: true, boolVal: &cfg.Execution.DisableToolBudget},
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

// settingSkills is the /config → Skills sub-menu.
func (m *configMenu) settingSkills() {
	m.current = m.settingSkills
	items := []tui.SelectorItem{
	{Value: "execution_mode", Label: "Execution mode", Description: m.ctx.Config.Skills.ExecutionMode},
	}
	m.ctx.SelectOption("Skills settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.handleSkillsSetting(selected)
	})
}

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

// applySet invokes applyConfigSet and lets it surface errors to the user via
// the output buffer. The return value is always nil in practice, so we do not
// propagate it inside async callbacks.
func (m *configMenu) applySet(key, value string) {
	_ = applyConfigSet(m.ctx, key, value)
}

// saveConfig persists the current config and flashes on error.
func (m *configMenu) saveConfig() {
	if m.ctx.ConfigSaver == nil {
		return
	}
	if err := m.ctx.ConfigSaver.Save(m.ctx.Config); err != nil {
		m.flash("Failed to save config: " + err.Error())
	}
}

func (m *configMenu) flash(text string) {
	m.ctx.Flash(text)
}

func configuredProviderItems(cfg *config.Config) []tui.SelectorItem {
	seen := map[string]bool{}
	var items []tui.SelectorItem
	for _, p := range cfg.Providers {
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		items = append(items, tui.SelectorItem{Value: p.ID, Label: p.ID, Description: p.Name})
	}
	if len(items) == 0 {
		items = append(items, tui.SelectorItem{
			Value:       "",
			Label:       "(no providers configured)",
			Description: "use /config:add provider",
		})
	}
	return items
}

func modelManagerItems(cfg *config.Config) []tui.SelectorItem {
	var items []tui.SelectorItem
	items = append(items, tui.SelectorItem{Value: "__add__", Label: "Add model…", Description: "Configure a new model"})
	items = append(items, tui.SelectorItem{Value: "__set_active__", Label: "Set active model", Description: cfg.ActiveModel})
	for _, mod := range cfg.Models {
		items = append(items, tui.SelectorItem{
			Value:       "__edit__" + mod.ID,
			Label:       "Edit " + mod.ID,
			Description: mod.Model,
		})
		items = append(items, tui.SelectorItem{
			Value:       "__remove__" + mod.ID,
			Label:       "Remove " + mod.ID,
			Description: "",
		})
	}
	return items
}

func multiAgentLabel(cfg *config.Config, orch *multiagent.ForegroundOrchestrator) string {
	if cfg.MultiAgent.Enabled {
		return "on"
	}
	if orch != nil {
		mode := orch.Mode()
		if mode == multiagent.WorkflowAgentDriven || mode == multiagent.WorkflowCompanionMinor {
			return "on"
		}
	}
	return "off"
}

func thinkingBlocksLabel(cfg *config.Config) string {
	if cfg.TUI.Transparency.ThinkingCollapsed {
		return "collapsed"
	}
	return "expanded"
}

func boolLabel(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func (m *configMenu) modeItems() []tui.SelectorItem {
	if m.ctx.ModeRegistry != nil {
		majors := m.ctx.ModeRegistry.Majors()
		items := make([]tui.SelectorItem, 0, len(majors))
		for _, major := range majors {
			var desc string
			if spec, err := m.ctx.ModeRegistry.Resolve(major); err == nil {
				desc = spec.Description
			}
			items = append(items, tui.SelectorItem{
				Value:       string(major),
				Label:       string(major),
				Description: desc,
			})
		}
		return items
	}
	return []tui.SelectorItem{
		{Value: "coder", Label: "coder", Description: "full coding mode"},
		{Value: "planner", Label: "planner", Description: "architecture mode"},
		{Value: "reviewer", Label: "reviewer", Description: "code review mode"},
	}
}

func modeItems() []tui.SelectorItem {
	return []tui.SelectorItem{
		{Value: "yolo", Label: "yolo", Description: "run tools without confirmation"},
		{Value: "solo", Label: "solo", Description: "auto-run tools constrained to the codebase"},
		{Value: "confirm", Label: "confirm", Description: "confirm each tool"},
		{Value: "review", Label: "review", Description: "review after each turn"},
	}
}

func themeItems() []tui.SelectorItem {
	return []tui.SelectorItem{
		{Value: "dark", Label: "dark", Description: "dark theme"},
		{Value: "light", Label: "light", Description: "light theme"},
	}
}

// deriveModelID creates a config-friendly model ID from a model string.
// It uses the last path segment, lowercases it, and replaces non-alphanumeric
// characters with dashes.
func deriveModelID(model string) string {
	base := model
	if idx := strings.LastIndex(model, "/"); idx >= 0 {
		base = model[idx+1:]
	}
	base = strings.ToLower(base)
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		return "model"
	}
	return id
}

// SetupCommand launches the setup wizard or shows first-run info.
type SetupCommand struct{}

func (c *SetupCommand) Name() string      { return "setup" }
func (c *SetupCommand) Aliases() []string { return []string{} }
func (c *SetupCommand) IsInternal() bool  { return true }
func (c *SetupCommand) ShortHelp() string { return "Launch the setup wizard" }
func (c *SetupCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *SetupCommand) Run(ctx core.Context, args []string) error {
	ctx.ControlEvent(event.ControlEvent{RunWizard: true})
	writeStr(ctx, "Launching setup wizard...\n")
	return nil
}
