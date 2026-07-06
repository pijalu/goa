// SPDX-License-Identifier-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/tui"
)

// TestOrchestratorTabs_Filmstrip_PersistenceAndPerFrameBar drives the FULL
// event sequence (start → stream → stats → steer → finish) through the
// persistent view and records a Filmstrip, asserting:
//   - the AgentTabBar layer is present in every frame after the run starts;
//   - the Stats-tab content eventually reflects the CH column once stats arrive;
//   - the view PERSISTS after finish (the last frame still has the bar) — the
//     regression guard for the old "overlay disappears on run end" defect.
//
// This is the §4.2 regression guard: a single-frame assertion cannot catch a
// transient-hide regression, so we assert across the whole filmstrip.
func TestOrchestratorTabs_Filmstrip_PersistenceAndPerFrameBar(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	film := captureLifecycleFilmstrip(t, sc)

	frames := film.Frames()
	if len(frames) < len(lifecycleEvents())+1 {
		t.Fatalf("captured %d frames, want at least %d", len(frames), len(lifecycleEvents())+1)
	}

	assertBarAbsent(t, frames[0], "pre-run frame should not show the tab bar")
	for i := 1; i < len(frames); i++ {
		assertBarPresent(t, frames[i], "frame %d (%s)", i, frames[i].Label)
	}
	if !frameShowsStatsCH(t, frames) {
		t.Error("no frame showed the stats table CH column")
	}
	assertBarPresent(t, frames[len(frames)-1], "tab bar disappeared after run finished (view must persist)")
	if sc.app.subs.agentView == nil || !sc.app.subs.agentView.Finished() {
		t.Error("view not finished after run_finished event")
	}
}

// captureLifecycleFilmstrip records a pre-run frame plus one frame per
// translated lifecycle event, applying each on the command loop.
func captureLifecycleFilmstrip(t *testing.T, sc *orchViewScenario) *tui.Filmstrip {
	t.Helper()
	film := tui.NewFilmstrip()
	film.Capture("pre-run", sc.frame(), "")
	for _, ev := range lifecycleEvents() {
		ne, ok := translateOrchEvent(ev)
		if !ok {
			continue
		}
		v := sc.app.subs.agentView
		sc.engine.ApplySync(func() { v.ApplyEvent(ne); sc.app.updateOrchInputPrompt() })
		film.Capture(string(ev.Type), sc.frame(), "")
	}
	return film
}

func assertBarPresent(t *testing.T, s tui.Snapshot, format string, args ...any) {
	t.Helper()
	if s.Frame.FindNode("orchestrator.AgentTabBar") == nil {
		t.Errorf("tab bar missing: "+format, args...)
	}
}

func assertBarAbsent(t *testing.T, s tui.Snapshot, msg string) {
	t.Helper()
	if s.Frame.FindNode("orchestrator.AgentTabBar") != nil {
		t.Error(msg)
	}
}

func frameShowsStatsCH(t *testing.T, frames []tui.Snapshot) bool {
	t.Helper()
	for _, fr := range frames {
		c := fr.Frame.FindNode("orchestrator.AgentContent")
		if c != nil && strings.Contains(c.Text, "CH") {
			return true
		}
	}
	return false
}

// keep the orchestrator import referenced if helpers evolve.
var _ = orchestrator.EventRunStarted
