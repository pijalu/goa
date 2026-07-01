// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"sync"
	"time"

	"github.com/pijalu/goa/config"
)

// ModelValidator runs background probes that check whether each configured
// model is still available on its provider. Results are exposed so the UI can
// mark invalid models (e.g. in red).
type ModelValidator struct {
	pm     *ProviderManager
	cfg    *config.Config
	mu     sync.RWMutex
	status map[string]bool // model config ID -> valid
}

// NewModelValidator creates a validator bound to a provider manager and config.
func NewModelValidator(pm *ProviderManager, cfg *config.Config) *ModelValidator {
	return &ModelValidator{
		pm:     pm,
		cfg:    cfg,
		status: make(map[string]bool),
	}
}

// Start launches a background validation loop. The loop runs an initial
// validation immediately, then repeats every interval while ctx is active.
func (v *ModelValidator) Start(ctx context.Context, interval time.Duration) {
	if interval < time.Minute {
		interval = 5 * time.Minute
	}
	go func() {
		v.ValidateAll()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				v.ValidateAll()
			}
		}
	}()
}

// ValidateAll probes every configured model's provider and records validity.
func (v *ModelValidator) ValidateAll() {
	if v.cfg == nil {
		return
	}
	for _, m := range v.cfg.Models {
		v.validateOne(m)
	}
}

func (v *ModelValidator) validateOne(m config.ModelConfig) {
	valid := false
	if v.pm != nil {
		valid = v.checkModel(m)
	}
	v.mu.Lock()
	v.status[m.ID] = valid
	v.mu.Unlock()
}

func (v *ModelValidator) checkModel(m config.ModelConfig) bool {
	provider := v.cfg.GetProviderByID(m.ProviderID)
	if provider == nil {
		return false
	}
	models, err := v.pm.ListModelsCached(m.ProviderID, modelCacheTTL)
	if err != nil {
		return false
	}
	name := m.Model
	if name == "" {
		name = m.ID
	}
	for _, info := range models {
		if info.ID == name {
			return true
		}
	}
	return false
}

const modelCacheTTL = 5 * time.Minute

// IsValid reports the last known validity of a model config ID.
func (v *ModelValidator) IsValid(modelID string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.status[modelID]
}

// SetValid manually overrides the validity of a model config ID.
func (v *ModelValidator) SetValid(modelID string, valid bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.status[modelID] = valid
}
