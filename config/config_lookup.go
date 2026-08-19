// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal"
)

type LoggingConfig struct {
	Level       string `yaml:"level"`
	File        string `yaml:"file"`
	TraceKeys   bool   `yaml:"trace_keys"`
	TerminalLog string `yaml:"terminal_log"`
	RenderTrace string `yaml:"render_trace"`
	// CaptureStream enables the agent stream capture: the exact stream flow
	// is written as JSONL to this path for replay/diagnosis (--capture-stream).
	CaptureStream string `yaml:"capture_stream"`
}

// Validate checks the config for semantic correctness.
// GetProviderByID returns the provider config for the given ID, or nil if not found.
func (c *Config) GetProviderByID(id string) *ProviderConfig {
	for i := range c.Providers {
		if c.Providers[i].ID == id {
			return &c.Providers[i]
		}
	}
	return nil
}

// GetModelByID returns the model config for the given ID, or nil if not found.
func (c *Config) GetModelByID(id string) *ModelConfig {
	for i := range c.Models {
		if c.Models[i].ID == id {
			return &c.Models[i]
		}
	}
	return nil
}

// PreferredProvider returns the first provider marked as preferred, or the first provider.
func (c *Config) PreferredProvider() *ProviderConfig {
	if len(c.Providers) == 0 {
		return nil
	}
	for i := range c.Providers {
		if c.Providers[i].Preferred {
			return &c.Providers[i]
		}
	}
	return &c.Providers[0]
}

// DeepMerge merges another Config into this one, following deep-merge rules.
//  3. Autonomy: mode.default.autonomy → mode.defaults[major] → built-in fallback
func (c *Config) DefaultModeState() internal.ModeState {
	ms := c.Mode.Default

	if ms.Major == "" {
		ms.Major = internal.MajorCoder
	}

	// If no explicit autonomy set, check mode.defaults for this major
	if ms.Autonomy == "" && c.Mode.Defaults != nil {
		if aut, ok := c.Mode.Defaults[ms.Major]; ok {
			ms.Autonomy = aut
		}
	}

	// If still no autonomy, use built-in defaults based on major
	if ms.Autonomy == "" {
		ms.Autonomy = DefaultAutonomyForMajor(ms.Major)
	}

	return ms
}

// MigrateActiveProfile moves the deprecated ActiveProfile field into
// Mode.Default.Major, overwriting any existing value. It is called once by the
// cascade loader after all layers are merged. The ActiveProfile field is
// cleared so it is not persisted back.
func (c *Config) MigrateActiveProfile() {
	if c.ActiveProfile == "" {
		return
	}
	c.Mode.Default.Major = internal.MajorMode(c.ActiveProfile)
	c.ActiveProfile = ""
}

// ResolvePlanFilePath returns the absolute path to the plan file.
// If Mode.PlanFilePath is set, it is expanded and made absolute relative to
// the project directory (or the current working directory when projectDir is
// empty). The default is `<projectDir>/.goa/plan.md`.
func (c *Config) ResolvePlanFilePath(projectDir string) string {
	p := c.Mode.PlanFilePath
	if p == "" {
		if projectDir == "" {
			return ".goa/plan.md"
		}
		return filepath.Join(projectDir, ".goa", "plan.md")
	}

	p = os.ExpandEnv(p)
	if strings.HasPrefix(p, "~/") {
		if home, ok := internal.GoaHome(); ok {
			p = filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	if projectDir != "" {
		return filepath.Join(projectDir, p)
	}
	return p
}

// ActiveMajor returns the active major mode as a string.
func (c *Config) ActiveMajor() string {
	return string(c.DefaultModeState().Major)
}

// SetActiveMajor sets the active major mode (profile).
func (c *Config) SetActiveMajor(major string) {
	c.Mode.Default.Major = internal.MajorMode(major)
}

// DefaultAutonomyForMajor returns the built-in default autonomy for a major.
// Unknown modes default to SOLO so new modes defined only in metadata do not
// require a code change here.
func DefaultAutonomyForMajor(major internal.MajorMode) internal.AutonomyLevel {
	return internal.AutonomySolo
}

// DeepCopy returns a deep copy of the Config. Every reference-kind field
// (pointer, map, slice) is reallocated so the copy shares no mutable state
// with the original. This must be a faithful copy — the previous
// DeepMerge-based implementation silently dropped whole sections guarded by
// merge flags (e.g. the entire ContextCompression block when disabled),
// which both lost settings on Save and made copies unusable for
// validate-before-commit flows.
