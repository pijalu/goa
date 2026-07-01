// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"fmt"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/perms"
	"github.com/pijalu/goa/prompts"
)

// MajorModeSpec defines the capabilities of a mode.
type MajorModeSpec struct {
	Major           internal.MajorMode     `yaml:"major" json:"major"`
	Name            string                 `yaml:"name" json:"name"`
	Description     string                 `yaml:"description,omitempty" json:"description,omitempty"`
	DefaultSkills   []string               `yaml:"default_skills,omitempty" json:"default_skills,omitempty"`
	AllowedTools    []string               `yaml:"allowed_tools,omitempty" json:"allowed_tools,omitempty"`
	BlockedPaths    []string               `yaml:"blocked_paths,omitempty" json:"blocked_paths,omitempty"`
	DefaultAutonomy internal.AutonomyLevel `yaml:"default_autonomy" json:"default_autonomy"`
	Temperature     float64                `yaml:"temperature,omitempty" json:"temperature,omitempty"`
	MaxTokens       int                    `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	Body            string              `yaml:"body,omitempty" json:"body,omitempty"`
	Guard           perms.GuardConfig   `yaml:"guard,omitempty" json:"guard,omitempty"`
}

// SkillSpec defines a skill that can be on the skill stack.
type SkillSpec struct {
	Name            string                 `yaml:"name" json:"name"`
	Description     string                 `yaml:"description" json:"description"`
	LinkedMajor     internal.MajorMode     `yaml:"linked_major,omitempty" json:"linked_major,omitempty"`
	DefaultAutonomy internal.AutonomyLevel `yaml:"default_autonomy,omitempty" json:"default_autonomy,omitempty"`
}

// ModeRegistry holds all valid modes, their profiles, and registered skills.
type ModeRegistry struct {
	majors   map[internal.MajorMode]MajorModeSpec
	skills   map[string]SkillSpec // all known skills
	builtins []internal.MajorMode // built-in majors
	loader   *prompts.Registry
}

// NewModeRegistry creates a registry with built-in modes loaded from the
// embedded prompts/mode/ directory via the provided prompt registry.
func NewModeRegistry(loader *prompts.Registry) *ModeRegistry {
	r := &ModeRegistry{
		majors: make(map[internal.MajorMode]MajorModeSpec),
		skills: make(map[string]SkillSpec),
		loader: loader,
	}
	r.loadBuiltins()
	return r
}

func (r *ModeRegistry) loadBuiltins() {
	if r.loader == nil {
		return
	}
	names, err := r.loader.ListModes()
	if err != nil {
		return
	}
	for _, name := range names {
		def, err := r.loader.LoadMode(name)
		if err != nil {
			continue
		}
		spec := specFromMode(def)
		r.majors[spec.Major] = spec
		r.builtins = append(r.builtins, spec.Major)
	}
}

// RegisterMajor adds or overwrites a mode specification.
func (r *ModeRegistry) RegisterMajor(spec MajorModeSpec) {
	r.majors[spec.Major] = spec
}

// RegisterSkill adds a skill specification.
func (r *ModeRegistry) RegisterSkill(spec SkillSpec) {
	r.skills[spec.Name] = spec
}

// Resolve returns the MajorModeSpec for a major, or error if unknown.
func (r *ModeRegistry) Resolve(major internal.MajorMode) (MajorModeSpec, error) {
	spec, ok := r.majors[major]
	if !ok {
		return MajorModeSpec{}, fmt.Errorf("unknown major mode: %q", major)
	}
	return spec, nil
}

// Majors returns all registered modes.
func (r *ModeRegistry) Majors() []internal.MajorMode {
	result := make([]internal.MajorMode, 0, len(r.majors))
	for m := range r.majors {
		result = append(result, m)
	}
	return result
}

// IsBuiltin reports whether a major mode is one of the embedded defaults.
func (r *ModeRegistry) IsBuiltin(major internal.MajorMode) bool {
	for _, b := range r.builtins {
		if b == major {
			return true
		}
	}
	return false
}

// Validate checks if a ModeState is valid (known mode, known skills).
func (r *ModeRegistry) Validate(ms internal.ModeState) error {
	if ms.Major == "" {
		return fmt.Errorf("mode state has no major set")
	}
	if _, ok := r.majors[ms.Major]; !ok {
		return fmt.Errorf("unknown major mode: %q", ms.Major)
	}
	for _, skill := range ms.Skills {
		if _, ok := r.skills[skill]; !ok {
			// Unknown skills are allowed but warn via error
			return fmt.Errorf("unknown skill: %q", skill)
		}
	}
	return nil
}

// DefaultForMajor returns the default ModeState for a mode.
// Returns a zero ModeState if the mode is unknown.
func (r *ModeRegistry) DefaultForMajor(major internal.MajorMode) internal.ModeState {
	spec, ok := r.majors[major]
	if !ok {
		return internal.ModeState{}
	}
	return internal.ModeState{
		Major:    spec.Major,
		Skills:   append([]string(nil), spec.DefaultSkills...),
		Autonomy: spec.DefaultAutonomy,
	}
}

// SystemPrompt returns the mode body for a major, or empty string if unknown.
func (r *ModeRegistry) SystemPrompt(major internal.MajorMode) string {
	spec, ok := r.majors[major]
	if !ok {
		return ""
	}
	return spec.Body
}

func specFromMode(m *prompts.ModeDefinition) MajorModeSpec {
	return MajorModeSpec{
		Major:           internal.MajorMode(m.Major),
		Name:            m.Name,
		Description:     m.Description,
		DefaultSkills:   append([]string(nil), m.DefaultSkills...),
		AllowedTools:    append([]string(nil), m.AllowedTools...),
		BlockedPaths:    append([]string(nil), m.BlockedPaths...),
		DefaultAutonomy: internal.AutonomyLevel(m.DefaultAutonomy),
		Temperature:     m.Temperature,
		MaxTokens:       m.MaxTokens,
		Body:            m.Body,
		Guard:           m.Guard,
	}
}
