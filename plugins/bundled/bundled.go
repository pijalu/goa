// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package bundled embeds Goa's built-in plugins so they ship with the binary
// and load without a separate install step. The provider-quota plugin is the
// first bundled plugin; others follow the same layout.
package bundled

import (
	"embed"
	"io/fs"

	"gopkg.in/yaml.v3"
)

// FS holds all bundled plugins, rooted at the repository's plugins/bundled
// directory. Each immediate subdirectory is one plugin (plugin.yaml + entry).
//
//go:embed provider-quota
var FS embed.FS

// ProviderQuotaID is the plugin id of the bundled quota plugin.
const ProviderQuotaID = "provider-quota"

// manifest mirrors the plugin.yaml fields Go needs at load time.
type manifest struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
}

// ProviderQuotaSource returns the BundledSource describing the embedded
// provider-quota plugin, reading its version from the embedded manifest.
func ProviderQuotaSource() (manifest, error) {
	data, err := FS.ReadFile(ProviderQuotaID + "/plugin.yaml")
	if err != nil {
		return manifest{}, err
	}
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

// ReadFile exposes the embed FS ReadFile for the plugins package.
func ReadFile(name string) ([]byte, error) { return FS.ReadFile(name) }

// ReadDir exposes the embed FS ReadDir for the plugins package.
func ReadDir(name string) ([]fs.DirEntry, error) { return FS.ReadDir(name) }
