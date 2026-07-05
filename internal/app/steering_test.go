// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/tui"
)

// TestMaybeSteerAgent_WhenIdle returns false so normal dispatch happens.
func TestMaybeSteerAgent_WhenIdle(t *testing.T) {
	app, subs := testAppWithAgent(t)
	subs.agentMgr.SetSteeringQueue(core.NewSteeringQueue())
	chat := tui.NewChatViewport()
	engine := tui.NewTUI(&testTerminal{w: 80, h: 24})

	steered := app.maybeSteerAgent(engine, chat, "not running")
	if steered {
		t.Error("expected maybeSteerAgent to be false when agent is idle")
	}
	if sq := subs.agentMgr.SteeringQueue(); sq != nil && sq.Len() != 0 {
		t.Errorf("steering queue should be empty, got %d", sq.Len())
	}
}

func testAppWithAgent(t *testing.T) (*App, *subsystems) {
	t.Helper()
	app := &App{}
	cfg := &config.Config{}
	agentMgr := core.NewAgentManager(cfg, nil, nil, nil, nil, "")
	subs := &subsystems{
		cfg:      cfg,
		agentMgr: agentMgr,
		footer:   tui.NewFooter(),
	}
	app.subs = subs
	return app, subs
}
