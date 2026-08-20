// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/tools"
)

// schedulePreTurnProvider adapts the schedule store to the agentic
// PreTurnProvider hook: at the start of every turn it atomically claims due
// jobs and renders them as one user-role reminder message.
type schedulePreTurnProvider struct {
	store tools.ScheduleStore
}

// PreTurnMessages claims every due job and renders them as a single
// injection-resistant reminder message. Because TakeDue claims atomically, a
// due job is delivered exactly once even if the process restarts between
// turns.
func (p *schedulePreTurnProvider) PreTurnMessages() []string {
	due, err := p.store.TakeDue(time.Now())
	if err != nil || len(due) == 0 {
		return nil
	}
	return []string{tools.RenderDueReminders(due)}
}

// wireScheduleDelivery registers the schedule store as the agent's pre-turn
// provider so due jobs are delivered as user messages on the next turn (user
// turns and goal continuation turns alike). Safe to call with a nil store.
func wireScheduleDelivery(agentMgr *core.AgentManager, store tools.ScheduleStore) {
	if agentMgr == nil || store == nil {
		return
	}
	agentMgr.SetPreTurnProvider(&schedulePreTurnProvider{store: store})
}
