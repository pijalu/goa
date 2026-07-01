// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/tui"
)

// ModelCommand sets or displays the active LLM model.
type ModelCommand struct{}

func (c *ModelCommand) Name() string      { return "model" }
func (c *ModelCommand) Aliases() []string { return []string{} }
func (c *ModelCommand) ShortHelp() string {
	return "Select or display the active LLM model"
}
func (c *ModelCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// Status implements core.StatusProvider so /model? prints the live state
// instead of the static short-help text.
func (c *ModelCommand) Status(ctx core.Context) string {
	if ctx.Config == nil {
		return ""
	}
	model := ctx.Config.ActiveModel
	if model == "" {
		model = "(none)"
	}
	provider := ctx.Config.ActiveProvider
	if provider == "" {
		provider = "(none)"
	}
	return fmt.Sprintf("Model: %s   Provider: %s", model, provider)
}

func (c *ModelCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	// Use locally configured models for completion (no HTTP calls).
	// Fetching from the provider on every keystroke causes CPU spikes and hangs.
	if ctx.Config == nil {
		return nil
	}
	var comps []core.ArgCompletion
	for _, m := range ctx.Config.Models {
		if prefix != "" && !strings.HasPrefix(m.ID, prefix) {
			continue
		}
		desc := fmt.Sprintf("provider=%s model=%s", m.ProviderID, m.Model)
		if m.ID == ctx.Config.ActiveModel {
			desc += " (active)"
		}
		comps = append(comps, core.ArgCompletion{Value: m.ID, Description: desc})
	}
	return comps
}

func (c *ModelCommand) Run(ctx core.Context, args []string) error {
	return runModelCommand(ctx, ctx.ProviderManager, ctx.Config, ctx.ConfigSaver, args)
}

func runModelCommand(host core.UIHost, pm core.ProviderManager, cfg *config.Config, saver config.ConfigSaver, args []string) error {
	if pm == nil {
		writeStr(host, "No provider configured.\n")
		return nil
	}
	pCfg, _ := pm.Active()
	if pCfg == nil {
		writeStr(host, "No provider configured.\n")
		return nil
	}

	if len(args) == 0 {
		return showModelSelector(host, cfg, saver, pCfg)
	}

	selected := args[0]
	if np := providerIDForModel(cfg, selected); np != "" && np != cfg.ActiveProvider {
		if cfg.GetProviderByID(np) == nil {
			writeFmt(host, "Cannot switch to %s: provider %q is not configured. Run /config to add it.\n", selected, np)
			return nil
		}
		cfg.ActiveProvider = np
	}
	cfg.ActiveModel = selected
	if err := saveHomeProvidersAndModels(cfg, saver); err != nil {
		return err
	}
	propagateModelSwitch(host, cfg)
	host.FooterRefresh()
	writeFmt(host, "Switched to model: %s\n", selected)
	return nil
}

func showModelSelector(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, pCfg *config.ProviderConfig) error {
	activeModel := cfg.ActiveModel
	validator := modelValidatorFor(host)
	items := configuredModelItems(cfg, activeModel, validator)
	if len(items) == 0 {
		items = []tui.SelectorItem{{Value: activeModel, Label: activeModel, Description: "current"}}
	}
	// "custom model" always sorts last (see modelItemLess).
	items = append(items, tui.SelectorItem{
		Value: "__custom__", Label: "── custom model ──", Description: "type any model name",
	})
	sort.SliceStable(items, modelItemLess(items, activeModel))

	host.SelectOption("Select model:", items, activeModel, func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		if strings.HasPrefix(selected, "__delete__") {
			modelID := strings.TrimPrefix(selected, "__delete__")
			confirmAndRemoveModel(host, cfg, saver, pCfg, modelID)
			return
		}
		if selected == "__custom__" {
			promptCustomModel(host, cfg, saver)
			return
		}
		applyModelSelection(host, cfg, saver, selected)
	})
	return nil
}

// modelValidatorFor returns the model validator from the host context, if any.
func modelValidatorFor(host core.UIHost) core.ModelValidator {
	if ctx, ok := host.(core.Context); ok {
		return ctx.ModelValidator
	}
	return nil
}

// confirmAndRemoveModel shows a confirmation dialog and removes the model.
func confirmAndRemoveModel(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, pCfg *config.ProviderConfig, modelID string) {
	host.SelectOption("Remove model "+modelID+"?", []tui.SelectorItem{
		{Value: "yes", Label: "Yes, remove model", Description: modelID},
		{Value: "no", Label: "No, cancel", Description: ""},
	}, "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			// Re-show the picker on cancel
			_ = showModelSelector(host, cfg, saver, pCfg)
			return
		}
		removeModelFromConfig(cfg, modelID, saver, host)
		// Re-show the picker after removal
		_ = showModelSelector(host, cfg, saver, pCfg)
	})
}

// removeModelFromConfig removes a model from the configuration and persists.
func removeModelFromConfig(cfg *config.Config, id string, saver config.ConfigSaver, host core.UIHost) {
	for i, mod := range cfg.Models {
		if mod.ID != id {
			continue
		}
		cfg.Models = append(cfg.Models[:i], cfg.Models[i+1:]...)
		if cfg.ActiveModel == id {
			cfg.ActiveModel = ""
		}
		if err := saveHomeProvidersAndModels(cfg, saver); err != nil {
			host.Flash("Failed to save: " + err.Error())
			return
		}
		host.Flash("Model " + id + " removed.")
		return
	}
	host.Flash("Model " + id + " not found.")
}

// modelItemLess returns a stable-sort comparator for the model picker: the
// active model sorts first, the custom entry sorts last, everything else is
// alphabetical (case-insensitive).
func modelItemLess(items []tui.SelectorItem, activeModel string) func(i, j int) bool {
	return func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Value == "__custom__" {
			return false
		}
		if b.Value == "__custom__" {
			return true
		}
		if a.Value == activeModel {
			return true
		}
		if b.Value == activeModel {
			return false
		}
		return strings.ToLower(a.Value) < strings.ToLower(b.Value)
	}
}

// promptCustomModel opens an input dialog for a free-form model name and, on
// confirm, applies it via applyModelSelection.
func promptCustomModel(host core.UIHost, cfg *config.Config, saver config.ConfigSaver) {
	host.ShowInput("Enter custom model name:", "", func(customModel string, ok bool) {
		if !ok || customModel == "" {
			return
		}
		applyModelSelection(host, cfg, saver, customModel)
	})
}

// applyModelSelection records the chosen model, follows its configured
// provider when the model belongs to a different one, persists, and notifies
// the UI. Extracted to keep showModelSelector within the complexity budget.
func applyModelSelection(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, selected string) {
	if np := providerIDForModel(cfg, selected); np != "" && np != cfg.ActiveProvider {
		if cfg.GetProviderByID(np) == nil {
			host.Flash(fmt.Sprintf("Provider %q is not configured. Run /config to add it.", np))
			return
		}
		cfg.ActiveProvider = np
	}
	cfg.ActiveModel = selected
	if err := saveHomeProvidersAndModels(cfg, saver); err != nil {
		host.Flash(err.Error())
		return
	}
	propagateModelSwitch(host, cfg)
	host.Flash("Switched to model: " + selected)
	host.FooterRefresh()
}

// propagateModelSwitch pushes a config model/provider change into the
// provider manager and active agent so the next turn uses the new model.
func propagateModelSwitch(host core.UIHost, cfg *config.Config) {
	ctx, ok := host.(core.Context)
	if !ok || ctx.ProviderManager == nil || ctx.AgentManager == nil {
		return
	}
	if err := ctx.ProviderManager.SetActive(cfg.ActiveProvider, cfg.ActiveModel); err != nil {
		ctx.Flash(fmt.Sprintf("Cannot switch to %s: %v", cfg.ActiveModel, err))
		return
	}
	if mdl, err := ctx.ProviderManager.ResolveActiveModel(); err == nil {
		ctx.AgentManager.SetModel(mdl)
	}
	// Refresh stream options (API key, headers, timeout) so the new provider's
	// credentials are used on the next turn instead of the old provider's.
	newOpts := ctx.ProviderManager.BuildStreamOptions()
	ctx.AgentManager.SetStreamOptions(newOpts)
}

// providerIDForModel returns the provider ID associated with a configured model ID.
// Returns "" if the model is not in cfg.Models (e.g. a custom/remote model).
func providerIDForModel(cfg *config.Config, modelID string) string {
	for _, m := range cfg.Models {
		if m.ID == modelID {
			return m.ProviderID
		}
	}
	return ""
}

// configuredModelItems returns selector items from the local model configuration.
//
// By default, models from ALL providers are listed (the active model is
// marked) so /model can be used to switch provider+model in one step.
// Pass activeProviderOnly=true to restrict to the active provider (used by
// the tab-completion path where a shorter list is preferable).
//
// Models that the background validator has marked invalid are shown in red.
func configuredModelItems(cfg *config.Config, activeModel string, validator core.ModelValidator) []tui.SelectorItem {
	return configuredModelItemsFiltered(cfg, activeModel, false, validator)
}

func configuredModelItemsFiltered(cfg *config.Config, activeModel string, activeProviderOnly bool, validator core.ModelValidator) []tui.SelectorItem {
	var items []tui.SelectorItem
	providerID := cfg.ActiveProvider
	for _, m := range cfg.Models {
		if activeProviderOnly && m.ProviderID != providerID {
			continue
		}
		desc := fmt.Sprintf("provider=%s model=%s", m.ProviderID, m.Model)
		if m.ID == activeModel {
			desc += " (active)"
		}
		item := tui.SelectorItem{
			Value:       m.ID,
			Label:       m.ID,
			Description: desc,
		}
		if validator != nil && !validator.IsValid(m.ID) {
			item.Color = tui.TheTheme.ColorHex("error")
		}
		items = append(items, item)
	}
	return items
}
