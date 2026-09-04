// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/provider"
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
		if m.Ephemeral {
			continue
		}
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
	// "/model add" mirrors "/config add model": it must work even when no
	// provider is active yet, so it is handled before the provider guards.
	if len(args) > 0 && args[0] == "add" {
		return runModelAdd(host, cfg, saver, args[1:])
	}
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
	if isModelSentinel(selected) {
		writeFmt(host, "Invalid model name: %s\n", selected)
		return nil
	}
	// A model bound to another configured provider switches that provider
	// with it; a custom/remote model keeps the current provider.
	np := providerIDForModel(cfg, selected)
	if np != "" && np != cfg.ActiveProvider && cfg.GetProviderByID(np) == nil {
		writeFmt(host, "Cannot switch to %s: provider %q is not configured. Run /config to add it.\n", selected, np)
		return nil
	}
	// One coupled unit: manager → config → persist → agent push → footer.
	// A team governing the session model suppresses the persist step (RC-5):
	// saving would leak the team's model as the user's choice.
	team, sessionOnly := teamModelPersistenceSuppressed(host)
	if err := applyCoupledSwitchPersisting(host, cfg, saver, np, selected, !sessionOnly); err != nil {
		writeFmt(host, "Cannot switch to %s: %v\n", selected, err)
		return nil
	}
	if sessionOnly {
		writeFmt(host, "Team %q governs the session model — change is session-only (not saved).\n", team)
	}
	writeFmt(host, "Switched to model: %s\n", selected)
	return nil
}

func showModelSelector(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, pCfg *config.ProviderConfig) error {
	activeModel := cfg.ActiveModel
	items := configuredModelItems(cfg, activeModel)
	if len(items) == 0 {
		items = []tui.SelectorItem{{Value: activeModel, Label: activeModel, Description: "current"}}
	}
	// "custom model" always sorts last (see modelItemLess).
	items = append(items, tui.SelectorItem{
		Value: "__custom__", Label: ansi.RepeatHorizontal(2) + " custom model " + ansi.RepeatHorizontal(2), Description: "type any model name",
	})
	sort.SliceStable(items, modelItemLess(items, activeModel))

	host.SelectOption("Select model:", items, activeModel, func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		if selected == "__add__" {
			runAddModelFromSelector(host, cfg, saver)
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

// modelListForProvider returns the model list for an add-model picker: the
// provider's live /models list merged with the built-in registry models for
// that provider. Live entries win on ID conflict; registry entries fill gaps
// (e.g. z.ai's coding endpoint, whose /models list is incomplete). On live
// fetch error the registry list alone is returned (may still be empty) and a
// warning is flashed so the fallback is visible instead of silent.
func modelListForProvider(host core.UIHost, providerID string) []provider.ModelInfo {
	live, err := fetchLiveModels(host, providerID)
	if err != nil {
		warnLiveModelDiscoveryFallback(host, providerID, err)
	}

	var registry []provider.ModelInfo
	if pm, ok := host.(interface {
		ListRegistryModels(string) []provider.ModelInfo
	}); ok {
		registry = pm.ListRegistryModels(providerID)
	} else if ctx, ok := host.(core.Context); ok && ctx.ProviderManager != nil {
		if pm, ok := ctx.ProviderManager.(interface {
			ListRegistryModels(string) []provider.ModelInfo
		}); ok {
			registry = pm.ListRegistryModels(providerID)
		}
	}

	seen := make(map[string]bool, len(live))
	out := append([]provider.ModelInfo{}, live...)
	for _, m := range live {
		seen[m.ID] = true
	}
	for _, m := range registry {
		if !seen[m.ID] {
			out = append(out, m)
			seen[m.ID] = true
		}
	}
	return out
}

// fetchLiveModels interrogates the provider's live GET /models endpoint via
// the provider manager (using its TTL cache when available).
func fetchLiveModels(host core.UIHost, providerID string) ([]provider.ModelInfo, error) {
	ctx, ok := host.(core.Context)
	if !ok || ctx.ProviderManager == nil {
		return nil, nil
	}
	if pm, ok := ctx.ProviderManager.(interface {
		ListModelsCached(string, time.Duration) ([]provider.ModelInfo, error)
	}); ok {
		return pm.ListModelsCached(providerID, modelCacheTTL)
	}
	return ctx.ProviderManager.ListModels(providerID)
}

// runModelAdd handles "/model add". With no arguments it opens the
// interactive add-model flow (same as pressing '+' in the model picker).
// With <id> <provider-id> <model-name> it adds the model directly, mirroring
// "/config add model".
func runModelAdd(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, args []string) error {
	if len(args) == 0 {
		runAddModelFromSelector(host, cfg, saver)
		return nil
	}
	if len(args) < 3 {
		return fmt.Errorf("usage: /model add <id> <provider-id> <model-name>")
	}
	return doAddModel(cfg, saver, host, args[0], args[1], args[2])
}

// runAddModelFromSelector guides the user through adding a model: it ALWAYS
// asks for the provider first, then proposes that provider's known models
// (loaded asynchronously via pickModelFromProvider). The model list is scoped
// to the chosen provider only — never models known for other providers. The
// active provider is pre-selected (not auto-chosen) so the user confirms or
// picks a different one explicitly.
func runAddModelFromSelector(host core.UIHost, cfg *config.Config, saver config.ConfigSaver) {
	providers := configuredProviderItemsSimple(cfg)
	if len(providers) == 0 {
		host.Flash("No providers configured. Use /config to add one.")
		return
	}
	// Pre-select the active provider when one is set, as a convenience default.
	active := ""
	if cfg.ActiveProvider != "" && cfg.GetProviderByID(cfg.ActiveProvider) != nil {
		active = cfg.ActiveProvider
	}
	host.SelectOption("Select provider:", providers, active, func(providerID string, ok bool) {
		if !ok || providerID == "" {
			return
		}
		pickModelFromProvider(host, cfg, saver, providerID)
	})
}

// pickModelFromProvider fetches models from the given provider (live list
// merged with registry models) and shows a selector to pick one to add. The
// fetch runs asynchronously behind a loading placeholder so the UI stays
// responsive during a slow GET /models (falls back to synchronous when the
// host has no async-select support).
func pickModelFromProvider(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, providerID string) {
	fetch := func() []tui.SelectorItem {
		models := modelListForProvider(host, providerID)
		items := make([]tui.SelectorItem, 0, len(models)+1)
		for _, mod := range models {
			desc := providerID
			if modelIndex(cfg.Models, mod.ID) >= 0 {
				desc += " ✓ configured"
			}
			items = append(items, tui.SelectorItem{
				Value:       mod.ID,
				Label:       mod.ID,
				Description: desc,
				SearchLabel: modelSearchLabel(mod.ID, providerID, mod.ID),
			})
		}
		items = append(items, tui.SelectorItem{
			Value: "__custom__", Label: ansi.RepeatHorizontal(2) + " custom model " + ansi.RepeatHorizontal(2), Description: "type any model name",
		})
		return items
	}
	onSelected := func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		if selected == "__custom__" {
			promptCustomModelName(host, cfg, saver, providerID)
			return
		}
		addAndShowModel(host, cfg, saver, providerID, selected)
	}
	if ctx, ok := host.(core.Context); ok {
		ctx.SelectOptionAsync("Select model to add:", fetch, onSelected)
		return
	}
	host.SelectOption("Select model to add:", fetch(), "", onSelected)
}

// promptCustomModelName asks for a model name manually and adds it.
func promptCustomModelName(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, providerID string) {
	host.ShowInput("Model name:", "", func(modelName string, ok bool) {
		if !ok || modelName == "" {
			return
		}
		addAndShowModel(host, cfg, saver, providerID, modelName)
	})
}

// addAndShowModel adds a model to config, persists, and re-shows the model selector.
//
// Model identity is provider-scoped (Bug A, 2026-08-27): the duplicate guard
// fires ONLY for the exact same provider+name pair (a genuine re-add). A model
// whose name already exists under a DIFFERENT provider is a distinct entry —
// it is appended under a unique provider-qualified ID (uniqueModelID) so the
// bare-ID-keyed lookups stay unambiguous and the existing binding is left
// untouched.
func addAndShowModel(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, providerID, modelName string) {
	if isModelSentinel(modelName) {
		return
	}
	if modelIndexForProvider(cfg.Models, providerID, modelName) >= 0 {
		host.Flash("Model " + modelName + " already configured for provider " + providerID + ".")
		_ = showModelSelector(host, cfg, saver, cfg.GetProviderByID(providerID))
		return
	}
	modelID := uniqueModelID(cfg.Models, deriveModelID(modelName), providerID)
	cfg.Models = append(cfg.Models, config.ModelConfig{
		ID:         modelID,
		Name:       modelName,
		ProviderID: providerID,
		Model:      modelName,
	})
	if err := persistModelCatalogChange(host, cfg, saver); err != nil {
		host.Flash("Failed to save: " + err.Error())
	}
	host.Flash("Model " + modelID + " added.")
	pCfg := cfg.GetProviderByID(cfg.ActiveProvider)
	_ = showModelSelector(host, cfg, saver, pCfg)
}

// configuredProviderItemsSimple returns configured provider selector items.
func configuredProviderItemsSimple(cfg *config.Config) []tui.SelectorItem {
	var items []tui.SelectorItem
	seen := map[string]bool{}
	for _, p := range cfg.Providers {
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		items = append(items, tui.SelectorItem{Value: p.ID, Label: p.ID, Description: p.Name})
	}
	return items
}

// modelSearchLabel builds the SelectorItem.SearchLabel for a model row: the
// only terms a user means when filtering the model picker — the model ID,
// the provider ID, and the underlying model name. The visible Description
// ("provider=X model=Y") stays out of the search space so typing "model" or
// "provider" no longer matches every row.
func modelSearchLabel(id, providerID, modelName string) string {
	return id + " " + providerID + " " + modelName
}

// modelIndex returns the index of a model by ID, or -1 if not found.
func modelIndex(models []config.ModelConfig, id string) int {
	for i, m := range models {
		if m.ID == id {
			return i
		}
	}
	return -1
}

// modelIndexForProvider returns the index of the model served by providerID
// under the given name, or -1 if not found. Model identity is provider-scoped
// (Bug A, 2026-08-27): a name carried by ANOTHER provider is not a match, so
// cross-provider same-name models are distinct entries rather than duplicates.
func modelIndexForProvider(models []config.ModelConfig, providerID, modelName string) int {
	for i, m := range models {
		if m.ProviderID == providerID && m.Model == modelName {
			return i
		}
	}
	return -1
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
		// Clear every reference to the removed model (team member models,
		// orchestrator role models / pool caps, per-model compression
		// overrides, active_model) so no dangling reference survives to
		// hard-fail the next startup validation (B-CfgStaleModel). The loader
		// also heals stale references from configs written before this cleanup
		// existed (sanitizeDanglingModelRefs).
		cfg.ClearModelReferences(id)
		if err := persistModelCatalogChange(host, cfg, saver); err != nil {
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
// It first tries to show available models from ALL configured providers for
// autocomplete-style selection, falling back to a plain text input.
func promptCustomModel(host core.UIHost, cfg *config.Config, saver config.ConfigSaver) {
	allModels := fetchAllProviderModels(host, cfg)
	if len(allModels) == 0 {
		showCustomModelInput(host, cfg, saver)
		return
	}
	host.SelectOption("Select model:", modelSelectorItems(allModels, cfg.ActiveModel), cfg.ActiveModel,
		customModelSelectionHandler(host, cfg, saver, providerByModelID(allModels)))
}

// providerByModelID maps each fetched model ID to the provider it was
// listed under, so a selection can carry its true provider instead of
// being re-derived from cfg.Models (where remote IDs are usually absent).
// First entry wins, mirroring fetchAllProviderModels' dedup order.
func providerByModelID(entries []providerModelEntry) map[string]string {
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		if _, ok := m[e.Model.ID]; !ok {
			m[e.Model.ID] = e.ProviderID
		}
	}
	return m
}

func showCustomModelInput(host core.UIHost, cfg *config.Config, saver config.ConfigSaver) {
	host.ShowInput("Enter custom model name:", "", func(customModel string, ok bool) {
		if ok && customModel != "" {
			applyModelSelection(host, cfg, saver, customModel)
		}
	})
}

func modelSelectorItems(allModels []providerModelEntry, active string) []tui.SelectorItem {
	items := make([]tui.SelectorItem, 0, len(allModels)+1)
	for _, entry := range allModels {
		desc := entry.ProviderID
		if entry.Model.ID == active {
			desc += " (active)"
		}
		items = append(items, tui.SelectorItem{
			Value: entry.Model.ID, Label: entry.Model.ID, Description: desc,
			SearchLabel: modelSearchLabel(entry.Model.ID, entry.ProviderID, entry.Model.ID),
		})
	}
	return append(items, tui.SelectorItem{Value: "__custom__", Label: ansi.RepeatHorizontal(2) + " custom model " + ansi.RepeatHorizontal(2), Description: "type any model name"})
}

func customModelSelectionHandler(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, providerByID map[string]string) func(string, bool) {
	return func(selected string, ok bool) {
		if !ok || selected == "" {
			return
		}
		if selected == "__custom__" {
			showCustomModelInput(host, cfg, saver)
			return
		}
		applyModelSelectionForProvider(host, cfg, saver, providerByID[selected], selected)
	}
}

// providerModelEntry pairs a model with the provider it came from.
type providerModelEntry struct {
	ProviderID string
	Model      provider.ModelInfo
}

// fetchAllProviderModels fetches available models from ALL configured providers,
// aggregating the results into a single flat list.
func fetchAllProviderModels(host core.UIHost, cfg *config.Config) []providerModelEntry {
	ctx, ok := host.(core.Context)
	if !ok || ctx.ProviderManager == nil {
		return nil
	}
	var entries []providerModelEntry
	seen := make(map[string]bool) // deduplicate model IDs
	for _, p := range cfg.Providers {
		if p.ID == "" {
			continue
		}
		models := fetchProviderModels(host, p.ID)
		for _, mod := range models {
			if seen[mod.ID] {
				continue
			}
			seen[mod.ID] = true
			entries = append(entries, providerModelEntry{ProviderID: p.ID, Model: mod})
		}
	}
	return entries
}

// fetchProviderModels tries to get the model list from a single provider.
// On live fetch failure a warning is flashed so the picker's fallback (to
// other providers / custom input) is visible instead of silently empty.
func fetchProviderModels(host core.UIHost, providerID string) []provider.ModelInfo {
	ctx, ok := host.(core.Context)
	if !ok || ctx.ProviderManager == nil {
		return nil
	}
	if pm, ok := ctx.ProviderManager.(interface {
		ListModelsCached(string, time.Duration) ([]provider.ModelInfo, error)
	}); ok {
		models, err := pm.ListModelsCached(providerID, 5*time.Minute)
		if err == nil {
			return models
		}
		warnLiveModelDiscoveryFallback(host, providerID, err, registryModelCount(host, providerID))
		return nil
	}
	models, err := ctx.ProviderManager.ListModels(providerID)
	if err != nil {
		warnLiveModelDiscoveryFallback(host, providerID, err, registryModelCount(host, providerID))
		return nil
	}
	return models
}

// registryModelCount reports how many known (registry/catalog) models a
// provider has, so the discovery-failure flash can distinguish "using known
// models" (fallback actually has entries) from "no known models" (the picker
// will offer only the custom-model row). Zero when the host exposes no
// registry lookup — the flash then keeps the legacy wording.
func registryModelCount(host core.UIHost, providerID string) int {
	if pm, ok := host.(interface {
		ListRegistryModels(string) []provider.ModelInfo
	}); ok {
		return len(pm.ListRegistryModels(providerID))
	}
	if ctx, ok := host.(core.Context); ok && ctx.ProviderManager != nil {
		if pm, ok := ctx.ProviderManager.(interface {
			ListRegistryModels(string) []provider.ModelInfo
		}); ok {
			return len(pm.ListRegistryModels(providerID))
		}
	}
	return 0
}

// warnLiveModelDiscoveryFallback flashes a warning when a provider's live
// /models endpoint cannot be interrogated, so the picker's fallback to
// cached/registry models becomes visible instead of silent. When the registry
// fallback is also empty the message says so — "using known models" would be
// a lie (the picker then shows only the custom-model row), which is exactly
// what the openai-codex subscription endpoint (no /models route, Cloudflare
// 403) produced before it gained a registry alias.
func warnLiveModelDiscoveryFallback(host core.UIHost, providerID string, err error, registryCount ...int) {
	known := true
	if len(registryCount) > 0 {
		known = registryCount[0] > 0
	}
	reason := singleLineErr(err)
	if !known {
		host.Flash(fmt.Sprintf("Model discovery failed for %s (%s); no known models for this provider — type a custom model name.", providerID, reason))
		return
	}
	host.Flash(fmt.Sprintf("Model discovery failed for %s (%s); using known models.", providerID, reason))
}

// flashErrCap bounds the rendered error snippet in a discovery-failure flash,
// so even an unsanitized verbose error cannot overflow the flash overlay.
const flashErrCap = 200

// singleLineErr collapses an error to a bounded single line for flash
// rendering (Bug B, 2026-08-27): whitespace runs become one space, any raw
// markup collapses to a fixed note, and the result is hard-capped. This is
// defense-in-depth behind the source-side sanitize in provider/manager.go —
// it guarantees the flash never carries raw HTML or newlines regardless of
// which error reaches it.
func singleLineErr(err error) string {
	if err == nil {
		return "unknown error"
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if looksLikeHTMLString(s) {
		return "(HTML error page)"
	}
	if len(s) > flashErrCap {
		s = s[:flashErrCap] + "…"
	}
	return s
}

// looksLikeHTMLString mirrors the HTML detection used for discovery errors:
// a tag opener at the start or a known document tag anywhere marks markup.
func looksLikeHTMLString(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(l, "<") {
		return true
	}
	for _, tag := range []string{"<html", "<!doctype", "<head", "<body", "<svg", "<div", "<meta"} {
		if strings.Contains(l, tag) {
			return true
		}
	}
	return false
}

// isModelSentinel reports whether v is a selector action/sentinel value that
// leaked into the model-value space (e.g. "__delete__X" emitted by the
// backspace/delete hotkey in a picker whose callback has no delete handler).
// Such values must never become the active model or a configured model name
// (the picker left the active model named "__delete__deepseek-v4-flash").
func isModelSentinel(v string) bool {
	return strings.HasPrefix(v, "__")
}

// applyModelSelection records the chosen model through the atomic couple
// switch (provider follows its configured provider when needed), then
// notifies the UI. Extracted to keep showModelSelector within the complexity
// budget.
// applyModelSelection switches to a model selected by ID alone. The provider
// is re-derived from cfg.Models; models absent from the local configuration
// (custom/remote catalog picks) keep the current provider.
func applyModelSelection(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, selected string) {
	applyModelSelectionForProvider(host, cfg, saver, "", selected)
}

// applyModelSelectionForProvider is applyModelSelection with an explicit
// provider candidate. pickerProvider comes from the selection context that
// already knows where the model lives (e.g. the all-providers fetch list);
// it wins over the cfg.Models lookup because a remote/catalog model ID is
// not necessarily present there. An empty pickerProvider keeps the legacy
// behavior: derive from cfg.Models, else stay on the current provider.
//
// Bug1: the custom-model picker knew each entry's ProviderID but dropped
// it, so picking e.g. "stealth/ox-alpha" served by configured provider
// "stealth" silently attached it to the current provider — the footer then
// showed the mixed pair "(openai-codex) stealth/ox-alpha" and requests went
// to the wrong endpoint.
func applyModelSelectionForProvider(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, pickerProvider, selected string) {
	if isModelSentinel(selected) {
		return
	}
	np := pickerProvider
	if np == "" {
		np = providerIDForModel(cfg, selected)
	}
	if np != "" && np != cfg.ActiveProvider && cfg.GetProviderByID(np) == nil {
		host.Flash(fmt.Sprintf("Provider %q is not configured. Run /config to add it.", np))
		return
	}
	team, sessionOnly := teamModelPersistenceSuppressed(host)
	if err := applyCoupledSwitchPersisting(host, cfg, saver, np, selected, !sessionOnly); err != nil {
		host.Flash(err.Error())
		return
	}
	if sessionOnly {
		// RC-5: tell the user the change is deliberately not persisted.
		host.Flash(fmt.Sprintf("Team %q governs the session model — change is session-only (not saved).", team))
	}
	host.Flash("Switched to model: " + selected)
}

// teamModelPersistenceSuppressed reports whether a team currently governs the
// session model (session-level activation or goal overlay). While it does,
// /model must not persist the chosen model: the value in effect may be the
// TEAM's model, and saving it would leak into home/project config as the
// user's choice (RC-5 — observed: project config ended up with the
// companion's model). Returns the effective team name for the user message.
func teamModelPersistenceSuppressed(host core.UIHost) (string, bool) {
	ctx, ok := host.(core.Context)
	if !ok {
		return "", false
	}
	tm := teamManager(ctx)
	if tm == nil {
		return "", false
	}
	if effective := tm.EffectiveTeam(); effective != "" {
		return effective, true
	}
	return "", false
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

// applyCoupledSwitch commits a provider/model couple as ONE unit across all
// surfaces that hold it: provider manager, config, persisted home config,
// live agent session (model + stream options + thinking level), and footer.
//
// Ordering is what guarantees "always updated together": the couple is pushed
// into the ProviderManager FIRST, and its SetActive validates the provider —
// on error nothing has been mutated anywhere, so no surface can end up
// showing a mixed pair like "(openai-codex) <openrouter-model>". The explicit
// cfg write afterwards keeps boot config and any hot-reloaded manager copy in
// lockstep.
//
// providerID "" means "keep the current provider" (custom-model switches);
// modelID may be "" when a provider has no configured model yet.
func applyCoupledSwitch(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, providerID, modelID string) error {
	return applyCoupledSwitchPersisting(host, cfg, saver, providerID, modelID, true)
}

// applyCoupledSwitchPersisting is applyCoupledSwitch with explicit control
// over the persist step. When persist is false (a team governs the session
// model — RC-5), every other surface still switches as one unit; only the
// write to home/project config is skipped so the team's model never becomes
// the user's saved choice.
func applyCoupledSwitchPersisting(host core.UIHost, cfg *config.Config, saver config.ConfigSaver, providerID, modelID string, persist bool) error {
	if providerID == "" {
		providerID = cfg.ActiveProvider
	}
	// 1. Validate + push into the manager's live copy before anything else.
	if ctx, ok := host.(core.Context); ok && ctx.ProviderManager != nil {
		if err := ctx.ProviderManager.SetActive(providerID, modelID); err != nil {
			return err
		}
	}
	// 2. Commit to the config object.
	cfg.ActiveProvider = providerID
	cfg.ActiveModel = modelID
	// 3. Persist (unless suppressed — see RC-5 above).
	if persist {
		if err := persistModelSwitch(cfg, saver); err != nil {
			return err
		}
	}
	// 4. Push into the live agent session and refresh the status bar.
	if ctx, ok := host.(core.Context); ok {
		propagateModelSwitch(ctx, cfg)
	}
	host.FooterRefresh()
	return nil
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
// Models served by a local provider (localhost endpoint) are shown in green;
// all other models keep the default color.
func configuredModelItems(cfg *config.Config, activeModel string) []tui.SelectorItem {
	return configuredModelItemsFiltered(cfg, activeModel, false)
}

func configuredModelItemsFiltered(cfg *config.Config, activeModel string, activeProviderOnly bool) []tui.SelectorItem {
	var items []tui.SelectorItem
	providerID := cfg.ActiveProvider
	for _, m := range cfg.Models {
		if m.Ephemeral {
			continue
		}
		if activeProviderOnly && m.ProviderID != providerID {
			continue
		}
		desc := fmt.Sprintf("provider=%s model=%s", m.ProviderID, m.Model)
		if m.ID == activeModel {
			desc += " (active)"
		}
		items = append(items, tui.SelectorItem{
			Value:       m.ID,
			Label:       m.ID,
			Description: desc,
			Color:       localModelColor(cfg, m.ProviderID),
			SearchLabel: modelSearchLabel(m.ID, m.ProviderID, m.Model),
		})
	}
	return items
}

// localModelColor returns the selector label color for a configured model:
// green when the model's provider is a local LLM server (localhost /
// 127.0.0.1 endpoint), empty (default color) otherwise.
func localModelColor(cfg *config.Config, providerID string) string {
	pCfg := cfg.GetProviderByID(providerID)
	if pCfg == nil || !provider.IsLocalEndpoint(pCfg.Endpoint) {
		return ""
	}
	return tui.TheTheme.ColorHex("tool_success")
}
