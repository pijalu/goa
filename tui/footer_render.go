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
	right1 := fmt.Sprintf("%s %s %s", f.goalProfileLabel(fg), ansi.BoxVertical, modeBadge)
	line1 := renderTwoCol(workdir, right1, width, styler)

	// Line 2: conversation stats / activity / steering hint (left) / model +
	// workflow hint (right). Goal detail is deliberately NOT rendered here:
	// the goal bubble is the dedicated chrome for objective/status/todos —
	// the footer carries only the ◈ active-goal marker on line 1.
	left2 := f.buildLeftSide(fg)

	// Build the model side for the space that actually remains — never for a
	// hypothetical minimum: claiming 30 columns that do not exist is what made
	// the ladder cut the model name instead of the fields above it (B11).
	minPad := 2
	availW := width - visibleWidth(left2) - minPad
	if availW < 0 {
		availW = 0
	}

	right2 := f.buildModelDisplay(fg, availW)
	right2 = f.appendPluginSegments(right2, fg)

	// Width ladder (B11): drop the lowest-value field first until the line
	// fits. The model name is ellipsized rather than cut, and the result can
	// never be wider than the terminal.
	left2, right2 = f.fitStatusLine(left2, right2, width, fg)

	line2 := renderTwoCol(left2, right2, width, styler)

	lines := []string{styler(line1), styler(line2)}
	lines = append(lines, f.renderOrchStatsLines(width, styler)...)
	lines = append(lines, f.renderAgentStatsLine(width, styler)...)
	return lines
}

// renderAgentStatsLine renders the ACTIVE multi-agent tab's stat line (T5) as
// one extra footer line in the status color. It returns nil when no line is
// set so the idle footer stays exactly its chrome rows.
func (f *Footer) renderAgentStatsLine(width int, styler func(string) string) []string {
	s := strings.TrimSpace(f.data.AgentTabStats)
	if s == "" {
		return nil
	}
	if vw := visibleWidth(s); vw > width {
		s = truncateToWidth(s, width, "")
	}
	return []string{styler(s)}
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

// appendWithSep appends s to base with a vertical-bar separator, or returns s when
// base is empty.
func appendWithSep(base, s string) string {
	if base == "" {
		return s
	}
	return base + " " + ansi.BoxVertical + " " + s
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
	// A line wider than the terminal is clipped by the screen mid-glyph, so the
	// right side is trimmed here as the final guarantee (the caller's ladder has
	// already dropped everything droppable).
	if leftW+rightW+1 > width {
		right = truncateToWidth(right, maxInt(width-leftW-1, 0), ellipsisGlyph)
		rightW = visibleWidth(right)
	}
	pad := width - leftW - rightW
	if pad < 1 {
		pad = 1
	}
	bar := left + strings.Repeat(" ", pad) + right
	vw := visibleWidth(bar)
	if vw > width {
		bar = truncateToWidth(bar, width, "")
		vw = visibleWidth(bar)
	}
	if vw < width {
		bar += strings.Repeat(" ", width-vw)
	}
	return bar
}

func (f *Footer) buildModelDisplay(fg string, availWidth int) string {
	// A delegated sub-agent actively streaming takes priority: show its real
	// provider/model so the status bar reflects what is actually running
	// (team bug #3). This applies in any mode, not just companion minor-mode.
	if f.data.ActiveAgentModel != "" {
		return f.buildActiveAgentDisplay(availWidth)
	}
	if f.data.MinorMode == "companion" {
		return f.buildCompanionModelDisplay(fg, availWidth)
	}
	return f.buildMainModelDisplay(fg, availWidth)
}

// buildActiveAgentDisplay renders the status-bar model section for the
// sub-agent currently streaming via delegate_to/companion. It shows the main
// model dimmed alongside the active agent's provider/model, with the agent's
// role color-coded (matching the chat widget's color-coded first column).
func (f *Footer) buildActiveAgentDisplay(availWidth int) string {
	// Main model (context) on the left of the separator.
	mainModel := f.data.Model
	if availWidth <= 40 {
		if s := stripProviderPrefix(mainModel); s != "" {
			mainModel = s
		}
	}
	mainPart := FormatModelPart(mainModel, "", f.data.MainActivity, f.data.ModelBusy, false, peakStatusForProvider(f.data.Provider, time.Now()))

	// Active agent part: color the role label to match the chat widget.
	role := f.data.ActiveAgentRole
	if role == "" {
		role = "agent"
	}
	agentModel := f.data.ActiveAgentModel
	if f.data.ActiveAgentProvider != "" && availWidth > 45 {
		agentModel = "(" + f.data.ActiveAgentProvider + ") " + agentModel
	}
	label := ansi.Fg(hashColor(role)) + role + ansi.Reset + " "
	agentPart := label + FormatModelPart(agentModel, "", f.data.CompanionActivity, true, true, peakStatusForProvider(f.data.ActiveAgentProvider, time.Now()))

	return mainPart + " " + ansi.Fg("#8b949e") + "|" + ansi.Reset + " " + agentPart
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
// availWidth is the space left of the terminal after the stats side.
//
// The provider prefix and thinking badge are rendered whenever they exist: the
// width decision belongs to the B11 ladder (fitStatusLine), which drops them in
// priority order — provider first, thinking only alongside the model identity —
// instead of an independent width threshold dropping the provider before
// fields that rank below it.
func (f *Footer) buildMainModelDisplay(fg string, availWidth int) string {
	var right2 string
	if f.data.Model != "" {
		part := FormatModelPart(f.data.Model, f.data.ThinkingLevel, f.data.MainActivity, f.data.ModelBusy, true, peakStatusForProvider(f.data.Provider, time.Now()))
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

// fitStatusLine applies the B11 width ladder: while the assembled status line
// does not fit, the least valuable field is dropped, in the order documented in
// bugs.md (amount → provider → ↑/↓ → tool count → token speed → last CH → avg
// CH), then the model name is ellipsized, and only as a last resort is the
// result hard-truncated — the quota figure is the last survivor and a line is
// never wider than the terminal (a wider line is clipped mid-glyph by the
// screen, which is exactly the reported defect).
func (f *Footer) fitStatusLine(left, right string, width int, fg string) (string, string) {
	leftSegments := f.widthLadderSegments()
	fits := func() bool { return visibleWidth(left)+1+visibleWidth(right) <= width }

	for _, drop := range []func(){
		func() { leftSegments = dropFooterTier(leftSegments, FooterTierAmount) },
		func() { right = f.stripProviderPrefixes(right) },
		func() { leftSegments = dropFooterTier(leftSegments, FooterTierTokens) },
		func() { leftSegments = dropFooterTier(leftSegments, FooterTierTools) },
		func() { leftSegments = dropFooterTier(leftSegments, FooterTierSpeed) },
		func() { leftSegments = dropFooterTier(leftSegments, FooterTierLastCH) },
		func() { leftSegments = dropFooterTier(leftSegments, FooterTierAvgCH) },
		// Tier 8 decorations, removed before the model name is shortened: the
		// plugin contributions, the companion label, the thinking badge, the
		// companion cycle count and the activity word.
		func() { right = f.stripPluginSegments(right) },
		func() { right = f.stripCompanionLabel(right) },
		func() { right = f.stripThinkingLevels(right) },
		func() { right = f.stripCycleCount(right) },
		func() { right = f.stripActivityText(right) },
	} {
		if fits() {
			break
		}
		drop()
		if leftSegments != nil {
			left = joinFooterSegments(leftSegments)
		}
	}
	if fits() {
		return left, right
	}
	// Last identity-preserving step: shorten the model name instead of losing it.
	right = ellipsizeModelName(right, width-visibleWidth(left)-1)
	if fits() {
		return left, right
	}
	// Nothing droppable is left (a very narrow terminal): truncate the model
	// side to the remaining room and keep the line inside the viewport.
	right = truncateToWidth(right, maxInt(width-visibleWidth(left)-1, 0), ellipsisGlyph)
	return left, right
}

// ellipsisGlyph is the single-cell marker used when a status-line field must be
// shortened rather than dropped.
const ellipsisGlyph = "\u2026"

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// widthLadderSegments returns the line-2 left side split into droppable
// segments. Callers that provide only the pre-joined Stats string get a single
// never-dropped segment, so their behaviour is unchanged.
func (f *Footer) widthLadderSegments() []FooterSegment {
	if len(f.data.StatsSegments) > 0 {
		return append([]FooterSegment(nil), f.data.StatsSegments...)
	}
	if f.data.Stats == "" {
		return nil
	}
	return []FooterSegment{{Tier: FooterTierQuota, Text: f.data.Stats}}
}

// dropFooterTier removes every segment of one tier, returning nil when the
// input was nil (the "no tiered segments available" case, where the ladder must
// not substitute anything for the caller's own string).
func dropFooterTier(segments []FooterSegment, tier int) []FooterSegment {
	if segments == nil {
		return nil
	}
	kept := segments[:0]
	for _, s := range segments {
		if s.Tier != tier {
			kept = append(kept, s)
		}
	}
	return kept
}

// ellipsizeModelName shortens the model name inside an already-styled model
// display to maxWidth, keeping the ANSI-free text readable and the result
// marked as shortened ("deepseek-v4.…") instead of cut mid-word.
func ellipsizeModelName(right string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if visibleWidth(right) <= maxWidth {
		return right
	}
	return truncateToWidth(right, maxWidth, ellipsisGlyph)
}

// compactRightSide was the pre-B11 right-side-only compaction ladder. It is
// gone: fitStatusLine now drops fields from BOTH sides in one priority order
// (bugs.md B11), so there is a single place that decides what a narrow terminal
// gives up. The individual strip steps it used are still the ladder's tier-8
// actions.

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
// todo follows it (up to 3, then +(n-3)) — ◈◈◈ coding-posture ⬩⬩⬩+2 │ YOLO.
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
