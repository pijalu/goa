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

	"gopkg.in/yaml.v3"
)

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
	SubSkills   []string       `yaml:"sub-skills"`
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

// ImportedSkills returns the names of skills imported by this skill for use
// inside its sub-agent.
func (s *Skill) ImportedSkills() []string {
	return s.Meta.Skills
}

// HasSubSkills reports whether the skill references sub-skills that must be
// executed inside a sub-agent.
func (s *Skill) HasSubSkills() bool {
	return len(s.Meta.SubSkills) > 0
}

// IsInline returns true if the named skill is an inline skill.
func (r *SkillRegistry) IsInline(name string) bool {
	s, ok := r.skills[name]
	return ok && s.Meta.Inline
}

// SubSkills returns the sub-skills registered for the named skill, or nil
// if the skill has no sub-skills or is not found.
func (r *SkillRegistry) SubSkills(name string) []*Skill {
	return r.subSkills[name]
}

// ImportedSkills returns the skills imported by the named skill for use
// inside its sub-agent, or nil if the skill is not found.
func (r *SkillRegistry) ImportedSkills(name string) []*Skill {
	s, ok := r.skills[name]
	if !ok {
		return nil
	}
	var out []*Skill
	for _, impName := range s.Meta.Skills {
		if imp, ok := r.skills[impName]; ok {
			out = append(out, imp)
		}
	}
	return out
}

// HasSubSkills reports whether the named skill has sub-skills, either from
// frontmatter or from a skills/ subdirectory.
func (r *SkillRegistry) HasSubSkills(name string) bool {
	return r.hasAnySubSkill(name)
}

// SkillSummary is a lightweight skill description for listing.
type SkillSummary struct {
	Name             string
	Description      string
	Inline           bool
	Category         string
	FilePath         string
	RequiresSubAgent bool
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
	subSkills    map[string][]*Skill // parent skill name → sub-skills loaded from skills/ subdir
	dirs         []string
	embedFS      fs.FS        // optional embedded filesystem for built-in skills
	trustChecker TrustChecker // nil means all filesystem skills are trusted
	homeDir      string       // home dir path for source labeling ("home")
	projectDir   string       // project dir path for source labeling ("project")
}

// NewSkillRegistry creates a registry that scans the given directories.
func NewSkillRegistry(dirs []string) *SkillRegistry {
	return &SkillRegistry{
		skills:    make(map[string]*Skill),
		subSkills: make(map[string][]*Skill),
		dirs:      dirs,
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
		skill := parseSkill(name, string(data), "embedded", "skills/"+path)
		if skill != nil {
			r.skills[name] = skill
			r.scanEmbeddedSubSkills(name, path)
		}
		return nil
	})
	return err
}

// scanEmbeddedSubSkills loads sub-skills from a skills/ subdirectory inside
// the embedded parent skill directory.
func (r *SkillRegistry) scanEmbeddedSubSkills(parentName, parentPath string) {
	if r.embedFS == nil {
		return
	}
	subDir := filepath.Join(filepath.Dir(parentPath), "skills")
	entries, err := fs.ReadDir(r.embedFS, subDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(subDir, entry.Name(), "SKILL.md")
		data, err := fs.ReadFile(r.embedFS, skillPath)
		if err != nil {
			continue
		}
		skill := parseSkill(entry.Name(), string(data), "embedded", "skills/"+skillPath)
		if skill != nil {
			r.subSkills[parentName] = append(r.subSkills[parentName], skill)
		}
	}
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
			r.scanSubSkills(dir, name, source)
		}
	}
	return nil
}

// scanSubSkills loads sub-skills from a skills/ subdirectory inside the
// parent skill directory. Sub-skills are hidden from the main agent and are
// only available to the sub-agent executing the parent skill.
func (r *SkillRegistry) scanSubSkills(dir, parentName, source string) {
	subDir := filepath.Join(dir, parentName, "skills")
	entries, err := os.ReadDir(subDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(subDir, entry.Name(), "SKILL.md")
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
			r.subSkills[parentName] = append(r.subSkills[parentName], skill)
		}
	}
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
			Name:             s.Meta.Name,
			Description:      s.Meta.Description,
			Inline:           s.Meta.Inline,
			Category:         categoryOrDefault(s.Meta.Category),
			FilePath:         s.FilePath,
			RequiresSubAgent: r.hasAnySubSkill(s.Meta.Name),
		})
	}
	return summaries
}

// hasAnySubSkill reports whether the named skill has sub-skills from either
// frontmatter or a skills/ subdirectory.
func (r *SkillRegistry) hasAnySubSkill(name string) bool {
	if s, ok := r.skills[name]; ok && len(s.Meta.SubSkills) > 0 {
		return true
	}
	return len(r.subSkills[name]) > 0
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
			if err := yaml.Unmarshal([]byte(parts[1]), &meta); err == nil {
				body = strings.TrimSpace(parts[2])
			}
		}
	}

	return &Skill{
		Meta:     meta,
		Body:     body,
		Source:   source,
		FilePath: filePath,
	}
}

// categoryOrDefault returns the category if non-empty, otherwise "action".
func categoryOrDefault(category string) string {
	if category == "" {
		return SkillCategoryAction
	}
	return category
}
