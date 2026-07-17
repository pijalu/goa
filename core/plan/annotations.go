// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"fmt"
	"strings"
)

// AnnotationsSummary returns a Markdown summary of the plan's comments,
// formatted for injection into the planner agent's context.
//
// The summary includes: objective, revision, open comments grouped by item
// (with a short excerpt of each item's title), and resolved comments from
// the current revision. This mirrors the tone and format of
// review.Session.MarkdownSummary.
func AnnotationsSummary(p *Plan) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Plan Annotations\n\n")
	fmt.Fprintf(&b, "- **Objective:** %s\n", p.Objective)
	fmt.Fprintf(&b, "- **Revision:** %d\n", p.Revision)
	fmt.Fprintf(&b, "- **Total items:** %d\n", len(p.Items))
	fmt.Fprintf(&b, "- **Open comments:** %d\n", countOpenComments(p))

	oc := openCommentsByItem(p)
	rc := resolvedCommentsAtRevision(p, p.Revision)
	if len(oc) == 0 && len(rc) == 0 {
		b.WriteString("\nNo comments.\n")
		return b.String()
	}

	renderOpenGroups(&b, oc)
	renderResolved(&b, p, rc)
	return b.String()
}

func renderOpenGroups(b *strings.Builder, groups []commentGroup) {
	if len(groups) == 0 {
		return
	}
	b.WriteString("\n## Open Comments\n\n")
	for _, group := range groups {
		excerpt := excerptTitle(group.ItemTitle, 5)
		if group.ItemID != "" {
			fmt.Fprintf(b, "### %s (%s)\n\n", excerpt, group.ItemID)
		} else {
			b.WriteString("### Plan-level\n\n")
		}
		for _, c := range group.Comments {
			fmt.Fprintf(b, "- %s\n", c.Content)
		}
		b.WriteString("\n")
	}
}

func renderResolved(b *strings.Builder, p *Plan, comments []PlanComment) {
	if len(comments) == 0 {
		return
	}
	b.WriteString("\n## Resolved Comments (current revision)\n\n")
	for _, c := range comments {
		target := "plan"
		if c.ItemID != "" {
			target = c.ItemID
		}
		fmt.Fprintf(b, "- **%s** on %s: %s\n", c.ID[:minInt(8, len(c.ID))], target, c.Content)
	}
	b.WriteString("\n")
}

// commentGroup groups comments by item.
type commentGroup struct {
	ItemID   string
	ItemTitle string
	Comments []PlanComment
}

// openCommentsByItem returns open comments grouped by item,
// ordered by plan item order (plan-level first, then by item position).
func openCommentsByItem(p *Plan) []commentGroup {
	// Collect open comments and index by item ID.
	commentsByItem := make(map[string][]PlanComment)
	var planLevelComments []PlanComment

	for _, c := range p.Comments {
		if c.Resolved {
			continue
		}
		if c.ItemID == "" {
			planLevelComments = append(planLevelComments, c)
		} else {
			commentsByItem[c.ItemID] = append(commentsByItem[c.ItemID], c)
		}
	}

	// Build groups in item order.
	var result []commentGroup

	// Plan-level first.
	if len(planLevelComments) > 0 {
		result = append(result, commentGroup{
			Comments: planLevelComments,
		})
	}

	// Then items in order.
	for _, item := range p.Items {
		comments, ok := commentsByItem[item.ID]
		if !ok {
			continue
		}
		result = append(result, commentGroup{
			ItemID:    item.ID,
			ItemTitle: item.Title,
			Comments:  comments,
		})
	}

	return result
}

// resolvedCommentsAtRevision returns resolved comments from the specified revision.
func resolvedCommentsAtRevision(p *Plan, revision int) []PlanComment {
	var result []PlanComment
	for _, c := range p.Comments {
		if c.Resolved && c.Revision == revision {
			result = append(result, c)
		}
	}
	return result
}

// countOpenComments counts unresolved comments.
func countOpenComments(p *Plan) int {
	n := 0
	for _, c := range p.Comments {
		if !c.Resolved {
			n++
		}
	}
	return n
}

// excerptTitle returns the first n words of a title, appending "…" if truncated.
func excerptTitle(title string, n int) string {
	if n <= 0 {
		return ""
	}
	words := strings.Fields(title)
	if len(words) <= n {
		return title
	}
	return strings.Join(words[:n], " ") + " …"
}
