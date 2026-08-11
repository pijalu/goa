// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package review provides git-based code review support for Goa.
package review

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CommitInfo describes a single git commit for the commit picker.
type CommitInfo struct {
	SHA     string
	Subject string
}

// IsGitRepo reports whether dir is inside a git repository.
func IsGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	cmd.Stderr = nil
	err := cmd.Run()
	return err == nil
}

// HeadSHA returns the current HEAD commit SHA.
func HeadSHA(dir string) (string, error) {
	out, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	return out, nil
}

// ResolveSHA resolves a git ref to its full SHA.
func ResolveSHA(dir, ref string) (string, error) {
	out, err := gitOutput(dir, "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", ref, err)
	}
	return out, nil
}

// HasUncommittedChanges reports whether the working tree has modifications
// relative to HEAD.
func HasUncommittedChanges(dir string) (bool, error) {
	out, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

// DiffArgs returns the git argument list that produces the review diff for
// baseRef. It is the single source of truth shared by Diff (which executes
// it) and DiffCommand (which displays it), so the command shown to the user
// or agent always matches what actually runs.
func DiffArgs(baseRef string) []string {
	if baseRef == "HEAD" {
		// HEAD with no range shows working-tree changes when dirty and
		// nothing when clean. This matches the user's expectation for
		// uncommitted work.
		return []string{"diff", "HEAD"}
	}
	return []string{"diff", baseRef + "..HEAD"}
}

// DiffCommand returns the shell command that produces the review diff for
// baseRef (e.g. "git diff HEAD^1..HEAD"). Review summaries point the agent
// at this command instead of embedding diff content, keeping the submitted
// context small.
func DiffCommand(baseRef string) string {
	return "git " + strings.Join(DiffArgs(baseRef), " ")
}

// Diff returns a unified diff from baseRef to the current state.
// When baseRef is "HEAD" and the working tree is dirty, it returns the
// working-tree diff; otherwise it returns `git diff baseRef..HEAD`.
func Diff(dir, baseRef string) (string, error) {
	out, err := gitOutput(dir, DiffArgs(baseRef)...)
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return out, nil
}

// RefInfo describes a git tag or branch for review-base completion.
type RefInfo struct {
	// Name is the short ref name usable as a diff base (e.g. "v1.2.0", "main").
	Name string
	// Kind is "tag" or "branch".
	Kind string
}

// RecentRefs returns up to n tags and branches, most recent first.
// Recency uses for-each-ref's -creatordate: for an annotated tag it is the
// tag date, for a lightweight tag or branch it is the date of the commit the
// ref points to. Tags and branches are interleaved in one recency ordering so
// the completion surfaces genuinely recent checkpoints regardless of kind.
func RecentRefs(dir string, n int) ([]RefInfo, error) {
	if n <= 0 {
		n = 20
	}
	// NOTE: for-each-ref does not expand %x00 hex escapes (unlike git log),
	// so we emit only %(refname) and derive the short name + kind from the
	// namespace prefix instead of splitting on a NUL delimiter.
	out, err := gitOutput(dir,
		"for-each-ref",
		"--sort=-creatordate",
		"--format=%(refname)",
		"--count", fmt.Sprintf("%d", n),
		"refs/tags", "refs/heads",
	)
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	var refs []RefInfo
	for _, line := range strings.Split(out, "\n") {
		full := strings.TrimSpace(line)
		if full == "" {
			continue
		}
		// Distinguish tag from branch by namespace, not objecttype: a
		// lightweight tag points straight at a commit, exactly like a branch.
		if name, ok := strings.CutPrefix(full, "refs/tags/"); ok {
			refs = append(refs, RefInfo{Name: name, Kind: "tag"})
		} else if name, ok := strings.CutPrefix(full, "refs/heads/"); ok {
			refs = append(refs, RefInfo{Name: name, Kind: "branch"})
		}
	}
	return refs, nil
}

// RecentCommits returns the last n commits with subjects truncated to
// maxWidth runes so they fit in the terminal picker.
//
// git separates `--pretty=format:` records with a newline, so the output is
// `SHA\0subj\0\nSHA\0subj\0\n...`. We therefore parse line by line and split
// each line on the NUL delimiter, then TrimSpace defensively — a previous
// implementation split the whole blob on \x00, which left a leading "\n" on
// every SHA after the first and corrupted the selector overlay.
func RecentCommits(dir string, n, maxWidth int) ([]CommitInfo, error) {
	if n <= 0 {
		n = 10
	}
	format := "%H%x00%s%x00"
	out, err := gitOutput(dir, "log", "--pretty=format:"+format, "-n", fmt.Sprintf("%d", n))
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	var commits []CommitInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\x00")
		// Expect at least SHA and subject; trailing empty field from the
		// closing %x00 is ignored.
		if len(fields) < 2 {
			continue
		}
		sha := strings.TrimSpace(fields[0])
		subject := strings.TrimSpace(fields[1])
		if sha == "" {
			continue
		}
		commits = append(commits, CommitInfo{
			SHA:     sha,
			Subject: truncate(subject, maxWidth),
		})
	}
	return commits, nil
}

// DefaultBase returns the appropriate default base ref for the repository
// state: "HEAD" when the working tree has uncommitted changes, otherwise
// "HEAD^1". If HEAD^1 does not exist (e.g., only one commit), it falls back
// to "HEAD".
func DefaultBase(dir string) (string, error) {
	dirty, err := HasUncommittedChanges(dir)
	if err != nil {
		return "", err
	}
	if dirty {
		return "HEAD", nil
	}
	// Verify HEAD^1 exists; fall back to HEAD for the first commit.
	if _, err := gitOutput(dir, "rev-parse", "HEAD^1"); err == nil {
		return "HEAD^1", nil
	}
	return "HEAD", nil
}

// ProjectRoot returns the git repository root for dir.
func ProjectRoot(dir string) (string, error) {
	out, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	return out, nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func truncate(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// EnsureDir creates the parent directory for the given path.
func EnsureDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0755)
}
