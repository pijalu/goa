// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// TestOrchestrate_SubAgentWorkPersisted is the bugs.md item K regression: an
// orchestration run's sub-agent turns must be written to the session store,
// so a saved/restored session contains the full run — not just the bare
// /orchestrate command line.
func TestOrchestrate_SubAgentWorkPersisted(t *testing.T) {
	dir := t.TempDir()
	store := core.NewSessionStore(dir)
	store.StartSession()

	a := &App{subs: &subsystems{sessionStore: store}}

	// Drive a representative run through the single choke point.
	a.handleOrchViewEvent(orchpanel.AgentViewEvent{Kind: orchpanel.EvSourceStarted, Meta: map[string]string{"objective": "ship it"}})
	a.handleOrchViewEvent(orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentStarted, AgentID: "coder-1", Role: "coder", Model: "gemma", Provider: "google"})
	a.handleOrchViewEvent(orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentMessage, AgentID: "coder-1", Role: "coder", Text: "writing the fix"})
	a.handleOrchViewEvent(orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentToolCall, AgentID: "coder-1", Role: "coder", Tool: "edit", ToolInput: `{"path":"x.go"}`})
	a.handleOrchViewEvent(orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentFinished, AgentID: "coder-1", Role: "coder", Status: "ok"})
	a.handleOrchViewEvent(orchpanel.AgentViewEvent{Kind: orchpanel.EvSourceFinished, Status: "ok"})

	sessionID := store.SessionID()
	// WriteEvent is async (batched to disk); Close flushes before we read.
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	events, err := store.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	var blob strings.Builder
	for _, ev := range events {
		if ev.Type == agentic.EventContent {
			blob.WriteString(ev.Text)
			blob.WriteString("\n")
		}
	}
	out := blob.String()
	for _, want := range []string{"ship it", "coder", "writing the fix", "edit", "agent finished", "run finished"} {
		if !strings.Contains(out, want) {
			t.Errorf("session missing sub-agent work %q; persisted:\n%s", want, out)
		}
	}
}

// TestOrchestrate_PersistedEventsDoNotPoisonMainHistory: sub-agent turns are
// persisted as SYSTEM content, which EventsToHistory skips — so restoring the
// session must NOT fold sub-agent output into the main agent's conversation
// history (that would corrupt the restored model context).
func TestOrchestrate_PersistedEventsDoNotPoisonMainHistory(t *testing.T) {
	dir := t.TempDir()
	store := core.NewSessionStore(dir)
	store.StartSession()

	a := &App{subs: &subsystems{sessionStore: store}}
	a.handleOrchViewEvent(orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentMessage, AgentID: "coder-1", Role: "coder", Text: "sub-agent secret"})

	sessionID := store.SessionID()
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	events, err := store.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	history := agentic.EventsToHistory(events)
	for _, m := range history {
		if strings.Contains(m.Content, "sub-agent secret") {
			t.Fatalf("sub-agent output leaked into main agent history: %+v", m)
		}
	}
}
