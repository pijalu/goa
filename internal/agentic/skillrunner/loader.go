// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skillrunner

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	agentic "github.com/pijalu/goa/internal/agentic"
)

// SkillsLoader handles loading skills from various sources (filesystem or embedded).
type SkillsLoader struct {
	// fileDirs is the list of filesystem directories to scan for skills.
	// Used when loading from disk.
	fileDirs []string
	// embeddedFS is the embedded filesystem (set if using embedded mode).
	embeddedFS fs.FS
	// embeddedBasePath is the base path within the embedded FS to search.
	embeddedBasePath string
}

// NewFileSkillsLoader creates a loader that reads skills from filesystem directories.
func NewFileSkillsLoader(dirs []string) *SkillsLoader {
	return &SkillsLoader{
		fileDirs: dirs,
	}
}

// NewEmbeddedSkillsLoader creates a loader that reads skills from an embedded filesystem.
// The basePath should be the directory path within the FS that contains skill directories,
// typically "." for go:embed or a path like "skills" if embedded under a subdirectory.
//
// Example usage with go:embed:
//
//	//go:embed skills
//	var embeddedSkills embed.FS
//
//	loader := NewEmbeddedSkillsLoader(embeddedSkills, "skills")
func NewEmbeddedSkillsLoader(fsys fs.FS, basePath string) *SkillsLoader {
	return &SkillsLoader{
		embeddedFS:       fsys,
		embeddedBasePath: basePath,
	}
}

// Load loads all skills from the configured source.
// Returns an empty slice if no skills are found or on error (errors are logged).
func (l *SkillsLoader) Load(logger *agentic.Logger) []*Skill {
	if l.embeddedFS != nil {
		return l.loadFromEmbed(logger)
	}
	return l.loadFromFiles(logger)
}

// loadFromFiles loads skills from filesystem directories.
func (l *SkillsLoader) loadFromFiles(logger *agentic.Logger) []*Skill {
	var skills []*Skill
	for _, dir := range l.fileDirs {
		skills = append(skills, l.loadSkillsFromDir(dir, logger)...)
	}
	return skills
}

func (l *SkillsLoader) loadSkillsFromDir(dir string, logger *agentic.Logger) []*Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logWarnf(logger, "Failed to read skill directory %s: %v", dir, err)
		return nil
	}

	var skills []*Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(dir, entry.Name())
		if skill := loadSkillOrLog(skillDir, logger); skill != nil {
			skills = append(skills, skill)
		}
	}
	return skills
}

func loadSkillOrLog(skillDir string, logger *agentic.Logger) *Skill {
	skill, err := LoadSkill(skillDir)
	if err != nil {
		logWarnf(logger, "Failed to load skill from %s: %v", skillDir, err)
		return nil
	}
	logInfof(logger, "Loaded skill: %s", skill.Name)
	return skill
}

func logWarnf(logger *agentic.Logger, format string, args ...interface{}) {
	if logger != nil {
		logger.Log(agentic.Warn, format, args...)
	}
}

func logInfof(logger *agentic.Logger, format string, args ...interface{}) {
	if logger != nil {
		logger.Log(agentic.Info, format, args...)
	}
}

type pathSkill struct {
	path  string
	skill *Skill
}

// loadFromEmbed loads skills from an embedded filesystem, building a hierarchy
// of skills and sub-skills based on directory structure.
func (l *SkillsLoader) loadFromEmbed(logger *agentic.Logger) []*Skill {
	allSkills := l.walkEmbeddedSkills(logger)
	return buildSkillHierarchy(allSkills)
}

func (l *SkillsLoader) walkEmbeddedSkills(logger *agentic.Logger) []pathSkill {
	var allSkills []pathSkill
	err := fs.WalkDir(l.embeddedFS, l.embeddedBasePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if skill := l.tryLoadEmbeddedSkill(path, logger); skill != nil {
			allSkills = append(allSkills, pathSkill{path: path, skill: skill})
		}
		return nil
	})
	if err != nil {
		logWarnf(logger, "Error walking embedded FS: %v", err)
	}
	return allSkills
}

func (l *SkillsLoader) tryLoadEmbeddedSkill(path string, logger *agentic.Logger) *Skill {
	content, err := fs.ReadFile(l.embeddedFS, filepath.Join(path, "SKILL.md"))
	if err != nil {
		return nil
	}
	skill, err := parseSkillMD(string(content), path)
	if err != nil {
		logWarnf(logger, "Failed to parse embedded skill %s: %v", path, err)
		return nil
	}
	if !skillNameMatchesDir(skill.Name, path) {
		logWarnf(logger, "Skill name %q does not match directory name %q in embedded skill %s", skill.Name, filepath.Base(path), path)
		return nil
	}
	logInfof(logger, "Loaded embedded skill: %s", skill.Name)
	return skill
}

func skillNameMatchesDir(name, path string) bool {
	return name == filepath.Base(path)
}

func buildSkillHierarchy(allSkills []pathSkill) []*Skill {
	pathMap := make(map[string]*Skill, len(allSkills))
	for _, ps := range allSkills {
		pathMap[ps.path] = ps.skill
	}

	var topLevel []*Skill
	for _, ps := range allSkills {
		if parent, ok := pathMap[filepath.Dir(ps.path)]; ok {
			parent.SubSkills = append(parent.SubSkills, ps.skill)
		} else {
			topLevel = append(topLevel, ps.skill)
		}
	}
	return topLevel
}

// LoadSkill loads a skill from a directory containing SKILL.md and recursively
// loads any sub-skills from immediate subdirectories that also contain SKILL.md.
func LoadSkill(skillDir string) (*Skill, error) {
	skillDir = filepath.Clean(skillDir)
	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	content, err := os.ReadFile(skillMDPath)
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	skill, err := parseSkillMD(string(content), skillDir)
	if err != nil {
		return nil, fmt.Errorf("parse SKILL.md: %w", err)
	}

	// Validate skill name matches directory name
	dirName := filepath.Base(skillDir)
	if skill.Name != dirName {
		return nil, fmt.Errorf("skill name %q does not match directory name %q", skill.Name, dirName)
	}

	skill.SubSkills = loadSubSkills(skillDir)
	return skill, nil
}

func loadSubSkills(skillDir string) []*Skill {
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		return nil
	}

	var subSkills []*Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subSkillDir := filepath.Join(skillDir, entry.Name())
		if subSkill, ok := tryLoadSubSkill(subSkillDir); ok {
			subSkills = append(subSkills, subSkill)
		}
	}
	return subSkills
}

func tryLoadSubSkill(subSkillDir string) (*Skill, bool) {
	subSkillMDPath := filepath.Join(subSkillDir, "SKILL.md")
	if _, err := os.Stat(subSkillMDPath); err != nil {
		return nil, false
	}
	subSkill, err := LoadSkill(subSkillDir)
	if err != nil {
		return nil, false
	}
	return subSkill, true
}

// LoadSkillFromFS loads a single skill from an embedded FS at the given path.
// The path should point to a directory containing SKILL.md.
func LoadSkillFromFS(fsys fs.FS, path string) (*Skill, error) {
	skillMDPath := filepath.Join(path, "SKILL.md")
	content, err := fs.ReadFile(fsys, skillMDPath)
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	skill, err := parseSkillMD(string(content), path)
	if err != nil {
		return nil, fmt.Errorf("parse SKILL.md: %w", err)
	}

	// Validate skill name matches directory name
	dirName := filepath.Base(path)
	if skill.Name != dirName {
		return nil, fmt.Errorf("skill name %q does not match directory name %q", skill.Name, dirName)
	}

	return skill, nil
}
