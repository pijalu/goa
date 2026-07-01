// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"sync"
	"time"
)

// ModelCache stores the last successful model list per provider with a TTL.
// It lets the UI show model lists instantly while refreshing in the background.
type ModelCache struct {
	mu      sync.RWMutex
	entries map[string]cachedModels
}

type cachedModels struct {
	models []ModelInfo
	at     time.Time
}

// NewModelCache creates an empty model cache.
func NewModelCache() *ModelCache {
	return &ModelCache{entries: make(map[string]cachedModels)}
}

// Get returns the cached model list for providerID if it exists and is fresh.
func (c *ModelCache) Get(providerID string, ttl time.Duration) ([]ModelInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[providerID]
	if !ok || time.Since(entry.at) > ttl {
		return nil, false
	}
	out := make([]ModelInfo, len(entry.models))
	copy(out, entry.models)
	return out, true
}

// Set stores a model list for providerID.
func (c *ModelCache) Set(providerID string, models []ModelInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[providerID] = cachedModels{
		models: models,
		at:     time.Now(),
	}
}

// Invalidate removes a provider's cached model list.
func (c *ModelCache) Invalidate(providerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, providerID)
}
