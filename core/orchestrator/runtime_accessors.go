// SPDX-License-Identifier: GPL-3.0-or-later
package orchestrator

import "strings"

type AgentRow struct {
	ID, Role, Model, Provider, Thinking                                       string
	Status                                                                    AgentStatus
	Turns, TokensIn, TokensOut, CacheRead, CacheCreation, ToolCalls, Messages int
}

func (r *Runtime) Snapshot() []AgentRow {
	hs := r.pool.Live()
	rows := make([]AgentRow, 0, len(hs))
	for _, h := range hs {
		s := h.Stats.Snapshot()
		rows = append(rows, AgentRow{ID: h.ID, Role: h.Role, Model: h.Model, Provider: h.Provider, Thinking: h.Thinking, Status: s.Status, Turns: s.Turns, TokensIn: s.TokensIn, TokensOut: s.TokensOut, CacheRead: s.CacheRead, CacheCreation: s.CacheCreation, ToolCalls: s.ToolCalls})
	}
	return rows
}
func (r *Runtime) Objective() string   { return r.objective }
func (r *Runtime) Topology() Topology  { return r.topology }
func (r *Runtime) SetName(name string) { r.name = name }
func (r *Runtime) Name() string        { return r.name }
func (r *Runtime) NameOrID() string {
	if r.name != "" {
		return r.name
	}
	return r.runID
}
func (r *Runtime) RunID() string { return r.runID }
func (r *Runtime) Resume(store EventStore, snap *RunSnapshot) {
	if r.sink != nil {
		r.sink.close()
	}
	r.store = store
	r.sink = newDurableSink(store)
	if snap == nil {
		return
	}
	r.resume = snap
	id := snap.RunID
	r.SetIDGenerator(func() string { return id })
}
func (r *Runtime) resumeFinishedRole(role string) (string, string, bool) {
	if r.resume == nil {
		return "", "", false
	}
	for _, a := range r.resume.Agents {
		if a.Role == role && a.Status == AgentFinished {
			return a.ID, strings.Join(a.Messages, ""), true
		}
	}
	return "", "", false
}
func (r *Runtime) resumeSkip(role, id, msg string) {
	r.setLastMessage(role, msg)
	r.emit(Event{Type: EventAgentStarted, AgentID: id, Role: role, Payload: map[string]any{"resumed": true}})
	r.emit(Event{Type: EventAgentFinished, AgentID: id, Role: role, Payload: map[string]any{"outcome": "resumed", "text": msg}})
}
