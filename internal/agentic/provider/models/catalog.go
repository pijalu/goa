// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"sort"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// catalog.go — the provider-level view of the embedded models.dev catalog.
//
// The model registry answers "which models exist"; this file answers "which
// providers exist, under which identity, reading which API-key environment
// variables". That is the single source of truth behind the sign-on surface
// (/login list + completions, the setup/provider credential prompt), so a
// provider Goa can select can always be given a credential — including
// catalog-only gateways such as vercel whose models.dev entry carries only
// npm/env and no base URL (bugs.md "Vercel AI Gateway: no way to add an API
// key", export 2026-09-26-113044).

// CatalogProvider is one provider the catalog offers as a sign-on target.
type CatalogProvider struct {
	// ID is the Goa identity: the id a user configures and the id /login
	// stores the credential under.
	ID string
	// Name is the catalog's display name.
	Name string
	// EnvKeys are the API-key environment variables the provider reads, in
	// priority order (curated ProviderDef first, then the models.dev "env"
	// names).
	EnvKeys []string
	// BaseURL is the catalog's default base URL, when it declares one ("" for
	// gateways whose models.dev entry carries none — see the catalog ProviderDef).
	BaseURL string
	// API is the wire protocol the catalog maps the provider to.
	API provider.Api
}

// CatalogProviders returns every provider the catalog offers, one entry per Goa
// identity, sorted by ID. Several models.dev keys can map to one identity
// (zai-coding-plan → zai); their env names are merged and the first name wins.
func CatalogProviders() []CatalogProvider {
	byID := make(map[string]CatalogProvider, len(modelsDevProvidersCache))
	for _, md := range ModelsDevProviders() {
		id := string(md.Identity)
		if prev, seen := byID[id]; seen {
			byID[id] = CatalogProvider{
				ID:      prev.ID,
				Name:    prev.Name,
				EnvKeys: mergeEnvKeys(prev.EnvKeys, catalogEnvKeys(md.Identity, md.Env)),
				BaseURL: prev.BaseURL,
				API:     prev.API,
			}
			continue
		}
		byID[id] = CatalogProvider{
			ID:      id,
			Name:    md.Name,
			EnvKeys: catalogEnvKeys(md.Identity, md.Env),
			BaseURL: md.BaseURL,
			API:     md.API,
		}
	}
	out := make([]CatalogProvider, 0, len(byID))
	for _, cp := range byID {
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CatalogEnvKeys returns the API-key environment variable names the catalog
// declares for a provider identity (the curated ProviderDef's EnvKeys first —
// they are ordered by priority — then the models.dev "env" names). It accepts
// either a Goa identity or a models.dev key, so callers holding a user-typed
// provider id always get the right names. Returns nil when the catalog knows
// none (e.g. a purely local endpoint).
func CatalogEnvKeys(id string) []string {
	var defKeys []string
	if def := schema.LookupProviderDef(provider.Provider(id)); def != nil {
		defKeys = def.EnvKeys
	}
	for _, md := range ModelsDevProviders() {
		if md.Key != id && string(md.Identity) != id {
			continue
		}
		return mergeEnvKeys(defKeys, md.Env)
	}
	return mergeEnvKeys(defKeys, nil)
}

// catalogEnvKeys merges the curated def's env keys (priority order) with the
// provider's models.dev env names.
func catalogEnvKeys(identity provider.Provider, modelsDevEnv []string) []string {
	var defKeys []string
	if def := schema.LookupProviderDef(identity); def != nil {
		defKeys = def.EnvKeys
	}
	return mergeEnvKeys(defKeys, modelsDevEnv)
}

// mergeEnvKeys concatenates env key lists, dropping empties and duplicates
// while preserving priority order (the first list wins).
func mergeEnvKeys(first, second []string) []string {
	if len(first) == 0 && len(second) == 0 {
		return nil
	}
	out := make([]string, 0, len(first)+len(second))
	seen := make(map[string]bool, len(first)+len(second))
	for _, list := range [][]string{first, second} {
		for _, v := range list {
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
