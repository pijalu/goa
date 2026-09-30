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

// activeActivityTimeout returns the effective event-stall window the agent
// retries on: the configured execution.activity_timeout narrowed to the
// watchdog's share of the byte budget, else the provider default. The byte
// guard keeps the full budget as its backstop.
func activeActivityTimeout(cfg *config.Config) time.Duration {
	if d, err := time.ParseDuration(cfg.Execution.ActivityTimeout); err == nil && d > 0 {
		return agenticprovider.EventStallTimeout(d)
	}
	return agenticprovider.EventStallTimeout(agenticprovider.DefaultStreamIdleTimeout)
}

// activityTimeoutLabel renders the auto-retry window and the warning that
// precedes it, mirroring the values the agent actually uses. Stall-timing
// values display as plain seconds (bugs.md): "225 (warn at 200)", not "3m45s".
func activityTimeoutLabel(cfg *config.Config) string {
	window := activeActivityTimeout(cfg)
	return fmt.Sprintf("%s (warn at %s)",
		formatStallSeconds(window.Round(time.Second)),
		formatStallSeconds(effectiveWarnAfter(cfg, window).Round(time.Second)))
}

func activityWarnAfterLabel(cfg *config.Config) string {
	window := activeActivityTimeout(cfg)
	if d, err := time.ParseDuration(cfg.Execution.ActivityWarnAfter); err == nil && d > 0 && d < window {
		return formatStallSeconds(d.Round(time.Second))
	}
	return fmt.Sprintf("%s (derived: 2/3 of %s)",
		formatStallSeconds(effectiveWarnAfter(cfg, window).Round(time.Second)),
		formatStallSeconds(window.Round(time.Second)))
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
// provider may stay silent before the agent retries. The prompt prefills
// plain seconds and accepts a bare number (bugs.md BUG-5); durations
// ("45s", "2m") stay valid.
func (m *configMenu) promptActivityTimeout() {
	m.current = m.settingRetrySettings
	current := m.ctx.Config.Execution.ActivityTimeout
	if d, err := time.ParseDuration(current); err == nil {
		current = formatStallSeconds(d)
	} else if current == "" {
		current = formatStallSeconds(agenticprovider.DefaultStreamIdleTimeout)
	}
	m.ctx.ShowInput("Auto-retry after provider silence (seconds, e.g. 60):", current, func(v string, accepted bool) {
		if accepted && v != "" {
			m.applySet("execution.activity_timeout", v)
		}
		m.settingRetrySettings()
	})
}

// promptActivityWarnAfter asks for execution.activity_warn_after — how long the
// provider may stay silent before the user is told the agent is still waiting.
// Plain seconds in, canonical duration persisted (bugs.md BUG-5).
func (m *configMenu) promptActivityWarnAfter() {
	m.current = m.settingRetrySettings
	current := m.ctx.Config.Execution.ActivityWarnAfter
	if d, err := time.ParseDuration(current); err == nil {
		current = formatStallSeconds(d)
	} else if current == "" {
		current = formatStallSeconds(effectiveWarnAfter(m.ctx.Config, activeActivityTimeout(m.ctx.Config)).Round(time.Second))
	}
	m.ctx.ShowInput("Stall warning after provider silence (seconds, e.g. 30):", current, func(v string, accepted bool) {
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
