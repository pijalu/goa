// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/pijalu/goa/core/goal"
)

// stallProbeTimeout bounds the git half of the fingerprint so a slow or
// wedged repository cannot stall a goal continuation turn.
const stallProbeTimeout = 5 * time.Second

// newGoalStallProbe builds the GoalDriver stall-watchdog probe. The
// fingerprint has two parts, hashed together:
//
//   - the goal's managed todo list (ID + status), which changes whenever the
//     model tracks work; and
//   - `git status --porcelain` of the workspace, which changes whenever the
//     work touches files.
//
// When projectDir is not a git repository (or git errors/times out) the git
// part degrades to a constant marker, leaving a todos-only fingerprint —
// documented in docs/GOALS.md.
func newGoalStallProbe(projectDir string, mode *goal.GoalMode) func() string {
	return func() string {
		h := sha256.New()
		h.Write([]byte(goalTodoFingerprint(mode)))
		h.Write([]byte{0})
		h.Write([]byte(gitWorkspaceFingerprint(projectDir)))
		return hex.EncodeToString(h.Sum(nil))
	}
}

// goalTodoFingerprint renders the active goal's todo list as a stable string
// (sorted by ID so reordering does not count as progress — only status or
// membership changes do). An empty snapshot fingerprints as "none".
func goalTodoFingerprint(mode *goal.GoalMode) string {
	if mode == nil {
		return "none"
	}
	snap := mode.GetActiveGoal()
	if snap == nil || len(snap.Todos) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(snap.Todos))
	for _, t := range snap.Todos {
		parts = append(parts, t.ID+"="+t.Status)
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

// gitWorkspaceFingerprint returns `git status --porcelain` for dir, or the
// constant "nogit" when dir is not a git work tree or git fails.
func gitWorkspaceFingerprint(dir string) string {
	if dir == "" {
		return "nogit"
	}
	ctx, cancel := context.WithTimeout(context.Background(), stallProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "nogit"
	}
	return string(out)
}
