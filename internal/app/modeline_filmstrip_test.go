// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
)

// TestModeLineFilmstrip_DefaultSegmentsFollowAgentState exercises configured
// modeline cells through the production app event path and records the visible
// terminal frames, including the narrow-width priority behavior.
func TestModeLineFilmstrip_DefaultSegmentsFollowAgentState(t *testing.T) {
	sc := newUIScenarioCfg(t, 120, 24, &config.TUIConfig{
		ModeLine: config.ModeLineSegmentConfig{
			Left:  []string{"app-badge", "mode-badge", "current-file"},
			Right: []string{"token-bar", "activity", "help-hint"},
		},
	})
	sc.engine.ApplySync(func() {
		data := sc.footer.Data()
		data.Workdir = "project"
		data.Mode = "normal"
		data.Model = "test-model"
		sc.footer.SetData(data)
	})
	for _, event := range []*agentic.OutputEvent{
		{Type: agentic.EventTokenStats, Timings: &agentic.TokenTimings{
			PromptN: 400, PredictedN: 20,
		}},
		{Type: agentic.EventContextStats, ContextStats: &agentic.ContextStats{
			MaxTokens: 16000, EstimatedTokens: 320,
		}},
		{Type: agentic.EventStateChange, State: agentic.StateThinking},
	} {
		sc.apply(event)
	}

	wide := sc.filmstrip().Last().Frame
	wideText := strings.Join(wide.Visible, "\n")
	for _, want := range []string{"⬡ goa", "SOLO", "project", "2.0%", "thinking", "/help", "test-model"} {
		if !strings.Contains(wideText, want) {
			t.Errorf("default modeline missing %q\n%s", want, wide.Dump())
		}
	}

	sc.term.w = 40
	sc.engine.RequestRender()
	sc.engine.RenderNow()
	narrow := sc.filmstrip().Capture("resize/40_columns", sc.engine.AgentFrame(), sc.statusText()).Frame
	narrowText := strings.Join(narrow.Visible, "\n")
	for _, want := range []string{"2.0%", "test-model"} {
		if !strings.Contains(narrowText, want) {
			t.Errorf("40-column modeline lost live segment %q\n%s", want, narrow.Dump())
		}
	}
	if strings.Contains(narrowText, "/help") {
		t.Errorf("lowest-priority help survived before essential modeline content:\n%s", narrow.Dump())
	}

	t.Logf("configured modeline agent frames:\nWIDE:\n%s\nNARROW:\n%s\nFILMSTRIP:\n%s",
		wide.Dump(), narrow.Dump(), sc.filmstrip().Render())
}
