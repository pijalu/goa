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

// TestHandleEditSteering_RecallsPendingIntoEditor covers the steering
// edit-before-send flow (Alt+E): pending steering text moves into the input
// line, the queue is emptied, and the pending bubble/footer indicator clear.
func TestHandleEditSteering_RecallsPendingIntoEditor(t *testing.T) {
	app, subs := testAppWithAgent(t)
	subs.agentMgr.SetSteeringQueue(core.NewSteeringQueue())
	subs.inputEditor = tui.NewEditor()
	chat := tui.NewChatViewport()
	engine := tui.NewTUI(&testTerminal{w: 80, h: 24})

	sq := subs.agentMgr.SteeringQueue()
	sq.Append("first steering")
	sq.Append("second steering")
	chat.AddSteeringPending("first steering")

	app.handleEditSteering(engine, chat)

	if got := subs.inputEditor.Text(); got != "first steering\n\nsecond steering" {
		t.Errorf("editor text = %q, want joined steering", got)
	}
	if sq.Len() != 0 {
		t.Errorf("steering queue should be flushed, got %d pending", sq.Len())
	}
	if chat.HasSteeringPending() {
		t.Error("steering bubble should be cleared after edit recall")
	}
}

// TestHandleEditSteering_NoPendingIsNoOp ensures Alt+E without pending
// steering does not clobber the editor content.
func TestHandleEditSteering_NoPendingIsNoOp(t *testing.T) {
	app, subs := testAppWithAgent(t)
	subs.agentMgr.SetSteeringQueue(core.NewSteeringQueue())
	subs.inputEditor = tui.NewEditor()
	subs.inputEditor.SetText("draft in progress")
	chat := tui.NewChatViewport()
	engine := tui.NewTUI(&testTerminal{w: 80, h: 24})

	app.handleEditSteering(engine, chat)

	if got := subs.inputEditor.Text(); got != "draft in progress" {
		t.Errorf("editor text = %q, want unchanged draft", got)
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
