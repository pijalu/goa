// SPDX-License-Identifier: GPL-3.0-or-later
package commands

import (
	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tui"
)

func buildProviderPresetItems() []tui.SelectorItem {
	presets := config.AllProviderPresets()
	items := make([]tui.SelectorItem, 0, len(presets)+1)
	for _, p := range presets {
		items = append(items, tui.SelectorItem{Value: p.ID, Label: p.Name, Description: p.Endpoint})
	}
	return append(items, tui.SelectorItem{Value: "__custom__", Label: "— custom provider —", Description: "enter endpoint and API key manually"})
}
func (m *configMenu) addProviderWizardHandler(v string, ok bool) {
	if !ok || v == "" {
		m.back()
		return
	}
	if v == "__custom__" {
		m.promptProviderEndpoint(func(e, k string) { m.promptProviderID(e, k) })
		return
	}
	p, found := findPresetProvider(v)
	if !found {
		m.back()
		return
	}
	m.finalizePresetProvider(p)
}
func findPresetProvider(id string) (config.ProviderPreset, bool) {
	if p := config.FindPreset(id); p != nil {
		return *p, true
	}
	return config.ProviderPreset{}, false
}
func (m *configMenu) finalizePresetProvider(p config.ProviderPreset) {
	if !p.NeedsAPIKey {
		m.finalizeAddProvider(p.ID, p.Name, p.Endpoint, "")
		return
	}
	m.ctx.ShowInput("API key for "+p.Name+":", "", func(k string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.finalizeAddProvider(p.ID, p.Name, p.Endpoint, k)
	})
}
func (m *configMenu) promptProviderEndpoint(done func(string, string)) {
	m.ctx.ShowInput("Provider endpoint (e.g. https://api.example.com/v1):", "", func(e string, ok bool) {
		if !ok || e == "" {
			m.back()
			return
		}
		m.ctx.ShowInput("API key (optional, press Enter to skip):", "", func(k string, ok bool) {
			if !ok {
				m.back()
				return
			}
			done(e, k)
		})
	})
}
func (m *configMenu) promptProviderID(endpoint, key string) {
	id := config.DeriveProviderID(endpoint)
	m.ctx.ShowInput("Provider ID (short identifier, e.g. 'my-provider'):", id, func(v string, ok bool) {
		if !ok || v == "" {
			m.back()
			return
		}
		m.finalizeAddProvider(v, v, endpoint, key)
	})
}
func (m *configMenu) finalizeAddProvider(id, name, endpoint, key string) {
	cfg := m.ctx.Config
	upsertProviderConfig(cfg, id, name, endpoint, key)
	if s := m.ctx.ConfigSaver; s != nil {
		if e := s.SaveHomeProvidersAndModels(cfg); e != nil {
			m.flash("Failed to save: " + e.Error())
			m.back()
			return
		}
	}
	m.flash("Provider '" + id + "' added.")
	m.promptAddModelForProvider(id)
}
func upsertProviderConfig(c *config.Config, id, name, endpoint, key string) {
	for i := range c.Providers {
		if c.Providers[i].ID == id {
			c.Providers[i].Endpoint = endpoint
			c.Providers[i].APIKey = key
			c.Providers[i].Name = name
			return
		}
	}
	c.Providers = append(c.Providers, config.ProviderConfig{ID: id, Name: name, Endpoint: endpoint, APIKey: key})
}
func (m *configMenu) promptAddModelForProvider(pid string) {
	m.ctx.ShowInput("Add a model? Enter model ID (or press Enter to skip):", "", func(id string, ok bool) {
		if !ok || id == "" {
			m.back()
			return
		}
		upsertModelConfig(m.ctx.Config, id, pid)
		if s := m.ctx.ConfigSaver; s != nil {
			if e := s.SaveHomeProvidersAndModels(m.ctx.Config); e != nil {
				m.flash("Failed to save model: " + e.Error())
				m.back()
				return
			}
		}
		m.flash("Model '" + id + "' added for '" + pid + "'.")
		m.back()
	})
}
