// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/usage"
)

// fakeUsageStore implements usageStore for tests.
type fakeUsageStore struct {
	stats     map[string][]usage.Stat // key: dim|project
	sum       usage.Stat
	daily     []usage.DayCount
	lastSince time.Time
}

func (f *fakeUsageStore) Query(dim usage.Dimension, project string, since time.Time) ([]usage.Stat, error) {
	f.lastSince = since
	return f.stats[dimKey(dim, project)], nil
}
func (f *fakeUsageStore) Sum(project string, since time.Time) (usage.Stat, error) {
	f.lastSince = since
	return f.sum, nil
}
func (f *fakeUsageStore) DailyCounts(project string, days int) ([]usage.DayCount, error) {
	return f.daily, nil
}
func (f *fakeUsageStore) Close() error { return nil }

func dimKey(d usage.Dimension, project string) string {
	return string(rune('0'+int(d))) + "|" + project
}

func newUsageCtx(buf *strings.Builder, project string) core.Context {
	return core.Context{OutputBuffer: buf, ProjectDir: project}
}

// TestUsageCommand_CacheWriteHiddenWhenZero covers bugs.md "Stats: cache
// write is always 0": OpenAI-style/local providers never report cache writes
// (only Anthropic does), so the summary line and the Cache R/W column must
// drop the write half when it is 0 — and keep it when real writes exist.
func TestUsageCommand_CacheWriteHiddenWhenZero(t *testing.T) {
	t.Run("hidden when zero", func(t *testing.T) {
		var buf strings.Builder
		store := &fakeUsageStore{
			sum: usage.Stat{Turns: 2, PromptN: 100, PredictedN: 50, CacheRead: 4000, CacheWrite: 0},
			stats: map[string][]usage.Stat{
				dimKey(usage.ByModel, ""): {{Key: "local-model", Turns: 2, PromptN: 100, PredictedN: 50, CacheRead: 4000, CacheWrite: 0}},
			},
		}
		cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
		if err := cmd.Run(newUsageCtx(&buf, "/a"), nil); err != nil { // global view: summary + sections
			t.Fatalf("Run: %v", err)
		}
		out := buf.String()
		if strings.Contains(out, "write") {
			t.Fatalf("cache write must be hidden when 0:\n%s", out)
		}
		if !strings.Contains(out, "Cache: 4.0K read") {
			t.Fatalf("summary must keep the read half:\n%s", out)
		}
	})

	t.Run("shown when non-zero", func(t *testing.T) {
		var buf strings.Builder
		store := &fakeUsageStore{
			sum: usage.Stat{Turns: 2, PromptN: 100, PredictedN: 50, CacheRead: 4000, CacheWrite: 1500},
			stats: map[string][]usage.Stat{
				dimKey(usage.ByModel, ""): {{Key: "claude", Turns: 2, PromptN: 100, PredictedN: 50, CacheRead: 4000, CacheWrite: 1500}},
			},
		}
		cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
		if err := cmd.Run(newUsageCtx(&buf, "/a"), nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "Cache: 4.0K read / 1.5K write") {
			t.Fatalf("summary must show read/write when writes exist:\n%s", out)
		}
		if !strings.Contains(out, "4.0K/1.5K") {
			t.Fatalf("table cell must show read/write when writes exist:\n%s", out)
		}
	})
}

func TestUsageCommand_GlobalShowsAllSections(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{
		sum: usage.Stat{Turns: 3, PromptN: 200, PredictedN: 100},
		stats: map[string][]usage.Stat{
			dimKey(usage.ByProject, ""):  {{Key: "/a", Turns: 2, PromptN: 150, PredictedN: 70}},
			dimKey(usage.ByProvider, ""): {{Key: "kimi", Turns: 3, PromptN: 200, PredictedN: 100}},
			dimKey(usage.ByModel, ""):    {{Key: "k2", Turns: 3, PromptN: 200, PredictedN: 100}},
		},
	}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Global usage", "By project", "By provider", "By model", "kimi", "k2", "/a"} {
		if !strings.Contains(out, want) {
			t.Errorf("global /usage missing %q:\n%s", want, out)
		}
	}
}

func TestUsageCommand_ScopeFiltersSections(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{
		sum: usage.Stat{Turns: 1, PromptN: 10, PredictedN: 5},
		stats: map[string][]usage.Stat{
			dimKey(usage.ByModel, ""): {{Key: "k2", Turns: 1, PromptN: 10, PredictedN: 5}},
		},
	}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"model"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "By model") {
		t.Errorf("/usage model missing model section:\n%s", out)
	}
	if strings.Contains(out, "By provider") || strings.Contains(out, "By project") {
		t.Errorf("/usage model should not show other sections:\n%s", out)
	}
}

func TestUsageCommand_HereScopesToProject(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{
		sum: usage.Stat{Turns: 2, PromptN: 50, PredictedN: 25},
		stats: map[string][]usage.Stat{
			dimKey(usage.ByProvider, "/a"): {{Key: "kimi", Turns: 2, PromptN: 50, PredictedN: 25}},
		},
	}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }, ProjectDir: "/a"}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"here"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Usage for /a") {
		t.Errorf("/usage here missing project label:\n%s", out)
	}
	if !strings.Contains(out, "kimi") {
		t.Errorf("/usage here missing provider row:\n%s", out)
	}
}

func TestUsageCommand_EmptyStoreMessage(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{sum: usage.Stat{Turns: 0}}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "No usage recorded") {
		t.Errorf("empty store should print guidance:\n%s", buf.String())
	}
}

func TestUsageCommand_UnknownScope(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"bogus"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "Unknown /usage argument") {
		t.Errorf("unknown scope should print usage hint:\n%s", buf.String())
	}
}

func TestUsageCommand_SevenDaysSetsSince(t *testing.T) {
	var buf strings.Builder
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.Local)
	store := &fakeUsageStore{sum: usage.Stat{Turns: 1, PromptN: 10, PredictedN: 5}}
	cmd := &UsageCommand{
		OpenStore: func() (usageStore, error) { return store, nil },
		Now:       func() time.Time { return now },
	}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"7d"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := now.AddDate(0, 0, -7)
	if !store.lastSince.Equal(want) {
		t.Errorf("since = %v, want %v", store.lastSince, want)
	}
	if !strings.Contains(buf.String(), "last 7 days") {
		t.Errorf("output should label the range:\n%s", buf.String())
	}
}

func TestUsageCommand_ThirtyDaysAndTodaySetSince(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		arg  string
		want time.Time
		tag  string
	}{
		{"30d", now.AddDate(0, 0, -30), "last 30 days"},
		{"today", time.Date(2026, 6, 15, 0, 0, 0, 0, now.Location()), "today"},
	} {
		var buf strings.Builder
		store := &fakeUsageStore{sum: usage.Stat{Turns: 1, PromptN: 10, PredictedN: 5}}
		cmd := &UsageCommand{
			OpenStore: func() (usageStore, error) { return store, nil },
			Now:       func() time.Time { return now },
		}
		if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{tc.arg}); err != nil {
			t.Fatalf("Run %s: %v", tc.arg, err)
		}
		if !store.lastSince.Equal(tc.want) {
			t.Errorf("%s: since = %v, want %v", tc.arg, store.lastSince, tc.want)
		}
		if !strings.Contains(buf.String(), tc.tag) {
			t.Errorf("%s: output missing tag %q:\n%s", tc.arg, tc.tag, buf.String())
		}
	}
}

func TestUsageCommand_RangeCombinesWithScope(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{stats: map[string][]usage.Stat{
		dimKey(usage.ByProvider, "/a"): {{Key: "kimi", Turns: 1, PromptN: 10, PredictedN: 5}},
	}}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }, ProjectDir: "/a"}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"30d", "here", "provider"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "last 30 days") || !strings.Contains(out, "this project") {
		t.Errorf("combined scope should label range and project:\n%s", out)
	}
	if !strings.Contains(out, "kimi") {
		t.Errorf("combined scope should show provider row:\n%s", out)
	}
}

func TestUsageCommand_SectionShowsShareColumnAndColoredBar(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{
		sum: usage.Stat{Turns: 2, PromptN: 90, PredictedN: 10},
		stats: map[string][]usage.Stat{
			dimKey(usage.ByProvider, ""): {
				{Key: "anthropic", Turns: 1, PromptN: 75, PredictedN: 5},
				{Key: "ollama", Turns: 1, PromptN: 15, PredictedN: 5},
			},
		},
	}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"provider"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[38;2;") {
		t.Errorf("split bar should contain truecolor SGR sequences:\n%q", out)
	}
	if !strings.Contains(out, "anthropic 80%") || !strings.Contains(out, "ollama 20%") {
		t.Errorf("legend percentages wrong:\n%s", out)
	}
	// Legend is one entry per line.
	if !strings.Contains(out, "anthropic 80%\n") || !strings.Contains(out, "ollama 20%\n") {
		t.Errorf("legend entries should be one per line:\n%s", out)
	}
	// One empty line separates the graph from whatever follows.
	if !strings.Contains(out, "ollama 20%\n\n") {
		t.Errorf("graph should be followed by an empty line:\n%q", out)
	}
	if !strings.Contains(out, "| Share |") {
		t.Errorf("table should carry a Share column:\n%s", out)
	}
}

func TestUsageCommand_CostView(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{stats: map[string][]usage.Stat{
		dimKey(usage.ByModel, ""): {
			{Key: "priced", Turns: 2, PromptN: 1_000_000, PredictedN: 500_000},
			{Key: "local-llm", Turns: 1, PromptN: 100},
		},
	}}
	cmd := &UsageCommand{
		OpenStore: func() (usageStore, error) { return store, nil },
		CostLookup: func(model string) (ModelPricing, bool) {
			if model == "priced" {
				return ModelPricing{Input: 3, Output: 15}, true // $3 + $7.50
			}
			return ModelPricing{}, false
		},
	}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"cost"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Cost by model", "$10.50", "Total (priced models): $10.50",
		"100%", "No pricing data for: local-llm",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("cost view missing %q:\n%s", want, out)
		}
	}
}

func TestUsageCommand_CostViewEmpty(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"cost"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "No usage recorded") {
		t.Errorf("empty cost view should print guidance:\n%s", buf.String())
	}
}

func TestUsageCommand_ActivityHeatmap(t *testing.T) {
	var buf strings.Builder
	today := time.Now().UTC()
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	store := &fakeUsageStore{daily: []usage.DayCount{
		{Day: today.AddDate(0, 0, -2), Turns: 1, Tokens: 100},
		{Day: today.AddDate(0, 0, -1)},
		{Day: today, Turns: 3, Tokens: 400},
	}}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), []string{"activity"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Less", "More", "2 active days", "Mon", "█"} {
		if !strings.Contains(out, want) {
			t.Errorf("heatmap output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "\x1b[38;2;") {
		t.Errorf("heatmap should contain truecolor SGR sequences:\n%q", out)
	}
}

func TestHumanTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1500, "1.5K"},
		{2_500_000, "2.5M"},
	}
	for _, tc := range cases {
		if got := humanTokens(tc.in); got != tc.want {
			t.Errorf("humanTokens(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatUSD(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "$0"},
		{0.0042, "$0.0042"},
		{10.5, "$10.50"},
		{1234, "$1.2K"},
	}
	for _, tc := range cases {
		if got := formatUSD(tc.in); got != tc.want {
			t.Errorf("formatUSD(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHeatLevel(t *testing.T) {
	cases := []struct {
		tokens, max int
		want        int
	}{
		{0, 100, 0},
		{10, 100, 1},
		{30, 100, 2},
		{60, 100, 3},
		{90, 100, 4},
		{5, 0, 0}, // zero max guard
	}
	for _, tc := range cases {
		if got := heatLevel(tc.tokens, tc.max); got != tc.want {
			t.Errorf("heatLevel(%d, %d) = %d, want %d", tc.tokens, tc.max, got, tc.want)
		}
	}
}

func TestTokenSegmentsFoldsOverflowIntoOther(t *testing.T) {
	rows := make([]usage.Stat, 0, len(usagePalette)+2)
	for i := 0; i < len(usagePalette)+2; i++ {
		rows = append(rows, usage.Stat{Key: string(rune('a' + i)), PromptN: 100 - i})
	}
	segs := tokenSegments(rows)
	if len(segs) != len(usagePalette)+1 {
		t.Fatalf("segments = %d, want palette+1 (other)", len(segs))
	}
	last := segs[len(segs)-1]
	if last.label != "other" || last.value != rows[len(rows)-2].Total()+rows[len(rows)-1].Total() {
		t.Errorf("overflow should fold into other: %+v", last)
	}
}
