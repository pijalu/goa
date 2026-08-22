// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/filefind"
	"github.com/pijalu/goa/internal/review"
	"github.com/pijalu/goa/tui"
)

// usageFileReview is shown when /review:file carries no path.
const usageFileReview = "Usage: /review:file:<path> (text files only)\n"

// startFileReview opens one text file in the file review pager
// (/review:file:<path>). Per D4 it needs no git repository: any directory
// works. Every failure path answers with a chat message and emits no event;
// ShowFileReviewPager goes out only after the content loaded and the session
// persisted, so the app-side overlay handler always receives a usable pager.
func (c *ReviewCommand) startFileReview(ctx core.Context, args []string) error {
	if len(args) == 0 || args[0] == "" {
		writeStr(ctx, usageFileReview)
		return nil
	}

	projectDir := reviewProjectDir(ctx)
	content, err := review.LoadReviewFile(projectDir, args[0])
	if err != nil {
		writeFmt(ctx, "Cannot review file: %v\n", err)
		return nil
	}

	session, err := review.NewFileSession(projectDir, content.Path)
	if err != nil {
		writeFmt(ctx, "Cannot start file review: %v\n", err)
		return nil
	}

	store := review.NewStore(session.ProjectDir)
	if err := store.Save(session); err != nil {
		writeFmt(ctx, "Cannot save review session: %v\n", err)
		return nil
	}

	pager := tui.NewFileReviewPager(session, content)
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
		ctx.EventBus.Chat <- event.ChatEvent{
			ShowFileReviewPager: &event.ShowFileReviewPager{Pager: pager},
		}
	}
	return nil
}

// fileCompletions suggests files for the nested /review:file:<path> scope
// through the shared @-completion engine (internal/filefind, D5): fd-backed
// search when installed, os.ReadDir fallback otherwise, identical ranking.
// Values carry the "file:" scope prefix so the popup expands to
// "/review:file:<path>".
//
// A colon inside pathPrefix means either the TUI's level-2 expansion probe
// (expandArg re-queries with "<value>:" appended) or a user-typed path that
// contains ':' — such paths are unreachable through the colon router anyway
// (documented constraint), and answering empty keeps the base "/review:"
// popup from being flooded by filesystem entries.
func (c *ReviewCommand) fileCompletions(ctx core.Context, pathPrefix string) []core.ArgCompletion {
	if strings.Contains(pathPrefix, ":") {
		return nil
	}
	root := reviewProjectDir(ctx)
	if root == "" {
		return nil
	}
	entries := filefind.Complete(root, pathPrefix)
	out := make([]core.ArgCompletion, 0, len(entries))
	for _, e := range entries {
		out = append(out, core.ArgCompletion{Value: "file:" + e.Path})
	}
	return out
}
