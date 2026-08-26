// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"
	"sync"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/tui"
)

// Ensure tui is used
var _ = tui.SelectorItem{}

// pickerProviderMu serializes the provider picker's optimistic add + async
// failure rollback, which mutate cfg.Providers off the commandLoop (unlike
// every other provider edit, which is loop-owned). Tests also gate their
// config reads on it to stay race-free.
var pickerProviderMu sync.Mutex

// ProviderCommand sets or displays the active provider.
//
// Consolidated command (replaces the former /providers, /prs, and /provider):
//
//	/provider             open interactive picker (lists all providers)
//	/provider <id>        switch to <id>
//	/provider?            display current provider + model
//	/provider??           long help
type ProviderCommand struct{}

func (c *ProviderCommand) Name() string      { return "provider" }
func (c *ProviderCommand) Aliases() []string { return []string{} }
func (c *ProviderCommand) ShortHelp() string { return "Set, list, or display the active LLM provider" }
func (c *ProviderCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// Status implements core.StatusProvider so /provider? prints the live state
// instead of the static short-help text.
func (c *ProviderCommand) Status(ctx core.Context) string {
	if ctx.Config == nil {
		return ""
	}
	name := ctx.Config.ActiveProvider
	if name == "" {
		name = "(none)"
	}
	model := ctx.Config.ActiveModel
	if model == "" {
		model = "(none)"
	}
	return fmt.Sprintf("Provider: %s   Model: %s", name, model)
}

func (c *ProviderCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return showProviderPicker(ctx, ctx.Config, ctx.ConfigSaver)
	}
	return switchProvider(ctx, ctx.Config, args[0])
}

func switchProvider(ctx core.Context, cfg *config.Config, providerID string) error {
	prevProvider := cfg.ActiveProvider

	// Pick the first model from the new provider if the current model doesn't belong to it
	modelID := cfg.ActiveModel
	if !isModelForProvider(cfg, modelID, providerID) {
		if m := firstModelForProvider(cfg, providerID); m != nil {
			modelID = m.ID
		} else {
			modelID = ""
		}
	}

	// One coupled unit — manager validation FIRST, then config + persist +
	// agent push (SetModel/stream options) together, so the live session can
	// never keep the previous provider's credentials or model.
	if err := applyCoupledSwitch(ctx, cfg, ctx.ConfigSaver, providerID, modelID); err != nil {
		return err
	}

	ctx.FooterRefresh()
	if prevProvider != providerID {
		writeFmt(ctx, "Switched to provider: %s (model: %s)\n", providerID, cfg.ActiveModel)
	} else {
		writeFmt(ctx, "Provider: %s\n", providerID)
	}
	return nil
}

func isModelForProvider(cfg *config.Config, modelID, providerID string) bool {
	for _, m := range cfg.Models {
		if m.ID == modelID && m.ProviderID == providerID {
			return true
		}
	}
	return false
}

func firstModelForProvider(cfg *config.Config, providerID string) *config.ModelConfig {
	for _, m := range cfg.Models {
		if m.ProviderID == providerID && !m.Ephemeral {
			return &m
		}
	}
	return nil
}

func showProviderPicker(host core.UIHost, cfg *config.Config, saver config.ConfigSaver) error {
	if len(cfg.Providers) == 0 {
		writeFmt(host, "Current provider: %s\n", cfg.ActiveProvider)
		return nil
	}

	items := make([]tui.SelectorItem, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		items = append(items, tui.SelectorItem{
			Value:       p.ID,
			Label:       p.ID,
			Description: providerPickerDesc(cfg, p),
		})
	}

	current := cfg.ActiveProvider
	host.SelectOption("Select provider:", items, current, func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		if selected == "__add__" {
			runAddProviderFromPicker(host, cfg, saver)
			return
		}
		if strings.HasPrefix(selected, "__delete__") {
			providerID := strings.TrimPrefix(selected, "__delete__")
			confirmAndRemoveProvider(host, cfg, saver, providerID)
			return
		}
		if selected == current {
			return
		}
		applyProviderSelection(host, cfg, saver, selected)
	})
	return nil
}

// runAddProviderFromPicker guides the user through adding a provider from the
// /provider picker ('+' hotkey), mirroring /model's runAddModelFromSelector.
// Without this, the selector's "__add__" sentinel would be treated as a
// provider ID and persisted as the active provider.
func runAddProviderFromPicker(host core.UIHost, cfg *config.Config, saver config.ConfigSaver) {
	ctx, ok := host.(core.Context)
	if !ok {
		host.Flash("Add provider: use /config to add a provider interactively.")
		return
	}
	ctx.SelectOption("Select provider type:", buildProviderPresetItems(), "", func(v string, ok bool) {
		if !ok || v == "" {
			return
		}
		if v == "__custom__" {
			promptCustomProvider(ctx, cfg, saver)
			return
		}
		preset := config.FindPreset(v)
		if preset == nil {
			return
		}
		finalizePresetProviderFromPicker(ctx, cfg, saver, preset)
	})
}

// finalizePresetProviderFromPicker adds a preset provider. Codex-capable
// presets first ask how to authenticate (Sign in with ChatGPT / OAuth vs API
// key) instead of forcing a key prompt; other presets prompt for an API key
// when the preset requires one.
func finalizePresetProviderFromPicker(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, preset *config.ProviderPreset) {
	ctx, isCtx := host.(core.Context)
	if isCtx && isCodexAuthSelectable(preset) {
		promptCodexAuthChoice(ctx, cfg, saver, preset)
		return
	}
	if !preset.NeedsAPIKey {
		finalizePickedProvider(host, cfg, saver, preset.ID, preset.Name, preset.Endpoint, "")
		return
	}
	host.ShowInput("API key for "+preset.Name+":", "", func(apiKey string, ok bool) {
		if !ok {
			return
		}
		finalizePickedProvider(host, cfg, saver, preset.ID, preset.Name, preset.Endpoint, apiKey)
	})
}

// isCodexAuthSelectable reports whether a preset is an OpenAI Codex provider
// that supports both OAuth sign-in and an API key.
func isCodexAuthSelectable(preset *config.ProviderPreset) bool {
	switch preset.ID {
	case "openai-codex", "codex", "openai":
		return true
	default:
		return false
	}
}

// promptCodexAuthChoice asks how to authenticate a codex provider. OAuth runs
// the codex login flow (browser or device) then adds the provider without a
// stored key; API key falls back to the normal key prompt.
func promptCodexAuthChoice(ctx core.Context, cfg *config.Config, saver config.ConfigSaver, preset *config.ProviderPreset) {
	items := []tui.SelectorItem{
		{Value: "oauth", Label: "Sign in with ChatGPT (OAuth)", Description: "Browser or device-code sign-in"},
		{Value: "apikey", Label: "Use an API key", Description: "Paste a pre-generated OpenAI API key"},
	}
	ctx.SelectOption("Authenticate "+preset.Name+":", items, "oauth", func(choice string, ok bool) {
		if !ok || choice == "" {
			return
		}
		if choice == "oauth" {
			startCodexOAuthFromPicker(ctx, cfg, saver, preset)
			return
		}
		ctx.ShowInput("API key for "+preset.Name+":", "", func(apiKey string, ok bool) {
			if !ok {
				return
			}
			finalizePickedProvider(ctx, cfg, saver, preset.ID, preset.Name, preset.Endpoint, apiKey)
		})
	})
}

// startCodexOAuthFromPicker launches the codex OAuth sign-in started from the
// provider picker. It is invoked from the auth-choice selector callback, which
// production wiring runs on the TUI commandLoop; the login flow's method
// prompt (clarify) and browser/device wait would block that loop — freezing
// the whole TUI — so they run on a background goroutine (all UI calls on that
// path are goroutine-safe event-bus posts / internally applied). The provider
// is added immediately with no stored key; if sign-in fails it is rolled back
// unless it pre-existed.
func startCodexOAuthFromPicker(ctx core.Context, cfg *config.Config, saver config.ConfigSaver, preset *config.ProviderPreset) {
	pickerProviderMu.Lock()
	preExisting := cfg.GetProviderByID(preset.ID) != nil
	finalizePickedProvider(ctx, cfg, saver, preset.ID, preset.Name, preset.Endpoint, "")
	pickerProviderMu.Unlock()
	ctx.Flash("Starting " + preset.Name + " sign-in…")
	go func() {
		if err := loginFlowRunner(ctx, preset.ID); err != nil && !preExisting {
			pickerProviderMu.Lock()
			removeProviderConfig(ctx, cfg, saver, preset.ID)
			pickerProviderMu.Unlock()
			ctx.Flash(preset.Name + " sign-in failed: " + err.Error() + " (provider removed)")
		}
	}()
}

// removeProviderConfig deletes a provider entry and persists, mirroring
// doRemoveProvider but without user-facing messaging (the caller flashes).
func removeProviderConfig(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, providerID string) {
	for i, p := range cfg.Providers {
		if p.ID != providerID {
			continue
		}
		cfg.Providers = append(cfg.Providers[:i], cfg.Providers[i+1:]...)
		if cfg.ActiveProvider == providerID {
			cfg.ActiveProvider = ""
		}
		_ = persistModelCatalogChange(host, cfg, saver)
		return
	}
}

// promptCustomProvider collects endpoint, API key and ID for a custom provider.
func promptCustomProvider(host core.UIHost, cfg *config.Config, saver config.ConfigSaver) {
	host.ShowInput("Provider endpoint (e.g. https://api.example.com/v1):", "", func(endpoint string, ok bool) {
		if !ok || endpoint == "" {
			return
		}
		host.ShowInput("API key (optional, press Enter to skip):", "", func(apiKey string, ok bool) {
			if !ok {
				return
			}
			defaultID := config.DeriveProviderID(endpoint)
			host.ShowInput("Provider ID (short identifier, e.g. 'my-provider'):", defaultID, func(id string, ok bool) {
				if !ok || id == "" {
					return
				}
				finalizePickedProvider(host, cfg, saver, id, id, endpoint, apiKey)
			})
		})
	})
}

// finalizePickedProvider upserts the provider and persists the change.
func finalizePickedProvider(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, id, name, endpoint, apiKey string) {
	upsertProviderConfig(cfg, id, name, endpoint, apiKey)
	if saver != nil {
		if err := saver.SaveHomeProvidersAndModels(cfg); err != nil {
			host.Flash("Failed to save: " + err.Error())
			return
		}
	}
	host.Flash("Provider '" + id + "' added.")
	host.FooterRefresh()
}

// confirmAndRemoveProvider shows a confirmation and removes a provider.
func confirmAndRemoveProvider(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, providerID string) {
	host.SelectOption("Remove provider "+providerID+"?", []tui.SelectorItem{
		{Value: "yes", Label: "Yes, remove provider", Description: providerID},
		{Value: "no", Label: "No, cancel", Description: ""},
	}, "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			_ = showProviderPicker(host, cfg, saver)
			return
		}
		doRemoveProvider(cfg, saver, host, providerID)
		_ = showProviderPicker(host, cfg, saver)
	})
}

func doRemoveProvider(cfg *config.Config, saver config.ConfigSaver, host core.UIHost, providerID string) {
	for i, p := range cfg.Providers {
		if p.ID != providerID {
			continue
		}
		cfg.Providers = append(cfg.Providers[:i], cfg.Providers[i+1:]...)
		if cfg.ActiveProvider == providerID {
			cfg.ActiveProvider = ""
		}
		if err := persistModelCatalogChange(host, cfg, saver); err != nil {
			host.Flash("Failed to save: " + err.Error())
			return
		}
		host.Flash("Provider " + providerID + " removed.")
		return
	}
	host.Flash("Provider " + providerID + " not found.")
}

// providerPickerDesc builds the description line for a provider picker item.
func providerPickerDesc(cfg *config.Config, p config.ProviderConfig) string {
	desc := p.Name
	if m := firstModelForProvider(cfg, p.ID); m != nil {
		desc = m.Model
	}
	if p.ID == cfg.ActiveProvider {
		desc += " (active)"
	}
	return desc
}

// applyProviderSelection switches the active provider through the atomic
// couple switch, picking a valid model for it when the current model is
// foreign, persists, and notifies the UI — including pushing the new couple
// into the live agent session (model + stream options), which keeps the next
// turn on the chosen provider's credentials. When the provider has no
// configured model, the preset default is used; if there is none (custom
// provider), the active model is cleared — a foreign model must never remain
// active on the new provider.
func applyProviderSelection(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, selected string) {
	modelID := cfg.ActiveModel
	if !isModelForProvider(cfg, modelID, selected) {
		modelID = defaultModelForProvider(cfg, selected)
	}
	if err := applyCoupledSwitch(host, cfg, saver, selected, modelID); err != nil {
		host.Flash(err.Error())
		return
	}
	host.Flash("Switched to: " + selected + " / " + cfg.ActiveModel)
}

// defaultModelForProvider returns the first configured model for providerID,
// else the preset's default model, else "".
func defaultModelForProvider(cfg *config.Config, providerID string) string {
	if m := firstModelForProvider(cfg, providerID); m != nil {
		return m.ID
	}
	if preset := config.FindPreset(providerID); preset != nil {
		return preset.DefaultModel
	}
	return ""
}

// ProvidersCommand and ModelsCommand were removed. Their functionality is
// now part of ProviderCommand (/provider?) and ModelCommand (/model), so the
// command namespace exposes a single canonical name per resource.
