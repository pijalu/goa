// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package docs provides access to Goa's embedded documentation files.
// All docs/*.md are embedded at build time and accessible at runtime
// via the /docs command and the "docs:" namespace in DocEngine.
package docs

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed *.md
var embeddedDocs embed.FS

// DocInfo describes a single documentation file.
type DocInfo struct {
	Name        string // e.g., "ARCHITECTURE"
	File        string // e.g., "ARCHITECTURE.md"
	Path        string // e.g., "docs/ARCHITECTURE.md"
	Description string // one-line summary
}

// List returns all available documentation files sorted by name.
func List() ([]DocInfo, error) {
	entries, err := fs.ReadDir(embeddedDocs, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded docs: %w", err)
	}

	var docs []DocInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".md")
		docs = append(docs, DocInfo{
			Name:        name,
			File:        entry.Name(),
			Path:        "docs/" + entry.Name(),
			Description: shortDescription(name),
		})
	}

	sort.Slice(docs, func(i, j int) bool {
		return docs[i].Name < docs[j].Name
	})

	return docs, nil
}

// Get returns the content of a documentation file by name (without .md).
// Returns an error if the file is not found.
func Get(name string) (string, error) {
	path := name + ".md"
	data, err := embeddedDocs.ReadFile(path)
	if err != nil {
		// Try case-insensitive match for user convenience
		entries, _ := fs.ReadDir(embeddedDocs, ".")
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), path) {
				data, err = embeddedDocs.ReadFile(entry.Name())
				if err == nil {
					return string(data), nil
				}
			}
		}
		return "", fmt.Errorf("documentation not found: %s (try /docs to list available)", name)
	}
	return string(data), nil
}

// ReadDoc reads a documentation file by full filename (e.g., "ARCHITECTURE.md").
// This is the raw embed.FS access for programmatic use.
func ReadDoc(filename string) (string, error) {
	data, err := embeddedDocs.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("doc file not found: %s", filename)
	}
	return string(data), nil
}

// FS returns the embedded filesystem for use by other packages.
func FS() embed.FS {
	return embeddedDocs
}

// shortDescription returns a one-line summary for known documentation files.
func shortDescription(name string) string {
	if desc, ok := docDescriptions[name]; ok {
		return desc
	}
	desc := strings.ReplaceAll(name, "-", " ")
	desc = strings.ReplaceAll(desc, "_", " ")
	return strings.ToLower(desc) + " documentation"
}

var docDescriptions = map[string]string{
	"AGENTIC-SDK":            "How Goa wraps the agentic SDK — integration points, observers, events",
	"ARCHITECTURE":           "Full system architecture — component design, data flow, subsystem boundaries",
	"COMMANDS":               "Complete command system reference — all built-in commands and usage",
	"CONFIGURATION":          "Configuration cascade, all settings, environment overrides, schema",
	"DEVELOPMENT":            "Development guide — building, testing, debugging, contributing",
	"FIX-PLAN-2026-07-04":    "Execution plan for the 2026-07-04 bug-fix and review session",
	"GOALS":                  "Autonomous goals — lifecycle, budgets, queues, and control commands",
	"IMPLEMENTATION-ROADMAP": "Milestone roadmap — M10–M30 future development plan",
	"INPUT-LINE-GAP-PLAN":    "Gap analysis and plan for input line improvements",
	"PI-GAP-ANALYSIS":        "Analysis of gaps against reference implementation",
	"ORCHESTRATION-DESIGN":   "Design document for the orchestration track",
	"PLUGINS":                "JS extensions — create custom tools, commands, and UI elements",
	"PROFILES":               "Agent profiles — built-in profiles, custom profiles, extends inheritance",
	"PROVIDERS":              "Provider configuration — variants, custom providers, URL templates",
	"REVIEW-WORKFLOW":        "Multi-agent review workflows — Pair and Reviewer modes",
	"SETUP":                  "Installation and setup guide — first-run wizard, provider configuration",
	"SKILLS":                 "Skills system — built-in skills, custom skills, inline vs sub-agent",
	"TOOLS":                  "Tool system reference — all native tools, schemas, examples",
	"TUI":                    "TUI layout and usage — keybindings, panes, transparency features",
	"WORKFLOWS":              "Workflows — pre-defined agent pipelines and task automation",
}

// LookupDoc tries to find a doc file matching the given query.
// It supports exact name match and case-insensitive prefix match.
func FindDocFile(query string) (DocInfo, error) {
	docs, err := List()
	if err != nil {
		return DocInfo{}, err
	}

	q := strings.ToUpper(strings.ReplaceAll(query, " ", "_"))
	q = strings.ReplaceAll(q, "-", "_")

	// Exact match first
	for _, d := range docs {
		if strings.EqualFold(d.Name, q) {
			return d, nil
		}
	}

	// Prefix match
	for _, d := range docs {
		if strings.HasPrefix(strings.ToUpper(d.Name), q) {
			return d, nil
		}
	}

	return DocInfo{}, fmt.Errorf("no documentation found for %q", query)
}

// ListCategory returns docs matching a prefix. E.g., all agentic docs:
// ListCategory("AGENTIC") returns all docs starting with "AGENTIC".
func ListCategory(prefix string) ([]DocInfo, error) {
	docs, err := List()
	if err != nil {
		return nil, err
	}

	prefix = strings.ToUpper(prefix)
	var filtered []DocInfo
	for _, d := range docs {
		if strings.HasPrefix(strings.ToUpper(d.Name), prefix) {
			filtered = append(filtered, d)
		}
	}

	if len(filtered) == 0 {
		// Fall back to containing the prefix somewhere
		for _, d := range docs {
			if strings.Contains(strings.ToUpper(d.Name), prefix) {
				filtered = append(filtered, d)
			}
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Name < filtered[j].Name
	})

	return filtered, nil
}

// ParseGoaURL extracts a documentation name from a goa:// URL.
// Supported forms: goa://NAME, goa://NAME.md, goa://docs/NAME, goa://docs/NAME.md.
func ParseGoaURL(path string) (string, bool) {
	const prefix = "goa://"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(path, prefix)
	name = strings.TrimPrefix(name, "docs/")
	name = strings.TrimSuffix(name, ".md")
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	return name, true
}

// FilePath returns the relative path for a doc name.
func FilePath(docName string) string {
	return filepath.Join("docs", docName+".md")
}
