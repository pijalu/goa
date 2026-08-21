// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"

	"github.com/pijalu/goa/config"
	internalprovider "github.com/pijalu/goa/internal/agentic/provider"
)

// dreamResolver is a ProviderResolver stub returning a fixed model.
type dreamResolver struct {
	modelID string
}

func (r *dreamResolver) ResolveActiveModel() (internalprovider.Model, error) {
	return internalprovider.Model{ID: r.modelID}, nil
}
func (r *dreamResolver) BuildStreamOptions() internalprovider.StreamOptions {
	return internalprovider.StreamOptions{}
}

// TestDreamResolveModel_RestoresActiveCouple pins the couple-drift fix: a
// dream run with its own configured provider/model must be a one-shot side
// session — the ACTIVE provider/model couple in config must be restored after
// resolution, or the main session silently switches to the dream couple
// (wrong status-bar provider, wrong next-turn routing).
func TestDreamResolveModel_RestoresActiveCouple(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "main",
		ActiveModel:    "main-model",
		Providers: []config.ProviderConfig{
			{ID: "main", Endpoint: "http://localhost:1234/v1"},
			{ID: "dreamprov", Endpoint: "http://localhost:9999/v1"},
		},
	}
	cfg.Memory.Dream.Provider = "dreamprov"
	cfg.Memory.Dream.Model = "dream-model"

	d := &DreamEngine{cfg: cfg, providerMgr: &dreamResolver{modelID: "dream-model"}}
	mdl, _, err := d.resolveModel()
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if mdl.ID != "dream-model" {
		t.Errorf("resolved dream model = %q, want dream-model", mdl.ID)
	}
	if cfg.ActiveProvider != "main" || cfg.ActiveModel != "main-model" {
		t.Errorf("active couple after dream = (%s, %s), want restored (main, main-model)",
			cfg.ActiveProvider, cfg.ActiveModel)
	}

	// Without an explicit dream couple, resolution uses — and must not disturb
	// — the active couple.
	cfg2 := &config.Config{
		ActiveProvider: "main",
		ActiveModel:    "main-model",
		Providers:      []config.ProviderConfig{{ID: "main", Endpoint: "http://localhost:1234/v1"}},
	}
	d2 := &DreamEngine{cfg: cfg2, providerMgr: &dreamResolver{modelID: "main-model"}}
	if _, _, err := d2.resolveModel(); err != nil {
		t.Fatalf("resolveModel (no dream couple): %v", err)
	}
	if cfg2.ActiveProvider != "main" || cfg2.ActiveModel != "main-model" {
		t.Errorf("active couple disturbed without dream override: (%s, %s)",
			cfg2.ActiveProvider, cfg2.ActiveModel)
	}
}
