// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"fmt"
	"strings"
)

// LineAnchor maps a rendered line number to a plan item ID.
type LineAnchor struct {
	Line   int    `json:"line"`
	ItemID string `json:"item_id"`
}

// Render produces a deterministic Markdown representation of the plan.
// It returns the Markdown string and a slice of line anchors for the pager.
func Render(p *Plan) (string, []LineAnchor) {
	var b strings.Builder
	var anchors []LineAnchor

	line := renderHeader(&b, p)
	line = renderItems(&b, p, line, &anchors)
	renderComments(&b, p, &line)
	return b.String(), anchors
}

func renderHeader(b *strings.Builder, p *Plan) int {
	line := 1
	fmt.Fprintf(b, "# Plan: %s (revision %d)\n", p.Name, p.Revision)
	line++
	fmt.Fprintf(b, "**Objective:** %s\n", p.Objective)
	line++
	fmt.Fprintf(b, "**Status:** %s\n", p.Status)
	b.WriteString("\n")
	return line + 1 // account for blank line
}

func renderItems(b *strings.Builder, p *Plan, line int, anchors *[]LineAnchor) int {
	for i, item := range p.Items {
		line++
		fmt.Fprintf(b, "## %d. %s  <!-- anchor: %s -->\n", i+1, item.Title, item.ID)
		*anchors = append(*anchors, LineAnchor{Line: line, ItemID: item.ID})

		if item.Description != "" {
			line++
			b.WriteString(item.Description)
			b.WriteString("\n")
		}

		line++
		fmt.Fprintf(b, "_Status: %s_", item.Status)

		if len(item.DependsOn) > 0 {
			fmt.Fprintf(b, " | _Depends on: %s_", strings.Join(item.DependsOn, ", "))
		}
		if item.Role != "" {
			fmt.Fprintf(b, " | _Role: %s_", item.Role)
		}
		if item.Result != "" {
			fmt.Fprintf(b, "\n_Result: %s_", truncateInline(item.Result, 80))
		}

		b.WriteString("\n\n")
		line++
	}
	return line
}

func renderComments(b *strings.Builder, p *Plan, line *int) {
	if len(p.Comments) == 0 {
		return
	}
	*line++
	b.WriteString("---\n")
	*line++
	b.WriteString("## Comments\n")
	for _, c := range p.Comments {
		*line++
		status := "open"
		if c.Resolved {
			status = "resolved"
		}
		target := "plan"
		if c.ItemID != "" {
			target = c.ItemID
		}
		fmt.Fprintf(b, "- **%s** on %s (revision %d, %s): %s\n", c.ID[:minInt(8, len(c.ID))], target, c.Revision, status, c.Content)
	}
	b.WriteString("\n")
	*line++
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// truncateInline truncates a string to maxLen characters, appending "…" if truncated.
func truncateInline(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}
