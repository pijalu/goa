// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"

	"github.com/pijalu/goa/config"
	internal "github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
)

func marker(providerID, modelID string) agentic.OutputEvent {
	return ModelSelectedMarker(ModelMarkerSourceSwitch, providerID, modelID)
}

// TestSessionModelFromEvents_LastWins pins the read-back contract for the
// bugs.md entry "session restore does not bind the session's provider/model":
// empty and legacy streams report not-ok; non-marker events never count; and
// among several markers the LAST one wins for BOTH provider and model.
func TestSessionModelFromEvents_LastWins(t *testing.T) {
	if _, _, ok := SessionModelFromEvents(nil); ok {
		t.Fatal("empty event stream must report ok=false")
	}
	if _, _, ok := SessionModelFromEvents([]agentic.OutputEvent{
		{Type: agentic.EventContent, Text: "hello"},
		{Type: agentic.EventProgress, Metadata: map[string]string{"event": "companion_started"}},
	}); ok {
		t.Fatal("stream without a model_selected marker must report ok=false")
	}

	events := []agentic.OutputEvent{
		{Type: agentic.EventContent},
		marker("p1", "m1"),
		{Type: agentic.EventToolResult},
		marker("p2", "m2"),
	}
	gotP, gotM, ok := SessionModelFromEvents(events)
	if !ok || gotP != "p2" || gotM != "m2" {
		t.Fatalf("last-wins: got (%q,%q,%v), want (p2,m2,true)", gotP, gotM, ok)
	}
}

// TestSessionModelFromEvents_SkipsModellessMarkers: a malformed record
// without a model cannot drive an adoption; scanning continues to older ones.
func TestSessionModelFromEvents_SkipsModellessMarkers(t *testing.T) {
	events := []agentic.OutputEvent{
		marker("old", "m0"),
		ModelSelectedMarker(ModelMarkerSourceStart, "", ""),
	}
	gotP, gotM, ok := SessionModelFromEvents(events)
	if !ok || gotP != "old" || gotM != "m0" {
		t.Fatalf("model-less marker must be skipped: got (%q,%q,%v)", gotP, gotM, ok)
	}
}

// newMarkerTestManager wires a manager with a REAL session store in a temp
// project dir so marker writes can be read back from disk.
func newMarkerTestManager(t *testing.T) (*AgentManager, *SessionStore) {
	t.Helper()
	dir := t.TempDir()
	ss := NewSessionStore(dir)
	cfg := &config.Config{}
	am := NewAgentManager(cfg, ss, nil, NewSessionState(internal.ModeState{Major: internal.MajorCoder}), event.MakeBus(4, 4, 4, 4), dir)
	t.Cleanup(func() { _ = ss.Close() })
	return am, ss
}

func sessionMarkers(t *testing.T, ss *SessionStore, name string) []agentic.OutputEvent {
	t.Helper()
	events, err := ss.LoadSession(name)
	if err != nil {
		t.Fatalf("LoadSession(%s): %v", name, err)
	}
	var found []agentic.OutputEvent
	for _, ev := range events {
		if isModelSelectedMarker(ev) {
			found = append(found, ev)
		}
	}
	return found
}

func startOn(am *AgentManager, mdl agenticprovider.Model) error {
	_, err := am.StartSession(mdl, agenticprovider.StreamOptions{}, "sys", nil, &config.Config{})
	return err
}

// TestAgentManager_StartSession_RecordsModelMarker: binding a model to a
// fresh session persists exactly one model_selected record carrying the
// resolved model ID + provider, under THAT session's file.
func TestAgentManager_StartSession_RecordsModelMarker(t *testing.T) {
	am, ss := newMarkerTestManager(t)

	mdl := agenticprovider.Model{
		ID:       "m1",
		Name:     "model-one-api",
		Api:      agenticprovider.ApiOpenAICompletions,
		Provider: agenticprovider.Provider("p1"),
		BaseURL:  "https://p1.example/chat/completions",
	}
	if err := startOn(am, mdl); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	sid := am.SessionID()
	if sid == "" {
		t.Fatal("session must be live after StartSession")
	}
	_ = ss.Close()

	markers := sessionMarkers(t, ss, sid)
	if len(markers) != 1 {
		t.Fatalf("want exactly 1 start marker, got %d", len(markers))
	}
	p, m, ok := SessionModelFromEvents([]agentic.OutputEvent{markers[0]})
	if !ok || p != "p1" || m != "m1" {
		t.Fatalf("start marker = (%q,%q,%v), want (p1,m1,true)", p, m, ok)
	}
}

// TestAgentManager_SetModel_MarkerOnSwitchOnly: every CHANGED couple appends
// a switch marker to the current session file; repeating the same couple
// stays silent (no duplicate noise on picker re-selects).
func TestAgentManager_SetModel_MarkerOnSwitchOnly(t *testing.T) {
	am, ss := newMarkerTestManager(t)

	start := agenticprovider.Model{ID: "m1", Api: agenticprovider.ApiOpenAICompletions, Provider: agenticprovider.Provider("p1")}
	if err := startOn(am, start); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	sid := am.SessionID()

	switched := agenticprovider.Model{ID: "m2", Api: agenticprovider.ApiOpenAICompletions, Provider: agenticprovider.Provider("p2")}
	am.SetModel(switched)
	am.SetModel(switched) // dedupe: identical repeat must not append
	_ = ss.Close()

	markers := sessionMarkers(t, ss, sid)
	if len(markers) != 2 {
		t.Fatalf("markers = %d ([start] + [switch-once]), got %+v", len(markers), markers)
	}
	p, m, ok := SessionModelFromEvents([]agentic.OutputEvent{markers[len(markers)-1]})
	if !ok || p != "p2" || m != "m2" {
		t.Fatalf("last marker = (%q,%q,%v), want (p2,m2,true)", p, m, ok)
	}
}

// TestAgentManager_SetModel_MarkerLandsInCurrentSessionFile verifies the
// switch marker is appended to whatever session is live when the write
// happens (after StartSessionWithID rotates the writer) — the restore path's
// fresh-marker guarantee.
func TestAgentManager_SetModel_MarkerLandsInCurrentSessionFile(t *testing.T) {
	am, ss := newMarkerTestManager(t)

	start := agenticprovider.Model{ID: "m1", Api: agenticprovider.ApiOpenAICompletions, Provider: agenticprovider.Provider("p1")}
	if err := startOn(am, start); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	// Simulate the restore flow re-pointing persistence at the restored id.
	const restored = "restored_session"
	if id := ss.StartSessionWithID(restored); id != restored {
		t.Fatalf("StartSessionWithID = %q, want %q", id, restored)
	}

	switched := agenticprovider.Model{ID: "m9", Api: agenticprovider.ApiOpenAICompletions, Provider: agenticprovider.Provider("p9")}
	am.SetModel(switched)
	_ = ss.Close()

	// The restored file gains the switch marker with NO start marker.
	markers := sessionMarkers(t, ss, restored)
	if len(markers) != 1 {
		t.Fatalf("restored file should hold exactly the fresh switch marker, got %d", len(markers))
	}
}
