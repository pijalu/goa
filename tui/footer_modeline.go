// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

const (
	modeLineAppBadge = "⬡ goa"
	modeLineHelpHint = "/help"
)

// modeLineConfig is the renderer-owned copy of tui.modeline. Nil lists mean no
// configuration; non-nil empty lists are explicit empty sides.
type modeLineConfig struct {
	left  []string
	right []string
}

func (c modeLineConfig) enabled() bool { return c.left != nil || c.right != nil }

// SetModeLine selects the segments rendered on the first footer row. The
// configuration is independent of FooterData, so routine stats/activity updates
// cannot reset it. Input slices are copied and their nil/empty distinction is
// preserved.
func (f *Footer) SetModeLine(left, right []string) {
	f.modeLine.left = cloneModeLineSide(left)
	f.modeLine.right = cloneModeLineSide(right)
}

func cloneModeLineSide(side []string) []string {
	if side == nil {
		return nil
	}
	return append([]string{}, side...)
}

// modelRowActivity prevents the configured activity segment from being
// duplicated by the model formatter. If activity is not configured, the model
// row remains its fallback location so the live state is not lost.
func (f *Footer) modelRowActivity(activity string) string {
	for _, side := range [][]string{f.modeLine.left, f.modeLine.right} {
		for _, name := range side {
			if name == "activity" {
				return ""
			}
		}
	}
	return activity
}

// renderConfiguredFooter renders the configurable modeline above the essential
// model row. Stats and activity belong to the modeline in this layout; the
// second row keeps steering, model/team/workflow state, and plugin segments.
func (f *Footer) renderConfiguredFooter(width int, fg string, styler func(string) string) []string {
	modeline := f.renderModeLine(width, fg, styler)
	left2 := f.buildSteeringHint(fg)
	availW := maxInt(width-visibleWidth(left2)-2, 0)
	right2 := f.buildModelDisplay(fg, availW)
	right2 = f.appendPluginSegments(right2, fg)
	left2, right2 = f.fitStatusLineSegments(left2, nil, right2, width, fg)
	line2 := styler(renderTwoCol(left2, right2, width, styler))

	lines := []string{modeline, line2}
	return append(lines, f.renderAgentStatsLine(width, styler)...)
}

func (f *Footer) renderModeLine(width int, fg string, styler func(string) string) string {
	left := f.modeLineSegments(f.modeLine.left, width, fg)
	right := f.modeLineSegments(f.modeLine.right, width, fg)
	left, right = fitModeLineSegments(left, right, width)
	return styler(renderTwoCol(joinFooterSegments(left), joinFooterSegments(right), width, styler))
}

func (f *Footer) modeLineSegments(names []string, width int, fg string) []FooterSegment {
	var segments []FooterSegment
	for _, name := range names {
		switch name {
		case "app-badge":
			segments = append(segments, FooterSegment{Tier: FooterTierProvider, Text: modeLineAppBadge})
		case "mode-badge":
			segments = append(segments, FooterSegment{Tier: FooterTierQuota, Text: f.modeBadgeText(fg)})
		case "current-file":
			segments = append(segments, FooterSegment{Tier: FooterTierIdentity, Text: f.formatWorkdirAdaptive(width)})
		case "token-bar":
			segments = append(segments, f.modeLineTokenSegments()...)
		case "activity":
			segments = append(segments, FooterSegment{Tier: FooterTierTools, Text: f.data.Activity})
		case "help-hint":
			segments = append(segments, FooterSegment{Tier: FooterTierHelp, Text: modeLineHelpHint})
		}
	}
	return segments
}

func (f *Footer) modeLineTokenSegments() []FooterSegment {
	segments := f.widthLadderSegments()
	if len(segments) == 0 && f.data.Tokens != "" {
		return []FooterSegment{{Tier: FooterTierTokens, Text: f.data.Tokens}}
	}
	return segments
}

func (f *Footer) modeBadgeText(fg string) string {
	modeBadge := ansi.Fg(f.modeColor()) + strings.ToUpper(f.data.Mode) + ansi.Reset + fg
	return fmt.Sprintf("%s %s %s", f.goalProfileLabel(fg), ansi.BoxVertical, modeBadge)
}

// fitModeLineSegments applies the shared footer priority ladder to both sides.
// Help is the first casualty; mode and context/quota remain the last survivors.
func fitModeLineSegments(left, right []FooterSegment, width int) ([]FooterSegment, []FooterSegment) {
	fits := func() bool {
		return visibleWidth(joinFooterSegments(left))+1+visibleWidth(joinFooterSegments(right)) <= width
	}
	for tier := FooterTierHelp; tier <= FooterTierIdentity && !fits(); tier++ {
		left = dropFooterTier(left, tier)
		right = dropFooterTier(right, tier)
	}
	return left, right
}
