// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestModeLineFilmstrip_DefaultFooterFollowsAgentState exercises the shipped
// two-row footer through the production app event path and records visible
// terminal frames, including narrow-width priority behavior.
func TestModeLineFilmstrip_DefaultFooterFollowsAgentState(t *testing.T) {
	sc := newUIScenarioCfg(t, 120, 24, nil)
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
	if len(wide.Visible) < 2 {
		t.Fatalf("default frame has too few rows: %s", wide.Dump())
	}
	rows := wide.Visible[len(wide.Visible)-2:]
	for _, want := range []string{"project", "SOLO"} {
		if !strings.Contains(rows[0], want) {
			t.Errorf("default first row missing %q\n%s", want, wide.Dump())
		}
	}
	for _, want := range []string{"↑400", "↓20", "2.0%", "thinking", "test-model"} {
		if !strings.Contains(rows[1], want) {
			t.Errorf("default statistics row missing %q\n%s", want, wide.Dump())
		}
	}
	wideText := strings.Join(wide.Visible, "\n")
	for _, unwanted := range []string{"⬡ goa", "/help"} {
		if strings.Contains(wideText, unwanted) {
			t.Errorf("default footer unexpectedly renders %q\n%s", unwanted, wide.Dump())
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
		t.Errorf("default narrow footer unexpectedly gained a help hint:\n%s", narrow.Dump())
	}

	t.Logf("default footer agent frames:\nWIDE:\n%s\nNARROW:\n%s\nFILMSTRIP:\n%s",
		wide.Dump(), narrow.Dump(), sc.filmstrip().Render())
}
