// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/tui"
)

// gitCmd runs git in dir and fails the test on error.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// initGitRepo creates a repo with one commit on branch "main", independent of
// the host's init.defaultBranch.
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

// TestRefreshFooterGitOnce_PicksUpBranchSwitch is the regression test for the
// stale status bar: the footer used to capture the branch once at startup and
// never update. A refresh pass must surface a branch switch made outside goa.
func TestRefreshFooterGitOnce_PicksUpBranchSwitch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	a := &App{}
	a.subs = &subsystems{projectDir: dir}
	footer := tui.NewFooter()
	footer.SetData(tui.FooterData{Workdir: dir})

	// Initial state, as captured at startup.
	a.refreshFooterGitOnce(footer, dir)
	if got := footer.Data().GitBranch; got != "main" {
		t.Fatalf("expected initial branch %q, got %q", "main", got)
	}

	// User switches branch in another terminal — the next refresh must see it.
	gitCmd(t, dir, "checkout", "-b", "feature/other")
	a.refreshFooterGitOnce(footer, dir)
	if got := footer.Data().GitBranch; got != "feature/other" {
		t.Errorf("expected refreshed branch %q, got %q", "feature/other", got)
	}

	// Dirty state appears after an untracked write.
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.refreshFooterGitOnce(footer, dir)
	if !footer.Data().GitDirty {
		t.Error("expected dirty flag after untracked write")
	}
}

// TestRunGitRefreshLoop_Lifecycle: the loop refreshes on ticks and exits on
// done without leaking the goroutine. Reads happen only after wg.Wait, so no
// concurrent access to the footer.
func TestRunGitRefreshLoop_Lifecycle(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	a := &App{}
	footer := tui.NewFooter()
	footer.SetData(tui.FooterData{Workdir: dir})
	a.subs = &subsystems{projectDir: dir, footer: footer}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.runGitRefreshLoop(done, 15*time.Millisecond)
	}()

	// Several ticks at 15ms; each gather spawns two git subprocesses.
	time.Sleep(250 * time.Millisecond)
	close(done)

	waitCh := make(chan struct{})
	go func() { wg.Wait(); close(waitCh) }()
	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("runGitRefreshLoop did not exit after done was closed")
	}

	if got := footer.Data().GitBranch; got != "main" {
		t.Errorf("expected loop to have refreshed branch to %q, got %q", "main", got)
	}
}

// TestRunGitRefreshLoop_NoFooter: headless runs (no footer) must exit
// immediately instead of spinning.
func TestRunGitRefreshLoop_NoFooter(t *testing.T) {
	a := &App{}
	a.subs = &subsystems{projectDir: t.TempDir()}

	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		a.runGitRefreshLoop(done, time.Millisecond)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("loop without footer should return immediately")
	}
	close(done)
}
