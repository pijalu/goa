// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"errors"
	"os"
	"sort"
	"time"
)

// AgentSnapshot is the reconstructed view of a single managed agent after
// replaying a run's event log. It is what Resume feeds back into a fresh
// Orchestrator so the run can continue.
type AgentSnapshot struct {
	ID          string
	Role        string
	Model       string
	Status      AgentStatus
	Turns       int
	TokensIn    int
	TokensOut   int
	CacheRead   int
	CacheCreation int
	ToolCalls   int
	StartedAt   time.Time
	UpdatedAt   time.Time
	PendingSteering []string
	Messages     []string
}

// RunSnapshot is the fully-replayed state of a run, used by Resume.
type RunSnapshot struct {
	RunID     string
	Topology  Topology
	Objective string
	GoalID    string
	Started   bool
	Finished  bool
	Agents    map[string]*AgentSnapshot
}

// ReplaySnapshot rebuilds the in-memory state of a run from its event log.
// It does NOT re-acquire live agents — a crashed agent is marked Crashed so
// the runtime knows to re-acquire it. This is the pure, side-effect-free core
// of Phase 4 step 21 (Resume); the actual re-acquisition lives in the runtime.
func ReplaySnapshot(store EventStore) (*RunSnapshot, error) {
	if store == nil {
		return nil, errors.New("orchestrator: nil event store")
	}
	events, err := store.Replay()
	if err != nil {
		return nil, err
	}
	snap := &RunSnapshot{Agents: map[string]*AgentSnapshot{}}
	for _, e := range events {
		snap.RunID = e.RunID
		applyEvent(snap, e)
	}
	return snap, nil
}

func applyEvent(snap *RunSnapshot, e Event) {
	switch e.Type {
	case EventRunStarted:
		snap.Started = true
		snap.Objective = stringVal(e.Payload, "objective")
		snap.GoalID = stringVal(e.Payload, "goal_id")
		if t := stringVal(e.Payload, "topology"); t != "" {
			snap.Topology = Topology(t)
		}
	case EventAgentStarted:
		a := getAgent(snap, e.AgentID)
		a.Role, a.Model, a.Status = e.Role, e.Model, AgentIdle
		a.StartedAt = e.Timestamp
		a.UpdatedAt = e.Timestamp
	case EventAgentMessage:
		a := getAgent(snap, e.AgentID)
		if text := stringVal(e.Payload, "text"); text != "" {
			a.Messages = append(a.Messages, text)
		}
		a.UpdatedAt = e.Timestamp
	case EventAgentSteered:
		a := getAgent(snap, e.AgentID)
		if text := stringVal(e.Payload, "text"); text != "" {
			a.PendingSteering = append(a.PendingSteering, text)
		}
	case EventAgentStats:
		a := getAgent(snap, e.AgentID)
		a.Turns = intVal(e.Payload, "turns", a.Turns)
		a.TokensIn = intVal(e.Payload, "tokens_in", a.TokensIn)
		a.TokensOut = intVal(e.Payload, "tokens_out", a.TokensOut)
		a.CacheRead = intVal(e.Payload, "cache_read", a.CacheRead)
		a.CacheCreation = intVal(e.Payload, "cache_creation", a.CacheCreation)
		a.ToolCalls = intVal(e.Payload, "tool_calls", a.ToolCalls)
	case EventAgentFinished:
		a := getAgent(snap, e.AgentID)
		if outcome := stringVal(e.Payload, "outcome"); outcome == "crashed" {
			a.Status = AgentCrashed
		} else {
			a.Status = AgentFinished
		}
		a.UpdatedAt = e.Timestamp
	case EventRunFinished:
		snap.Finished = true
	}
}

func getAgent(snap *RunSnapshot, id string) *AgentSnapshot {
	if id == "" {
		return &AgentSnapshot{}
	}
	a, ok := snap.Agents[id]
	if !ok {
		a = &AgentSnapshot{ID: id}
		snap.Agents[id] = a
	}
	return a
}

func stringVal(p map[string]any, k string) string {
	if v, ok := p[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func intVal(p map[string]any, k string, fallback int) int {
	if v, ok := p[k]; ok {
		switch n := v.(type) {
		case int:
			return n
		case int64:
			return int(n)
		case float64:
			return int(n)
		}
	}
	return fallback
}

// RunSummary is the lightweight descriptor returned by ListRuns for the TUI
// run picker.
type RunSummary struct {
	RunID     string
	Finished  bool
	StartedAt time.Time
	UpdatedAt time.Time
	Topology  Topology
	Objective string
	AgentCount int
}

// ListRuns scans rootDir/<run-id>/events.jsonl and returns one summary per
// run, most-recently-updated first. Directories without a readable event log
// are skipped.
func ListRuns(rootDir string) ([]RunSummary, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var summaries []RunSummary
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		runID := ent.Name()
		store := NewFileEventStore(rootDir, runID)
		snap, err := ReplaySnapshot(store)
		if err != nil {
			continue
		}
		if !snap.Started && len(snap.Agents) == 0 {
			continue // empty / not-a-run directory
		}
		s := RunSummary{
			RunID:      runID,
			Finished:   snap.Finished,
			Topology:   snap.Topology,
			Objective:  snap.Objective,
			AgentCount: len(snap.Agents),
		}
		events, _ := store.Replay()
		if len(events) > 0 {
			s.StartedAt = events[0].Timestamp
			s.UpdatedAt = events[len(events)-1].Timestamp
		}
		summaries = append(summaries, s)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})
	return summaries, nil
}
