// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os/exec"
	"strings"
)

// Footer displays a two-line status bar at the bottom of the TUI.
//
// Concurrency: the commandLoop is the sole owner of data. SetData/SetMinorMode/
// SetModelBusy/SetCompanionBusy/RefreshGit and Render all run on the loop
// (serialized by the commandLoop), so no mutex is required.
type Footer struct {
	data FooterData
}

// NewFooter creates a Footer.
func NewFooter() *Footer { return &Footer{} }

// Data returns the current footer data.
func (f *Footer) Data() FooterData { return f.data }

// SetData updates displayed data. Preserves git info and minor mode across updates.
func (f *Footer) SetData(data FooterData) {
	f.data = preserveFooterData(f.data, data)
}

// SetMinorMode explicitly sets or clears the minor mode label, bypassing
// SetData's preservation logic. Use this when the user toggles a minor mode
// on or off so the footer reflects the change immediately.
func (f *Footer) SetMinorMode(mode string) { f.data.MinorMode = mode }

// SetModelBusy sets the main model busy indicator directly.
func (f *Footer) SetModelBusy(busy bool) { f.data.ModelBusy = busy }

// SetCompanionBusy sets the companion model busy indicator directly.
func (f *Footer) SetCompanionBusy(busy bool) { f.data.CompanionBusy = busy }

// RefreshGit updates the git branch, dirty status, and conflict count.
func (f *Footer) RefreshGit() {
	f.data.GitBranch = ""
	f.data.GitDirty = false
	f.data.GitConflicts = false
	dir := f.data.Workdir
	if dir == "" {
		return
	}
	// Get branch name
	branch, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return
	}
	f.data.GitBranch = strings.TrimSpace(string(branch))

	// Check for dirty status and merge conflicts
	status, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err == nil && len(status) > 0 {
		f.data.GitDirty = true
		// Check for merge conflicts (lines starting with "UU")
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "UU") {
				f.data.GitConflicts = true
				break
			}
		}
	}
}

// HandleInput is a no-op.
func (f *Footer) HandleInput(data string) {}

// Invalidate is a no-op.
func (f *Footer) Invalidate() {}
