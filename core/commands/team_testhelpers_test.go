// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/team"
	"github.com/pijalu/goa/internal"
)

// teamCmdConfig builds a config with two shorthand teams.

// teamCmdConfig builds a config with two shorthand teams.
func teamCmdConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Teams.Definitions = map[string]config.TeamDefinition{
		"alpha": {
			Main:      &config.TeamMember{Model: "m1", ThinkingLevel: "high"},
			Companion: &config.TeamMember{Model: "m2", ThinkingLevel: "low"},
			Review:    "agent",
		},
		"beta": {
			Main:   &config.TeamMember{Model: "m3"},
			Review: "off",
		},
	}
	return cfg
}

// teamCmdContext builds a command context with a real TeamManager over fakes
// (headless: nil pool/review, session = in-memory fake via adapters is
// covered in internal/app; here we use a minimal session stub through
// team.NewManager with nil deps — activation reduces to definition state).

// teamCmdContext builds a command context with a real TeamManager over fakes
// (headless: nil pool/review, session = in-memory fake via adapters is
// covered in internal/app; here we use a minimal session stub through
// team.NewManager with nil deps — activation reduces to definition state).
func teamCmdContext(t *testing.T, cfg *config.Config) core.Context {
	t.Helper()
	sess := &stubSession{providerID: "p0", modelID: "m0"}
	m := team.NewManager(cfg, sess, nil, nil, nil, nil)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	return core.Context{
		Config:      cfg,
		TeamManager: m,
		ConfigSaver: config.NewCascadeLoader(t.TempDir(), "", nil),
	}
}

// stubSession is a minimal team.SessionController for command tests.

// stubSession is a minimal team.SessionController for command tests.
type stubSession struct {
	providerID, modelID string
	mode                internal.ModeState
	thinking            string
}

func (s *stubSession) SwitchModel(pid, mid string) error {
	s.providerID, s.modelID = pid, mid
	return nil
}

func (s *stubSession) CurrentModel() (string, string) { return s.providerID, s.modelID }

func (s *stubSession) CurrentMode() internal.ModeState { return s.mode }

func (s *stubSession) SetMode(ms internal.ModeState) error { s.mode = ms; return nil }

func (s *stubSession) SetThinkingLevel(l string) error { s.thinking = l; return nil }

func (s *stubSession) CurrentThinkingLevel() string { return s.thinking }
