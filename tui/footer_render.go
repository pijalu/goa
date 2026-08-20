// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/ansi"
)

// Render renders two status lines with adaptive width. During orchestration,
// the chrome is replaced by one line per active agent (each carrying its own
// model and role context).
//
// The footer height is intentionally NOT padded to a constant value: the chat
// viewport is the layout fill and absorbs any chrome-height change, so the
// total canvas height stays == terminal height and the compositor updates the
// shifted rows differentially (no full redraw). Padding the footer with a
// blank line when idle would instead waste the bottom terminal row forever.
func (f *Footer) Render(width int) []string {
	if width <= 0 {
		return nil
	}

	fg := ansi.Fg("#8b949e")
	// styler wraps a line with the status-line foreground color only, using
	// the terminal's default background. The footer's layout provides enough
	// visual boundary without a dedicated background.
	styler := func(s string) string { return fg + s + ansi.Reset }

	// Orchestration mode: render only the per-agent lines.
	if f.data.OrchestrationStats != "" {
		return f.renderOrchStatsLines(width, styler)
	}

	// Line 1: working directory (left) / [◈ active-goal marker] profile(minor) + mode badge (right)
	workdir := f.formatWorkdirAdaptive(width)
	modeBadge := ansi.Fg(f.modeColor()) + strings.ToUpper(f.data.Mode) + ansi.Reset + fg
	right1 := fmt.Sprintf("%s │ %s", f.goalProfileLabel(fg), modeBadge)
	line1 := renderTwoCol(workdir, right1, width, styler)

	// Line 2: conversation stats / activity / steering hint (left) / model +
	// workflow hint (right). Goal detail is deliberately NOT rendered here:
	// the goal bubble is the dedicated chrome for objective/status/todos —
	// the footer carries only the ◈ active-goal marker on line 1.
	left2 := f.buildLeftSide(fg)

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
	right2 = f.appendPluginSegments(right2, fg)

	// If still doesn't fit — compact the right side by stripping lower-priority items
	if leftW+visibleWidth(right2)+minPad > width {
		targetW := width - leftW - minPad
		if targetW > 10 {
			right2 = f.compactRightSide(right2, fg, targetW)
		}
	}

	line2 := renderTwoCol(left2, right2, width, styler)

	lines := []string{styler(line1), styler(line2)}
	lines = append(lines, f.renderOrchStatsLines(width, styler)...)
	return lines
}

// renderOrchStatsLines renders the per-agent orchestration stats (one line per
// active model, newline-separated) in the footer's status color, each line
// fitted to the terminal width. It returns nil when no run is active so the
// idle footer is exactly its two chrome lines (no blank spacer: see Render).
func (f *Footer) renderOrchStatsLines(width int, styler func(string) string) []string {
	var out []string
	for _, raw := range strings.Split(f.data.OrchestrationStats, "\n") {
		ol := strings.TrimSpace(raw)
		if ol == "" {
			continue
		}
		if vw := visibleWidth(ol); vw > width {
			ol = truncateToWidth(ol, width, "")
		}
		out = append(out, styler(ol))
	}
	return out
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

// appendPluginSegments appends rendered plugin status-bar segments to the
// right (model) side, ordered by priority. Each segment is rendered as
// " • text" so it reads as a suffix of the model display and can be dropped
// cleanly by stripPluginSegments under width pressure. Empty texts are
// elided. Segment content is treated as trusted ANSI (plugins are trusted
// code) but measured with ANSI stripped for layout.
func (f *Footer) appendPluginSegments(right2, fg string) string {
	segs := f.sortedPluginSegments()
	for _, seg := range segs {
		if strings.TrimSpace(ansi.Strip(seg.Text)) == "" {
			continue
		}
		right2 += fg + " • " + ansi.Reset + seg.Text
	}
	return right2
}

// sortedPluginSegments returns a copy of the plugin segments ordered by
// priority (lower first), stable for equal priorities.
func (f *Footer) sortedPluginSegments() []PluginSegment {
	segs := make([]PluginSegment, len(f.data.PluginSegments))
	copy(segs, f.data.PluginSegments)
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].Priority < segs[j].Priority })
	return segs
}

// stripPluginSegments drops all appended plugin segments (the first
// compaction step) by cutting the right side at the last model content before
// the first plugin segment marker. It is a no-op when no segments are present.
func (f *Footer) stripPluginSegments(s string) string {
	if len(f.data.PluginSegments) == 0 {
		return s
	}
	// Segments were appended as " • <text>" after the model display; remove
	// from the first marker that introduces a plugin segment. We identify it
	// by matching each known segment text after a " • " separator.
	cut := len(s)
	for _, seg := range f.data.PluginSegments {
		text := strings.TrimSpace(ansi.Strip(seg.Text))
		if text == "" {
			continue
		}
		idx := strings.LastIndex(ansi.Strip(s), text)
		if idx >= 0 && idx < cut {
			cut = idx
		}
	}
	if cut == len(s) {
		return s
	}
	// Back off the trailing " • " separator that introduced the segment.
	out := s[:cut]
	if idx := strings.LastIndex(out, " • "); idx >= 0 {
		out = out[:idx]
	}
	return out
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
		part := FormatModelPart(modelName, level, f.data.MainActivity, f.data.ModelBusy, true, peakStatusForProvider(f.data.Provider, time.Now()))
		right2 = part
	} else {
		right2 = "no-model"
	}
	if f.data.Team != "" {
		badge := "⛃ " + f.data.Team
		if f.data.TeamDrifted {
			badge += "*"
		}
		right2 = ansi.Fg("#56d4dd") + badge + ansi.Reset + " " + right2
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
		f.stripPluginSegments,
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
	return FormatModelPart(mainModel, mainLevel, f.data.MainActivity, f.data.ModelBusy, mainActive, peakStatusForProvider(f.data.Provider, time.Now()))
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
	return FormatModelPart(companionDisplay, compLevel, f.data.CompanionActivity, f.data.CompanionBusy, f.data.CompanionBusy, peakStatusForProvider(f.data.Provider, time.Now()))
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

// modelColor resolves the model-name color from the active flag and the
// provider peak status: red inside a peak window, orange within the 5-minute
// grace margin around one, and otherwise green when active (faint when idle).
func modelColor(active bool, peak schema.PeakStatus) string {
	switch peak {
	case schema.PeakOn:
		return ansi.Fg("#f85149")
	case schema.PeakNear:
		return ansi.Fg("#d29922")
	}
	if active {
		return ansi.Fg("#3fb950")
	}
	return ansi.Faint
}

// peakStatusForProvider returns the peak status for the given provider ID at
// now. Unknown/empty provider IDs (and providers without peak windows) are
// always PeakOff so the color falls back to the plain active/idle scheme.
func peakStatusForProvider(providerID string, now time.Time) schema.PeakStatus {
	if def := schema.LookupProviderDefByID(providerID); def != nil {
		return def.PeakStatusAt(now)
	}
	return schema.PeakOff
}

// FormatModelPart renders a model name with busy indicator and highlight.
// It is the package-level shared formatter used by both the normal footer
// and the per-agent orchestration lines. The peak status colors the name red
// during the provider's peak window, orange in the 5-minute grace margin
// around it, and green (or faint when idle) otherwise.
func FormatModelPart(model, level, activity string, busy, active bool, peak schema.PeakStatus) string {
	busyPrefix := ""
	if busy {
		if frame := CurrentSpinnerFrame(); frame != "" {
			busyPrefix = ansi.Fg("#d29922") + frame + " " + ansi.Reset
		} else {
			busyPrefix = ansi.Fg("#d29922") + "⟳ " + ansi.Reset
		}
	}
	color := modelColor(active, peak)
	part := busyPrefix + color + model + ansi.Reset + ansi.Fg("#8b949e")
	if activity != "" && busy {
		part += " " + ansi.Fg("#d29922") + activity + ansi.Reset + ansi.Fg("#8b949e")
	}
	return appendThinkingLevel(part, level)
}

// FormatFooterLine builds one rich footer line combining pre-formatted stats
// and model metadata. It is used by the per-agent orchestration lines and
// shares the same FormatModelPart primitive as the normal footer so the
// styling (busy spinner, active green highlight, thinking badge, activity
// text) stays identical across both contexts (DRY/SOLID).
// The caller provides:
//   - stats: pre-formatted stats string (e.g. from formatFooterStats)
//   - model, provider: model display fields; provider is prepended only when
//     model does not already include a provider prefix
//   - thinking: thinking level badge ("" or "off" to omit)
//   - activity: "streaming", "thinking", "tool", etc. (shown after model when busy)
//   - busy: true → prepend animated spinner frame
//   - active: true → model is green (the signal for "this agent is in flight")
//
// Returns the full styled line (SGR-encoded), width-capped by the caller.
func FormatFooterLine(stats, model, provider, thinking, activity string, busy, active bool) string {
	return formatFooterLineAt(stats, model, provider, thinking, activity, busy, active, time.Now())
}

// formatFooterLineAt is the time-injectable core of FormatFooterLine so the
// peak-window coloring can be tested deterministically.
func formatFooterLineAt(stats, model, provider, thinking, activity string, busy, active bool, now time.Time) string {
	peak := peakStatusForProvider(provider, now)
	modelPart := FormatModelPart(model, thinking, activity, busy, active, peak)
	var b strings.Builder
	if stats != "" {
		b.WriteString(stats)
		b.WriteByte(' ')
	}
	b.WriteString("- ")
	if provider != "" && !strings.Contains(model, "(") && !strings.Contains(model, provider+"/") {
		b.WriteString(modelColor(active, peak) + "(" + provider + ") " + ansi.Reset)
	}
	b.WriteString(modelPart)
	return b.String()
}

// goalProfileLabel renders the line-1 right-side label: while a goal is
// ACTIVE, a ◈ goal-count marker prefixes the profile label (one ◈ per goal
// up to 3, then a numeric prefix — "◈", "◈◈◈", "25◈"), and one ⬩ per pending
// todo follows it (up to 3, then +(n-3)) — "◈◈◈ coding-posture ⬩⬩⬩+2 │ YOLO".
// The markers are the ONLY goal signal in the footer: objective, status and
// todo detail live in the dedicated goal bubble chrome, so no goal detail is
// duplicated here. The ◈ decoration marks an active goal ONLY: a
// paused/blocked goal must not read as "goal running". Without an active
// goal the label is the bare profile(minor) text.
func (f *Footer) goalProfileLabel(fg string) string {
	label := f.data.Profile
	if f.data.MinorMode != "" {
		label = fmt.Sprintf("%s(%s)", f.data.Profile, f.data.MinorMode)
	}
	if f.data.GoalStatus != "active" {
		return label
	}
	sign := goalCountMarkers(f.data.GoalCount)
	if sign == "" {
		sign = "◈" // an active goal without a recorded count still marks
	}
	marked := ansi.Fg(TheTheme.ColorHex("tool_success")) + sign + ansi.Reset + fg + " " + label
	if todos := goalTodoMarkers(f.data.GoalPendingTodos); todos != "" {
		marked += " " + todos
	}
	return marked
}

// goalCountMarkers renders the goal-count sign with the same shape as the
// todo markers: one ◈ per goal (max 3 glyphs), then a numeric prefix for the
// overflow — 1 → "◈", 3 → "◈◈◈", 25 → "25◈".
func goalCountMarkers(n int) string {
	if n <= 0 {
		return ""
	}
	if n > 3 {
		return fmt.Sprintf("%d◈", n)
	}
	return strings.Repeat("◈", n)
}

// goalTodoMarkers renders one ⬩ per pending todo (max 3), with a +n counter
// for the overflow beyond the glyphs shown: 2 → "⬩⬩", 5 → "⬩⬩⬩+2".
func goalTodoMarkers(n int) string {
	if n <= 0 {
		return ""
	}
	shown := min(n, 3)
	markers := strings.Repeat("⬩", shown)
	if n > shown {
		markers += fmt.Sprintf("+%d", n-shown)
	}
	return markers
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
