// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"time"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"

	"github.com/pijalu/goa/tui"
)

func retrySettingsLabel(cfg *config.Config) string {
	maxRetries := cfg.Execution.Retries
	if maxRetries <= 0 {
		maxRetries = 5
	}
	return fmt.Sprintf("%d retries, %s stall retry", maxRetries, activityTimeoutLabel(cfg))
}

// settingRetrySettings shows the retry-settings sub-menu and dispatches the
// selected entry through retrySettingHandlers.
func (m *configMenu) settingRetrySettings() {
	m.current = m.settingRetrySettings
	cfg := m.ctx.Config
	items := []tui.SelectorItem{
		{Value: "retries", Label: "Maximum retries", Description: retrySettingsLabel(cfg)},
		{Value: "stall_timeout", Label: "Auto-retry after provider silence", Description: activityTimeoutLabel(cfg)},
		{Value: "stall_warn", Label: "Stall warning after provider silence", Description: activityWarnAfterLabel(cfg)},
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
		"stall_timeout":  (*configMenu).promptActivityTimeout,
		"stall_warn":     (*configMenu).promptActivityWarnAfter,
		"provider_idle":  (*configMenu).promptProviderIdleTimeout,
		"provider_delay": (*configMenu).promptProviderRetryDelay,
	}
}

// activeActivityTimeout returns the effective silent-stream window: the
// explicit execution.activity_timeout, else the 2-minute fallback the agent
// uses when the key is empty.
func activeActivityTimeout(cfg *config.Config) time.Duration {
	if d, err := time.ParseDuration(cfg.Execution.ActivityTimeout); err == nil && d > 0 {
		return d
	}
	return agenticprovider.DefaultStreamIdleTimeout
}

// activityTimeoutLabel renders the auto-retry window and the warning that
// precedes it, mirroring the values the agent actually uses.
func activityTimeoutLabel(cfg *config.Config) string {
	window := activeActivityTimeout(cfg)
	return fmt.Sprintf("%s (warn at %s)", window.Round(time.Second), effectiveWarnAfter(cfg, window).Round(time.Second))
}

func activityWarnAfterLabel(cfg *config.Config) string {
	window := activeActivityTimeout(cfg)
	if d, err := time.ParseDuration(cfg.Execution.ActivityWarnAfter); err == nil && d > 0 && d < window {
		return d.Round(time.Second).String()
	}
	return fmt.Sprintf("%s (derived: 2/3 of %s)", effectiveWarnAfter(cfg, window).Round(time.Second), window.Round(time.Second))
}

// effectiveWarnAfter mirrors the agent's rule (see
// agentic.Agent.effectiveStallWarnAfter): an explicit warn value strictly inside
// the window wins, otherwise two thirds of the window.
func effectiveWarnAfter(cfg *config.Config, window time.Duration) time.Duration {
	if d, err := time.ParseDuration(cfg.Execution.ActivityWarnAfter); err == nil && d > 0 && d < window {
		return d
	}
	return window * 2 / 3
}

// promptActivityTimeout asks for execution.activity_timeout — how long the
// provider may stay silent before the agent retries.
func (m *configMenu) promptActivityTimeout() {
	m.current = m.settingRetrySettings
	current := m.ctx.Config.Execution.ActivityTimeout
	if current == "" {
		current = agenticprovider.DefaultStreamIdleTimeout.String()
	}
	m.ctx.ShowInput("Auto-retry after provider silence (duration):", current, func(v string, accepted bool) {
		if accepted && v != "" {
			m.applySet("execution.activity_timeout", v)
		}
		m.settingRetrySettings()
	})
}

// promptActivityWarnAfter asks for execution.activity_warn_after — how long the
// provider may stay silent before the user is told the agent is still waiting.
func (m *configMenu) promptActivityWarnAfter() {
	m.current = m.settingRetrySettings
	current := m.ctx.Config.Execution.ActivityWarnAfter
	if current == "" {
		current = effectiveWarnAfter(m.ctx.Config, activeActivityTimeout(m.ctx.Config)).Round(time.Second).String()
	}
	m.ctx.ShowInput("Stall warning after provider silence (duration):", current, func(v string, accepted bool) {
		if accepted && v != "" {
			m.applySet("execution.activity_warn_after", v)
		}
		m.settingRetrySettings()
	})
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
			// A provider idle_timeout shrinks the stall window the running
			// session uses, so push the rebuilt options too (shared helper).
			syncStreamOptions(m.ctx)
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
			// Retry caps are part of StreamOptions; push them live as well.
			syncStreamOptions(m.ctx)
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
