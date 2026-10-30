// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func newModeLineTestFooter(left, right []string) *Footer {
	f := NewFooter()
	f.SetModeLine(left, right)
	f.SetData(FooterData{
		Workdir:   "/tmp/project",
		GitBranch: "main",
		Profile:   "coding-posture",
		Mode:      "yolo",
		Model:     "(provider) test-model",
		Stats:     "↑1k ↓2k $0.6259 50%/100k",
		StatsSegments: []FooterSegment{
			{Tier: FooterTierTokens, Text: "↑1k"},
			{Tier: FooterTierTokens, Text: "↓2k"},
			{Tier: FooterTierAmount, Text: "$0.6259"},
			{Tier: FooterTierQuota, Text: "50%/100k"},
		},
		Activity: "tool calling",
	})
	return f
}

func modeLineRow(t *testing.T, f *Footer, width int) string {
	t.Helper()
	lines := f.Render(width)
	if len(lines) < 2 {
		t.Fatalf("Render returned %d lines, want configured modeline plus model row", len(lines))
	}
	return ansi.Strip(lines[0])
}

func TestFooterModeLineSegments(t *testing.T) {
	tests := []modeLineSegmentTestCase{
		{name: "app badge", left: []string{"app-badge"}, right: []string{}, want: []string{"⬡ goa"}},
		{name: "mode badge", left: []string{"mode-badge"}, right: []string{}, want: []string{"coding-posture", "│", "YOLO"}},
		{name: "current file", left: []string{"current-file"}, right: []string{}, want: []string{"/tmp/project", "⎇ main"}},
		{name: "token bar", left: []string{}, right: []string{"token-bar"}, want: []string{"↑1k", "↓2k", "$0.6259", "50%/100k"}, wantSecond: []string{"↑1k", "↓2k"}},
		{name: "activity", left: []string{}, right: []string{"activity"}, want: []string{"tool calling"}, wantSecond: []string{"tool calling"}},
		{name: "help hint", left: []string{}, right: []string{"help-hint"}, want: []string{"/help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertModeLineSegmentCase(t, tt) })
	}
}

type modeLineSegmentTestCase struct {
	name       string
	left       []string
	right      []string
	want       []string
	wantSecond []string
}

func assertModeLineSegmentCase(t *testing.T, tt modeLineSegmentTestCase) {
	t.Helper()
	f := newModeLineTestFooter(tt.left, tt.right)
	row := modeLineRow(t, f, 220)
	assertContainsAll(t, row, tt.want)

	second := ansi.Strip(f.Render(220)[1])
	if !strings.Contains(second, "test-model") {
		t.Errorf("essential model display missing from second row: %q", second)
	}
	for _, unwanted := range tt.wantSecond {
		if strings.Contains(second, unwanted) {
			t.Errorf("configured segment %q duplicated on model row: %q", unwanted, second)
		}
	}
}

func assertContainsAll(t *testing.T, text string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("modeline does not contain %q: %q", want, text)
		}
	}
}

func TestFooterModeLineSelectionOrderingAndUnknownEntries(t *testing.T) {
	f := newModeLineTestFooter(
		[]string{"current-file", "unknown-segment", "app-badge", "", "mode-badge"},
		[]string{"help-hint", "token-bar", "activity"},
	)
	row := modeLineRow(t, f, 240)
	assertTextOrder(t, row, "/tmp/project", "⬡ goa", "coding-posture")
	assertTextOrder(t, row, "/help", "↑1k", "tool calling")
	if strings.Contains(row, "unknown-segment") {
		t.Errorf("unknown segment name leaked into output: %q", row)
	}
}

func assertTextOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	previous := -1
	for _, part := range parts {
		index := strings.Index(text, part)
		if index < 0 {
			t.Fatalf("%q missing from %q", part, text)
		}
		if index <= previous {
			t.Fatalf("%q appears out of order in %q", part, text)
		}
		previous = index
	}
}

func TestFooterModeLineExplicitEmptyLists(t *testing.T) {
	f := newModeLineTestFooter([]string{}, []string{})
	if row := modeLineRow(t, f, 100); strings.TrimSpace(row) != "" {
		t.Errorf("explicit empty modeline = %q, want blank row", row)
	}
	if second := ansi.Strip(f.Render(100)[1]); !strings.Contains(second, "test-model") {
		t.Errorf("model row missing after empty modeline: %q", second)
	}
}

func TestFooterModeLineNilListsKeepLegacyLayout(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir: "/tmp/project",
		Mode:    "yolo",
		Profile: "coding-posture",
		Model:   "test-model",
		Stats:   "↑1k 50%/100k",
	})
	lines := f.Render(120)
	if first := ansi.Strip(lines[0]); !strings.Contains(first, "/tmp/project") || !strings.Contains(first, "YOLO") {
		t.Errorf("legacy first row changed: %q", first)
	}
	if second := ansi.Strip(lines[1]); !strings.Contains(second, "↑1k") || !strings.Contains(second, "test-model") {
		t.Errorf("legacy second row changed: %q", second)
	}
}

func TestFooterModeLineNeverExceedsWidth(t *testing.T) {
	f := newModeLineTestFooter(
		[]string{"app-badge", "mode-badge", "current-file"},
		[]string{"token-bar", "activity", "help-hint"},
	)
	for width := 240; width >= 8; width-- {
		lines := f.Render(width)
		if len(lines) != 2 {
			t.Fatalf("width %d: got %d rows, want 2", width, len(lines))
		}
		for row, line := range lines {
			if got := visibleWidth(line); got > width {
				t.Errorf("width %d row %d overflows to %d cells: %q", width, row, got, ansi.Strip(line))
			}
		}
	}
}

func TestFooterModeLineDropsHelpBeforeEssentialFields(t *testing.T) {
	f := newModeLineTestFooter(
		[]string{"app-badge", "mode-badge", "current-file"},
		[]string{"token-bar", "activity", "help-hint"},
	)
	for width := 240; width >= 20; width-- {
		row := modeLineRow(t, f, width)
		if strings.Contains(row, "/help") {
			continue
		}
		for _, want := range []string{"YOLO", "↑1k", "50%/100k"} {
			if !strings.Contains(row, want) {
				t.Fatalf("width %d dropped %q together with or before help: %q", width, want, row)
			}
		}
		if second := ansi.Strip(f.Render(width)[1]); !strings.Contains(second, "test-model") {
			t.Fatalf("width %d lost model when dropping help: %q", width, second)
		}
		return
	}
	t.Fatal("help hint was never dropped under width pressure")
}

func TestFooterModeLineActivityMovesFromModelRow(t *testing.T) {
	tests := []struct {
		name     string
		data     FooterData
		activity string
	}{
		{name: "main", activity: "thinking", data: FooterData{
			Model: "test-model", Activity: "thinking", MainActivity: "thinking", ModelBusy: true,
		}},
		{name: "companion", activity: "reviewing", data: FooterData{
			MinorMode: "companion", Model: "test-model", Activity: "reviewing",
			CompanionActivity: "reviewing", CompanionBusy: true,
		}},
		{name: "delegated", activity: "reviewing", data: FooterData{
			Model: "test-model", ActiveAgentModel: "sub-model", Activity: "reviewing",
			CompanionActivity: "reviewing",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFooter()
			f.SetModeLine(nil, []string{"activity"})
			f.SetData(tt.data)
			lines := f.Render(180)
			if first := ansi.Strip(lines[0]); !strings.Contains(first, tt.activity) {
				t.Errorf("configured activity missing from modeline: %q", first)
			}
			if second := ansi.Strip(lines[1]); strings.Contains(second, tt.activity) {
				t.Errorf("configured activity duplicated on model row: %q", second)
			}
		})
	}
}

func TestFooterModeLineWithoutActivityKeepsModelRowActivity(t *testing.T) {
	f := NewFooter()
	f.SetModeLine([]string{"app-badge"}, nil)
	f.SetData(FooterData{
		Model: "test-model", Activity: "thinking", MainActivity: "thinking", ModelBusy: true,
	})
	if second := ansi.Strip(f.Render(180)[1]); !strings.Contains(second, "thinking") {
		t.Errorf("unconfigured activity must remain visible on model row: %q", second)
	}
}

func TestFooterModeLineKeepsDynamicFooterState(t *testing.T) {
	f := newModeLineTestFooter([]string{"mode-badge"}, []string{"token-bar"})
	data := f.Data()
	data.GoalStatus = "active"
	data.GoalCount = 2
	data.GoalPendingTodos = 4
	data.Team = "qa-pair"
	data.TeamDrifted = true
	data.SteeringHint = "type to steer"
	data.PluginSegments = []PluginSegment{{ID: "quota", Text: "5h:42%"}}
	f.SetData(data)
	lines := f.Render(180)
	first, second := ansi.Strip(lines[0]), ansi.Strip(lines[1])
	for _, want := range []string{"◈◈", "⬩⬩⬩+1", "YOLO"} {
		if !strings.Contains(first, want) {
			t.Errorf("modeline lost dynamic mode state %q: %q", want, first)
		}
	}
	for _, want := range []string{"⛃ qa-pair*", "5h:42%", "type to steer", "test-model"} {
		if !strings.Contains(second, want) {
			t.Errorf("model row lost dynamic state %q: %q", want, second)
		}
	}
}

func TestFooterModeLineOrchestrationStillReplacesChrome(t *testing.T) {
	f := newModeLineTestFooter([]string{"app-badge"}, []string{"help-hint"})
	f.SetData(FooterData{OrchestrationStats: "coder: ↑1 - test-model"})
	lines := f.Render(100)
	if len(lines) != 1 || !strings.Contains(ansi.Strip(lines[0]), "coder: ↑1 - test-model") {
		t.Fatalf("orchestration did not replace configured chrome: %q", lines)
	}
}
