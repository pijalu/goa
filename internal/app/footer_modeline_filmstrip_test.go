// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tui"
)

// Filmstrip validation for bugs.md B45: the configured modeline must survive
// a narrow terminal and routine footer rebuilds inside the real production
// component tree — the mode badge never blanks mid-stream, every rendered
// footer line stays inside the viewport, and no partial ANSI escape leaks
// into the visible text.
func TestFooterModeline_ResizeFilmstrip_BadgeStableWithinViewport(t *testing.T) {
	tuiCfg := &config.TUIConfig{
		ModeLine: config.ModeLineSegmentConfig{
			Left:  []string{"app-badge", "current-file"},
			Right: []string{"mode-badge", "token-bar", "activity", "help-hint"},
		},
	}
	sc := newUIScenarioCfg(t, 46, 12, tuiCfg)

	full := tui.FooterData{
		Workdir:       "/home/user/dev/goa",
		GitBranch:     "main",
		Mode:          "yolo",
		Profile:       "coding-posture",
		Model:         "(opencode) deepseek-v4.1-exp",
		Provider:      "opencode",
		ThinkingLevel: "high",
		Stats:         "↑169k ↓209k 35.8%/1.0M",
		StatsSegments: []tui.FooterSegment{
			{Tier: tui.FooterTierAvgCH, Text: "CH:35.8%"},
			{Tier: tui.FooterTierLastCH, Text: "▸35.8%"},
			{Tier: tui.FooterTierTokens, Text: "↑169k ↓209k"},
			{Tier: tui.FooterTierQuota, Text: "35.8%/1.0M"},
		},
		Activity:     "thinking",
		MainActivity: "thinking",
		ModelBusy:    true,
		SteeringHint: "type to steer",
	}

	assertViewport := func(t *testing.T, label string) {
		t.Helper()
		sc.engine.RenderNow()
		frame := sc.engine.AgentFrame()
		for _, line := range frame.Visible {
			if len([]rune(line)) > 46 {
				t.Fatalf("%s: footer line wider than the 46-column terminal: %q", label, line)
			}
			if strings.Contains(line, "\x1b") {
				t.Fatalf("%s: raw escape byte in visible viewport: %q", label, line)
			}
		}
		joined := strings.Join(frame.Visible, "\n")
		if !strings.Contains(joined, "YOLO") {
			t.Fatalf("%s: mode badge not visible in the frame:\n%s", label, frame.Dump())
		}
	}

	sc.engine.ApplySync(func() { sc.footer.SetData(full) })
	assertViewport(t, "initial")

	// Companion-stream tick in the exact shape the orchestrator issues it:
	// a fresh FooterData carrying only the field it owns.
	sc.engine.ApplySync(func() { sc.footer.SetData(tui.FooterData{CompanionActivity: "reviewing"}) })
	assertViewport(t, "companion tick")

	// Production token-stats rebuild carries Mode/Profile again and must keep
	// overriding whatever was preserved.
	sc.engine.ApplySync(func() {
		updated := full
		updated.Stats = "↑170k ↓210k 36%/1.0M"
		sc.footer.SetData(updated)
	})
	assertViewport(t, "stats rebuild")
	if got := sc.footer.Data().Mode; got != "yolo" {
		t.Fatalf("mode after stats rebuild = %q, want yolo", got)
	}
}
