// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"

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

func (c *ReviewCommand) Run(ctx core.Context, args []string) error {
	projectDir := ctx.ProjectDir
	if projectDir == "" {
		projectDir, _ = os.Getwd()
	}
	if !review.IsGitRepo(projectDir) {
		writeStr(ctx, "Review is only available in git projects.\n")
		return nil
	}

	if len(args) == 0 {
		return c.startReview(ctx, "")
	}

	switch args[0] {
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

func (c *ReviewCommand) startReview(ctx core.Context, baseRef string) error {
	session, err := review.NewSession(ctx.ProjectDir)
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
	commits, err := review.RecentCommits(ctx.ProjectDir, 10, 80)
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
			writeFmt(ctx, "  - %s:%d: %s\n", c.File, c.LineNum, truncateReviewText(c.Content, 60))
		}
	}
	return nil
}

func (c *ReviewCommand) submitReview(ctx core.Context) error {
	s, diff, ok := c.loadLatestSession(ctx)
	if !ok {
		return nil
	}
	text := s.MarkdownSummary(diff)
	if ctx.SubmitToAgent != nil {
		ctx.SubmitToAgent(text)
	} else {
		writeStr(ctx, text)
	}
	return nil
}

func (c *ReviewCommand) exportReview(ctx core.Context) error {
	s, diff, ok := c.loadLatestSession(ctx)
	if !ok {
		return nil
	}
	path, err := s.ExportPath(ctx.ProjectDir)
	if err != nil {
		writeFmt(ctx, "Cannot build export path: %v\n", err)
		return nil
	}
	if err := s.Export(diff, path); err != nil {
		writeFmt(ctx, "Cannot export review: %v\n", err)
		return nil
	}
	writeFmt(ctx, "Exported review to %s\n", path)
	return nil
}

func (c *ReviewCommand) loadLatestSession(ctx core.Context) (*review.Session, string, bool) {
	store := review.NewStore(ctx.ProjectDir)
	ids, err := store.List()
	if err != nil {
		writeFmt(ctx, "Cannot list reviews: %v\n", err)
		return nil, "", false
	}
	if len(ids) == 0 {
		writeStr(ctx, "No active review sessions. Start one with /review\n")
		return nil, "", false
	}
	id := ids[len(ids)-1]
	s, err := store.Load(id)
	if err != nil {
		writeFmt(ctx, "Cannot load review %s: %v\n", id, err)
		return nil, "", false
	}
	diff, err := review.Diff(s.ProjectDir, s.BaseRef)
	if err != nil {
		writeFmt(ctx, "Cannot generate diff: %v\n", err)
		return nil, "", false
	}
	return s, diff, true
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
