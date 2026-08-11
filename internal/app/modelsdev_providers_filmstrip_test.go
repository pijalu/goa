// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/tui"

	agenticmodels "github.com/pijalu/goa/internal/agentic/provider/models"
)

// deadEndpoint refuses every connection instantly (port 9 is discard), so a
// provider's live GET /models fails fast and the model picker falls back to the
// registry source (ListRegistryModels) — exactly the production behavior for a
// configured provider whose live list is unavailable. No network is touched.
const deadEndpoint = "http://127.0.0.1:9/v1"

// modelsdevProviderConfigs builds a ProviderConfig for each models.dev provider
// under test. The ID is the models.dev key and the Provider identity is Goa's
// mapping, so ListRegistryModels resolves each key to its embedded
// models.dev-derived model set.
func modelsdevProviderConfigs(pm *[]config.ProviderConfig, mp []agenticmodels.ModelsDevProvider) {
	for _, p := range mp {
		*pm = append(*pm, config.ProviderConfig{
			ID:       p.Key,
			Name:     p.Key,
			Provider: string(p.Identity),
			Endpoint: deadEndpoint,
		})
	}
}

// expectedModelsDevModels returns the models.dev model IDs (tool-calling only)
// for the given provider key, as declared by the embedded models.dev catalog.
func expectedModelsDevModels(t *testing.T, key string) []string {
	t.Helper()
	for _, p := range agenticmodels.ModelsDevProviders() {
		if p.Key == key {
			return p.ModelIDs
		}
	}
	t.Fatalf("models.dev provider %q not in embedded catalog", key)
	return nil
}

// TestModelsDev_AllProvidersReachableInPickerData proves that every provider
// from models.dev (all 178 keys, tool-calling subsets) is surfaced by the exact
// data source the /model add picker uses (ProviderManager.ListRegistryModels →
// the embedded models.dev registry). This is the data-layer half of "all
// providers from models.dev are visible in the TUI": if a provider's models are
// not in ListRegistryModels output, they can never appear in the picker.
func TestModelsDev_AllProvidersReachableInPickerData(t *testing.T) {
	all := agenticmodels.ModelsDevProviders()
	if len(all) < 150 {
		t.Fatalf("expected ~all models.dev providers, got %d", len(all))
	}

	var pcs []config.ProviderConfig
	modelsdevProviderConfigs(&pcs, all)
	cfg := &config.Config{Providers: pcs}
	pm := provider.NewProviderManager(cfg)

	missing := 0
	for _, p := range all {
		got := pm.ListRegistryModels(p.Key)
		if len(got) == 0 {
			missing++
			t.Errorf("provider %q (identity %q): ListRegistryModels returned no models", p.Key, p.Identity)
			continue
		}
		seen := make(map[string]bool, len(got))
		for _, m := range got {
			seen[m.ID] = true
		}
		for _, id := range p.ModelIDs {
			if !seen[id] {
				t.Errorf("provider %q: models.dev model %q missing from ListRegistryModels (got %d models)", p.Key, id, len(got))
			}
		}
	}
	if missing == 0 {
		t.Logf("all %d models.dev providers surfaced through ListRegistryModels", len(all))
	}
}

// newModelsDevModelPickerScenario wires the production app component tree with
// a ProviderManager over the given models.dev provider keys and a registered
// command router, ready to drive real slash commands through the TUI.
func newModelsDevModelPickerScenario(t *testing.T, keys ...string) *uiScenario {
	t.Helper()
	sc := newUIScenario(t, 100, 40)
	cfg := sc.app.subs.cfg
	var pcs []config.ProviderConfig
	for _, k := range keys {
		ident := ""
		for _, p := range agenticmodels.ModelsDevProviders() {
			if p.Key == k {
				ident = string(p.Identity)
				break
			}
		}
		if ident == "" {
			t.Fatalf("key %q not a models.dev provider", k)
		}
		pcs = append(pcs, config.ProviderConfig{ID: k, Name: k, Provider: ident, Endpoint: deadEndpoint})
	}
	cfg.Providers = pcs
	cfg.ActiveProvider = keys[0]
	sc.app.subs.providerMgr = provider.NewProviderManager(cfg)

	registry := core.NewCommandRegistry()
	if err := commands.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	sc.app.subs.cmdRouter = core.NewCommandRouter(registry, core.NewDocEngine(registry))
	return sc
}

// TestModelsDev_ProviderPickerShowsModelsDevProviders_Filmstrip drives the real
// `/model add` command and filmstrip-captures the provider picker, proving the
// models.dev-configured providers are selectable rows in the TUI.
func TestModelsDev_ProviderPickerShowsModelsDevProviders_Filmstrip(t *testing.T) {
	sc := newModelsDevModelPickerScenario(t, "deepseek", "tensorx", "zai")
	film := sc.filmstrip()

	sc.engine.ApplySync(func() { sc.app.handleSlashCommand("/model:add") })
	sc.engine.RenderNow()
	film.Capture("provider-picker", sc.engine.AgentFrame(), sc.status.Text())

	frame := film.Last().Frame
	visible := frame.Visible
	joined := strings.Join(visible, "\n")
	for _, want := range []string{"deepseek", "tensorx", "zai"} {
		if !strings.Contains(joined, want) {
			t.Errorf("provider picker missing %q; film:\n%s", want, film.Render())
		}
	}
}

// TestModelsDev_ModelsVisibleInTUIPicker_Filmstrip renders, for representative
// models.dev providers (mapped + unmapped-fallback), the exact item set the
// /model add picker presents (ListRegistryModels, the production fallback when a
// live /models fetch is unavailable) through the real TUI Selector, and asserts
// from the filmstrip that every models.dev model ID for that provider is visible
// in the scrolled picker. This is the filmstrip half of "all providers from
// models.dev are visible in the TUI": the models.dev catalog → picker data →
// rendered pixel rows reach the agent-visible filmstrip.
func TestModelsDev_ModelsVisibleInTUIPicker_Filmstrip(t *testing.T) {
	// One mapped provider (zai → zai-api) and one unmapped-fallback provider
	// (tensorx, the historical invisible case), plus deepseek (mapped) to span
	// the mapping paths without rendering all 4927 models.dev models.
	for _, key := range []string{"zai", "tensorx", "deepseek"} {
		t.Run(key, func(t *testing.T) {
			want := expectedModelsDevModels(t, key)
			ident := ""
			for _, p := range agenticmodels.ModelsDevProviders() {
				if p.Key == key {
					ident = string(p.Identity)
					break
				}
			}
			cfg := &config.Config{Providers: []config.ProviderConfig{
				{ID: key, Provider: ident, Endpoint: deadEndpoint},
			}}
			pm := provider.NewProviderManager(cfg)

			// Items exactly as modelListForProvider builds them from the registry
			// when the live /models fetch fails (the common keyless case).
			models := pm.ListRegistryModels(key)
			if len(models) == 0 {
				t.Fatalf("ListRegistryModels(%q) empty for provider in filmstrip", key)
			}
			items := make([]tui.SelectorItem, 0, len(models)+1)
			for _, m := range models {
				items = append(items, tui.SelectorItem{Value: m.ID, Label: m.ID, Description: key})
			}
			items = append(items, tui.SelectorItem{Value: "__custom__", Label: "—— custom model ——"})

			term := &testTerminal{w: 100, h: 40}
			engine := tui.NewTUI(term)
			if err := engine.Start(); err != nil {
				t.Fatalf("engine Start: %v", err)
			}
			defer engine.Stop()

			engine.ShowSelector("Select model to add:", items, "")
			film := tui.NewFilmstrip()

			seen := map[string]bool{}
			// Capture the first window, then scroll the selector far enough that
			// every 8-item window is rendered at least once. The Selector window
			// advances only as the highlight moves past maxShow/2, so we must send
			// up to len(items) KeyDowns (the highlight wraps) to guarantee every
			// model row is visible in some captured frame.
			for step := 0; step < len(items)+8 && len(seen) < len(want); step++ {
				engine.RenderNow()
				label := strings.TrimSpace("model-picker-" + key + "-window@" + itoaApp(step))
				film.Capture(label, engine.AgentFrame(), "")
				last := film.Last()
				if last == nil {
					continue
				}
				for _, line := range last.Frame.Visible {
					for _, id := range want {
						if strings.Contains(line, id) {
							seen[id] = true
						}
					}
				}
				if len(seen) == len(want) {
					break
				}
				engine.SendKey(tui.KeyDown) // scroll to the next window
			}

			var unseen []string
			for _, id := range want {
				if !seen[id] {
					unseen = append(unseen, id)
				}
			}
			if len(unseen) > 0 {
				t.Errorf("provider %s: models.dev models not visible in TUI picker: %v\nfilm:\n%s",
					key, unseen, film.Render())
			}
		})
	}
}

// itoaApp is a tiny int→string helper (the tui.itoa is unexported).
func itoaApp(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
