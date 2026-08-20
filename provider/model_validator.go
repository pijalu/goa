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

// ModelValidity is the tri-state probe outcome for a configured model
// (Model list). It distinguishes "we don't know" from "we checked
// and it's gone" so a transient provider outage (e.g. LM Studio down at probe
// time) never paints an entry red.
type ModelValidity int

const (
	// ValidityUnknown means the model was never probed, or every probe so far
	// errored (provider unreachable / HTTP error) before any verdict.
	ValidityUnknown ModelValidity = iota
	// ValidityValid means the provider answered and lists the model.
	ValidityValid
	// ValidityInvalid means the provider answered and does NOT list the model
	// (or the model's provider no longer exists in config).
	ValidityInvalid
)

// String renders the validity for logs and diagnostics.
func (v ModelValidity) String() string {
	switch v {
	case ValidityValid:
		return "valid"
	case ValidityInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// ModelValidator runs background probes that check whether each configured
// model is still available on its provider. Results are exposed so the UI can
// mark confirmed-available models green and confirmed-missing models red.
type ModelValidator struct {
	pm     *ProviderManager
	cfg    *config.Config
	mu     sync.RWMutex
	status map[string]ModelValidity // model config ID -> tri-state verdict
}

// NewModelValidator creates a validator bound to a provider manager and config.
func NewModelValidator(pm *ProviderManager, cfg *config.Config) *ModelValidator {
	return &ModelValidator{
		pm:     pm,
		cfg:    cfg,
		status: make(map[string]ModelValidity),
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

// validateOne probes one model. A probe ERROR (provider unreachable, HTTP
// failure) is transient: the previous verdict is kept (unknown on first
// probe) so a flapping local server does not oscillate entries between green
// and red. Only a definitive answer flips the state.
func (v *ModelValidator) validateOne(m config.ModelConfig) {
	listed, definitive := v.checkModel(m)
	if !definitive {
		return // transient probe failure — keep the last known verdict
	}
	state := ValidityInvalid
	if listed {
		state = ValidityValid
	}
	v.mu.Lock()
	v.status[m.ID] = state
	v.mu.Unlock()
}

// checkModel reports whether the provider lists the model, and whether the
// answer is definitive. A missing provider config is definitive (not
// transient); a fetch/parse error is not.
func (v *ModelValidator) checkModel(m config.ModelConfig) (listed, definitive bool) {
	provider := v.cfg.GetProviderByID(m.ProviderID)
	if provider == nil {
		return false, true // provider removed from config: definitively unusable
	}
	models, err := v.pm.ListModelsCached(m.ProviderID, modelCacheTTL)
	if err != nil {
		return false, false // transient: offline, timeout, 5xx, bad payload
	}
	name := m.Model
	if name == "" {
		name = m.ID
	}
	for _, info := range models {
		if info.ID == name {
			return true, true
		}
	}
	return false, true
}

const modelCacheTTL = 5 * time.Minute

// State reports the last known tri-state validity of a model config ID.
// Models never probed (or only probed during outages) report ValidityUnknown.
func (v *ModelValidator) State(modelID string) ModelValidity {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.status[modelID]
}

// IsValid reports the last known validity of a model config ID.
// It is true only for a confirmed ValidityValid verdict.
func (v *ModelValidator) IsValid(modelID string) bool {
	return v.State(modelID) == ValidityValid
}

// SetValid manually overrides the validity of a model config ID.
func (v *ModelValidator) SetValid(modelID string, valid bool) {
	state := ValidityInvalid
	if valid {
		state = ValidityValid
	}
	v.SetState(modelID, state)
}

// SetState manually overrides the tri-state validity of a model config ID.
func (v *ModelValidator) SetState(modelID string, state ModelValidity) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.status[modelID] = state
}
