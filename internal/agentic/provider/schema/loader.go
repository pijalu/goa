// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal"
)

//go:embed variants/*.json
var variantsFS embed.FS

const (
	userConfigDir    = ".goa"
	projectConfigDir = ".goa"
	localConfigDir   = ".goa"
	providersSubdir  = "providers"
)

// LoadEmbeddedProfiles loads all embedded variant profiles.
func LoadEmbeddedProfiles() ([]VariantProfile, error) {
	entries, err := variantsFS.ReadDir("variants")
	if err != nil {
		return nil, fmt.Errorf("read embedded variants: %w", err)
	}
	var profiles []VariantProfile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := variantsFS.ReadFile(filepath.Join("variants", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read embedded profile %s: %w", entry.Name(), err)
		}
		var p VariantProfile
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("parse embedded profile %s: %w", entry.Name(), err)
		}
		if p.ID == "" {
			p.ID = strings.TrimSuffix(entry.Name(), ".json")
		}
		profiles = append(profiles, p)
	}
	return profiles, nil
}

// LoadProfileFromFile loads a single variant profile from disk.
func LoadProfileFromFile(path string) (VariantProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return VariantProfile{}, fmt.Errorf("read profile %s: %w", path, err)
	}
	var p VariantProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return VariantProfile{}, fmt.Errorf("parse profile %s: %w", path, err)
	}
	return p, nil
}

// ProfileLayer is one source of variant profiles in the config cascade, in
// increasing precedence order: embedded defaults, then the user, project and
// local config directories. Later layers override earlier ones (docs/PROVIDERS.md
// "Config cascade"), and within one layer the most specific match wins.
type ProfileLayer struct {
	// Name identifies the source for diagnostics ("embedded", "user",
	// "project", "local").
	Name string
	// Profiles are the profiles that source supplies.
	Profiles []VariantProfile
}

// LoadUserProfiles loads user, project, and local profile overrides.
func LoadUserProfiles() ([]VariantProfile, error) {
	layers, err := LoadProfileLayers()
	if err != nil {
		return nil, err
	}
	var out []VariantProfile
	for _, l := range layers {
		if l.Name == layerEmbedded {
			continue
		}
		out = append(out, l.Profiles...)
	}
	return out, nil
}

// profileSource names one user-configurable profile directory.
type profileSource struct {
	name string
	dir  string
}

// profileSearchSources lists the user-configurable profile directories in
// cascade order.
func profileSearchSources() []profileSource {
	var srcs []profileSource
	if home, ok := internal.GoaHome(); ok {
		srcs = append(srcs, profileSource{layerUser, filepath.Join(home, userConfigDir, providersSubdir)})
	}
	if cwd, err := os.Getwd(); err == nil {
		srcs = append(srcs,
			profileSource{layerProject, filepath.Join(cwd, projectConfigDir, providersSubdir)},
			profileSource{layerLocal, filepath.Join(cwd, localConfigDir, providersSubdir+".local")},
		)
	}
	return srcs
}

// Layer names, used as ProfileLayer.Name and in profileSource.
const (
	layerEmbedded = "embedded"
	layerUser     = "user"
	layerProject  = "project"
	layerLocal    = "local"
)

func loadProfilesInDir(dir string) ([]VariantProfile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var profiles []VariantProfile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		p, err := LoadProfileFromFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if p.ID == "" {
			p.ID = strings.TrimSuffix(entry.Name(), ".json")
		}
		profiles = append(profiles, p)
	}
	return profiles, nil
}

// LoadAllProfiles loads embedded defaults and user overrides as one flat
// slice. Callers that need the cascade structure use LoadProfileLayers.
func LoadAllProfiles() ([]VariantProfile, error) {
	layers, err := LoadProfileLayers()
	if err != nil {
		return nil, err
	}
	var out []VariantProfile
	for _, l := range layers {
		out = append(out, l.Profiles...)
	}
	return out, nil
}

// LoadProfileLayers returns every variant profile source in cascade order:
// embedded defaults first, then the user, project and local config
// directories. Later layers override earlier ones.
func LoadProfileLayers() ([]ProfileLayer, error) {
	embedded, err := LoadEmbeddedProfiles()
	if err != nil {
		return nil, err
	}
	layers := []ProfileLayer{{Name: layerEmbedded, Profiles: embedded}}
	for _, src := range profileSearchSources() {
		profiles, err := loadProfilesInDir(src.dir)
		if err != nil {
			return nil, err
		}
		if len(profiles) == 0 {
			continue
		}
		layers = append(layers, ProfileLayer{Name: src.name, Profiles: profiles})
	}
	return layers, nil
}
