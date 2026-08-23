// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/usage"
)

// applyUsageBasedDefaultModel pins the boot-time default model from the
// persistent usage store when no config layer provides one (Bug6): the
// most-used configured model becomes the session default instead of an
// arbitrary fallback. Project-scoped stats win over global ones, mirroring
// the per-project last-used pin written by model switches.
//
// Best-effort by design: store errors leave the config untouched.
func applyUsageBasedDefaultModel(cfg *config.Config, projectDir string) {
	applyUsageBasedDefaultModelWith(cfg, projectDir, openUsageStoreDefault)
}

func openUsageStoreDefault() (*usage.Store, error) {
	p, err := usage.DefaultPath()
	if err != nil {
		return nil, err
	}
	return usage.Open(p)
}

func applyUsageBasedDefaultModelWith(cfg *config.Config, projectDir string, open func() (*usage.Store, error)) {
	// Only fill a genuine gap: an explicit active_model (config, CLI --model,
	// or the setup wizard) always wins, and the first-run wizard handles its
	// own selection.
	if cfg == nil || cfg.ActiveModel != "" || cfg.FirstRun || len(cfg.Providers) == 0 {
		return
	}
	st, err := open()
	if err != nil || st == nil {
		return
	}
	defer func() { _ = st.Close() }()

	// Project scope first, then global; Query orders by total tokens DESC,
	// so the first entry matching a still-configured model is the winner.
	projectStats, err := st.Query(usage.ByModel, projectDir, time.Time{})
	if err != nil {
		return
	}
	if pid, mid, ok := pickMostUsedModel(projectStats, cfg); ok {
		cfg.ActiveProvider = pid
		cfg.ActiveModel = mid
		return
	}
	globalStats, err := st.Query(usage.ByModel, "", time.Time{})
	if err != nil {
		return
	}
	if pid, mid, ok := pickMostUsedModel(globalStats, cfg); ok {
		cfg.ActiveProvider = pid
		cfg.ActiveModel = mid
	}
}

// pickMostUsedModel maps ordered usage stats onto configured models: entries
// whose key matches a ModelConfig ID (or its wire name) are candidates;
// unknown keys (removed/renamed models) are skipped so stale history never
// resurrects a model that no longer exists.
func pickMostUsedModel(stats []usage.Stat, cfg *config.Config) (providerID, modelID string, ok bool) {
	for _, s := range stats {
		for i := range cfg.Models {
			m := &cfg.Models[i]
			if s.Key == m.ID || s.Key == m.Model {
				return m.ProviderID, m.ID, true
			}
		}
	}
	return "", "", false
}
