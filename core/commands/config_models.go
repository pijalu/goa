// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/tui"
)

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
