// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/provider"
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

func configSubcommandCompletions(prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, v := range []struct{ val, desc string }{
		{"set", "set a config key"},
		{"add", "add a provider or model"},
		{"remove", "remove a provider or model"},
		{"reload", "reload config"},
	} {
		if prefix == "" || strings.HasPrefix(v.val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
		}
	}
	return comps
}

func prefixKeys(subPrefix, key string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, k := range configKeyCompletions(key) {
		comps = append(comps, core.ArgCompletion{Value: subPrefix + k.Value, Description: k.Description})
	}
	return comps
}

func prefixValues(subPrefix, key, valuePrefix string, ctx core.Context) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, v := range configValueCompletions(ctx, key, valuePrefix) {
		comps = append(comps, core.ArgCompletion{Value: subPrefix + key + ":" + v.Value, Description: v.Description})
	}
	return comps
}

func configKeyCompletions(prefix string) []core.ArgCompletion {
	keys := []struct{ value, description string }{
		{"mode.default.major", "coder | planner | reviewer | <custom>"},
		{"active_provider", "provider id"},
		{"active_model", "model id"},
		{"execution.mode", "yolo | confirm | review | solo"},
		{"mode.plan_file_path", "path to plan file (default: .goa/plan.md)"},
		{"execution.max_tool_calls", "integer"},
		{"execution.max_tool_repeat_total", "integer"},
		{"execution.max_tool_repeat_consecutive", "integer"},
		{"execution.max_tool_repeat", "integer"},
		{"tui.theme", "dark | light"},
		{"tui.spinner", "spinner name or none"},
		{"tui.transparency.show_thinking", "true | false"},
		{"tui.transparency.thinking_collapsed", "true | false"},
		{"logging.level", "debug | info | warn | error"},
		{"logging.file", "path"},
		{"thinking_level", "off | minimal | low | medium | high | xhigh"},
		{"multi_agent.enabled", "true | false"},
		{"multi_agent.companion_model", "model id"},
		{"multi_agent.companion_provider", "provider id"},
	}
	var comps []core.ArgCompletion
	for _, k := range keys {
		if prefix == "" || strings.HasPrefix(k.value, prefix) {
			comps = append(comps, core.ArgCompletion{Value: k.value, Description: k.description})
		}
	}
	return comps
}

func configValueCompletions(ctx core.Context, key, prefix string) []core.ArgCompletion {
	switch key {
	case "mode.default.major":
		return profileCompletionValues(ctx, prefix)
	case "execution.mode":
		return modeCompletionValues(prefix)
	case "mode.plan_file_path":
		return []core.ArgCompletion{{Value: ".goa/plan.md", Description: "default plan file in project root"}}
	case "tui.theme":
		return themeCompletionValues(prefix)
	case "tui.transparency.show_thinking", "tui.transparency.thinking_collapsed", "multi_agent.enabled":
		return boolCompletionValues(prefix)
	case "thinking_level":
		return thinkingLevelCompletionValues(prefix)
	case "active_model":
		return modelCompletionValues(ctx, prefix)
	case "active_provider", "multi_agent.companion_provider":
		return providerCompletionValues(ctx, prefix)
	}
	return nil
}

func profileCompletionValues(ctx core.Context, prefix string) []core.ArgCompletion {
	if ctx.ModeRegistry != nil {
		majors := ctx.ModeRegistry.Majors()
		values := make([]string, 0, len(majors))
		for _, m := range majors {
			values = append(values, string(m))
		}
		return filteredCompletions(values, prefix, "")
	}
	return filteredCompletions([]string{"coder", "planner", "reviewer"}, prefix, "")
}

func modeCompletionValues(prefix string) []core.ArgCompletion {
	return filteredCompletions([]string{"yolo", "solo", "confirm", "review"}, prefix, "")
}

func themeCompletionValues(prefix string) []core.ArgCompletion {
	return filteredCompletions([]string{"dark", "light"}, prefix, "")
}

func boolCompletionValues(prefix string) []core.ArgCompletion {
	return filteredCompletions([]string{"true", "false"}, prefix, "")
}

func thinkingLevelCompletionValues(prefix string) []core.ArgCompletion {
	return filteredCompletions([]string{"off", "minimal", "low", "medium", "high", "xhigh"}, prefix, "")
}

func modelCompletionValues(ctx core.Context, prefix string) []core.ArgCompletion {
	var values []string
	for _, m := range ctx.Config.Models {
		values = append(values, m.ID)
	}
	return filteredCompletions(values, prefix, "")
}

func providerCompletionValues(ctx core.Context, prefix string) []core.ArgCompletion {
	var values []string
	seen := map[string]bool{}
	for _, p := range ctx.Config.Providers {
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		values = append(values, p.ID)
	}
	return filteredCompletions(values, prefix, "")
}

func filteredCompletions(values []string, prefix, desc string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, v := range values {
		if prefix == "" || strings.HasPrefix(v, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v, Description: desc})
		}
	}
	return comps
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
	default:
		return fmt.Errorf("unknown config subcommand: %s (use 'set', 'add', 'remove' or 'reload')", args[0])
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
	items := []tui.SelectorItem{
		{Value: "profile", Label: "Mode", Description: cfg.ActiveMajor()},
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
		{Value: "tools", Label: "Tools", Description: toolsEnabledLabel(cfg)},
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
		"tools":           (*configMenu).openTools,
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
func (m *configMenu) selectModelPage(title, current string, onSelected func(string)) {
	baseLen := len(m.history)
	m.current = func() { m.selectModelPage(title, current, onSelected) }
	items := m.configuredModelItems()
	items = append(items, tui.SelectorItem{
		Value:       "__other__",
		Label:       "— other model —",
		Description: "type or fetch from provider",
	})
	m.ctx.SelectOption(title, items, current, func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if selected != "__other__" {
			m.returnTo(baseLen)
			onSelected(selected)
			return
		}
		m.open(func() { m.promptOtherModel(onSelected, baseLen) })
	})
}

func (m *configMenu) configuredModelItems() []tui.SelectorItem {
	var items []tui.SelectorItem
	for _, mod := range m.ctx.Config.Models {
		items = append(items, tui.SelectorItem{
			Value:       mod.ID,
			Label:       mod.ID,
			Description: mod.Model,
		})
	}
	if len(items) == 0 {
		items = append(items, tui.SelectorItem{
			Value:       "",
			Label:       "(no models configured)",
			Description: "choose — other model —",
		})
	}
	return items
}

func (m *configMenu) promptOtherModel(onSelected func(string), baseLen int) {
	providers := configuredProviderItems(m.ctx.Config)
	if len(providers) == 0 || providers[0].Value == "" {
		m.flash("No providers configured. Add a provider first.")
		m.back()
		return
	}
	if len(providers) == 1 {
		m.open(func() { m.resolveModel(providers[0].Value, "", onSelected, baseLen) })
		return
	}
	m.ctx.SelectOption("Select provider:", providers, "", func(providerID string, ok bool) {
		if !ok || providerID == "" {
			m.back()
			return
		}
		m.open(func() { m.resolveModel(providerID, "", onSelected, baseLen) })
	})
}

const modelCacheTTL = 5 * time.Minute

func (m *configMenu) resolveModel(providerID, modelName string, onSelected func(string), baseLen int) {
	var models []provider.ModelInfo
	var err error
	if pm, ok := m.ctx.ProviderManager.(interface {
		ListModelsCached(string, time.Duration) ([]provider.ModelInfo, error)
	}); ok {
		models, err = pm.ListModelsCached(providerID, modelCacheTTL)
	} else {
		models, err = m.ctx.ProviderManager.ListModels(providerID)
	}
	if err != nil || len(models) == 0 {
		if modelName != "" {
			m.returnTo(baseLen)
			onSelected(modelName)
			return
		}
		m.open(func() { m.promptCustomModel(onSelected, baseLen) })
		return
	}
	items := make([]tui.SelectorItem, 0, len(models)+1)
	for _, mod := range models {
		items = append(items, tui.SelectorItem{Value: mod.ID, Label: mod.ID, Description: providerID})
	}
	items = append(items, tui.SelectorItem{
		Value:       "__custom__",
		Label:       "— custom model —",
		Description: "type a model string",
	})
	m.ctx.SelectOption("Select model:", items, modelName, func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if selected == "__custom__" {
			m.open(func() { m.promptCustomModel(onSelected, baseLen) })
			return
		}
		m.returnTo(baseLen)
		onSelected(selected)
	})
}

func (m *configMenu) promptCustomModel(onSelected func(string), baseLen int) {
	m.ctx.ShowInput("Model string:", "", func(value string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.returnTo(baseLen)
		onSelected(value)
	})
}

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

func upsertModelConfig(cfg *config.Config, modelID, providerID string) {
	for i := range cfg.Models {
		if cfg.Models[i].ID == modelID {
			cfg.Models[i].ProviderID = providerID
			cfg.Models[i].Model = modelID
			return
		}
	}
	cfg.Models = append(cfg.Models, config.ModelConfig{
		ID:         modelID,
		ProviderID: providerID,
		Model:      modelID,
	})
}

func (m *configMenu) settingModels() {
	m.current = m.settingModels
	items := modelManagerItems(m.ctx.Config)
	m.ctx.SelectOption("Model manager:", items, "", func(selected string, ok bool) {
		if !ok || selected == "" {
			m.back()
			return
		}
		switch {
		case selected == "__add__":
			m.open(m.runAddModel)
		case selected == "__set_active__":
			m.open(m.runSetActiveModel)
		case strings.HasPrefix(selected, "__edit__"):
			m.open(func() { m.runEditModel(strings.TrimPrefix(selected, "__edit__")) })
		case strings.HasPrefix(selected, "__remove__"):
			m.open(func() { m.runRemoveModel(strings.TrimPrefix(selected, "__remove__")) })
		default:
			m.back()
		}
	})
}

func (m *configMenu) runAddModel() {
	m.current = m.runAddModel
	providers := configuredProviderItems(m.ctx.Config)
	if len(providers) == 0 || providers[0].Value == "" {
		m.flash("No providers configured. Add a provider first.")
		m.back()
		return
	}
	m.ctx.SelectOption("Select provider for new model:", providers, "", func(providerID string, ok bool) {
		if !ok || providerID == "" {
			m.back()
			return
		}
		m.selectModelPage("Select model:", "", func(modelID string) {
			if modelID == "" {
				return
			}
			m.addModel(providerID, deriveModelID(modelID), modelID)
		})
	})
}

func (m *configMenu) addModel(providerID, modelID, modelName string) {
	cfg := m.ctx.Config
	cfg.Models = append(cfg.Models, config.ModelConfig{
		ID:         modelID,
		Name:       modelName,
		ProviderID: providerID,
		Model:      modelName,
	})
	m.saveConfig()
	m.flash(fmt.Sprintf("Model %q added.", modelID))
	m.settingModels()
}

func (m *configMenu) runSetActiveModel() {
	m.current = m.runSetActiveModel
	items := m.configuredModelItems()
	if len(items) == 0 || items[0].Value == "" {
		m.flash("No models configured.")
		m.back()
		return
	}
	m.ctx.SelectOption("Set active model:", items, m.ctx.Config.ActiveModel, func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if selected != "" {
			m.applySet("active_model", selected)
		}
		m.settingModels()
	})
}

func (m *configMenu) runEditModel(id string) {
	m.current = func() { m.runEditModel(id) }
	cfg := m.ctx.Config
	idx := findModelIndex(cfg.Models, id)
	if idx < 0 {
		m.flash(fmt.Sprintf("Model %q not found.", id))
		m.back()
		return
	}
	m.promptTemperatureEdit(idx)
}

func (m *configMenu) promptTemperatureEdit(idx int) {
	cfg := m.ctx.Config
	mod := cfg.Models[idx]
	m.ctx.ShowInput("Temperature:", fmt.Sprintf("%f", mod.Temperature), func(value string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if value != "" {
			m.applyTemperature(idx, value)
		}
		m.settingModels()
	})
}

func (m *configMenu) applyTemperature(idx int, value string) {
	cfg := m.ctx.Config
	temp, err := strconv.ParseFloat(value, 64)
	if err != nil {
		m.flash(fmt.Sprintf("Invalid temperature %q.", value))
		return
	}
	cfg.Models[idx].Temperature = temp
	m.saveConfig()
	m.flash(fmt.Sprintf("Model %q updated.", cfg.Models[idx].ID))
}

func findModelIndex(models []config.ModelConfig, id string) int {
	for i, mod := range models {
		if mod.ID == id {
			return i
		}
	}
	return -1
}

func (m *configMenu) runRemoveModel(id string) {
	m.confirmRemoveModel(id)
}

// confirmRemoveModel shows a confirmation dialog before removing a model.
func (m *configMenu) confirmRemoveModel(id string) {
	m.current = func() { m.confirmRemoveModel(id) }
	items := []tui.SelectorItem{
		{Value: "yes", Label: "Yes, remove model", Description: id},
		{Value: "no", Label: "No, cancel", Description: ""},
	}
	m.ctx.SelectOption("Remove model "+id+"?", items, "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			m.back()
			return
		}
		m.doRemoveModel(id)
	})
}

// doRemoveModel actually removes a model from the configuration.
func (m *configMenu) doRemoveModel(id string) {
	cfg := m.ctx.Config
	for i, mod := range cfg.Models {
		if mod.ID != id {
			continue
		}
		cfg.Models = append(cfg.Models[:i], cfg.Models[i+1:]...)
		if cfg.ActiveModel == id {
			cfg.ActiveModel = ""
		}
		m.saveConfig()
		m.flash(fmt.Sprintf("Model %q removed.", id))
		m.settingModels()
		return
	}
	m.flash(fmt.Sprintf("Model %q not found.", id))
	m.settingModels()
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
func (m *configMenu) settingCompression() {
	m.current = m.settingCompression
	cfg := m.ctx.Config
	strategy := cfg.ContextCompression.Strategy
	if strategy == "" {
		strategy = "tool_elision"
	}
	items := []tui.SelectorItem{
		{Value: "strategy", Label: "Strategy", Description: strategy},
		{Value: "threshold", Label: "Trigger threshold", Description: fmt.Sprintf("%d%%", cfg.ContextCompression.ThresholdPercent)},
		{Value: "max_tokens", Label: "Max tokens", Description: maxTokensLabel(cfg.ContextCompression.MaxTokens)},
		{Value: "enabled", Label: "Enabled", Description: boolLabel(cfg.ContextCompression.Enabled)},
		{Value: "on_context_error", Label: "Compress on context error", Description: boolLabel(cfg.ContextCompression.OnContextError)},
	}
	m.ctx.SelectOption("Compression settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		switch selected {
		case "strategy":
			m.open(m.settingCompressionStrategy)
		case "threshold":
			m.open(m.settingCompressionThreshold)
		case "max_tokens":
			m.open(m.settingCompressionMaxTokens)
		case "enabled":
			m.applySet("context_compression.enabled", toggleBoolLabel(cfg.ContextCompression.Enabled))
			m.settingCompression()
		case "on_context_error":
			m.applySet("context_compression.on_context_error", toggleBoolLabel(cfg.ContextCompression.OnContextError))
			m.settingCompression()
		}
	})
}

func (m *configMenu) settingCompressionStrategy() {
	m.current = m.settingCompressionStrategy
	current := m.ctx.Config.ContextCompression.Strategy
	if current == "" {
		current = "tool_elision"
	}
	items := []tui.SelectorItem{
		{Value: "micro", Label: "micro", Description: "truncate old tool result bodies (cache-friendly)"},
		{Value: "tool_elision", Label: "tool_elision", Description: "replace old tool args/results with placeholders"},
		{Value: "selective", Label: "selective", Description: "drop oldest messages, keep system + recent turns"},
		{Value: "hybrid", Label: "hybrid", Description: "tool_elision → selective → summarize"},
		{Value: "summarize", Label: "summarize", Description: "ask the LLM to summarize older turns"},
	}
	m.ctx.SelectOption("Compression strategy:", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.strategy", v)
		m.back()
	})
}

func (m *configMenu) settingCompressionThreshold() {
	m.current = m.settingCompressionThreshold
	items := []tui.SelectorItem{
		{Value: "50", Label: "50%", Description: "early"},
		{Value: "75", Label: "75%", Description: "balanced"},
		{Value: "80", Label: "80%", Description: "default"},
		{Value: "90", Label: "90%", Description: "late"},
		{Value: "100", Label: "100%", Description: "only at the limit"},
	}
	current := fmt.Sprintf("%d", m.ctx.Config.ContextCompression.ThresholdPercent)
	m.ctx.SelectOption("Trigger threshold (% of max tokens):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.threshold_percent", v)
		m.back()
	})
}

func (m *configMenu) settingCompressionMaxTokens() {
	m.current = m.settingCompressionMaxTokens
	items := []tui.SelectorItem{
		{Value: "0", Label: "auto", Description: "use the model's context window"},
		{Value: "8192", Label: "8,192", Description: "small models"},
		{Value: "16384", Label: "16,384", Description: ""},
		{Value: "32768", Label: "32,768", Description: ""},
		{Value: "65536", Label: "65,536", Description: ""},
		{Value: "131072", Label: "131,072", Description: "large models"},
	}
	current := fmt.Sprintf("%d", m.ctx.Config.ContextCompression.MaxTokens)
	m.ctx.SelectOption("Max tokens (compression limit; 0 = auto):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.max_tokens", v)
		m.back()
	})
}

// compressionLabel returns a one-line summary for the root /config menu.
func compressionLabel(cfg *config.Config) string {
	if !cfg.ContextCompression.Enabled {
		return "off"
	}
	strategy := cfg.ContextCompression.Strategy
	if strategy == "" {
		strategy = "tool_elision"
	}
	return fmt.Sprintf("%s @ %d%%", strategy, cfg.ContextCompression.ThresholdPercent)
}

// maxTokensLabel renders the compression max_tokens value for display.
func maxTokensLabel(v int) string {
	if v <= 0 {
		return "auto"
	}
	return fmt.Sprintf("%d", v)
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
		{"ssh_bash", "Remote SSH command execution"},
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
	if err := m.ctx.ConfigSaver.SaveHomeField([]string{"tools", "enabled", toolName}, enabled); err != nil {
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

func handleConfigAdd(ctx core.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /config:add provider <id> <endpoint> [api-key]  or  /config:add model <id> <provider-id> <model-name>")
	}
	switch args[0] {
	case "provider":
		return addProvider(ctx, args[1:])
	case "model":
		return addModel(ctx, args[1:])
	default:
		return fmt.Errorf("unknown add target: %q (use 'provider' or 'model')", args[0])
	}
}

func addProvider(ctx core.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: /config:add provider <id> <endpoint> [api-key]")
	}
	return doAddProvider(ctx.Config, ctx.ConfigSaver, ctx, args[0], args[1], strings.Join(args[2:], ""))
}

func doAddProvider(cfg *config.Config, saver config.ConfigSaver, out core.OutputWriter, id, endpoint, apiKey string) error {
	for i := range cfg.Providers {
		if cfg.Providers[i].ID != id {
			continue
		}
		cfg.Providers[i].Endpoint = endpoint
		cfg.Providers[i].APIKey = apiKey
		if cfg.Providers[i].Name == "" {
			cfg.Providers[i].Name = id
		}
		return saveAndReport(out, saver, cfg, "provider", id)
	}
	cfg.Providers = append(cfg.Providers, config.ProviderConfig{
		ID:       id,
		Name:     id,
		Endpoint: endpoint,
		APIKey:   apiKey,
	})
	return saveAndReport(out, saver, cfg, "provider", id)
}

func addModel(ctx core.Context, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: /config:add model <id> <provider-id> <model-name>")
	}
	return doAddModel(ctx.Config, ctx.ConfigSaver, ctx, args[0], args[1], args[2])
}

func doAddModel(cfg *config.Config, saver config.ConfigSaver, out core.OutputWriter, id, providerID, modelName string) error {
	for i := range cfg.Models {
		if cfg.Models[i].ID != id {
			continue
		}
		cfg.Models[i].ProviderID = providerID
		cfg.Models[i].Model = modelName
		if cfg.Models[i].Name == "" {
			cfg.Models[i].Name = modelName
		}
		return saveAndReport(out, saver, cfg, "model", id)
	}
	cfg.Models = append(cfg.Models, config.ModelConfig{
		ID:         id,
		Name:       modelName,
		ProviderID: providerID,
		Model:      modelName,
	})
	return saveAndReport(out, saver, cfg, "model", id)
}

func saveAndReport(out core.OutputWriter, saver config.ConfigSaver, cfg *config.Config, kind, id string) error {
	if saver == nil {
		writeFmt(out, "Added %s %s (in memory; no saver)\n", kind, id)
		return nil
	}
	if err := saver.Save(cfg); err != nil {
		writeFmt(out, "Added %s %s in memory, but failed to save: %v\n", kind, id, err)
		return nil
	}
	writeFmt(out, "Added %s %s\n", kind, id)
	return nil
}

// handleConfigRemove removes a provider or model from the configuration.
func handleConfigRemove(ctx core.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: /config:remove provider <id>  or  /config:remove model <id>")
	}
	switch args[0] {
	case "provider":
		return removeProvider(ctx, args[1:])
	case "model":
		return removeModel(ctx, args[1:])
	default:
		return fmt.Errorf("unknown remove target: %q (use 'provider' or 'model')", args[0])
	}
}

func removeProvider(ctx core.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /config:remove provider <id>")
	}
	id := args[0]
	cfg := ctx.Config
	for i, p := range cfg.Providers {
		if p.ID != id {
			continue
		}
		cfg.Providers = append(cfg.Providers[:i], cfg.Providers[i+1:]...)
		// Also remove models associated with this provider
		var remaining []config.ModelConfig
		for _, m := range cfg.Models {
			if m.ProviderID != id {
				remaining = append(remaining, m)
			}
		}
		cfg.Models = remaining
		if cfg.ActiveProvider == id {
			cfg.ActiveProvider = ""
			cfg.ActiveModel = ""
		}
		return saveAndReport(ctx, ctx.ConfigSaver, cfg, "provider", id)
	}
	return fmt.Errorf("provider %q not found", id)
}

func removeModel(ctx core.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /config:remove model <id>")
	}
	id := args[0]
	cfg := ctx.Config
	for i, m := range cfg.Models {
		if m.ID != id {
			continue
		}
		cfg.Models = append(cfg.Models[:i], cfg.Models[i+1:]...)
		if cfg.ActiveModel == id {
			cfg.ActiveModel = ""
		}
		return saveAndReport(ctx, ctx.ConfigSaver, cfg, "model", id)
	}
	return fmt.Errorf("model %q not found", id)
}

func handleConfigSet(ctx core.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: /config:set <key> <value>")
	}
	key := args[0]
	value := strings.Join(args[1:], " ")
	return applyConfigSet(ctx, key, value)
}

// configSavePaths maps user-facing dotted keys to the canonical YAML path used
// for persistence. Most keys map 1:1, but shortcuts like "thinking_level"
// expand to nested config fields.
var configSavePaths = map[string][]string{
	"thinking_level": {"thinking_levels", "main_agent"},
}

// modeDefaultsPath returns the YAML path for persisting the autonomy level of
// the current major mode. This makes /config:set execution.mode yolo survive a
// restart even when the previous session had a non-yolo default.
func modeDefaultsPath(cfg *config.Config) []string {
	major := cfg.DefaultModeState().Major
	if major == "" {
		major = internal.MajorCoder
	}
	return []string{"mode", "defaults", string(major)}
}

func applyConfigSet(ctx core.Context, key, value string) error {
	path := strings.Split(key, ".")
	prevProvider := ctx.Config.ActiveProvider
	if err := setConfigField(ctx.Config, path, value); err != nil {
		writeFmt(ctx, "Invalid value for %s: %v\n", key, err)
		return nil
	}
	// execution.mode needs the new-mode-system default updated before we sync
	// the runtime agent mode, otherwise the agent manager keeps the old autonomy.
	if key == "execution.mode" {
		updateModeDefault(ctx.Config, value)
	}
	if err := syncRuntimeConfig(ctx, key, value); err != nil {
		writeFmt(ctx, "%v\n", err)
		return nil
	}
	if ctx.ConfigSaver == nil {
		writeFmt(ctx, "Set %s = %s (in memory; no saver)\n", key, value)
		return nil
	}
	if err := persistConfigValue(ctx, key, path, value); err != nil {
		writeFmt(ctx, "%v\n", err)
		return nil
	}
	// Changing the active model may also change the provider; persist and
	// propagate that switch so the next turn uses the new provider+model.
	if key == "active_model" && ctx.Config.ActiveProvider != prevProvider {
		if err := ctx.ConfigSaver.SaveHomeField([]string{"active_provider"}, ctx.Config.ActiveProvider); err != nil {
			writeFmt(ctx, "set active_model = %s (provider switch to %s not persisted: %v)\n", value, ctx.Config.ActiveProvider, err)
		} else {
			writeFmt(ctx, "Set active_provider = %s\n", ctx.Config.ActiveProvider)
		}
		propagateModelSwitch(ctx, ctx.Config)
	}
	writeFmt(ctx, "Set %s = %s\n", key, value)
	ctx.FooterRefresh()
	return nil
}

func syncRuntimeConfig(ctx core.Context, key, value string) error {
	if ctx.AgentManager == nil {
		return nil
	}
	switch key {
	case "thinking_level":
		if err := ctx.AgentManager.SetThinkingLevel(value); err != nil {
			return fmt.Errorf("set %s = %s (in memory, but failed to sync runtime: %v)", key, value, err)
		}
	case "mode.default.major", "execution.mode":
		ctx.AgentManager.SetMode(ctx.Config.DefaultModeState())
	}
	return nil
}

func persistConfigValue(ctx core.Context, key string, path []string, value string) error {
	savePath := path
	if override, ok := configSavePaths[key]; ok {
		savePath = override
	}
	if err := ctx.ConfigSaver.SaveHomeField(savePath, scalarValue(value)); err != nil {
		return fmt.Errorf("set %s = %s (in memory, but failed to persist: %v)", key, value, err)
	}
	if key == "execution.mode" {
		if err := persistModeDefault(ctx, value); err != nil {
			return fmt.Errorf("set %s = %s (mode default not persisted: %v)", key, value, err)
		}
	}
	return nil
}

func updateModeDefault(cfg *config.Config, value string) {
	if cfg.Mode.Defaults == nil {
		cfg.Mode.Defaults = make(map[internal.MajorMode]internal.AutonomyLevel)
	}
	major := cfg.DefaultModeState().Major
	cfg.Mode.Defaults[major] = internal.AutonomyLevel(value)
}

func persistModeDefault(ctx core.Context, value string) error {
	return ctx.ConfigSaver.SaveHomeField(modeDefaultsPath(ctx.Config), scalarValue(value))
}

// scalarValue converts common UI values to scalars suitable for YAML.
func scalarValue(value string) any {
	if v, err := strconv.ParseBool(value); err == nil {
		return v
	}
	if v, err := strconv.Atoi(value); err == nil {
		return v
	}
	return value
}

// configSetter updates a single config field from a string value.
type configSetter func(cfg *config.Config, value string) error

var configSetters = map[string]configSetter{
	"mode.default.major":                    setActiveMajor,
	"active_provider":                       setString(func(cfg *config.Config) *string { return &cfg.ActiveProvider }),
	"active_model":                          setActiveModel,
	"multi_agent.companion_model":           setStringWithValidate(func(cfg *config.Config) *string { return &cfg.MultiAgent.CompanionModel }, validateActiveModel),
	"execution.mode":                        setExecutionMode,
	"mode.plan_file_path":                   setString(func(cfg *config.Config) *string { return &cfg.Mode.PlanFilePath }),
	"execution.max_tool_calls":              setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolCalls }),
	"execution.max_tool_repeat_total":       setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolRepeatTotal }),
	"execution.max_tool_repeat_consecutive": setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolRepeatConsecutive }),
	"execution.max_tool_repeat":             setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolRepeatTotal }),
	"tui.theme":                             setString(func(cfg *config.Config) *string { return &cfg.TUI.Theme }),
	"tui.spinner":                           setSpinnerName,
	"tui.transparency.show_thinking":        setBool(func(cfg *config.Config) *bool { return &cfg.TUI.Transparency.ShowThinking }),
	"tui.transparency.thinking_collapsed":   setThinkingCollapsed,
	"logging.level":                         setString(func(cfg *config.Config) *string { return &cfg.Logging.Level }),
	"logging.file":                          setString(func(cfg *config.Config) *string { return &cfg.Logging.File }),
	"thinking_level":                        setThinkingLevel,
	"multi_agent.enabled":                   setBool(func(cfg *config.Config) *bool { return &cfg.MultiAgent.Enabled }),
	"multi_agent.companion_provider":        setString(func(cfg *config.Config) *string { return &cfg.MultiAgent.CompanionProvider }),
	"context_compression.enabled":           setBool(func(cfg *config.Config) *bool { return &cfg.ContextCompression.Enabled }),
	"context_compression.strategy":          setCompressionStrategy,
	"context_compression.threshold_percent": setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.ThresholdPercent }, 0, 100),
	"context_compression.max_tokens":        setInt(func(cfg *config.Config) *int { return &cfg.ContextCompression.MaxTokens }),
	"context_compression.on_context_error":  setBool(func(cfg *config.Config) *bool { return &cfg.ContextCompression.OnContextError }),
}

func setActiveMajor(cfg *config.Config, value string) error {
	cfg.SetActiveMajor(value)
	return nil
}

// setString creates a configSetter that sets a string field from a string value.
// Setter names like "active_model" can pass additional validation.
func setString(getter func(*config.Config) *string) configSetter {
	return func(cfg *config.Config, value string) error {
		*getter(cfg) = value
		return nil
	}
}

// setStringWithValidate creates a configSetter that validates the value before setting.
func setStringWithValidate(getter func(*config.Config) *string, validate func(string) error) configSetter {
	return func(cfg *config.Config, value string) error {
		if err := validate(value); err != nil {
			return err
		}
		*getter(cfg) = value
		return nil
	}
}

// setActiveModel sets the active model and follows the model's configured
// provider when it differs from the current provider. The change is rejected
// if the model's provider is not configured.
func setActiveModel(cfg *config.Config, value string) error {
	if err := validateActiveModel(value); err != nil {
		return err
	}
	if np := providerIDForModel(cfg, value); np != "" && np != cfg.ActiveProvider {
		if cfg.GetProviderByID(np) == nil {
			return fmt.Errorf("provider %q for model %q is not configured", np, value)
		}
		cfg.ActiveProvider = np
	}
	cfg.ActiveModel = value
	return nil
}

// validateActiveModel rejects values that look like rendered footer display strings
// (e.g., "llama3 \u2022 high | llama3 (companion) \u2022 medium") instead of bare model IDs.
func validateActiveModel(value string) error {
	// Footer display strings contain " | " between main and companion model parts.
	if strings.Contains(value, " | ") {
		return fmt.Errorf("invalid model value: %q looks like a TUI footer display string, not a model ID", value)
	}
	// Footer display strings contain thinking level indicators (\u2022 = bullet).
	if strings.Contains(value, " \u2022 ") {
		return fmt.Errorf("invalid model value: %q contains thinking level indicator, use bare model ID instead", value)
	}
	return nil
}

func setInt(getter func(*config.Config) *int) configSetter {
	return func(cfg *config.Config, value string) error {
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		*getter(cfg) = v
		return nil
	}
}

func setBool(getter func(*config.Config) *bool) configSetter {
	return func(cfg *config.Config, value string) error {
		*getter(cfg) = parseBool(value)
		return nil
	}
}

func setThinkingCollapsed(cfg *config.Config, value string) error {
	cfg.TUI.Transparency.ThinkingCollapsed = parseToggle(value, true)
	return nil
}

var validThinkingLevels = map[string]bool{
	"off":     true,
	"minimal": true,
	"low":     true,
	"medium":  true,
	"high":    true,
	"xhigh":   true,
}

func setThinkingLevel(cfg *config.Config, value string) error {
	if !validThinkingLevels[strings.ToLower(value)] {
		return fmt.Errorf("thinking_level must be one of: off, minimal, low, medium, high, xhigh")
	}
	cfg.ThinkingLevels.MainAgent = strings.ToLower(value)
	return nil
}

func setExecutionMode(cfg *config.Config, value string) error {
	switch strings.ToLower(value) {
	case "yolo", "solo", "confirm", "review":
		cfg.Execution.Mode = internal.ExecutionMode(value)
		return nil
	}
	return fmt.Errorf("execution.mode must be yolo, solo, confirm, or review")
}

// setCompressionStrategy validates and applies a context_compression.strategy
// value. Allowed values mirror agentic.CompressionStrategy.
// setSpinnerName validates and sets tui.spinner config value.
func setSpinnerName(cfg *config.Config, value string) error {
	if value == "" || value == "none" {
		cfg.TUI.Spinner = value
		return nil
	}
	if _, ok := spinner.Get(value); ok {
		cfg.TUI.Spinner = value
		return nil
	}
	return fmt.Errorf("unknown spinner: %q (choose from: none, %s)", value, strings.Join(spinner.Names(), ", "))
}

func setCompressionStrategy(cfg *config.Config, value string) error {
	switch strings.ToLower(value) {
	case "", "tool_elision", "selective", "summarize", "hybrid", "micro":
		cfg.ContextCompression.Strategy = strings.ToLower(value)
		return nil
	}
	return fmt.Errorf("context_compression.strategy must be one of: tool_elision, selective, summarize, hybrid, micro")
}

// setIntRange returns a setter that parses an int and enforces an inclusive
// [min,max] range. Used for fields like threshold_percent where out-of-range
// values would silently disable the feature.
func setIntRange(getter func(*config.Config) *int, min, max int) configSetter {
	return func(cfg *config.Config, value string) error {
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		if v < min || v > max {
			return fmt.Errorf("value must be between %d and %d (got %d)", min, max, v)
		}
		*getter(cfg) = v
		return nil
	}
}

func setConfigField(cfg *config.Config, path []string, value string) error {
	key := strings.Join(path, ".")
	setter, ok := configSetters[key]
	if !ok {
		return fmt.Errorf("unknown config key: %s", key)
	}
	return setter(cfg, value)
}

func parseBool(value string) bool {
	switch strings.ToLower(value) {
	case "true", "on", "1", "yes":
		return true
	default:
		return false
	}
}

// parseToggle parses a UI-friendly on/off value.
// When inverted is true, "off" means the underlying boolean is true (used for
// thinking_collapsed, where off = collapse = true).
func parseToggle(value string, inverted bool) bool {
	v := parseBool(value)
	if isOnOff(value) {
		v = strings.ToLower(value) == "on"
	}
	if inverted {
		return !v
	}
	return v
}

func isOnOff(value string) bool {
	switch strings.ToLower(value) {
	case "on", "off":
		return true
	default:
		return false
	}
}

func handleConfigReload(ctx core.Context) error {
	if ctx.ConfigSaver == nil {
		writeStr(ctx, "Config saver not available. Cannot reload.\n")
		return nil
	}
	fresh, err := ctx.ConfigSaver.Reload()
	if err != nil {
		writeFmt(ctx, "Error reloading config: %v\n", err)
		return nil
	}
	*ctx.Config = *fresh
	writeStr(ctx, "Config reloaded from all cascade layers.\n")
	return nil
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
