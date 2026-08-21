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
// SetModelBusy/SetCompanionBusy/SetGitInfo/RefreshGit and Render all run on the
// loop (serialized by the commandLoop), so no mutex is required.
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

// SetGoalStatus explicitly sets or clears the goal status, goal count and
// pending-todo count, bypassing SetData's preservation logic — the goal
// equivalent of SetMinorMode. updateGoalFooter is the sole caller; pass an
// empty status to clear the ◈ goal-count marker and ⬩ todo markers when no
// goal exists.
func (f *Footer) SetGoalStatus(status string, goalCount, pendingTodos int) {
	f.data.GoalStatus = status
	f.data.GoalCount = goalCount
	f.data.GoalPendingTodos = pendingTodos
}

// SetTeam explicitly sets or clears the team badge (name + drift marker),
// bypassing SetData's preservation logic — the team equivalent of
// SetGoalStatus. Pass an empty name to clear the badge.
func (f *Footer) SetTeam(name string, drifted bool) {
	f.data.Team = name
	f.data.TeamDrifted = drifted
}

// SetAgentStats explicitly sets or clears the ACTIVE multi-agent tab's stat
// line (T5), bypassing SetData's preservation logic — the tab equivalent of
// SetGoalStatus. updateAgentCtxFooter is the sole caller: pass a non-empty
// line when a delegation tab is active, "" when the main tab is (clearing the
// extra footer line). Single-writer + preservation keeps the line stable
// across routine stats rebuilds instead of flapping between them.
func (f *Footer) SetAgentStats(line string) { f.data.AgentTabStats = line }

// SetModelBusy sets the main model busy indicator directly.
func (f *Footer) SetModelBusy(busy bool) { f.data.ModelBusy = busy }

// SetCompanionBusy sets the companion model busy indicator directly.
func (f *Footer) SetCompanionBusy(busy bool) { f.data.CompanionBusy = busy }

// GitInfo carries git status for the footer, gathered off the commandLoop.
type GitInfo struct {
	Branch    string // current branch (empty if not a git repo)
	Dirty     bool   // true if working tree has changes
	Conflicts bool   // true if merge conflicts exist
}

// GatherGitInfo collects branch, dirty and conflict state for dir. It is a
// pure function with no Footer state and spawns subprocesses, so callers may
// run it from any goroutine off the commandLoop and apply the result on the
// loop via SetGitInfo.
func GatherGitInfo(dir string) GitInfo {
	var info GitInfo
	if dir == "" {
		return info
	}
	// Get branch name
	branch, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return info
	}
	info.Branch = strings.TrimSpace(string(branch))

	// Check for dirty status and merge conflicts
	status, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err == nil && len(status) > 0 {
		info.Dirty = true
		// Check for merge conflicts (lines starting with "UU")
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "UU") {
				info.Conflicts = true
				break
			}
		}
	}
	return info
}

// SetGitInfo updates the git display fields. Must run on the commandLoop.
func (f *Footer) SetGitInfo(info GitInfo) {
	f.data.GitBranch = info.Branch
	f.data.GitDirty = info.Dirty
	f.data.GitConflicts = info.Conflicts
}

// RefreshGit updates the git branch, dirty status, and conflict count.
// It blocks on subprocesses; prefer GatherGitInfo + SetGitInfo when called
// from a context where blocking the commandLoop matters.
func (f *Footer) RefreshGit() {
	f.SetGitInfo(GatherGitInfo(f.data.Workdir))
}

// HandleInput is a no-op.
func (f *Footer) HandleInput(data string) {}

// Invalidate is a no-op.
func (f *Footer) Invalidate() {}
