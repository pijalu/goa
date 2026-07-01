// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package prompts

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Registry resolves prompts from user directories or embedded defaults.
// Prompt names use dot notation: "companion.system", "pair.planner", etc.
// Files are stored as {name}.md in the directory structure.
type Registry struct {
	embedded fs.FS
	userDirs []string
}

// NewRegistry creates a registry that checks userDirs before falling back to embedded defaults.
func NewRegistry(embedded fs.FS, userDirs ...string) *Registry {
	return &Registry{embedded: embedded, userDirs: userDirs}
}

// Load resolves a prompt name to its text.
// Resolution order:
//  1. User project dir: .goa/prompts/{name}.md
//  2. User home dir:    ~/.goa/prompts/{name}.md
//  3. Embedded default: prompts/{name}.md
func (r *Registry) Load(name string) (string, error) {
	for _, dir := range r.userDirs {
		path := filepath.Join(dir, name+".md")
		if data, err := os.ReadFile(path); err == nil {
			return string(data), nil
		}
	}
	if r.embedded != nil {
		path := name + ".md"
		if data, err := fs.ReadFile(r.embedded, path); err == nil {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("prompt %q not found", name)
}

// MustLoad is like Load but returns an error if the prompt cannot be resolved.
// It is kept for callers that want a single helper for required prompts, but
// they should surface the error to the user instead of crashing.
func (r *Registry) MustLoad(name string) (string, error) {
	text, err := r.Load(name)
	if err != nil {
		return "", fmt.Errorf("required prompt %q not found: %w", name, err)
	}
	return text, nil
}

// Render loads a prompt and executes text/template on it.
func (r *Registry) Render(name string, data interface{}) (string, error) {
	text, err := r.Load(name)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(name).Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse prompt %q: %w", name, err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render prompt %q: %w", name, err)
	}
	return buf.String(), nil
}

// List returns all available prompt names from embedded defaults.
func (r *Registry) List() ([]string, error) {
	if r.embedded == nil {
		return nil, nil
	}
	var names []string
	err := fs.WalkDir(r.embedded, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			names = append(names, strings.TrimSuffix(path, ".md"))
		}
		return nil
	})
	return names, err
}

// Source returns where a prompt was loaded from: "user", "embedded", or "missing".
func (r *Registry) Source(name string) string {
	for _, dir := range r.userDirs {
		path := filepath.Join(dir, name+".md")
		if _, err := os.Stat(path); err == nil {
			return "user"
		}
	}
	if r.embedded != nil {
		if _, err := fs.Stat(r.embedded, name+".md"); err == nil {
			return "embedded"
		}
	}
	return "missing"
}
