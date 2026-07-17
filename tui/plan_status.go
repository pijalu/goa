// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core/plan"
	"github.com/pijalu/goa/internal/ansi"
)

// PlanStatusOverlay renders a read-only full-screen view of plan execution
// progress. It is opened via /plan:status and never steals focus on its own.
type PlanStatusOverlay struct {
	Store *plan.Store

	scrollTop int
	cursor    int
	viewportW int
	viewportH int

	detailItem string // item ID whose detail pane is open; empty = no detail

	RequestRender func()
	OnClose       func()
}

// NewPlanStatusOverlay creates a status overlay.
func NewPlanStatusOverlay(store *plan.Store) *PlanStatusOverlay {
	return &PlanStatusOverlay{Store: store}
}

// SetViewport tells the overlay the available terminal dimensions.
func (o *PlanStatusOverlay) SetViewport(width, height int) {
	o.viewportW = width
	o.viewportH = height
}

// Render implements Component.
func (o *PlanStatusOverlay) Render(width int) []string {
	snap, err := o.Store.Snapshot()
	if err != nil {
		return []string{ansi.Fg("#f85149") + "Error loading plan" + ansi.Reset}
	}
	o.clampCursor(snap)

	var out []string
	o.renderStatusHeader(&out, snap, width)
	o.renderStatusItems(&out, snap, width)
	o.renderClarifications(&out, snap)
	out = append(out, "")
	out = append(out, ansi.Fg("#8b949e")+"↑/↓ select  Enter toggle detail  q/esc close"+ansi.FgReset)
	return out
}

func (o *PlanStatusOverlay) renderStatusHeader(out *[]string, snap *plan.Plan, width int) {
	title := fmt.Sprintf("Plan: %s  [%s]  rev %d", snap.Name, snap.Status, snap.Revision)
	*out = append(*out, ansi.Bold+truncate(title, width)+ansi.BoldReset)
	*out = append(*out, ansi.Fg("#8b949e")+"Objective: "+snap.Objective+ansi.FgReset)
	if snap.RunID != "" {
		*out = append(*out, ansi.Fg("#8b949e")+"Run: "+snap.RunID+ansi.FgReset)
	}
	*out = append(*out, "")

	done, skipped, inProg, blocked := countItemStatuses(snap)
	total := len(snap.Items)
	terminal := done + skipped
	progressLine := fmt.Sprintf("Progress: %d/%d items terminal  (%d done, %d skipped, %d in_progress, %d blocked)",
		terminal, total, done, skipped, inProg, blocked)
	*out = append(*out, progressLine)

	if total > 0 && width > 10 {
		barWidth := width - 4
		if barWidth > 60 {
			barWidth = 60
		}
		filled := (terminal * barWidth) / total
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		*out = append(*out, "  "+ansi.Fg("#3fb950")+bar+ansi.FgReset)
	}
	*out = append(*out, "")
}

func countItemStatuses(snap *plan.Plan) (done, skipped, inProg, blocked int) {
	for _, item := range snap.Items {
		switch item.Status {
		case plan.ItemDone:
			done++
		case plan.ItemSkipped:
			skipped++
		case plan.ItemInProgress:
			inProg++
		case plan.ItemBlocked:
			blocked++
		}
	}
	return
}

func (o *PlanStatusOverlay) renderStatusItems(out *[]string, snap *plan.Plan, width int) {
	for i, item := range snap.Items {
		line := o.buildItemLine(item, i, snap)
		*out = append(*out, line)

		if i == o.cursor && o.detailItem == item.ID {
			detail := o.renderDetail(item, snap, width)
			*out = append(*out, detail...)
		}
	}
}

func (o *PlanStatusOverlay) buildItemLine(item plan.PlanItem, idx int, snap *plan.Plan) string {
	glyph := statusGlyph(item.Status)
	selected := idx == o.cursor

	var line string
	if selected {
		line = ansi.Bg("#1e4273")
	}
	line += glyph + " " + item.Title

	if item.Role != "" {
		line += ansi.Fg("#8b949e") + " [" + item.Role + "]" + ansi.FgReset
	}

	openComments := 0
	for _, c := range snap.Comments {
		if c.ItemID == item.ID && !c.Resolved {
			openComments++
		}
	}
	if openComments > 0 {
		line += ansi.Fg("#d29922") + fmt.Sprintf(" [%d]", openComments) + ansi.FgReset
	}

	if selected {
		line += ansi.Reset
	}
	return line
}

func (o *PlanStatusOverlay) renderClarifications(out *[]string, snap *plan.Plan) {
	hasOpen := false
	for _, c := range snap.Comments {
		if !c.Resolved {
			hasOpen = true
			break
		}
	}
	if !hasOpen {
		return
	}
	*out = append(*out, "")
	*out = append(*out, ansi.Bold+"Open Clarifications"+ansi.BoldReset)
	for _, c := range snap.Comments {
		if c.Resolved {
			continue
		}
		target := "plan"
		if c.ItemID != "" {
			target = c.ItemID
		}
		*out = append(*out, ansi.Fg("#d29922")+"  ["+target+"] "+c.Content+ansi.FgReset)
	}
}

func (o *PlanStatusOverlay) renderDetail(item plan.PlanItem, p *plan.Plan, width int) []string {
	var lines []string
	lines = append(lines, "")
	lines = append(lines, "  "+ansi.Bold+item.Title+ansi.BoldReset)
	if item.Description != "" {
		lines = append(lines, "    "+item.Description)
	}
	if len(item.DependsOn) > 0 {
		lines = append(lines, "    Depends on: "+strings.Join(item.DependsOn, ", "))
	}
	if item.Result != "" {
		result := item.Result
		if len(result) > 80 {
			result = result[:80] + "…"
		}
		lines = append(lines, "    Result: "+result)
	}
	// Clarifications for this item
	for _, c := range p.Comments {
		if c.ItemID == item.ID {
			status := "open"
			if c.Resolved {
				status = "resolved"
			}
			lines = append(lines, ansi.Fg("#8b949e")+"    ["+status+"] "+c.Content+ansi.FgReset)
		}
	}
	lines = append(lines, "")
	return lines
}

// HandleInput implements Component.
func (o *PlanStatusOverlay) HandleInput(data string) {
	snap, err := o.Store.Snapshot()
	if err != nil {
		return
	}

	switch data {
	case "up", "k":
		if o.cursor > 0 {
			o.cursor--
			o.detailItem = ""
		}
		o.requestRender()
	case "down", "j":
		if o.cursor < len(snap.Items)-1 {
			o.cursor++
			o.detailItem = ""
		}
		o.requestRender()
	case "enter":
		if o.cursor >= 0 && o.cursor < len(snap.Items) {
			itemID := snap.Items[o.cursor].ID
			if o.detailItem == itemID {
				o.detailItem = "" // toggle off
			} else {
				o.detailItem = itemID
			}
		}
		o.requestRender()
	case "q", "esc", "ctrl+c":
		if o.OnClose != nil {
			o.OnClose()
		}
	}
}

func (o *PlanStatusOverlay) clampCursor(snap *plan.Plan) {
	if o.cursor < 0 {
		o.cursor = 0
	}
	if len(snap.Items) > 0 && o.cursor >= len(snap.Items) {
		o.cursor = len(snap.Items) - 1
	}
	if o.cursor < 0 {
		o.cursor = 0
	}
}

func (o *PlanStatusOverlay) requestRender() {
	if o.RequestRender != nil {
		o.RequestRender()
	}
}

// Invalidate implements Component.
func (o *PlanStatusOverlay) Invalidate() {}

// statusGlyph returns a Unicode glyph for the item status.
func statusGlyph(s plan.ItemStatus) string {
	switch s {
	case plan.ItemPending:
		return "☐"
	case plan.ItemInProgress:
		return "◐"
	case plan.ItemDone:
		return "☑"
	case plan.ItemBlocked:
		return "✖"
	case plan.ItemSkipped:
		return "–"
	default:
		return "?"
	}
}
