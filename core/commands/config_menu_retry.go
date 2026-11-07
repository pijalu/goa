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

// retriesLabel renders the retry count the entry edits: a bare number, with no
// other setting folded in.
func retriesLabel(cfg *config.Config) string {
	return fmt.Sprintf("%d", cfg.Execution.Retries)
}

// retrySettingsLabel summarizes the whole category for the parent /config row.
// This is the ONLY place these settings are combined; the sub-menu itself shows
// one value per line (see settingRetrySettings).
func retrySettingsLabel(cfg *config.Config) string {
	return fmt.Sprintf("%d retries, %ss silence budget", configuredRetries(cfg), activityTimeoutLabel(cfg))
}

// configuredRetries returns the retry count the sub-menu entry edits, falling
// back to the shipped 5 when the key is unset/zero.
func configuredRetries(cfg *config.Config) int {
	if cfg.Execution.Retries > 0 {
		return cfg.Execution.Retries
	}
	return 5
}

// settingRetrySettings shows the retry-settings sub-menu and dispatches the
// selected entry through retrySettingHandlers.
func (m *configMenu) settingRetrySettings() {
	m.current = m.settingRetrySettings
	cfg := m.ctx.Config
	items := []tui.SelectorItem{
		{Value: "retries", Label: "Maximum retries", Description: retriesLabel(cfg)},
		{Value: "stall_timeout", Label: "Provider silence budget before retry", Description: activityTimeoutLabel(cfg)},
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

// activityTimeoutLabel renders the configured byte-idle budget — exactly the
// value this entry's prompt prefills and persists. One value, no warn folded in:
// the warning lead has its own entry (bugs.md: retry menu grouping).
func activityTimeoutLabel(cfg *config.Config) string {
	return formatStallSeconds(configuredActivityTimeout(cfg).Round(time.Second))
}

// configuredActivityTimeout returns execution.activity_timeout, falling back to
// the provider default when unset or unparseable — the same value the prompt
// offers for editing.
func configuredActivityTimeout(cfg *config.Config) time.Duration {
	if d, err := time.ParseDuration(cfg.Execution.ActivityTimeout); err == nil && d > 0 {
		return d
	}
	return agenticprovider.DefaultStreamIdleTimeout
}

// activityWarnAfterLabel renders the warning lead this entry's prompt edits: the
// configured value when it fits inside the retry window, else the value the
// agent derives. The derivation rule lives in the prompt title, never in the
// menu row.
func activityWarnAfterLabel(cfg *config.Config) string {
	return formatStallSeconds(effectiveWarnAfter(cfg, activeActivityTimeout(cfg)).Round(time.Second))
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

// promptActivityTimeout asks for execution.activity_timeout — the byte-idle
// budget: how long the provider may stay silent before the stream is treated as
// stalled. The agent's event watchdog retries at three quarters of it, which is
// what the prompt title tells the user. The prompt prefills plain seconds and
// accepts a bare number (bugs.md BUG-5); durations ("45s", "2m") stay valid.
func (m *configMenu) promptActivityTimeout() {
	m.current = m.settingRetrySettings
	current := m.ctx.Config.Execution.ActivityTimeout
	if d, err := time.ParseDuration(current); err == nil {
		current = formatStallSeconds(d)
	} else if current == "" {
		current = formatStallSeconds(agenticprovider.DefaultStreamIdleTimeout)
	}
	m.ctx.ShowInput("Provider silence budget before retry (seconds, e.g. 120; retries at 3/4 of it):", current, func(v string, accepted bool) {
		if accepted && v != "" {
			m.applySet("execution.activity_timeout", v)
		}
		m.settingRetrySettings()
	})
}

// promptActivityWarnAfter asks for execution.activity_warn_after — how long the
// provider may stay silent before the user is told the agent is still waiting.
// The derivation fallback (2/3 of the retry window) is stated in the prompt
// title, not in the menu row (bugs.md: retry menu grouping). Plain seconds in,
// canonical duration persisted (bugs.md BUG-5).
func (m *configMenu) promptActivityWarnAfter() {
	m.current = m.settingRetrySettings
	current := m.ctx.Config.Execution.ActivityWarnAfter
	if d, err := time.ParseDuration(current); err == nil {
		current = formatStallSeconds(d)
	} else if current == "" {
		current = formatStallSeconds(effectiveWarnAfter(m.ctx.Config, activeActivityTimeout(m.ctx.Config)).Round(time.Second))
	}
	m.ctx.ShowInput("Stall warning after provider silence (seconds, e.g. 80; empty or too large = 2/3 of the budget):", current, func(v string, accepted bool) {
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
	m.ctx.ShowInput("Provider stream idle timeout (duration, e.g. 2m; empty = default):", activeProviderIdleTimeout(cfg), func(v string, accepted bool) {
		if accepted && v != "" {
			m.applySet("providers."+cfg.ActiveProvider+".idle_timeout", v)
			// A provider idle_timeout shrinks the stall window the running
			// session uses, so push the rebuilt options too (shared helper).
			syncStreamOptions(m.ctx)
		}
		m.settingRetrySettings()
	})
}

// activeProviderIdleTimeout renders the active provider's stream idle timeout as
// the plain value the entry edits: the configured duration, else the default
// labelled by the caller. It is never decorated with a marker — the same string
// feeds the prompt prefill, and a marker would be persisted as part of the
// duration.
func activeProviderIdleTimeout(cfg *config.Config) string {
	for _, p := range cfg.Providers {
		if p.ID == cfg.ActiveProvider && p.IdleTimeout != "" {
			return p.IdleTimeout
		}
	}
	return agenticprovider.DefaultStreamIdleTimeout.String()
}

// promptProviderRetryDelay asks for the active provider's max_retry_delay value,
// then returns to the retry-settings menu.
func (m *configMenu) promptProviderRetryDelay() {
	cfg := m.ctx.Config
	if cfg.ActiveProvider == "" {
		m.flash("Select an active provider first.")
		m.settingRetrySettings()
		return
	}
	m.current = m.settingRetrySettings
	m.ctx.ShowInput("Provider retry cap (duration, e.g. 2m; empty = default):", activeProviderRetryDelay(cfg), func(v string, accepted bool) {
		if accepted && v != "" {
			m.applySet("providers."+cfg.ActiveProvider+".max_retry_delay", v)
			// Retry caps are part of StreamOptions; push them live as well.
			syncStreamOptions(m.ctx)
		}
		m.settingRetrySettings()
	})
}

// activeProviderRetryDelay renders the active provider's retry cap as the plain
// value the entry edits (see activeProviderIdleTimeout for why it carries no
// marker).
func activeProviderRetryDelay(cfg *config.Config) string {
	for _, p := range cfg.Providers {
		if p.ID == cfg.ActiveProvider && p.MaxRetryDelay != "" {
			return p.MaxRetryDelay
		}
	}
	return agenticprovider.DefaultStreamIdleTimeout.String()
}
