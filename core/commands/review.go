// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/review"
	"github.com/pijalu/goa/tui"
)

// ReviewCommand lets the user review code changes in a git project.
// It is a user-activated slash command; the agent may also invoke it via
// the goa tool with {"command_string":"/review ..."}.
type ReviewCommand struct{}

func (c *ReviewCommand) Name() string      { return "review" }
func (c *ReviewCommand) Aliases() []string { return nil }
func (c *ReviewCommand) ShortHelp() string { return "Review code changes with comments" }
func (c *ReviewCommand) LongHelp() string  { return help.LongHelp(c.Name()) }

// CompleteArgs implements core.ArgCompleter. It suggests diff bases for
// /review:<base>: HEAD^N ancestry plus the most recent tags and branches,
// with the "file:" single-file scope offered right after the ancestry trio.
// Once the prefix enters that nested scope ("file:<path>"), completion
// switches to filesystem paths via internal/filefind (@-like); values keep
// the "file:" scope prefix so the popup expands to "/review:file:<path>".
func (c *ReviewCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	if sub, rest, ok := splitGoalCompletionPrefix(prefix); ok && sub == "file" {
		return c.fileCompletions(ctx, rest)
	}
	comps := reviewAncestryCompletions(prefix)
	comps = append(comps, filterCompletions(reviewFileScopeCompletions, prefix)...)
	return append(comps, reviewRefCompletions(ctx.ProjectDir, prefix)...)
}

// reviewFileScopeCompletions offers the nested single-file review scope. The
// trailing colon mirrors the goal command's subcommand scopes: it is part of
// the value so accepting it puts the editor into "/review:file:" path mode.
var reviewFileScopeCompletions = []core.ArgCompletion{
	{Value: "file:", Description: "Review a single file"},
}

// reviewAncestryCompletions suggests HEAD^N bases. "^1" is the default and
// always offered first; a few deeper ancestors are listed for convenience.
func reviewAncestryCompletions(prefix string) []core.ArgCompletion {
	ancestry := []core.ArgCompletion{
		{Value: "^1", Description: "Default base — previous commit"},
		{Value: "^2", Description: "2 commits back"},
		{Value: "^3", Description: "3 commits back"},
	}
	return filterCompletions(ancestry, prefix)
}

// reviewRefCompletions lists the most recent tags and branches as named
// checkpoints. Outside a git repository it yields nothing.
func reviewRefCompletions(projectDir, prefix string) []core.ArgCompletion {
	if projectDir == "" || !review.IsGitRepo(projectDir) {
		return nil
	}
	refs, err := review.RecentRefs(projectDir, 15)
	if err != nil {
		return nil
	}
	candidates := make([]core.ArgCompletion, 0, len(refs))
	for _, r := range refs {
		candidates = append(candidates, core.ArgCompletion{
			Value:       r.Name,
			Description: "Review base (" + r.Kind + ")",
		})
	}
	return filterCompletions(candidates, prefix)
}

// filterCompletions keeps candidates whose value starts with prefix. An
// empty prefix keeps everything, preserving candidate order.
func filterCompletions(candidates []core.ArgCompletion, prefix string) []core.ArgCompletion {
	if prefix == "" {
		return candidates
	}
	var out []core.ArgCompletion
	for _, c := range candidates {
		if strings.HasPrefix(c.Value, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func (c *ReviewCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return c.startReview(ctx, "")
	}

	switch args[0] {
	case "file":
		// First-class subcommand (D4): single-file review needs no git, so it
		// dispatches before any git-dependent work. A branch literally named
		// "file" is shadowed — same as "list", "status", "submit", "export".
		return c.startFileReview(ctx, args[1:])
	case "list":
		return c.listCommits(ctx)
	case "status":
		return c.showStatus(ctx)
	case "submit":
		return c.submitReview(ctx)
	case "export":
		return c.exportReview(ctx)
	default:
		return c.startReview(ctx, args[0])
	}
}

// reviewProjectDir resolves the directory review operations run against:
// the configured project dir, or the process working directory when unset.
func reviewProjectDir(ctx core.Context) string {
	if ctx.ProjectDir != "" {
		return ctx.ProjectDir
	}
	dir, _ := os.Getwd()
	return dir
}

func (c *ReviewCommand) startReview(ctx core.Context, baseRef string) error {
	// The git guard lives here rather than in Run (D4): only the diff review
	// needs git — file review, status, submit and export work in any project.
	projectDir := reviewProjectDir(ctx)
	if !review.IsGitRepo(projectDir) {
		writeStr(ctx, "Review is only available in git projects.\n")
		return nil
	}

	session, err := review.NewSession(projectDir)
	if err != nil {
		writeFmt(ctx, "Cannot start review: %v\n", err)
		return nil
	}
	if baseRef != "" {
		session.BaseRef = baseRef
	}

	diff, err := review.Diff(session.ProjectDir, session.BaseRef)
	if err != nil {
		writeFmt(ctx, "Cannot generate diff: %v\n", err)
		return nil
	}

	store := review.NewStore(session.ProjectDir)
	if err := store.Save(session); err != nil {
		writeFmt(ctx, "Cannot save review session: %v\n", err)
		return nil
	}

	commits, err := review.RecentCommits(session.ProjectDir, 10, 60)
	if err != nil {
		commits = nil
	}

	pager := tui.NewReviewPager(session, diff)
	pager.RecentCommits = commits
	pager.OnCommentSaved = func() {
		_ = store.Save(session)
	}
	pager.OnSubmitReview = func(text string) {
		if ctx.SubmitToAgent != nil {
			ctx.SubmitToAgent(text)
		}
		_ = store.Save(session)
	}

	if ctx.EventBus != nil {
		ctx.EventBus.Chat <- event.ChatEvent{ShowReviewPager: &event.ShowReviewPager{Pager: pager}}
	}
	return nil
}

func (c *ReviewCommand) listCommits(ctx core.Context) error {
	// Same guard rationale as startReview (D4): listing commits is a
	// git-only view; everything else under /review is not.
	projectDir := reviewProjectDir(ctx)
	if !review.IsGitRepo(projectDir) {
		writeStr(ctx, "Review is only available in git projects.\n")
		return nil
	}
	commits, err := review.RecentCommits(projectDir, 10, 80)
	if err != nil {
		writeFmt(ctx, "Cannot list commits: %v\n", err)
		return nil
	}
	writeStr(ctx, "Recent commits:\n")
	for _, c := range commits {
		writeFmt(ctx, "  %s  %s\n", c.SHA[:7], c.Subject)
	}
	return nil
}

func (c *ReviewCommand) showStatus(ctx core.Context) error {
	store := review.NewStore(ctx.ProjectDir)
	ids, err := store.List()
	if err != nil {
		writeFmt(ctx, "Cannot list reviews: %v\n", err)
		return nil
	}
	if len(ids) == 0 {
		writeStr(ctx, "No active review sessions.\n")
		return nil
	}
	for _, id := range ids {
		s, err := store.Load(id)
		if err != nil {
			continue
		}
		writeFmt(ctx, "Review %s  base:%s  comments:%d\n", id, s.BaseRef, len(s.Comments))
		for _, c := range s.Comments {
			writeFmt(ctx, "  - %s: %s\n", c.AnchorLabel(), truncateReviewText(c.Content, 60))
		}
	}
	return nil
}

func (c *ReviewCommand) submitReview(ctx core.Context) error {
	s, ok := c.loadLatestSession(ctx)
	if !ok {
		return nil
	}
	text := s.MarkdownSummary()
	if ctx.SubmitToAgent != nil {
		ctx.SubmitToAgent(text)
	} else {
		writeStr(ctx, text)
	}
	return nil
}

func (c *ReviewCommand) exportReview(ctx core.Context) error {
	s, ok := c.loadLatestSession(ctx)
	if !ok {
		return nil
	}
	path, err := s.ExportPath(ctx.ProjectDir)
	if err != nil {
		writeFmt(ctx, "Cannot build export path: %v\n", err)
		return nil
	}
	if err := s.Export(path); err != nil {
		writeFmt(ctx, "Cannot export review: %v\n", err)
		return nil
	}
	writeFmt(ctx, "Exported review to %s\n", path)
	return nil
}

// loadLatestSession loads the most recent stored review session. It does not
// compute the diff: submit/export only need the session metadata and point
// the agent at the diff command, so running a potentially huge `git diff`
// here would be wasted work.
func (c *ReviewCommand) loadLatestSession(ctx core.Context) (*review.Session, bool) {
	store := review.NewStore(ctx.ProjectDir)
	ids, err := store.List()
	if err != nil {
		writeFmt(ctx, "Cannot list reviews: %v\n", err)
		return nil, false
	}
	if len(ids) == 0 {
		writeStr(ctx, "No active review sessions. Start one with /review\n")
		return nil, false
	}
	id := ids[len(ids)-1]
	s, err := store.Load(id)
	if err != nil {
		writeFmt(ctx, "Cannot load review %s: %v\n", id, err)
		return nil, false
	}
	return s, true
}

func truncateReviewText(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
