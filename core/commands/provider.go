// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/tui"
)

// Ensure tui is used
var _ = tui.SelectorItem{}

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
	cfg.ActiveProvider = providerID

	// Pick the first model from the new provider if the current model doesn't belong to it
	if !isModelForProvider(cfg, cfg.ActiveModel, providerID) {
		if m := firstModelForProvider(cfg, providerID); m != nil {
			cfg.ActiveModel = m.ID
		} else {
			cfg.ActiveModel = ""
		}
	}

	if err := saveHomeProvidersAndModels(cfg, ctx.ConfigSaver); err != nil {
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
		if m.ProviderID == providerID {
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
		if err := saveHomeProvidersAndModels(cfg, saver); err != nil {
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

// applyProviderSelection switches the active provider, picks a valid model for
// it when the current model is foreign, persists, and notifies the UI.
func applyProviderSelection(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, selected string) {
	cfg.ActiveProvider = selected
	if !isModelForProvider(cfg, cfg.ActiveModel, selected) {
		if m := firstModelForProvider(cfg, selected); m != nil {
			cfg.ActiveModel = m.ID
		}
	}
	if saver != nil {
		if err := saver.SaveHomeProvidersAndModels(cfg); err != nil {
			host.Flash("Failed to save: " + err.Error())
			return
		}
	}
	host.Flash("Switched to: " + selected + " / " + cfg.ActiveModel)
	host.FooterRefresh()
}

// ProvidersCommand and ModelsCommand were removed. Their functionality is
// now part of ProviderCommand (/provider?) and ModelCommand (/model), so the
// command namespace exposes a single canonical name per resource.
