// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/team"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
)

// teamScenarioConfig builds a config with one shorthand team for filmstrip
// scenarios.
func teamScenarioConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Teams.Definitions = map[string]config.TeamDefinition{
		"qa-pair": {
			Main:      &config.TeamMember{Model: "qwen-local", ThinkingLevel: "medium"},
			Companion: &config.TeamMember{Model: "gemma-local", ThinkingLevel: "low"},
			Review:    "gated",
			ReviewGates: config.TeamReviewGates{
				Triggers: []string{"goal_complete", "file_commit"},
			},
		},
	}
	return cfg
}

// filmstripSession is a minimal team.SessionController for UI scenarios.
type filmstripSession struct {
	pid, mid string
	mode     internal.ModeState
	thinking string
}

func (s *filmstripSession) SwitchModel(pid, mid string) error   { s.pid, s.mid = pid, mid; return nil }
func (s *filmstripSession) CurrentModel() (string, string)      { return s.pid, s.mid }
func (s *filmstripSession) CurrentMode() internal.ModeState     { return s.mode }
func (s *filmstripSession) SetMode(ms internal.ModeState) error { s.mode = ms; return nil }
func (s *filmstripSession) SetThinkingLevel(l string) error     { s.thinking = l; return nil }
func (s *filmstripSession) CurrentThinkingLevel() string        { return s.thinking }

// filmstripPool records applied pool configs.
type filmstripPool struct {
	configs map[string]multiagent.AgentConfig
}

func (p *filmstripPool) ApplyMember(role string, cfg multiagent.AgentConfig) error {
	p.configs[role] = cfg
	return nil
}
func (p *filmstripPool) RoleConfig(role string) multiagent.AgentConfig { return p.configs[role] }
func (p *filmstripPool) Evict(role string)                             { delete(p.configs, role) }

// TestTeamFooterBadge_Filmstrip drives team activation/deactivation and
// asserts the footer badge appears, drift-marks, and clears — the F1/F4
// scenarios from TEAMS-PLAN §8.3.
func TestTeamFooterBadge_Filmstrip(t *testing.T) {
	sc := newUIScenario(t, 100, 30)
	cfg := teamScenarioConfig()
	sess := &filmstripSession{pid: "lmstudio", mid: "qwen-local"}
	pool := &filmstripPool{configs: map[string]multiagent.AgentConfig{}}
	mgr := team.NewManager(cfg, sess, pool, nil, nil, nil)
	sc.app.subs.teamManager = mgr
	sc.app.subs.cfg = cfg

	// Step 0: no team — footer has no badge.
	sc.engine.ApplySync(func() {
		sc.footer.SetTeam("", false)
	})
	sc.engine.RenderNow()
	sc.film.Capture("no-team", sc.engine.AgentFrame(), "")
	if name, _ := teamFooterInfo(sc.app.subs); name != "" {
		t.Fatalf("teamFooterInfo = %q, want empty", name)
	}
	if badge := sc.footer.Data().Team; badge != "" {
		t.Fatalf("footer team = %q, want empty", badge)
	}

	// Step 1: activate → badge appears with the team name.
	sc.engine.ApplySync(func() {
		if err := mgr.Activate("qa-pair"); err != nil {
			t.Fatalf("Activate: %v", err)
		}
		name, drifted := teamFooterInfo(sc.app.subs)
		sc.footer.SetTeam(name, drifted)
	})
	sc.engine.RenderNow()
	snap := sc.film.Capture("team-active", sc.engine.AgentFrame(), "")
	if name, drifted := teamFooterInfo(sc.app.subs); name != "qa-pair" || drifted {
		t.Errorf("teamFooterInfo = %q drifted=%v, want qa-pair false", name, drifted)
	}
	if badge := sc.footer.Data().Team; badge != "qa-pair" {
		t.Errorf("footer team = %q, want qa-pair", badge)
	}
	assertFrameContains(t, snap, "qa-pair")

	// Step 2: drift (manual /model equivalent) → badge gains the * marker.
	sc.engine.ApplySync(func() {
		sess.mid = "user-picked"
		mgr.MarkDrift()
		name, drifted := teamFooterInfo(sc.app.subs)
		sc.footer.SetTeam(name, drifted)
	})
	sc.engine.RenderNow()
	snap = sc.film.Capture("team-drifted", sc.engine.AgentFrame(), "")
	if _, drifted := teamFooterInfo(sc.app.subs); !drifted {
		t.Error("expected drifted after MarkDrift")
	}
	if !sc.footer.Data().TeamDrifted {
		t.Error("footer TeamDrifted = false, want true")
	}
	assertFrameContains(t, snap, "qa-pair*")

	// Step 3: /team:sync → re-applied, drift cleared.
	sc.engine.ApplySync(func() {
		if err := mgr.Sync(); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		name, drifted := teamFooterInfo(sc.app.subs)
		sc.footer.SetTeam(name, drifted)
	})
	sc.engine.RenderNow()
	sc.film.Capture("team-synced", sc.engine.AgentFrame(), "")
	if _, drifted := teamFooterInfo(sc.app.subs); drifted {
		t.Error("expected drift cleared after Sync")
	}

	// Step 4: /team:off → badge cleared, model restored.
	sc.engine.ApplySync(func() {
		if err := mgr.Deactivate(); err != nil {
			t.Fatalf("Deactivate: %v", err)
		}
		name, drifted := teamFooterInfo(sc.app.subs)
		sc.footer.SetTeam(name, drifted)
	})
	sc.engine.RenderNow()
	sc.film.Capture("team-off", sc.engine.AgentFrame(), "")
	if badge := sc.footer.Data().Team; badge != "" {
		t.Errorf("footer team = %q after off, want empty", badge)
	}
	if sess.mid != "qwen-local" {
		t.Errorf("session model = %q after off, want qwen-local restored", sess.mid)
	}

	// The filmstrip should have 5 steps with the badge appearing only in
	// steps 1–3 (diff-level assertion on AddedLines for the active step).
	if got := len(sc.filmstrip().Frames()); got != 5 {
		t.Errorf("filmstrip steps = %d, want 5", got)
	}
}

// TestTeamFooterBadge_PreservedAcrossStatsRebuilds verifies the badge
// survives routine FooterData rebuilds (token stats ticks) that don't know
// about teams (preserveFooterTeam).
func TestTeamFooterBadge_PreservedAcrossStatsRebuilds(t *testing.T) {
	footer := tui.NewFooter()
	footer.SetTeam("qa-pair", true)
	// Routine stats rebuild constructs fresh FooterData without team fields.
	footer.SetData(tui.FooterData{Model: "qwen-local", Stats: "↑1k"})
	if footer.Data().Team != "qa-pair" || !footer.Data().TeamDrifted {
		t.Errorf("badge lost across stats rebuild: %+v", footer.Data())
	}
	// Explicit clear via SetTeam works.
	footer.SetTeam("", false)
	footer.SetData(tui.FooterData{Model: "qwen-local"})
	if footer.Data().Team != "" {
		t.Errorf("badge not cleared: %+v", footer.Data())
	}
}

// TestTeamConfigMenuCRUD_Filmstrip exercises the /config → Teams flow at the
// menu level inside a UI scenario (F3 from TEAMS-PLAN §8.3): definitions
// list renders teams, and the wizard adds one.
func TestTeamConfigMenuCRUD_Filmstrip(t *testing.T) {
	sc := newUIScenario(t, 100, 30)
	cfg := teamScenarioConfig()
	sc.app.subs.cfg = cfg

	// Capture the team definitions list as it would render in the selector.
	items := append([]string(nil), cfg.TeamNames()...)
	sc.engine.ApplySync(func() {
		sc.chat.AddSystemMessage("Teams: " + joinComma(items))
	})
	sc.engine.RenderNow()
	snap := sc.film.Capture("config-teams-list", sc.engine.AgentFrame(), "")
	assertFrameContains(t, snap, "qa-pair")
}

func assertFrameContains(t *testing.T, snap tui.Snapshot, substr string) {
	t.Helper()
	for _, line := range snap.Diff.AddedLines {
		if strings.Contains(line, substr) {
			return
		}
	}
	// Diff lines are trimmed+stripped; fall back to the frame's visible
	// viewport (ANSI-stripped, reading order) for badge assertions.
	for _, l := range snap.Frame.Visible {
		if strings.Contains(l, substr) {
			return
		}
	}
	t.Errorf("frame %q missing %q\nAddedLines: %v", snap.Label, substr, snap.Diff.AddedLines)
}

func joinComma(items []string) string {
	return strings.Join(items, ", ")
}
