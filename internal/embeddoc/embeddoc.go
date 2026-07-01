// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package embeddoc provides helpers for loading embedded documentation files,
// including plain-text files and markdown files with YAML frontmatter.
package embeddoc

import (
	"bytes"
	"embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Document is a markdown file split into YAML frontmatter and a body.
type Document struct {
	// Frontmatter holds the parsed YAML metadata.
	Frontmatter map[string]any
	// Body is the markdown content after the frontmatter.
	Body string
}

// LoadText reads a plain-text file from an embedded filesystem and returns its
// trimmed content, skipping an optional leading HTML comment (e.g. SPDX
// header). If the file is missing or empty, an empty string is returned.
func LoadText(fs embed.FS, name string) string {
	data, err := fs.ReadFile(name)
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(stripLeadingComment(data)))
}

// LoadDocument reads a markdown file from an embedded filesystem and parses its
// YAML frontmatter. Files without frontmatter return an empty Frontmatter map
// and the full file body.
func LoadDocument(fs embed.FS, name string) (*Document, error) {
	data, err := fs.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", name, err)
	}
	return ParseDocument(data)
}

// ParseDocument splits raw markdown data into frontmatter and body.
func ParseDocument(data []byte) (*Document, error) {
	trimmed := bytes.TrimSpace(stripLeadingComment(data))

	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return &Document{Frontmatter: make(map[string]any), Body: string(trimmed)}, nil
	}

	rest := trimmed[3:]
	endIdx := bytes.Index(rest, []byte("---"))
	if endIdx < 0 {
		return &Document{Frontmatter: make(map[string]any), Body: string(trimmed)}, nil
	}

	fmBytes := bytes.TrimSpace(rest[:endIdx])
	bodyBytes := bytes.TrimSpace(rest[endIdx+3:])

	var fm map[string]any
	if len(fmBytes) > 0 {
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			return nil, fmt.Errorf("parse frontmatter: %w", err)
		}
	}
	if fm == nil {
		fm = make(map[string]any)
	}

	return &Document{Frontmatter: fm, Body: string(bodyBytes)}, nil
}

// stripLeadingComment removes an optional HTML comment block at the start of a
// file. Embedded prompts include an SPDX license header wrapped in <!-- -->,
// which should not be treated as document content.
func stripLeadingComment(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	for bytes.HasPrefix(trimmed, []byte("<!--")) {
		endIdx := bytes.Index(trimmed, []byte("-->"))
		if endIdx < 0 {
			break
		}
		trimmed = bytes.TrimSpace(trimmed[endIdx+3:])
	}
	return trimmed
}
