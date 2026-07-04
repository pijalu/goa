// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// Render renders two status lines with adaptive width.
// When the terminal is too narrow, low-priority items are dropped to
// maintain readability: token stats → git branch → thinking level → provider prefix.
func (f *Footer) Render(width int) []string {
	if width <= 0 {
		return nil
	}

	fg := ansi.Fg("#8b949e")
	// styler wraps a line with the status-line foreground color only, using
	// the terminal's default background. The footer's layout provides enough
	// visual boundary without a dedicated background.
	styler := func(s string) string { return fg + s + ansi.Reset }

	// Line 1: working directory (left) / profile(minor) + mode badge (right)
	workdir := f.formatWorkdirAdaptive(width)
	modeBadge := ansi.Fg(f.modeColor()) + strings.ToUpper(f.data.Mode) + ansi.Reset + fg
	profileLabel := f.data.Profile
	if f.data.MinorMode != "" {
		profileLabel = fmt.Sprintf("%s(%s)", f.data.Profile, f.data.MinorMode)
	}
	right1 := fmt.Sprintf("%s │ %s", profileLabel, modeBadge)
	line1 := renderTwoCol(workdir, right1, width, styler)

	// Line 2: conversation stats / activity / steering hint / goal status (left) / model + workflow hint (right)
	left2 := f.buildLeftSide(fg)
	if f.data.GoalStatus != "" {
		goalText := f.formatGoalStatus(fg)
		if left2 != "" {
			left2 += " │ " + goalText
		} else {
			left2 = goalText
		}
	}

	// Calculate available width for the model display based on left-side content,
	// not raw terminal width. This ensures the provider prefix and thinking level
	// are shown if there's actual room, not just because width > arbitrary threshold.
	leftW := visibleWidth(left2)
	minPad := 2
	availW := width - leftW - minPad
	if availW < 30 {
		availW = 30 // minimum useful width for model display
	}

	right2 := f.buildModelDisplay(fg, availW)

	// If still doesn't fit — compact the right side by stripping lower-priority items
	if leftW+visibleWidth(right2)+minPad > width {
		targetW := width - leftW - minPad
		if targetW > 10 {
			right2 = f.compactRightSide(right2, fg, targetW)
		}
	}

	line2 := renderTwoCol(left2, right2, width, styler)

	return []string{styler(line1), styler(line2)}
}

// buildLeftSide builds the left portion of the second status line
// from stats, activity, tokens, steering hint, and pending steering text.
func (f *Footer) buildLeftSide(fg string) string {
	left2 := f.data.Stats
	if left2 == "" {
		left2 = f.data.Activity
		if f.data.Tokens != "" {
			left2 = appendWithSep(left2, f.data.Tokens)
		}
	}
	if f.data.SteeringHint != "" {
		hint := ansi.Fg("#d29922") + f.data.SteeringHint + ansi.Reset + fg
		left2 = appendWithSep(left2, hint)
	}
	if f.data.SteeringPending != "" {
		pendingText := f.data.SteeringPending
		if len(pendingText) > 40 {
			pendingText = pendingText[:40] + "…"
		}
		hint := "⏳ " + pendingText
		colored := ansi.Fg("#d29922") + hint + ansi.Reset + fg
		left2 = appendWithSep(left2, colored)
	}
	return left2
}

// appendWithSep appends s to base with a " │ " separator, or returns s when
// base is empty.
func appendWithSep(base, s string) string {
	if base == "" {
		return s
	}
	return base + " │ " + s
}

// formatWorkdirAdaptive returns the formatted working directory, optionally
// dropping the git branch when the terminal is too narrow.
func (f *Footer) formatWorkdirAdaptive(width int) string {
	dir := f.data.Workdir
	if dir == "" {
		return "."
	}
	home := os.Getenv("HOME")
	if home != "" && strings.HasPrefix(dir, home) {
		dir = "~" + dir[len(home):]
	}
	// Append git branch with color and symbol if there's room
	if f.data.GitBranch != "" && width > 50 {
		branch := f.data.GitBranch
		var color string
		var prefix string
		switch {
		case f.data.GitConflicts:
			color = "#f85149"
			prefix = "✗ "
		case f.data.GitDirty:
			color = "#d29922"
			prefix = "✱ "
		default:
			color = "#3fb950"
			prefix = "⎇ "
		}
		branch = ansi.Fg(color) + prefix + branch + ansi.Reset + ansi.Fg("#8b949e")
		dir = dir + " (" + branch + ")"
	}
	return dir
}

func appendThinkingLevel(modelPart, level string) string {
	if level == "" || level == "off" {
		return modelPart
	}
	return modelPart + " • " + level
}

func renderTwoCol(left, right string, width int, styler func(string) string) string {
	leftW := visibleWidth(left)
	rightW := visibleWidth(right)
	pad := width - leftW - rightW
	if pad < 1 {
		pad = 1
	}
	bar := left + strings.Repeat(" ", pad) + right
	vw := visibleWidth(bar)
	if vw < width {
		bar += strings.Repeat(" ", width-vw)
	}
	return bar
}

func (f *Footer) buildModelDisplay(fg string, availWidth int) string {
	if f.data.MinorMode == "companion" {
		return f.buildCompanionModelDisplay(fg, availWidth)
	}
	return f.buildMainModelDisplay(fg, availWidth)
}

// stripProviderPrefix removes the "(provider) " prefix from a model display string.
// For example, "(lmstudio) llama3" → "llama3". If there's no prefix, returns the original.
func stripProviderPrefix(model string) string {
	if strings.HasPrefix(model, "(") {
		if idx := strings.Index(model, ") "); idx >= 0 {
			return model[idx+2:]
		}
	}
	return model
}

// buildMainModelDisplay renders the main model section of the status bar.
// availWidth is the actual space available for the right side, not raw terminal width.
// The provider prefix and thinking level are shown when there's enough room.
func (f *Footer) buildMainModelDisplay(fg string, availWidth int) string {
	var right2 string
	if f.data.Model != "" {
		// Determine model name with or without provider prefix based on available width
		modelName := f.data.Model
		showProvider := availWidth > 40
		if !showProvider {
			stripped := stripProviderPrefix(modelName)
			if stripped != "" {
				modelName = stripped
			}
		}
		// Determine if we have room for thinking level
		showLevel := availWidth > 35 && f.data.ThinkingLevel != "" && f.data.ThinkingLevel != "off"
		level := ""
		if showLevel {
			level = f.data.ThinkingLevel
		}
		part := f.formatModelPart(modelName, level, f.data.MainActivity, f.data.ModelBusy, true)
		right2 = part
	} else {
		right2 = "no-model"
	}
	if f.data.WorkflowActive {
		right2 = ansi.Fg("#d29922") + "⟡ workflow" + ansi.Reset + " " + right2
	}
	return right2
}

// compactRightSide progressively strips lower-priority items from the right side
// until it fits within targetWidth. Stripping order for companion mode:
// (companion) label → thinking levels → provider prefixes → cycle count → model truncation.
// For main mode: thinking level → activity text → provider prefix → model truncation.
func (f *Footer) compactRightSide(right2, fg string, targetWidth int) string {
	steps := []func(string) string{
		f.stripCompanionLabel,
		f.stripThinkingLevels,
		f.stripProviderPrefixes,
		f.stripCycleCount,
		f.stripActivityText,
	}

	for _, step := range steps {
		if visibleWidth(right2) <= targetWidth {
			break
		}
		right2 = step(right2)
	}

	if visibleWidth(right2) > targetWidth && targetWidth > 10 {
		right2 = truncateToWidth(right2, targetWidth, "")
	}
	return right2
}

// stripCompanionLabel drops the verbose "(companion)" label in companion mode.
func (f *Footer) stripCompanionLabel(s string) string {
	if f.data.MinorMode != "companion" || !strings.Contains(ansi.Strip(s), "(companion)") {
		return s
	}
	s = strings.ReplaceAll(s, " (companion)", "")
	return strings.ReplaceAll(s, "(companion)", "~c")
}

// stripThinkingLevels removes all " • level" suffixes.
func (f *Footer) stripThinkingLevels(s string) string {
	for {
		idx := strings.LastIndex(s, " • ")
		if idx < 0 {
			break
		}
		s = s[:idx]
	}
	return s
}

// stripProviderPrefixes removes all "(provider) " prefixes.
func (f *Footer) stripProviderPrefixes(s string) string {
	for {
		idx := strings.Index(s, "(")
		if idx < 0 {
			break
		}
		endIdx := strings.Index(s[idx:], ") ")
		if endIdx < 0 {
			break
		}
		s = s[:idx] + s[idx+endIdx+2:]
	}
	return s
}

// stripCycleCount drops the companion cycle count suffix.
func (f *Footer) stripCycleCount(s string) string {
	if f.data.MinorMode != "companion" || f.data.CompanionCycleMax <= 0 {
		return s
	}
	idx := strings.LastIndex(s, " [")
	if idx < 0 {
		return s
	}
	endIdx := strings.Index(s[idx:], "]")
	if endIdx < 0 {
		return s
	}
	return s[:idx] + s[idx+endIdx+1:]
}

// stripActivityText removes the activity label from a model display.
func (f *Footer) stripActivityText(s string) string {
	if f.data.MainActivity == "" {
		return s
	}
	activityColor := ansi.Fg("#d29922")
	idx := strings.LastIndex(s, activityColor)
	if idx < 0 {
		return s
	}
	resetIdx := strings.Index(s[idx:], ansi.Reset)
	if resetIdx >= 0 {
		return s[:idx] + s[idx+resetIdx+len(ansi.Reset):]
	}
	return s[:idx]
}

// companionVis captures the width-dependent visibility flags for companion mode.
type companionVis struct {
	showThinking       bool
	showCompanionLabel bool
	showProvider       bool
	showCycle          bool
}

func companionVisibility(availWidth int, thinkingLevel string) companionVis {
	return companionVis{
		showThinking:       availWidth > 40 && thinkingLevel != "" && thinkingLevel != "off",
		showCompanionLabel: availWidth > 35,
		showProvider:       availWidth > 45,
		showCycle:          availWidth > 30,
	}
}

// buildCompanionModelDisplay renders the companion model section of the status bar.
// availWidth is the actual space available for the right side.
// Provider prefixes and the "(companion)" label are droppable when width is tight.
// Providers are dropped aggressively since they add the most visual weight.
func (f *Footer) buildCompanionModelDisplay(fg string, availWidth int) string {
	vis := companionVisibility(availWidth, f.data.ThinkingLevel)

	mainPart := f.buildCompanionMainPart(vis)
	companionPart := f.buildCompanionSubPart(vis)
	cycle := f.companionCycleText(vis)

	right2 := mainPart + " " + ansi.Fg("#8b949e") + "|" + ansi.Reset + " " + companionPart + cycle
	if f.data.WorkflowActive {
		right2 = ansi.Fg("#d29922") + "⟡ workflow" + ansi.Reset + " " + right2
	}
	return right2
}

func (f *Footer) buildCompanionMainPart(vis companionVis) string {
	mainModel := f.data.Model
	if !vis.showProvider {
		mainModel = stripProviderPrefixOrOriginal(mainModel)
	}
	mainLevel := ""
	if vis.showThinking {
		mainLevel = f.data.ThinkingLevel
	}
	mainActive := !f.data.CompanionBusy
	return f.formatModelPart(mainModel, mainLevel, f.data.MainActivity, f.data.ModelBusy, mainActive)
}

func (f *Footer) buildCompanionSubPart(vis companionVis) string {
	companionDisplay := f.data.CompanionModel
	if companionDisplay == "" {
		companionDisplay = f.data.Model
	}
	companionDisplay = f.applyCompanionProviderPrefix(companionDisplay, vis.showProvider)
	if vis.showCompanionLabel {
		companionDisplay += " (companion)"
	}
	compLevel := ""
	if vis.showThinking {
		compLevel = f.data.CompanionThinkingLevel
	}
	return f.formatModelPart(companionDisplay, compLevel, f.data.CompanionActivity, f.data.CompanionBusy, f.data.CompanionBusy)
}

func (f *Footer) applyCompanionProviderPrefix(companionDisplay string, showProvider bool) string {
	if !showProvider {
		return stripProviderPrefixOrOriginal(companionDisplay)
	}
	if f.data.Provider != "" && !strings.Contains(companionDisplay, "(") {
		return "(" + f.data.Provider + ") " + companionDisplay
	}
	return companionDisplay
}

func stripProviderPrefixOrOriginal(model string) string {
	if s := stripProviderPrefix(model); s != "" {
		return s
	}
	return model
}

func (f *Footer) companionCycleText(vis companionVis) string {
	if !vis.showCycle || f.data.CompanionCycleMax <= 0 {
		return ""
	}
	return fmt.Sprintf(" [%d/%d]", f.data.CompanionCycleCount, f.data.CompanionCycleMax)
}

// formatModelPart renders a model name with busy indicator and highlight.
// The busy indicator shows an animated spinner frame when available, or a
// static dot when idle. Activity text ("thinking", "tool", etc.) is shown
// alongside the model name when the model is busy.
func (f *Footer) formatModelPart(model, level, activity string, busy, active bool) string {
	busyPrefix := ""
	if busy {
		busyPrefix = ansi.Fg("#d29922") + "⟳ " + ansi.Reset
	}
	color := ansi.Faint
	if active {
		color = ansi.Fg("#3fb950")
	}
	part := busyPrefix + color + model + ansi.Reset + ansi.Fg("#8b949e")
	if activity != "" && busy {
		part += " " + ansi.Fg("#d29922") + activity + ansi.Reset + ansi.Fg("#8b949e")
	}
	return appendThinkingLevel(part, level)
}

func (f *Footer) modeColor() string {
	switch f.data.Mode {
	case "yolo":
		return "#3fb950"
	case "solo":
		return "#58a6ff"
	case "confirm":
		return "#d29922"
	case "review":
		return "#f85149"
	default:
		return "#8b949e"
	}
}

func (f *Footer) formatGoalStatus(fg string) string {
	status := f.data.GoalStatus
	color := ansi.Fg(ansiColorForGoalStatus(status))
	obj := f.data.GoalObjective
	if obj == "" {
		return color + "⟐ goal" + ansi.Reset + fg
	}
	return color + "⟐ " + truncateToWidth(obj, 30, "") + statusSuffix(status) + ansi.Reset + fg
}

func statusSuffix(status string) string {
	switch status {
	case "active":
		return ""
	case "paused", "blocked":
		return " (" + status + ")"
	default:
		return ""
	}
}

func ansiColorForGoalStatus(status string) string {
	switch status {
	case "active":
		return "#58a6ff"
	case "paused":
		return "#8b949e"
	case "blocked":
		return "#d29922"
	default:
		return "#8b949e"
	}
}
