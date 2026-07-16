// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tools/swarm"
	"github.com/pijalu/goa/tui"
)

// swarmEmitter bridges the tools/swarm ChatEmitter interface to the
// application's typed event bus, surfacing sub-agent lifecycle and output as
// InterAgent messages in the chat history.
type swarmEmitter struct {
	bus *event.Bus
}

func (e *swarmEmitter) Emit(from, to, content string) {
	if e.bus == nil || content == "" {
		return
	}
	select {
	case e.bus.Chat <- event.ChatEvent{InterAgent: &event.InterAgent{
		From:    from,
		To:      to,
		Content: content,
	}}:
	default:
	}
}

// wireSwarmTool completes the AgentSwarmTool wiring that requires the App
// (and therefore the TUI). It is called from New after subsystems exist.
func wireSwarmTool(a *App) {
	if a == nil || a.subs == nil || a.subs.toolRegistry == nil {
		return
	}
	t, ok := a.subs.toolRegistry.Get("agent_swarm")
	if !ok {
		return
	}
	st, ok := t.(*swarm.AgentSwarmTool)
	if !ok {
		return
	}
	st.ProgressReporter = (&swarmProgressUpdater{app: a}).Update
}

// swarmProgressUpdater updates the latest running agent_swarm tool widget so
// the user can see per-sub-agent status inside the tool block itself.
type swarmProgressUpdater struct {
	app *App
}

func (u *swarmProgressUpdater) Update(text string) {
	if u.app == nil || u.app.subs.chat == nil || text == "" {
		return
	}
	u.app.apply(func() {
		children := u.app.subs.chat.Children()
		for i := len(children) - 1; i >= 0; i-- {
			if tc, ok := children[i].(*tui.ToolExecutionComponent); ok && tc.ToolName() == "agent_swarm" {
				if tc.Status() == tui.ToolRunning || tc.Status() == tui.ToolPending {
					tc.SetOutput(text)
					tc.SetPartial(true)
					return
				}
			}
		}
	})
}
