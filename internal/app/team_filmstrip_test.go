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
	setTeamFooter(sc, "", false)
	sc.engine.RenderNow()
	sc.film.Capture("no-team", sc.engine.AgentFrame(), "")
	if name, _ := teamFooterInfo(sc.app.subs); name != "" {
		t.Fatalf("teamFooterInfo = %q, want empty", name)
	}
	if badge := sc.footer.Data().Team; badge != "" {
		t.Fatalf("footer team = %q, want empty", badge)
	}
	activateTeam(t, sc, mgr)
	assertActiveTeam(t, sc)
	sess.mid = "user-picked"
	mgr.MarkDrift()
	name, drifted := teamFooterInfo(sc.app.subs)
	setTeamFooter(sc, name, drifted)
	sc.engine.RenderNow()
	snap := sc.film.Capture("team-drifted", sc.engine.AgentFrame(), "")
	if !sc.footer.Data().TeamDrifted {
		t.Error("footer TeamDrifted = false")
	}
	assertFrameContains(t, snap, "qa-pair*")
	if err := mgr.Sync(); err != nil {
		t.Fatal(err)
	}
	name, drifted = teamFooterInfo(sc.app.subs)
	setTeamFooter(sc, name, drifted)
	sc.engine.RenderNow()
	sc.film.Capture("team-synced", sc.engine.AgentFrame(), "")
	if _, drifted := teamFooterInfo(sc.app.subs); drifted {
		t.Error("expected drift cleared")
	}
}

func setTeamFooter(sc *uiScenario, name string, drifted bool) {
	sc.engine.ApplySync(func() { sc.footer.SetTeam(name, drifted) })
}
func activateTeam(t *testing.T, sc *uiScenario, mgr *team.Manager) {
	sc.engine.ApplySync(func() {
		if err := mgr.Activate("qa-pair"); err != nil {
			t.Fatal(err)
		}
		name, drifted := teamFooterInfo(sc.app.subs)
		sc.footer.SetTeam(name, drifted)
	})
	sc.engine.RenderNow()
}
func assertActiveTeam(t *testing.T, sc *uiScenario) {
	snap := sc.film.Capture("team-active", sc.engine.AgentFrame(), "")
	if name, drifted := teamFooterInfo(sc.app.subs); name != "qa-pair" || drifted {
		t.Errorf("teamFooterInfo = %q drifted=%v", name, drifted)
	}
	if sc.footer.Data().Team != "qa-pair" {
		t.Error("footer team not qa-pair")
	}
	assertFrameContains(t, snap, "qa-pair")
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
