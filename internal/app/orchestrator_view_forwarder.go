// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/core/orchestrator"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// orchEventSource is the runtime surface the view forwarder depends on. It is
// satisfied by *orchestrator.Runtime and by test fakes, so the forwarder logic
// is unit-testable without a live model (the orchestrator.Event type is the
// only thing crossing this seam).
type orchEventSource interface {
	Subscribe() <-chan orchestrator.Event
	Done() <-chan struct{}
}

// runOrchestratorViewForwarder is the single owner of the persistent tabbed
// multi-agent run view. It watches the active-runtime holder; whenever a new run
// becomes active it attaches a fresh MultiAgentView to the AgentContent and
// AgentTabBar components and drains that run's events ON THE COMMAND LOOP (via
// a.apply) so view state is mutated only by the loop — preserving the R1
// single-owner invariant. Unlike the old panel overlay, the view is PERSISTENT:
// it stays after the run finishes until a new run replaces it.
func (a *App) runOrchestratorViewForwarder(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case <-a.subs.orchActive.Notify():
			src := a.subs.orchActive.Get()
			if src == nil {
				continue
			}
			a.attachOrchView(src)
			a.drainOrchView(done, src)
		}
	}
}

// attachOrchView creates the view for a new run and binds the render-only
// components + chat suppression + steering prompt to it, all on the command loop.
func (a *App) attachOrchView(src orchEventSource) {
	view := orchpanel.NewMultiAgentView("orchestration")
	a.apply(func() {
		a.subs.agentView = view
		if a.subs.agentContent != nil {
			a.subs.agentContent.SetView(view)
		}
		if a.subs.agentTabBar != nil {
			a.subs.agentTabBar.SetView(view)
		}
		if a.subs.chat != nil {
			a.subs.chat.SetSuppressed(true)
		}
		a.updateOrchInputPrompt()
	})
}

// drainOrchView translates each orchestrator event into a neutral
// AgentViewEvent and applies it to the view on the command loop. It returns
// when the run finishes (leaving the view attached and persistent) or when the
// app is stopping.
func (a *App) drainOrchView(done <-chan struct{}, src orchEventSource) {
	sub := src.Subscribe()
	for {
		select {
		case <-done:
			return
		case <-src.Done():
			// The view is persistent: leave it attached so the user can read
			// the final stats/transcript. A new run (attachOrchView) resets it.
			return
		case ev := <-sub:
			ne, ok := translateOrchEvent(ev)
			if !ok {
				continue
			}
			view := a.subs.agentView
			if view == nil {
				continue
			}
			a.apply(func() {
				view.ApplyEvent(ne)
				a.updateOrchInputPrompt()
			})
		}
	}
}
