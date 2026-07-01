// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package skills implements the skill system: discovery, loading, and
// execution. Skills are defined as SKILL.md files with YAML frontmatter.
// They can be inline (injected into system prompt) or sub-agent (run as
// a separate agent via SkillRunner).
package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SkillMeta holds the parsed metadata from a skill's frontmatter.
// SkillMeta holds the parsed metadata from a skill's frontmatter.
type SkillMeta struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Command     string         `yaml:"command"`
	Inline      bool           `yaml:"inline"`
	Mode        string         `yaml:"mode"`
	Category    string         `yaml:"category"` // "knowledge" or "action" (default)
	Autonomy    string         `yaml:"autonomy,omitempty"`
	MaxTokens   int            `yaml:"max_tokens,omitempty"`
	Temperature float64        `yaml:"temperature"`
	Tools       []string       `yaml:"tools"`
	Skills      []string       `yaml:"skills"`
	InputSchema map[string]any `yaml:"input-schema"`
	Hidden      bool           `yaml:"hidden"`
}

// Skill represents a fully loaded skill with metadata and instructions.
type Skill struct {
	Meta     SkillMeta
	Body     string // markdown body (after frontmatter)
	Source   string // "embedded", "home", "project"
	FilePath string
}

// LinkedMode returns the major mode this skill is linked to, or empty string.
func (s *Skill) LinkedMode() string {
	return s.Meta.Mode
}

// SuggestedSkills returns the skills this skill suggests activating.
func (s *Skill) SuggestedSkills() []string {
	return s.Meta.Skills
}

// SkillSummary is a lightweight skill description for listing.
type SkillSummary struct {
	Name        string
	Description string
	Inline      bool
	Category    string
	FilePath    string
}

const (
	// SkillCategoryAction indicates a skill that performs actions and must be
	// explicitly invoked via /skill:run or the run_skill tool.
	SkillCategoryAction = "action"

	// SkillCategoryKnowledge indicates a skill that provides knowledge or
	// instructions that can be injected into the system prompt.
	SkillCategoryKnowledge = "knowledge"
)

// TrustChecker decides whether a filesystem skill is trusted. Embedded
// skills are always trusted regardless of this checker.
type TrustChecker interface {
	IsTrusted(name, filePath string) (bool, error)
}

// SkillRegistry manages discovery and loading of skills from embedded
// (compiled-in), home (~/.agents/skills/), and project (.agents/skills/)
// directories. Embedded skills are discovered by walking the embedded
// filesystem directory tree — no manual registration needed.
type SkillRegistry struct {
	skills       map[string]*Skill
	dirs         []string
	embedFS      fs.FS        // optional embedded filesystem for built-in skills
	trustChecker TrustChecker // nil means all filesystem skills are trusted
	homeDir      string       // home dir path for source labeling ("home")
	projectDir   string       // project dir path for source labeling ("project")
}

// NewSkillRegistry creates a registry that scans the given directories.
func NewSkillRegistry(dirs []string) *SkillRegistry {
	return &SkillRegistry{
		skills: make(map[string]*Skill),
		dirs:   dirs,
	}
}

// SetEmbeddedFS sets the embedded filesystem for built-in skills.
// Skills are discovered by walking the FS for */SKILL.md entries.
func (r *SkillRegistry) SetEmbeddedFS(efs fs.FS) {
	r.embedFS = efs
}

// SetHomeDir records the home directory path so skills loaded from it
// are labeled with source "home" instead of the generic "file".
func (r *SkillRegistry) SetHomeDir(dir string) {
	r.homeDir = dir
}

// SetProjectDir records the project directory path so skills loaded from it
// are labeled with source "project" instead of the generic "file".
func (r *SkillRegistry) SetProjectDir(dir string) {
	r.projectDir = dir
}

// SetTrustChecker installs the trust gate used for filesystem skills.
// Embedded skills are always trusted and never checked.
func (r *SkillRegistry) SetTrustChecker(tc TrustChecker) {
	r.trustChecker = tc
}

// LoadAll discovers and loads all skills from:
//  1. Embedded filesystem (if set via SetEmbeddedFS)
//  2. Filesystem directories (dirs)
//
// Later sources override earlier ones on name collision.
func (r *SkillRegistry) LoadAll() error {
	// Load embedded skills first (lowest priority)
	if r.embedFS != nil {
		if err := r.scanEmbeddedFS(); err != nil {
			return fmt.Errorf("scan embedded skills: %w", err)
		}
	}

	// Scan dirs in order (later wins on name collision)
	for _, dir := range r.dirs {
		if err := r.scanDir(dir, "file"); err != nil {
			continue
		}
	}

	return nil
}

func (r *SkillRegistry) isTrusted(name, filePath, source string) bool {
	if source == "embedded" {
		return true
	}
	if r.trustChecker == nil {
		return true
	}
	ok, err := r.trustChecker.IsTrusted(name, filePath)
	if err != nil {
		return false
	}
	return ok
}

// scanEmbeddedFS walks the embedded filesystem for */SKILL.md entries.
func (r *SkillRegistry) scanEmbeddedFS() error {
	err := fs.WalkDir(r.embedFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		// path is <name>/SKILL.md
		name := filepath.Dir(path)
		if name == "." {
			return nil
		}
		data, err := fs.ReadFile(r.embedFS, path)
		if err != nil {
			return nil
		}
		skill := parseSkill(name, string(data), "embedded", "embedded:/"+path)
		if skill != nil {
			r.skills[name] = skill
		}
		return nil
	})
	return err
}

func (r *SkillRegistry) scanDir(dir, source string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		name := entry.Name()
		if !r.isTrusted(name, skillPath, source) {
			continue
		}
		skill := parseSkill(name, string(data), source, skillPath)
		if skill != nil {
			r.skills[name] = skill
		}
	}
	return nil
}

// Get returns a skill by name.
func (r *SkillRegistry) Get(name string) (*Skill, bool) {
	s, ok := r.skills[name]
	return s, ok
}

// List returns summaries of all registered skills.
func (r *SkillRegistry) List() []SkillSummary {
	var summaries []SkillSummary
	for _, s := range r.skills {
		summaries = append(summaries, SkillSummary{
			Name:        s.Meta.Name,
			Description: s.Meta.Description,
			Inline:      s.Meta.Inline,
			Category:    categoryOrDefault(s.Meta.Category),
			FilePath:    s.FilePath,
		})
	}
	return summaries
}

// IsInline returns true if the named skill is an inline skill.
func (r *SkillRegistry) IsInline(name string) bool {
	s, ok := r.skills[name]
	return ok && s.Meta.Inline
}

// parseSkill parses a SKILL.md file with YAML frontmatter.
// filePath is the location used in <available_skills> listings; pass an
// empty string only when the location is genuinely unknown.
func parseSkill(name, content, source, filePath string) *Skill {
	meta := SkillMeta{
		Name: name,
	}
	body := content

	// Extract YAML frontmatter between --- markers
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		parts := strings.SplitN(content, "---", 3)
		if len(parts) == 3 {
			// Parse frontmatter (simplified — YAML parsing in production)
			frontmatter := strings.TrimSpace(parts[1])
			for _, line := range strings.Split(frontmatter, "\n") {
				applySkillMetaField(&meta, strings.TrimSpace(line))
			}
			body = strings.TrimSpace(parts[2])
		}
	}

	return &Skill{
		Meta:     meta,
		Body:     body,
		Source:   source,
		FilePath: filePath,
	}
}

func applySkillMetaField(meta *SkillMeta, line string) {
	switch {
	case strings.HasPrefix(line, "name:"):
		meta.Name = strings.TrimSpace(line[5:])
	case strings.HasPrefix(line, "description:"):
		meta.Description = strings.TrimSpace(line[12:])
	case strings.HasPrefix(line, "mode:"):
		meta.Mode = strings.TrimSpace(line[5:])
	case strings.HasPrefix(line, "category:"):
		meta.Category = strings.TrimSpace(line[9:])
	case strings.HasPrefix(line, "command:"):
		meta.Command = strings.TrimSpace(line[8:])
	case strings.HasPrefix(line, "hidden:"):
		meta.Hidden = parseBoolField(line, 7)
	case strings.HasPrefix(line, "inline:"):
		meta.Inline = parseBoolField(line, 7)
	case strings.HasPrefix(line, "temperature:"):
		fmt.Sscanf(line[12:], "%f", &meta.Temperature)
	}
}

func parseBoolField(line string, prefixLen int) bool {
	return strings.TrimSpace(line[prefixLen:]) == "true"
}

// categoryOrDefault returns the category if non-empty, otherwise "action".
func categoryOrDefault(category string) string {
	if category == "" {
		return SkillCategoryAction
	}
	return category
}
