// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

// TestSpinnerLocation verifies the helper driving the chat/statusbar decision:
// unset and unknown values fall back to "chat" (current behavior, fail-safe).
func TestSpinnerLocation(t *testing.T) {
	if got := spinnerLocation(nil); got != "chat" {
		t.Errorf("nil config = %q, want chat", got)
	}
	cfg := &config.Config{}
	if got := spinnerLocation(cfg); got != "chat" {
		t.Errorf("unset = %q, want chat (default)", got)
	}
	cfg.TUI.SpinnerLocation = "statusbar"
	if got := spinnerLocation(cfg); got != "statusbar" {
		t.Errorf("statusbar = %q, want statusbar", got)
	}
	cfg.TUI.SpinnerLocation = "bogus"
	if got := spinnerLocation(cfg); got != "chat" {
		t.Errorf("unknown value = %q, want chat (fail-safe)", got)
	}
}

// TestFilmstrip_SpinnerLocation drives request events in both spinner-location
// modes: "chat" (default) renders the in-chat "Thinking..." status line;
// "statusbar" suppresses it from the chat timeline while the footer carries
// the animated busy frame next to the model (spinner-location entry).
func TestFilmstrip_SpinnerLocation(t *testing.T) {
	events := []*agentic.OutputEvent{
		{Type: agentic.EventStateChange, State: agentic.StateThinking},
		// Prompt progress is the flow that marks the footer model busy
		// (stats.go handlePromptProgress): status line + footer frame.
		{Type: agentic.EventProgress, PromptProgress: &agentic.PromptProgress{Total: 1000, Processed: 100}},
		{Type: agentic.EventContent, State: agentic.StateThinking, IsDelta: true, Text: "weighing options"},
	}

	// chat mode (default): in-chat status line renders frame + busy text.
	chatSc := newUIScenarioCfg(t, 100, 24, nil)
	for _, ev := range events {
		chatSc.apply(ev)
	}
	if rendered := chatSc.filmstrip().Render(); !strings.Contains(rendered, "⬡ Processing... 10%") {
		t.Errorf("chat mode: expected in-chat status line \"⬡ Processing... 10%%\", got:\n%s", rendered)
	}

	// statusbar mode: no in-chat status line; footer carries the spinner frame.
	barSc := newUIScenarioCfg(t, 100, 24, &config.TUIConfig{
		SpinnerLocation: "statusbar",
		Transparency:    config.TransparencyConfig{ShowThinking: true},
	})
	for _, ev := range events {
		barSc.apply(ev)
	}
	barRendered := barSc.filmstrip().Render()
	if strings.Contains(barRendered, "⬡ Processing") {
		t.Errorf("statusbar mode: in-chat status line must be suppressed, got:\n%s", barRendered)
	}
	// The busy-indicator path stays live: Show() updates the shared spinner
	// frame the footer consumes (tui.CurrentSpinnerFrame / FormatModelPart).
	if frame := tui.CurrentSpinnerFrame(); frame == "" {
		t.Error("statusbar mode: shared spinner frame must be set so the footer busy indicator can render")
	}
}
