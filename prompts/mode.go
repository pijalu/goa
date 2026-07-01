// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package prompts

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pijalu/goa/internal/embeddoc"
	"github.com/pijalu/goa/internal/perms"
)

// ModeDefinition is a parsed mode definition loaded from
// prompts/mode/<major>/definition.md.
type ModeDefinition struct {
	Major           string
	Name            string
	Description     string
	DefaultAutonomy string
	DefaultSkills   []string
	AllowedTools    []string
	BlockedPaths    []string
	Temperature     float64
	MaxTokens       int
	Body            string
	Guard           perms.GuardConfig
}

// LoadMode resolves a mode definition by major name.
// Resolution order: userDirs, then embedded defaults.
func (r *Registry) LoadMode(major string) (*ModeDefinition, error) {
	name := filepath.Join("mode", major, "definition")
	text, err := r.Load(name)
	if err != nil {
		return nil, fmt.Errorf("mode %q not found: %w", major, err)
	}
	return parseModeDefinition(major, text)
}

// ListModes returns all available mode major names from embedded defaults
// and user directories. User-defined modes override embedded modes but are
// listed only once.
func (r *Registry) ListModes() ([]string, error) {
	seen := make(map[string]bool)
	r.collectUserModes(seen)
	r.collectEmbeddedModes(seen)

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (r *Registry) collectUserModes(seen map[string]bool) {
	for _, dir := range r.userDirs {
		r.collectUserModeDir(dir, seen)
	}
}

func (r *Registry) collectUserModeDir(dir string, seen map[string]bool) {
	root := filepath.Join(dir, "mode")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), "definition.md")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		seen[e.Name()] = true
	}
}

func (r *Registry) collectEmbeddedModes(seen map[string]bool) {
	if r.embedded == nil {
		return
	}
	_ = fs.WalkDir(r.embedded, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		parts := splitModePath(path)
		if len(parts) != 2 || parts[1] != "definition" {
			return nil
		}
		seen[parts[0]] = true
		return nil
	})
}

// splitModePath splits a path like "mode/coder/definition.md" into
// ["coder", "definition"]. It returns nil for other paths.
func splitModePath(path string) []string {
	path = strings.TrimSuffix(path, ".md")
	parts := strings.Split(path, string(filepath.Separator))
	if len(parts) != 3 || parts[0] != "mode" {
		return nil
	}
	return []string{parts[1], parts[2]}
}

func parseModeDefinition(major, text string) (*ModeDefinition, error) {
	doc, err := embeddoc.ParseDocument([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("parse mode %q: %w", major, err)
	}
	m := &ModeDefinition{
		Major:       major,
		Name:        stringValue(doc.Frontmatter["name"], major),
		Description: stringValue(doc.Frontmatter["description"], ""),
		Body:        doc.Body,
	}
	if v, ok := doc.Frontmatter["default_autonomy"]; ok {
		m.DefaultAutonomy = stringValue(v, "")
	}
	m.DefaultSkills = stringSlice(doc.Frontmatter["default_skills"])
	m.AllowedTools = stringSlice(doc.Frontmatter["allowed_tools"])
	m.BlockedPaths = stringSlice(doc.Frontmatter["blocked_paths"])
	if v, ok := doc.Frontmatter["temperature"]; ok {
		m.Temperature = floatValue(v)
	}
	if v, ok := doc.Frontmatter["max_tokens"]; ok {
		m.MaxTokens = intValue(v)
	}
	if v, ok := doc.Frontmatter["guard"]; ok {
		m.Guard = parseGuardConfig(v)
	}
	return m, nil
}

func parseGuardConfig(v any) perms.GuardConfig {
	m, ok := v.(map[string]any)
	if !ok {
		return perms.GuardConfig{}
	}
	rulesRaw, ok := m["rules"].([]any)
	if !ok {
		return perms.GuardConfig{}
	}
	var rules []perms.GuardRule
	for _, raw := range rulesRaw {
		r, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rules = append(rules, perms.GuardRule{
			Tools:   stringSlice(r["tools"]),
			Allow:   stringSlice(r["allow"]),
			Expr:    stringValue(r["expr"], ""),
			Message: stringValue(r["message"], ""),
		})
	}
	return perms.GuardConfig{Rules: rules}
}

func stringValue(v any, fallback string) string {
	if v == nil {
		return fallback
	}
	if s, ok := v.(string); ok {
		return s
	}
	if s, ok := v.(fmt.Stringer); ok {
		return s.String()
	}
	return fmt.Sprintf("%v", v)
}

func floatValue(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		var out float64
		fmt.Sscanf(n, "%f", &out)
		return out
	}
	return 0
}

func intValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		var out int
		fmt.Sscanf(n, "%d", &out)
		return out
	}
	return 0
}

func stringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch vals := v.(type) {
	case []any:
		out := make([]string, 0, len(vals))
		for _, e := range vals {
			out = append(out, stringValue(e, ""))
		}
		return out
	case []string:
		return vals
	case string:
		if strings.TrimSpace(vals) == "" {
			return nil
		}
		return []string{vals}
	}
	return nil
}
