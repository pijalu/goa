// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package memory provides persistent memory file storage for Goa agents.
// Memory files are plain Markdown stored in .goa/memory/ with optional YAML
// frontmatter for metadata.
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MemoryFileInfo holds metadata about a memory file.
type MemoryFileInfo struct {
	Name    string    `json:"name"`
	Preview string    `json:"preview"`
	Mtime   time.Time `json:"mtime"`
}

// MemoryStore manages persistent memory files (.goa/memory/*.md).
// Project directory takes precedence over global directory for reads.
type MemoryStore struct {
	projectDir   string
	globalDir    string
	consolidated string
}

// NewMemoryStore creates a memory store rooted at the given directories.
func NewMemoryStore(projectDir, globalDir string) *MemoryStore {
	return &MemoryStore{
		projectDir:   filepath.Join(projectDir, ".goa", "memory"),
		globalDir:    filepath.Join(globalDir, "memory"),
		consolidated: filepath.Join(projectDir, ".goa", "memory.consolidated", "consolidated.md"),
	}
}

// SetConsolidatedPath overrides the default consolidated memory path.
func (s *MemoryStore) SetConsolidatedPath(path string) {
	s.consolidated = path
}

// ReadConsolidated returns the consolidated memory content if it exists.
func (s *MemoryStore) ReadConsolidated() (string, error) {
	if s.consolidated == "" {
		return "", fmt.Errorf("consolidated memory path not set")
	}
	data, err := os.ReadFile(s.consolidated)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// HasConsolidated returns true when a consolidated memory file exists.
func (s *MemoryStore) HasConsolidated() bool {
	if s.consolidated == "" {
		return false
	}
	_, err := os.Stat(s.consolidated)
	return err == nil
}

// ConsolidatedPath returns the configured consolidated memory path.
func (s *MemoryStore) ConsolidatedPath() string {
	return s.consolidated
}

// List returns all memory files with name, preview, and mtime.
func (s *MemoryStore) List() ([]MemoryFileInfo, error) {
	seen := make(map[string]bool)
	var files []MemoryFileInfo

	// Scan project dir first
	s.scanDir(s.projectDir, false, seen, &files)
	// Scan global dir (skip names already in project)
	s.scanDir(s.globalDir, true, seen, &files)

	sort.Slice(files, func(i, j int) bool {
		return files[i].Mtime.After(files[j].Mtime)
	})

	return files, nil
}

func (s *MemoryStore) scanDir(dir string, isGlobal bool, seen map[string]bool, files *[]MemoryFileInfo) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if seen[name] {
			continue
		}
		seen[name] = true
		info, _ := e.Info()
		displayName := name
		if isGlobal {
			displayName += " (global)"
		}
		preview := readPreview(filepath.Join(dir, e.Name()))
		*files = append(*files, MemoryFileInfo{
			Name:    displayName,
			Preview: preview,
			Mtime:   info.ModTime(),
		})
	}
}

// Read returns the full content of a memory file. Checks project dir first,
// falls back to global dir.
// memoryNamePattern validates memory file names.
var memoryNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// validateName checks that a memory name is safe for filesystem use.
func validateName(name string) error {
	if !memoryNamePattern.MatchString(name) {
		return fmt.Errorf("invalid memory name %q: use lowercase alphanumeric and hyphens only", name)
	}
	return nil
}

func (s *MemoryStore) Read(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}

	// Check project dir first
	projectPath := filepath.Join(s.projectDir, name+".md")
	if data, err := os.ReadFile(projectPath); err == nil {
		return string(data), nil
	}

	// Fall back to global dir
	globalPath := filepath.Join(s.globalDir, name+".md")
	if data, err := os.ReadFile(globalPath); err == nil {
		return string(data), nil
	}

	return "", fmt.Errorf("memory file %q not found", name)
}

// Write creates or overwrites a memory file in the project directory.
func (s *MemoryStore) Write(name, content string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(s.projectDir, 0755); err != nil {
		return fmt.Errorf("create memory dir: %w", err)
	}
	path := filepath.Join(s.projectDir, name+".md")
	frontmatter := fmt.Sprintf("---\ncreated: %s\nupdated: %s\n---\n\n",
		time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	return os.WriteFile(path, []byte(frontmatter+content), 0644)
}

// Append adds content under a section heading, or at the end if no section given.
func (s *MemoryStore) Append(name, section, content string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(s.projectDir, 0755); err != nil {
		return fmt.Errorf("create memory dir: %w", err)
	}
	path := filepath.Join(s.projectDir, name+".md")

	existing, err := os.ReadFile(path)
	var text string
	if err != nil {
		text = fmt.Sprintf("---\ncreated: %s\nupdated: %s\n---\n\n",
			time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	} else {
		text = string(existing)
	}

	if section != "" {
		text = appendToSection(text, section, content)
	} else {
		text += "\n" + content + "\n"
	}

	return os.WriteFile(path, []byte(text), 0644)
}

// Delete removes a memory file from the project directory.
func (s *MemoryStore) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	path := filepath.Join(s.projectDir, name+".md")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("memory file %q not found", name)
		}
		return fmt.Errorf("delete memory: %w", err)
	}
	return nil
}

// readPreview returns the first non-empty line (excluding frontmatter) from a file.
func readPreview(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	inFrontmatter := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			inFrontmatter = !inFrontmatter
			continue
		}
		if inFrontmatter {
			continue
		}
		if trimmed != "" {
			if len(trimmed) > 80 {
				trimmed = trimmed[:80] + "..."
			}
			return trimmed
		}
	}
	return ""
}

// appendToSection appends content under a Markdown H2 heading.
func appendToSection(text, section, content string) string {
	header := "## " + section
	if !strings.Contains(text, header) {
		return text + "\n" + header + "\n\n" + content + "\n"
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			// Find end of section (next ## or EOF)
			end := len(lines)
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(strings.TrimSpace(lines[j]), "##") {
					end = j
					break
				}
			}
			newLines := make([]string, 0, len(lines)+2)
			newLines = append(newLines, lines[:end]...)
			newLines = append(newLines, "", content)
			newLines = append(newLines, lines[end:]...)
			return strings.Join(newLines, "\n")
		}
	}
	return text
}
