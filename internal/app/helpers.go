// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	commands "github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/docs"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/skills"
)

// DocsProvider wraps the docs package to implement core.DocsProvider.
type DocsProvider struct{}

func (d *DocsProvider) List() ([]core.DocInfo, error) {
	docList, err := docs.List()
	if err != nil {
		return nil, err
	}
	result := make([]core.DocInfo, len(docList))
	for i, di := range docList {
		result[i] = core.DocInfo{
			Name:        di.Name,
			File:        di.File,
			Path:        di.Path,
			Description: di.Description,
		}
	}
	return result, nil
}

func (d *DocsProvider) Get(name string) (string, error) {
	return docs.Get(name)
}

func (d *DocsProvider) FindDocFile(query string) (core.DocInfo, error) {
	di, err := docs.FindDocFile(query)
	if err != nil {
		return core.DocInfo{}, err
	}
	return core.DocInfo{
		Name:        di.Name,
		File:        di.File,
		Path:        di.Path,
		Description: di.Description,
	}, nil
}

// ReloadHandler wraps subsystems to implement core.ReloadHandler for the /reload command.
type ReloadHandler struct {
	subs *subsystems
}

func (h *ReloadHandler) ReloadSkills() (int, error) {
	if h.subs == nil {
		return 0, fmt.Errorf("subsystems not initialized")
	}
	// Rebuild skill dirs (default dirs + configured dirs)
	skillDirs := append(config.DefaultSkillDirs(h.subs.projectDir), h.subs.cfg.Skills.Dirs...)
	h.subs.skillRegistry = skills.NewSkillRegistry(skillDirs)
	h.subs.skillRegistry.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	h.subs.skillRegistry.SetTrustChecker(newSkillTrustChecker(h.subs.trustMgr))
	if err := h.subs.skillRegistry.LoadAll(); err != nil {
		return 0, fmt.Errorf("reload skills: %w", err)
	}
	// Re-register skill shortcut commands
	commands.RegisterSkillShortcuts(h.subs.registry, h.subs.skillRegistry)
	count := len(h.subs.skillRegistry.List())
	return count, nil
}

func (h *ReloadHandler) ReloadContext() (int, error) {
	if h.subs == nil {
		return 0, fmt.Errorf("subsystems not initialized")
	}
	h.subs.contextFiles = internal.LoadProjectContextFiles(h.subs.projectDir, h.subs.cfg.ConfigDir)
	return len(h.subs.contextFiles), nil
}

func (h *ReloadHandler) ReloadPlugins() error {
	// Plugin reload is handled by the /reload command invoking PluginLoader.
	// The current PluginLoader doesn't support in-place reload, so this is a
	// no-op for now. Future work: stop old JSBridge instances and reload.
	if h.subs == nil {
		return fmt.Errorf("subsystems not initialized")
	}
	return nil
}

// parseYesNo interprets a short yes/no answer typed on the main input line
// for review confirmations (delete comment, submit review). It accepts y/yes
// (true) and n/no (false); empty or unrecognised input is treated as "no" so
// that cancel/escape cannot accidentally confirm a destructive action.
func parseYesNo(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	switch s[0] {
	case 'y':
		return true
	default:
		return false
	}
}
