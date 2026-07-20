// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/agentic/provider/models"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/usage"
)

// UsageCommand implements /usage: a cross-session usage view (like
// opencode-stats) read from the global SQLite store. Supports global,
// per-project, per-provider, and per-model aggregations with time ranges,
// per-model cost estimates, colored split bars, and an activity heatmap.
type UsageCommand struct {
	// OpenStore opens the usage store; nil uses usage.Open(usage.DefaultPath()).
	// Injectable for tests.
	OpenStore func() (usageStore, error)
	// ProjectDir is the current project; used to label the per-project view.
	ProjectDir string
	// CostLookup resolves per-1M-token USD rates for a model id; nil uses the
	// built-in model registry. Injectable for tests.
	CostLookup func(model string) (ModelPricing, bool)
	// Now returns the current time; nil uses time.Now. Injectable for tests.
	Now func() time.Time
}

// ModelPricing holds USD rates per 1M tokens for one model.
type ModelPricing struct {
	Input, Output, CacheRead, CacheWrite float64
}

// usageStore is the subset of *usage.Store the command needs (for tests).
type usageStore interface {
	Query(dim usage.Dimension, project string, since time.Time) ([]usage.Stat, error)
	Sum(project string, since time.Time) (usage.Stat, error)
	DailyCounts(project string, days int) ([]usage.DayCount, error)
	Close() error
}

func (c *UsageCommand) Name() string      { return "usage" }
func (c *UsageCommand) IsInternal() bool  { return false }
func (c *UsageCommand) Aliases() []string { return nil }
func (c *UsageCommand) ShortHelp() string {
	return "Usage stats: tokens, cost, split graphs, activity — /usage [7d|30d|today] [project|provider|model|here|cost|activity]"
}
func (c *UsageCommand) LongHelp() string { return help.LongHelp(c.Name()) }
func (c *UsageCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	candidates := []core.ArgCompletion{
		{Value: "7d", Description: "restrict to the last 7 days"},
		{Value: "30d", Description: "restrict to the last 30 days"},
		{Value: "today", Description: "restrict to today"},
		{Value: "project", Description: "per-project breakdown only"},
		{Value: "provider", Description: "per-provider breakdown only"},
		{Value: "model", Description: "per-model breakdown only"},
		{Value: "here", Description: "this project only"},
		{Value: "cost", Description: "per-model cost estimates"},
		{Value: "activity", Description: "365-day activity heatmap"},
	}
	var out []core.ArgCompletion
	for _, cand := range candidates {
		if prefix == "" || strings.HasPrefix(cand.Value, prefix) {
			out = append(out, cand)
		}
	}
	return out
}

func (c *UsageCommand) Run(ctx core.Context, args []string) error {
	open := c.OpenStore
	if open == nil {
		open = defaultStoreOpener
	}
	st, err := open()
	if err != nil {
		ctx.Writef("usage: cannot open store: %v\n", err)
		return nil
	}
	defer st.Close()

	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	project := c.ProjectDir
	if project == "" {
		project = ctx.ProjectDir
	}
	req := parseUsageArgs(args, now(), project)

	var b strings.Builder
	switch {
	case req.unknown != "":
		b.WriteString("Unknown /usage argument: " + req.unknown + "\n")
		b.WriteString("Usage: /usage [7d|30d|today] [project|provider|model|here] | /usage cost | /usage activity\n")
	case req.activity:
		c.writeActivity(&b, st, req)
	case req.cost:
		c.writeCost(&b, st, req)
	default:
		c.writeStats(&b, st, req)
	}
	ctx.Writef("%s", b.String())
	return nil
}

// usageRequest is the parsed form of /usage arguments.
type usageRequest struct {
	since    time.Time // zero = all time
	rangeTag string    // "", "last 7 days", "last 30 days", "today"
	project  string    // "" = global
	dim      usage.Dimension
	dimOnly  bool // show only req.dim's section
	cost     bool
	activity bool
	unknown  string
}

func parseUsageArgs(args []string, now time.Time, project string) usageRequest {
	var req usageRequest
	for _, a := range args {
		switch strings.ToLower(a) {
		case "", "all":
		case "7d":
			req.since, req.rangeTag = now.AddDate(0, 0, -7), "last 7 days"
		case "30d":
			req.since, req.rangeTag = now.AddDate(0, 0, -30), "last 30 days"
		case "today":
			y, m, d := now.Date()
			req.since, req.rangeTag = time.Date(y, m, d, 0, 0, 0, 0, now.Location()), "today"
		case "here", "this":
			req.project = project
		case "project":
			req.dim, req.dimOnly = usage.ByProject, true
		case "provider":
			req.dim, req.dimOnly = usage.ByProvider, true
		case "model":
			req.dim, req.dimOnly = usage.ByModel, true
		case "cost":
			req.cost = true
		case "activity", "heatmap":
			req.activity = true
		default:
			req.unknown = a
		}
	}
	return req
}

func (c *UsageCommand) writeStats(b *strings.Builder, st usageStore, req usageRequest) {
	if req.dimOnly {
		writeUsageSection(b, st, req.dim, req, sectionTitle(req.dim))
		return
	}
	writeUsageGlobal(b, st, req)
	writeUsageSection(b, st, usage.ByProject, req, sectionTitle(usage.ByProject))
	writeUsageSection(b, st, usage.ByProvider, req, sectionTitle(usage.ByProvider))
	writeUsageSection(b, st, usage.ByModel, req, sectionTitle(usage.ByModel))
}

func sectionTitle(dim usage.Dimension) string {
	switch dim {
	case usage.ByProvider:
		return "By provider"
	case usage.ByModel:
		return "By model"
	default:
		return "By project"
	}
}

// scopedTag returns a "(last 30 days · this project)" style suffix.
func scopedTag(req usageRequest) string {
	var parts []string
	if req.rangeTag != "" {
		parts = append(parts, req.rangeTag)
	}
	if req.project != "" {
		parts = append(parts, "this project")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, " · ") + ")"
}

func writeUsageGlobal(b *strings.Builder, st usageStore, req usageRequest) {
	sum, err := st.Sum(req.project, req.since)
	if err != nil {
		fmt.Fprintf(b, "usage: %v\n", err)
		return
	}
	label := "Global usage (all projects)"
	if req.project != "" {
		label = "Usage for " + req.project
	}
	fmt.Fprintf(b, "## %s%s\n\n", label, scopedTag(req))
	if sum.Turns == 0 {
		b.WriteString("No usage recorded for this range.\n\n")
		return
	}
	fmt.Fprintf(b, "Turns: %d   Input: %s   Output: %s   Total: %s   Cache: %s read / %s write\n\n",
		sum.Turns, humanTokens(sum.PromptN), humanTokens(sum.PredictedN), humanTokens(sum.Total()),
		humanTokens(sum.CacheRead), humanTokens(sum.CacheWrite))
}

func writeUsageSection(b *strings.Builder, st usageStore, dim usage.Dimension, req usageRequest, title string) {
	rows, err := st.Query(dim, req.project, req.since)
	if err != nil {
		fmt.Fprintf(b, "usage: %v\n", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	total := 0
	for _, r := range rows {
		total += r.Total()
	}
	fmt.Fprintf(b, "### %s%s\n\n", title, scopedTag(req))
	b.WriteString("| Name | Turns | Input | Output | Total | Share |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, r := range rows {
		fmt.Fprintf(b, "| %s | %d | %s | %s | %s | %d%% |\n",
			r.Key, r.Turns, humanTokens(r.PromptN), humanTokens(r.PredictedN), humanTokens(r.Total()),
			sharePct(r.Total(), total))
	}
	b.WriteString("\n")
	writeSplitBar(b, tokenSegments(rows))
}

// writeCost renders the /usage cost view: per-model USD estimates using the
// built-in pricing registry (same per-1M bucket math as the footer cost).
func (c *UsageCommand) writeCost(b *strings.Builder, st usageStore, req usageRequest) {
	rows, err := st.Query(usage.ByModel, req.project, req.since)
	if err != nil {
		fmt.Fprintf(b, "usage: %v\n", err)
		return
	}
	lookup := c.CostLookup
	if lookup == nil {
		lookup = defaultCostLookup
	}
	fmt.Fprintf(b, "## Cost by model%s\n\n", scopedTag(req))
	if len(rows) == 0 {
		b.WriteString("No usage recorded for this range.\n\n")
		return
	}

	type costRow struct {
		stat usage.Stat
		cost float64
	}
	var priced []costRow
	var unpriced []string
	grand := 0.0
	for _, r := range rows {
		p, ok := lookup(r.Key)
		if !ok {
			unpriced = append(unpriced, r.Key)
			continue
		}
		cost := costOf(r, p)
		grand += cost
		priced = append(priced, costRow{stat: r, cost: cost})
	}

	b.WriteString("| Model | Turns | Total tokens | Cost | Share |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: |\n")
	for _, pr := range priced {
		fmt.Fprintf(b, "| %s | %d | %s | %s | %d%% |\n",
			pr.stat.Key, pr.stat.Turns, humanTokens(pr.stat.Total()), formatUSD(pr.cost),
			sharePct(int(math.Round(pr.cost*100)), int(math.Round(grand*100))))
	}
	for _, key := range unpriced {
		for _, r := range rows {
			if r.Key == key {
				fmt.Fprintf(b, "| %s | %d | %s | — | — |\n", r.Key, r.Turns, humanTokens(r.Total()))
			}
		}
	}
	fmt.Fprintf(b, "\n**Total (priced models): %s**\n\n", formatUSD(grand))

	var segs []barSegment
	for i, pr := range priced {
		segs = append(segs, barSegment{
			label: pr.stat.Key,
			value: int(math.Round(pr.cost * 100)), // cents — only ratios matter
			color: usagePalette[i%len(usagePalette)],
		})
	}
	writeSplitBar(b, segs)
	if len(unpriced) > 0 {
		fmt.Fprintf(b, "_No pricing data for: %s. Set pricing in /config to include them._\n\n",
			strings.Join(unpriced, ", "))
	}
}

// writeActivity renders a GitHub-style activity heatmap over the last year.
func (c *UsageCommand) writeActivity(b *strings.Builder, st usageStore, req usageRequest) {
	days, err := st.DailyCounts(req.project, 371)
	if err != nil {
		fmt.Fprintf(b, "usage: %v\n", err)
		return
	}
	label := "Activity (all projects)"
	if req.project != "" {
		label = "Activity for " + req.project
	}
	fmt.Fprintf(b, "## %s\n\n", label)
	renderHeatmap(b, days)
}

// --- split bar ---

// barSegment is one colored slice of a stacked share bar. value is any
// proportional quantity (tokens, cents); only ratios matter.
type barSegment struct {
	label string
	value int
	color string
}

// usagePalette assigns stable, theme-independent colors to split-bar segments
// (Tokyo Night accents — readable on both dark and light terminals).
var usagePalette = []string{
	"#7aa2f7", "#bb9af7", "#7dcfff", "#9ece6a",
	"#e0af68", "#ff9e64", "#f7768e", "#73daca",
}

const otherColor = "#6b7280"

// tokenSegments maps usage rows (already sorted desc by total) onto colored
// bar segments, folding everything past the palette into one "other" slice.
func tokenSegments(rows []usage.Stat) []barSegment {
	segs := make([]barSegment, 0, len(usagePalette)+1)
	for i, r := range rows {
		if i < len(usagePalette) {
			segs = append(segs, barSegment{label: r.Key, value: r.Total(), color: usagePalette[i]})
			continue
		}
		if len(segs) == len(usagePalette) {
			segs = append(segs, barSegment{label: "other", color: otherColor})
		}
		segs[len(segs)-1].value += r.Total()
	}
	return segs
}

// writeSplitBar renders a stacked colored bar plus a legend with per-segment
// percentages. Colors are raw SGR sequences; the legend carries the same
// percentages textually so the output stays readable if a renderer strips
// colors.
func writeSplitBar(b *strings.Builder, segs []barSegment) {
	total := 0
	for _, s := range segs {
		total += s.value
	}
	if total <= 0 {
		return
	}
	const width = 36
	cells := 0
	for _, s := range segs {
		w := int(math.Round(float64(s.value) / float64(total) * width))
		if w < 1 {
			w = 1
		}
		if cells+w > width {
			w = width - cells
		}
		if w <= 0 {
			break
		}
		cells += w
		b.WriteString(ansi.Fg(s.color))
		b.WriteString(strings.Repeat("█", w))
	}
	b.WriteString(ansi.Reset)
	b.WriteString("\n")
	for _, s := range segs {
		fmt.Fprintf(b, "%s■%s %s %d%%\n", ansi.Fg(s.color), ansi.Reset, s.label,
			sharePct(s.value, total))
	}
	// Two trailing newlines: one ends the last legend line, one keeps an
	// empty line between the graph and the next section.
	b.WriteString("\n\n")
}

// --- heatmap ---

// heatColors maps intensity levels 0–4 onto a green scale (GitHub-style);
// level 0 is a dim gray for days with no recorded activity.
var heatColors = []string{"#3a3a3a", "#0e4429", "#006d32", "#26a641", "#39d353"}

// renderHeatmap draws a 7-row (Mon–Sun) × N-week grid of colored cells.
func renderHeatmap(b *strings.Builder, days []usage.DayCount) {
	if len(days) == 0 {
		b.WriteString("No usage recorded yet.\n\n")
		return
	}
	maxTok := 0
	active := 0
	for _, d := range days {
		if d.Tokens > maxTok {
			maxTok = d.Tokens
		}
		if d.Turns > 0 {
			active++
		}
	}
	// Align the grid start back to Monday so columns are whole weeks.
	start := days[0].Day
	for start.Weekday() != time.Monday {
		start = start.AddDate(0, 0, -1)
	}
	offset := int(days[0].Day.Sub(start).Hours() / 24)
	weeks := (offset + len(days) + 6) / 7

	labels := []string{"Mon", "   ", "Wed", "   ", "Fri", "   ", "   "}
	for row := 0; row < 7; row++ {
		b.WriteString(labels[row])
		b.WriteString(" ")
		for w := 0; w < weeks; w++ {
			dayIdx := w*7 + row - offset
			cell := heatColors[0]
			if dayIdx >= 0 && dayIdx < len(days) {
				cell = heatColors[heatLevel(days[dayIdx].Tokens, maxTok)]
			}
			b.WriteString(ansi.Fg(cell))
			b.WriteString("█")
		}
		b.WriteString(ansi.Reset)
		b.WriteString("\n")
	}
	b.WriteString("\nLess ")
	for _, hc := range heatColors {
		b.WriteString(ansi.Fg(hc) + "█")
	}
	b.WriteString(ansi.Reset + " More")
	fmt.Fprintf(b, "   %d active days · %s → %s\n\n",
		active, days[0].Day.Format("2006-01-02"), days[len(days)-1].Day.Format("2006-01-02"))
}

// heatLevel buckets a day's tokens into 0–4 relative to the max day.
func heatLevel(tokens, maxTok int) int {
	if tokens <= 0 || maxTok <= 0 {
		return 0
	}
	switch pct := float64(tokens) / float64(maxTok); {
	case pct <= 0.25:
		return 1
	case pct <= 0.50:
		return 2
	case pct <= 0.75:
		return 3
	default:
		return 4
	}
}

// --- cost helpers ---

func defaultCostLookup(model string) (ModelPricing, bool) {
	m := models.GetModel(model)
	if m == nil {
		return ModelPricing{}, false
	}
	c := m.Cost
	if c.Input == 0 && c.Output == 0 && c.CacheRead == 0 && c.CacheWrite == 0 {
		return ModelPricing{}, false
	}
	return ModelPricing{Input: c.Input, Output: c.Output, CacheRead: c.CacheRead, CacheWrite: c.CacheWrite}, true
}

// costOf mirrors computeCost in internal/app/stats.go: each bucket charged at
// its own per-1M rate.
func costOf(s usage.Stat, p ModelPricing) float64 {
	return float64(s.PromptN)/1e6*p.Input +
		float64(s.PredictedN)/1e6*p.Output +
		float64(s.CacheRead)/1e6*p.CacheRead +
		float64(s.CacheWrite)/1e6*p.CacheWrite
}

func formatUSD(v float64) string {
	switch {
	case v >= 1000:
		return fmt.Sprintf("$%.1fK", v/1000)
	case v >= 1:
		return fmt.Sprintf("$%.2f", v)
	case v > 0:
		return fmt.Sprintf("$%.4f", v)
	default:
		return "$0"
	}
}

func sharePct(part, total int) int {
	if total <= 0 {
		return 0
	}
	return int(math.Round(float64(part) / float64(total) * 100))
}

// --- shared ---

func defaultStoreOpener() (usageStore, error) {
	p, err := usage.DefaultPath()
	if err != nil {
		return nil, err
	}
	return usage.Open(p)
}

// humanTokens formats a token count compactly (1.2K, 3.4M).
func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
