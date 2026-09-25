// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"

	"github.com/pijalu/goa/config"

	"github.com/pijalu/goa/tui"
)

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
		{Value: "provider_idle", Label: "Active provider stream idle timeout", Description: activeProviderIdleTimeout(cfg)},
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
		"provider_idle":  (*configMenu).promptProviderIdleTimeout,
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

// promptProviderIdleTimeout asks for the active provider's stream idle timeout.
func (m *configMenu) promptProviderIdleTimeout() {
	cfg := m.ctx.Config
	if cfg.ActiveProvider == "" {
		m.flash("Select an active provider first.")
		m.settingRetrySettings()
		return
	}
	m.current = m.settingRetrySettings
	m.ctx.ShowInput("Provider stream idle timeout (duration):", activeProviderIdleTimeout(cfg), func(v string, accepted bool) {
		if accepted && v != "" {
			m.applySet("providers."+cfg.ActiveProvider+".idle_timeout", v)
		}
		m.settingRetrySettings()
	})
}

func activeProviderIdleTimeout(cfg *config.Config) string {
	for _, p := range cfg.Providers {
		if p.ID == cfg.ActiveProvider && p.IdleTimeout != "" {
			return p.IdleTimeout
		}
	}
	return "2m (default)"
}

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
