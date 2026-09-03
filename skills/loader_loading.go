// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pijalu/goa/internal/embeddoc"
	"gopkg.in/yaml.v3"
)

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
		if !r.allowed(name) || r.embeddedDefaultOff(name) {
			return nil
		}
		data, err := fs.ReadFile(r.embedFS, path)
		if err != nil {
			return nil
		}
		skill := parseSkill(name, string(data), "embedded", "skills/"+path)
		if skill != nil {
			r.applyStickyOverride(skill)
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
		if !r.allowed(entry.Name()) || r.embeddedDefaultOff(entry.Name()) {
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
		if !r.allowed(name) {
			continue
		}
		if !r.isTrusted(name, skillPath, source) {
			continue
		}
		skill := parseSkill(name, string(data), source, skillPath)
		if skill != nil {
			r.applyStickyOverride(skill)
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
		if !r.allowed(name) {
			continue
		}
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

// SourceOf reports the origin of a skill by name, scanning candidate
// locations (embedded FS + configured dirs) WITHOUT the enabled/disabled gate
// so disabled skills are still found. It is used to route skill toggles to
// the right config layer (embedded → global/home, file → project). Returns
// ("embedded", true) for compiled-in skills, ("file", true) for directory
// skills, and ("", false) when the name matches no discoverable skill.
func (r *SkillRegistry) SourceOf(name string) (string, bool) {
	if r.embedFS != nil {
		path := filepath.Join(name, "SKILL.md")
		if _, err := fs.Stat(r.embedFS, path); err == nil {
			return "embedded", true
		}
	}
	for _, dir := range r.dirs {
		if _, err := os.Stat(filepath.Join(dir, name, "SKILL.md")); err == nil {
			return "file", true
		}
	}
	return "", false
}

// ListEmbeddedDiscoverable returns summaries of EVERY skill discoverable in
// the embedded filesystem, including default-off and explicitly-disabled ones
// that LoadAll skipped. The /config skill toggle uses it so a default-off
// embedded skill (e.g. review) still appears in the menu and can be re-enabled;
// the agent never sees it (Get/List exclude it until enabled).
func (r *SkillRegistry) ListEmbeddedDiscoverable() []SkillSummary {
	if r.embedFS == nil {
		return nil
	}
	var out []SkillSummary
	_ = fs.WalkDir(r.embedFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		name := filepath.Dir(path)
		if name == "." || filepath.Dir(name) != "." {
			return nil // top-level skills only (sub-skills live under <parent>/skills/)
		}
		data, err := fs.ReadFile(r.embedFS, path)
		if err != nil {
			return nil
		}
		if skill := parseSkill(name, string(data), "embedded", "skills/"+path); skill != nil {
			out = append(out, SkillSummary{
				Name:        skill.Meta.Name,
				Description: skill.Meta.Description,
				Inline:      skill.Meta.Inline,
				Category:    categoryOrDefault(skill.Meta.Category),
				FilePath:    skill.FilePath,
				Source:      "embedded",
				// Invocation policy defaults are applied by parseSkill's
				// SkillMeta.UnmarshalYAML (both true when omitted).
				ModelInvocable: skill.Meta.ModelInvocable,
				UserInvocable:  skill.Meta.UserInvocable,
				Hidden:         skill.Meta.Hidden,
			})
		}
		return nil
	})
	return out
}

// EmbeddedDefaultDisabled reports whether the named skill is suppressed by the
// embedded default-off policy right now (in the default-off set AND not
// explicitly re-enabled). The config menu uses it to show the correct on/off
// state for discoverable-but-inactive embedded skills.
func (r *SkillRegistry) EmbeddedDefaultDisabled(name string) bool {
	return r.embeddedDefaultOff(name)
}

// IsEmbeddedDefaultOff reports whether the named skill is a MEMBER of the
// embedded default-off set (all embedded skills except telegram), regardless
// of whether the user has since re-enabled it. Unlike EmbeddedDefaultDisabled
// (the current suppressed state), this is stable across a toggle, so the
// disable path can tell a default-off skill (disabling = drop the opt-in)
// from a default-ON one (disabling = write an explicit Disabled entry).
func (r *SkillRegistry) IsEmbeddedDefaultOff(name string) bool {
	return r.embeddedDefaultDisabled[name]
}

// DefaultOnEmbeddedSkill is the single agent-facing embedded skill that stays
// ON by default; every other agent-facing embedded skill is OFF by default
const DefaultOnEmbeddedSkill = "telegram"

// DefaultEmbeddedOffNames returns the names of all embedded skills that are
// OFF by default: every agent-facing embedded skill except
// DefaultOnEmbeddedSkill (telegram). Two kinds are excluded and stay ON:
//   - telegram (the one kept agent-facing skill), and
//   - hidden/internal skills (e.g. dream): they are never listed to the agent
//     (Meta.Hidden) but internal features load them by name via Get, so
//     defaulting them off would break those features for zero prompt savings.
//
// Derived from the embedded FS so it never drifts as skills are added.
func DefaultEmbeddedOffNames(efs fs.FS) []string {
	var out []string
	if efs == nil {
		return nil
	}
	for _, n := range EmbeddedSkillNames(efs) {
		if n == DefaultOnEmbeddedSkill {
			continue
		}
		hidden := false
		if data, err := fs.ReadFile(efs, filepath.Join(n, "SKILL.md")); err == nil {
			if s := parseSkill(n, string(data), "embedded", ""); s != nil {
				hidden = s.Meta.Hidden
			}
		}
		if !hidden {
			out = append(out, n)
		}
	}
	return out
}

// EmbeddedSkillNames returns the names of every top-level skill discoverable
// in the embedded filesystem (sorted), regardless of enabled/disabled state.
// It lets the wiring layer derive the default-off set ("all embedded skills
// except telegram") without a hardcoded list that drifts as skills are added.
func EmbeddedSkillNames(efs fs.FS) []string {
	if efs == nil {
		return nil
	}
	var names []string
	_ = fs.WalkDir(efs, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		name := filepath.Dir(path)
		if name == "." || filepath.Dir(name) != "." {
			return nil // top-level skills only
		}
		names = append(names, name)
		return nil
	})
	sort.Strings(names)
	return names
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
			Sticky:           s.IsSticky(),
			Source:           s.Source,
			ModelInvocable:   s.Meta.ModelInvocable,
			UserInvocable:    s.Meta.UserInvocable,
			Hidden:           s.Meta.Hidden,
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
	// Invocation policy defaults to fully invocable (P16). The defaults are
	// re-applied by SkillMeta.UnmarshalYAML when frontmatter parses (explicit
	// false still wins); seeding them here keeps the invariant for SKILL.md
	// files with NO frontmatter or malformed frontmatter, where UnmarshalYAML
	// never runs — otherwise the skill loads but is invisible to both the
	// model catalog and the /skills menu.
	meta := SkillMeta{
		Name:           name,
		ModelInvocable: true,
		UserInvocable:  true,
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

	// Skill bodies are model-facing (inline expansion, sub-agent prompts):
	// HTML comments must not consume LLM context.
	body = string(embeddoc.StripHTMLComments([]byte(body)))

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
