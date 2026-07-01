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
)

//go:embed variants/*.json
var variantsFS embed.FS

const (
	userConfigDir    = ".goa"
	projectConfigDir = ".goa"
	localConfigDir   = ".goa"
	providersSubdir  = "providers"
)

// profileSource tracks where a profile was loaded from for debugging.
type profileSource struct {
	Profile VariantProfile
	Source  string
}

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

// LoadUserProfiles loads user, project, and local profile overrides.
func LoadUserProfiles() ([]VariantProfile, error) {
	return loadFromDirs(profileSearchDirs())
}

func profileSearchDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, userConfigDir, providersSubdir))
	}
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(cwd, projectConfigDir, providersSubdir))
		dirs = append(dirs, filepath.Join(cwd, localConfigDir, providersSubdir+".local"))
	}
	return dirs
}

func loadFromDirs(dirs []string) ([]VariantProfile, error) {
	var profiles []VariantProfile
	for _, dir := range dirs {
		loaded, err := loadProfilesInDir(dir)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, loaded...)
	}
	return profiles, nil
}

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

// LoadAllProfiles loads embedded defaults and user overrides.
func LoadAllProfiles() ([]VariantProfile, error) {
	embedded, err := LoadEmbeddedProfiles()
	if err != nil {
		return nil, err
	}
	user, err := LoadUserProfiles()
	if err != nil {
		return nil, err
	}
	return append(embedded, user...), nil
}
