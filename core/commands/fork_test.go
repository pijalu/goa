// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/sessiontree"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

// scriptedSelector answers SelectOption calls from a script, recording each
// invocation for inspection. When the script is exhausted the picker is
// treated as cancelled (ok=false).
type scriptedSelector struct {
	calls   []selectCall
	answers []selectAnswer
}

type selectCall struct {
	title   string
	items   []tui.SelectorItem
	current string
}

type selectAnswer struct {
	value string
	ok    bool
}

func (s *scriptedSelector) SelectOption(title string, items []tui.SelectorItem, current string, onSelected func(string, bool)) {
	s.calls = append(s.calls, selectCall{title: title, items: items, current: current})
	if len(s.answers) == 0 {
		if onSelected != nil {
			onSelected("", false)
		}
		return
	}
	ans := s.answers[0]
	s.answers = s.answers[1:]
	if onSelected != nil {
		onSelected(ans.value, ans.ok)
	}
}

// forkFixtureEvents builds an interleaved user/assistant/tool event stream
// with three user turns.
func forkFixtureEvents() []agentic.OutputEvent {
	return []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.User, Text: "first question"},
		{Type: agentic.EventContent, Role: agentic.Assistant, Text: "first answer"},
		{Type: agentic.EventEnd},
		{Type: agentic.EventContent, Role: agentic.User, Text: "second question"},
		{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: "ls", ToolCallID: "tc1"},
		{Type: agentic.EventToolResult, ToolCallID: "tc1", ToolName: "bash", ToolResult: "files"},
		{Type: agentic.EventContent, Role: agentic.Assistant, Text: "second answer"},
		{Type: agentic.EventEnd},
		{Type: agentic.EventContent, Role: agentic.User, Text: "third question"},
		{Type: agentic.EventContent, Role: agentic.Assistant, Text: "third answer"},
		{Type: agentic.EventEnd},
	}
}

// forkFixtureStore returns a store with one forkable session ("src") holding
// forkFixtureEvents plus a command-only session that must be filtered out.
func forkFixtureStore() *fakeSessionStore {
	store := newSessionStore([]core.SessionInfo{
		{Name: "src", Date: time.Now(), EventCount: 11, HasModelTurn: true},
		{Name: "cmds", Date: time.Now(), EventCount: 2, HasModelTurn: false},
	})
	store.AddEvents("src", forkFixtureEvents())
	return store
}

// waitReplay polls until n events have been replayed or the deadline passes.
func waitReplay(es *fakeEventSink, n int) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(es.Replayed()) >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// 1. No sessions → message, no selector shown.
func TestForkPicker_NoSessions(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	sel := &scriptedSelector{}
	store := newSessionStore(nil)

	if err := forkSessionPicker(w, es, sel, store, nil, 0, nil); err != nil {
		t.Fatalf("forkSessionPicker: %v", err)
	}
	if !strings.Contains(w.Text(), "No saved sessions found.") {
		t.Errorf("output = %q, want 'No saved sessions found.'", w.Text())
	}
	if len(sel.calls) != 0 {
		t.Errorf("selector calls = %d, want 0", len(sel.calls))
	}
}

// 2. Session picker shown with timestamped labels, PreserveOrder, model-turn filter.
func TestForkPicker_SessionItems(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	sel := &scriptedSelector{answers: []selectAnswer{{value: "", ok: false}}}
	store := forkFixtureStore()

	if err := forkSessionPicker(w, es, sel, store, nil, 0, nil); err != nil {
		t.Fatalf("forkSessionPicker: %v", err)
	}
	if len(sel.calls) != 1 {
		t.Fatalf("selector calls = %d, want 1", len(sel.calls))
	}
	items := sel.calls[0].items
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1 (command-only session filtered)", len(items))
	}
	if items[0].Value != "src" {
		t.Errorf("item value = %q, want src", items[0].Value)
	}
	if !items[0].PreserveOrder {
		t.Error("item PreserveOrder = false, want true")
	}
	if !strings.Contains(items[0].Label, "src") {
		t.Errorf("item label = %q, want it to contain session name", items[0].Label)
	}
}

// 3. Turn picker: user turns only, chronological, "Turn N" labels, last preselected.
func TestForkPicker_TurnItems(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	// Cancel on the turn picker (second selector call).
	sel := &scriptedSelector{answers: []selectAnswer{{value: "src", ok: true}}}
	store := forkFixtureStore()

	if err := forkSessionPicker(w, es, sel, store, nil, 0, nil); err != nil {
		t.Fatalf("forkSessionPicker: %v", err)
	}
	if len(sel.calls) != 2 {
		t.Fatalf("selector calls = %d, want 2 (session + turn)", len(sel.calls))
	}
	call := sel.calls[1]
	if len(call.items) != 3 {
		t.Fatalf("turn items = %d, want 3 user turns", len(call.items))
	}
	for i, item := range call.items {
		want := fmt.Sprintf("Turn %d", i+1)
		if !strings.HasPrefix(item.Label, want) {
			t.Errorf("item %d label = %q, want prefix %q", i, item.Label, want)
		}
		if !item.PreserveOrder {
			t.Errorf("item %d PreserveOrder = false, want true", i)
		}
	}
	// Chronological order: first item labels the first user message.
	if !strings.Contains(call.items[0].Label, "first question") {
		t.Errorf("first item label = %q, want it to contain 'first question'", call.items[0].Label)
	}
	// Cursor preselected on the last turn (pi-style: most recent message).
	last := call.items[len(call.items)-1]
	if call.current != last.Value {
		t.Errorf("current = %q, want last turn value %q", call.current, last.Value)
	}
}

// 4. Fork truncation: agent history equals EventsToHistory(events[:cut]);
// store writer switched to a derived fork ID; source session untouched.
func TestForkAtTurn_TruncatesHistory(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	store := forkFixtureStore()
	events := forkFixtureEvents()
	cut := 3 // before "second question" (event index 3)

	am := newTestAgentManager()
	agent := agentic.NewAgent(agentic.Config{})
	am.SetActiveAgentForTest(agent)

	if err := forkAtTurn(w, es, store, am, "src", events, cut, 2, 3, 0); err != nil {
		t.Fatalf("forkAtTurn: %v", err)
	}

	assertForkedHistory(t, agent, events, cut)
	assertForkSessionIdentity(t, store, agent, "src", len(events))
}

// assertForkedHistory checks the agent history equals EventsToHistory of the
// truncated event prefix and ends with the pre-cut answer.
func assertForkedHistory(t *testing.T, agent *agentic.Agent, events []agentic.OutputEvent, cut int) {
	t.Helper()
	want := agentic.EventsToHistory(events[:cut])
	got := agent.GetHistory()
	if len(got) != len(want) {
		t.Fatalf("history len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Errorf("history[%d] = {%v %q}, want {%v %q}", i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
		}
	}
	if len(got) == 0 || got[len(got)-1].Content != "first answer" {
		t.Errorf("last history message = %+v, want 'first answer'", got)
	}
}

// assertForkSessionIdentity checks the store writer switched to a derived fork
// ID (never the source ID), the agent stream options carry that derived ID for
// persistence identity, and the source session events are untouched.
func assertForkSessionIdentity(t *testing.T, store *fakeSessionStore, agent *agentic.Agent, srcID string, srcEvents int) {
	t.Helper()
	started := store.StartedID()
	if started == "" {
		t.Fatal("StartSessionWithID not called")
	}
	if started == srcID {
		t.Error("fork session ID = source ID; want a fresh derived ID")
	}
	wantPrefix := srcID + "_fork_"
	if !strings.HasPrefix(started, wantPrefix) {
		t.Errorf("fork session ID = %q, want prefix %s", started, wantPrefix)
	}
	// Agent stream options carry the fork ID for persistence identity.
	if sid := agent.StreamOptions().SessionID; sid != started {
		t.Errorf("agent SessionID = %q, want %q", sid, started)
	}
	// Source events map untouched.
	if len(store.events[srcID]) != srcEvents {
		t.Errorf("source events mutated: %d, want %d", len(store.events[srcID]), srcEvents)
	}
}

// 5. Replay: exactly events[:cut] replayed; ClearChat called once.
func TestForkAtTurn_ReplaysTruncatedEvents(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	store := forkFixtureStore()
	events := forkFixtureEvents()
	cut := 8 // before "third question" (event index 8)

	if err := forkAtTurn(w, es, store, nil, "src", events, cut, 3, 3, 0); err != nil {
		t.Fatalf("forkAtTurn: %v", err)
	}
	if !es.ClearCalled() {
		t.Error("ClearChat not called")
	}
	if !waitReplay(es, cut) {
		t.Fatalf("replayed %d events, want %d", len(es.Replayed()), cut)
	}
	replayed := es.Replayed()
	if len(replayed) != cut {
		t.Fatalf("replayed = %d, want exactly %d", len(replayed), cut)
	}
	for i, ev := range replayed {
		if ev.Type != events[i].Type || ev.Text != events[i].Text {
			t.Errorf("replayed[%d] = {%v %q}, want {%v %q}", i, ev.Type, ev.Text, events[i].Type, events[i].Text)
		}
	}
}

// 6a. Cancelling the session picker leaves agent/store untouched.
func TestForkPicker_CancelSession(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	sel := &scriptedSelector{answers: []selectAnswer{{value: "", ok: false}}}
	store := forkFixtureStore()

	if err := forkSessionPicker(w, es, sel, store, nil, 0, nil); err != nil {
		t.Fatalf("forkSessionPicker: %v", err)
	}
	if len(sel.calls) != 1 {
		t.Errorf("selector calls = %d, want 1", len(sel.calls))
	}
	if store.StartedID() != "" {
		t.Error("StartSessionWithID called on cancel")
	}
}

// 6b. Cancelling the turn picker leaves agent/store untouched.
func TestForkPicker_CancelTurn(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	sel := &scriptedSelector{answers: []selectAnswer{
		{value: "src", ok: true},
		{value: "", ok: false},
	}}
	store := forkFixtureStore()

	am := newTestAgentManager()
	agent := agentic.NewAgent(agentic.Config{})
	agent.SetHistory([]agentic.Message{{Type: agentic.Content, Role: agentic.User, Content: "keep me"}})
	am.SetActiveAgentForTest(agent)

	if err := forkSessionPicker(w, es, sel, store, am, 0, nil); err != nil {
		t.Fatalf("forkSessionPicker: %v", err)
	}
	if len(sel.calls) != 2 {
		t.Fatalf("selector calls = %d, want 2", len(sel.calls))
	}
	if store.StartedID() != "" {
		t.Error("StartSessionWithID called on cancel")
	}
	got := agent.GetHistory()
	if len(got) != 1 || got[0].Content != "keep me" {
		t.Errorf("agent history mutated: %+v", got)
	}
}

// 7. Session with no user turns → flash "No messages to fork from", no turn picker.
func TestForkPicker_NoUserTurns(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	sel := &scriptedSelector{answers: []selectAnswer{{value: "empty", ok: true}}}
	store := newSessionStore([]core.SessionInfo{
		{Name: "empty", Date: time.Now(), EventCount: 2, HasModelTurn: true},
	})
	store.AddEvents("empty", []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.Assistant, Text: "unsolicited"},
		{Type: agentic.EventEnd},
	})

	if err := forkSessionPicker(w, es, sel, store, nil, 0, nil); err != nil {
		t.Fatalf("forkSessionPicker: %v", err)
	}
	if len(sel.calls) != 1 {
		t.Errorf("selector calls = %d, want 1 (no turn picker)", len(sel.calls))
	}
	flashes := es.Flashes()
	found := false
	for _, f := range flashes {
		if strings.Contains(f, "No messages to fork from") {
			found = true
		}
	}
	if !found {
		t.Errorf("flashes = %v, want 'No messages to fork from'", flashes)
	}
}

// 8. deriveForkID: unsafe source names fall back to a valid generated ID.
func TestDeriveForkID(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantPrefix string
	}{
		{"plain", "mysession", "mysession_fork_"},
		{"numeric underscore", "1734_ab12cd", "1734_ab12cd_fork_"},
		{"unsafe slash", "bad/name", "fork_"},
		{"unsafe dots", "bad..name", "fork_"},
		{"unsafe space", "bad name", "fork_"},
		{"empty", "", "fork_"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveForkID(tt.src)
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("deriveForkID(%q) = %q, want prefix %q", tt.src, got, tt.wantPrefix)
			}
			if !validForkSessionID(got) {
				t.Errorf("deriveForkID(%q) = %q fails validForkSessionID", tt.src, got)
			}
		})
	}
}

// Editor prefill: confirming a turn passes the selected message text to the
// prefill hook (pi edit + resend parity).
func TestForkPicker_PrefillsSelectedMessage(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	// Turn 2 = event index 3 ("second question").
	sel := &scriptedSelector{answers: []selectAnswer{
		{value: "src", ok: true},
		{value: "3", ok: true},
	}}
	store := forkFixtureStore()
	var prefilled string
	prefill := func(text string) { prefilled = text }

	if err := forkSessionPicker(w, es, sel, store, nil, 0, prefill); err != nil {
		t.Fatalf("forkSessionPicker: %v", err)
	}
	if prefilled != "second question" {
		t.Errorf("prefilled = %q, want 'second question'", prefilled)
	}
	// Sanity: the fork also happened (history truncated before turn 2).
	if !waitReplay(es, 3) {
		t.Errorf("replayed %d events, want 3", len(es.Replayed()))
	}
}

// /fork:<node-id> keeps the session-tree branch behavior.
func TestForkCommand_NodeArgStillBranches(t *testing.T) {
	mgr := sessiontree.NewManager(nil)
	rootID := mgr.Tree().Root().ID
	cmd := &ForkCommand{Manager: mgr}
	buf := &strings.Builder{}
	ctx := core.Context{OutputBuffer: buf}

	if err := cmd.Run(ctx, []string{rootID}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "Created branch") {
		t.Errorf("output = %q, want 'Created branch'", buf.String())
	}
}

// /fork with no args opens the session picker (wired through Context).
func TestForkCommand_NoArgsOpensPicker(t *testing.T) {
	store := forkFixtureStore()
	var pickerCalls int
	buf := &strings.Builder{}
	ctx := core.Context{
		OutputBuffer: buf,
		SessionStore: store,
		SelectOptionFunc: func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
			pickerCalls++
			// Cancel immediately — we only assert the picker was shown.
			onSelected("", false)
		},
	}
	cmd := &ForkCommand{}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if pickerCalls != 1 {
		t.Errorf("picker calls = %d, want 1", pickerCalls)
	}
}
