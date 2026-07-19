// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitCmd runs git in dir and fails the test on error.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

// initGitRepo creates a repo in dir with one commit on branch "main",
// independent of the host's init.defaultBranch.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "config", "user.email", "goa-test@example.com")
	gitCmd(t, dir, "config", "user.name", "goa-test")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "seed.txt")
	gitCmd(t, dir, "commit", "-m", "seed")
}

func TestGatherGitInfo_Repo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	info := GatherGitInfo(dir)
	if info.Branch != "main" {
		t.Errorf("expected branch %q, got %q", "main", info.Branch)
	}
	if info.Dirty {
		t.Error("expected clean tree after commit")
	}
	if info.Conflicts {
		t.Error("expected no conflicts")
	}
}

func TestGatherGitInfo_DirtyAndBranchSwitch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// New untracked file marks the tree dirty.
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := GatherGitInfo(dir)
	if !info.Dirty {
		t.Error("expected dirty tree with untracked file")
	}

	// Branch switch must be reflected — this is the stale-footer scenario.
	gitCmd(t, dir, "checkout", "-b", "feature/x")
	info = GatherGitInfo(dir)
	if info.Branch != "feature/x" {
		t.Errorf("expected branch %q after checkout, got %q", "feature/x", info.Branch)
	}
}

func TestGatherGitInfo_NotARepo(t *testing.T) {
	info := GatherGitInfo(t.TempDir())
	if info != (GitInfo{}) {
		t.Errorf("expected zero GitInfo for non-repo, got %+v", info)
	}
}

func TestGatherGitInfo_EmptyDir(t *testing.T) {
	if info := GatherGitInfo(""); info != (GitInfo{}) {
		t.Errorf("expected zero GitInfo for empty dir, got %+v", info)
	}
}

func TestFooter_SetGitInfo(t *testing.T) {
	f := NewFooter()
	f.SetGitInfo(GitInfo{Branch: "main", Dirty: true, Conflicts: true})
	d := f.Data()
	if d.GitBranch != "main" || !d.GitDirty || !d.GitConflicts {
		t.Errorf("SetGitInfo not reflected: %+v", d)
	}

	// Zero value clears previous state (e.g. directory stopped being a repo).
	f.SetGitInfo(GitInfo{})
	d = f.Data()
	if d.GitBranch != "" || d.GitDirty || d.GitConflicts {
		t.Errorf("expected cleared git state, got %+v", d)
	}
}

func TestFooter_RefreshGit_UsesWorkdir(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	f := NewFooter()
	f.SetData(FooterData{Workdir: dir})
	f.RefreshGit()
	if got := f.Data().GitBranch; got != "main" {
		t.Errorf("expected branch %q, got %q", "main", got)
	}
}

// TestFooter_SetData_PreservesWorkdir guards the refresh loop: partial
// SetData updates (steering, stats, orchestration) must not wipe the workdir,
// or both the footer path display and RefreshGit silently break.
func TestFooter_SetData_PreservesWorkdir(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{Workdir: "/tmp/goa-workdir"})
	f.SetData(FooterData{Stats: "↑1k ↓2k"})
	if got := f.Data().Workdir; got != "/tmp/goa-workdir" {
		t.Errorf("expected Workdir preserved, got %q", got)
	}
}

// TestFooter_SetData_PreservesGitConflicts: git fields survive partial updates
// as a trio; losing Conflicts would briefly flip the footer to green mid-merge.
func TestFooter_SetData_PreservesGitConflicts(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{GitBranch: "main", GitDirty: true, GitConflicts: true})
	f.SetData(FooterData{Stats: "↑1k ↓2k"})
	d := f.Data()
	if d.GitBranch != "main" || !d.GitDirty || !d.GitConflicts {
		t.Errorf("expected git trio preserved, got %+v", d)
	}
}
