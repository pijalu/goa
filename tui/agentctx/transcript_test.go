// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentctx

import "testing"

// TestAgentTranscript_MountUnmountPreservesEntries verifies that unmounting a
// transcript and remounting it leaves its conversation entries untouched: the
// entry list is persistent state, so detach/reattach is loss-free.
func TestAgentTranscript_MountUnmountPreservesEntries(t *testing.T) {
	tr := NewAgentTranscript(MainAgentID)
	if tr.Mounted() {
		t.Fatal("new transcript should start unmounted")
	}

	tr.View().AddUserMessage("hello")
	tr.View().AddAssistantMessage("hi there")
	before := tr.Snapshot()
	if len(before) != 2 {
		t.Fatalf("expected 2 entries before unmount, got %d", len(before))
	}

	tr.Mount()
	if !tr.Mounted() {
		t.Fatal("Mount should mark the transcript mounted")
	}
	tr.Unmount()
	if tr.Mounted() {
		t.Fatal("Unmount should clear the mounted flag")
	}
	tr.Mount()

	after := tr.Snapshot()
	if len(after) != len(before) {
		t.Fatalf("entries lost across unmount/remount: before=%d after=%d", len(before), len(after))
	}
	for i := range before {
		if before[i].Type != after[i].Type || before[i].Text != after[i].Text {
			t.Errorf("entry %d changed across mount cycle: before=%+v after=%+v", i, before[i], after[i])
		}
	}
	if tr.Len() != len(before) {
		t.Errorf("Len() = %d, want %d", tr.Len(), len(before))
	}
}

// TestAgentTranscript_TwoTranscriptsDisjoint verifies that two transcripts own
// independent entry sets: appending to one never appears in the other.
func TestAgentTranscript_TwoTranscriptsDisjoint(t *testing.T) {
	a := NewAgentTranscript("main")
	b := NewAgentTranscript("coder/dlg-01")

	a.View().AddUserMessage("main user")
	a.View().AddAssistantMessage("main reply")
	b.View().AddUserMessage("coder user")

	asnap := a.Snapshot()
	bsnap := b.Snapshot()
	if len(asnap) != 2 {
		t.Fatalf("transcript a should hold 2 entries, got %d", len(asnap))
	}
	if len(bsnap) != 1 {
		t.Fatalf("transcript b should hold 1 entry, got %d", len(bsnap))
	}
	for _, e := range bsnap {
		if e.Text == "main user" || e.Text == "main reply" {
			t.Errorf("transcript b leaked a main-agent entry: %+v", e)
		}
	}
	if bsnap[0].Text != "coder user" {
		t.Errorf("transcript b first entry = %q, want %q", bsnap[0].Text, "coder user")
	}
}

// TestAgentTranscript_CompositorPureData verifies that the per-agent
// compositor holder is inert: it carries a snapshot with no terminal side
// effects, and Save/Snapshot round-trips the state.
func TestAgentTranscript_CompositorPureData(t *testing.T) {
	tr := NewAgentTranscript(MainAgentID)
	comp := tr.Compositor()
	if comp == nil {
		t.Fatal("transcript should own a compositor holder")
	}
	if got := comp.Snapshot(); got.PrevLines != nil || got.ScrollTop != 0 || got.VT != 0 {
		t.Fatalf("fresh compositor snapshot should be zero, got %+v", got)
	}

	want := CompositorSnapshot{PrevLines: []string{"row0", "row1"}, ScrollTop: 7, VT: 3}
	comp.Save(want)
	got := comp.Snapshot()
	if got.ScrollTop != 7 || got.VT != 3 || len(got.PrevLines) != 2 || got.PrevLines[0] != "row0" {
		t.Errorf("snapshot round-trip mismatch: got %+v want %+v", got, want)
	}
}

// TestAgentViewRegistry_HoldsMainView verifies the T1 registry shape: it holds
// exactly the main agent's view, which is the active view.
func TestAgentViewRegistry_HoldsMainView(t *testing.T) {
	reg := NewAgentViewRegistry()
	if reg.Len() != 0 {
		t.Fatalf("empty registry Len = %d, want 0", reg.Len())
	}
	if id, v := reg.Active(); id != "" || v != nil {
		t.Fatalf("empty registry Active = (%q, %v), want (\"\", nil)", id, v)
	}

	tr := NewAgentTranscript(MainAgentID)
	reg.Add(MainAgentID, &AgentView{Transcript: tr, Compositor: tr.Compositor()})

	if reg.Len() != 1 {
		t.Fatalf("registry Len = %d, want 1", reg.Len())
	}
	id, v := reg.Active()
	if id != MainAgentID {
		t.Errorf("active id = %q, want %q", id, MainAgentID)
	}
	if v == nil || v.Transcript != tr {
		t.Errorf("active view does not hold the main transcript: %+v", v)
	}
	got, ok := reg.Get(MainAgentID)
	if !ok || got.Transcript != tr {
		t.Errorf("Get(%q) = (%v, %v), want the main view", MainAgentID, got, ok)
	}
	if _, ok := reg.Get("nope"); ok {
		t.Error("Get of unknown id should report not-found")
	}
}

// TestAgentViewRegistry_FirstAddedStaysActive verifies that the active pointer
// latches to the first view added and does not move on later additions.
func TestAgentViewRegistry_FirstAddedStaysActive(t *testing.T) {
	reg := NewAgentViewRegistry()
	main := NewAgentTranscript(MainAgentID)
	other := NewAgentTranscript("coder/dlg-02")
	reg.Add(MainAgentID, &AgentView{Transcript: main, Compositor: main.Compositor()})
	reg.Add("coder/dlg-02", &AgentView{Transcript: other, Compositor: other.Compositor()})

	if reg.Len() != 2 {
		t.Fatalf("registry Len = %d, want 2", reg.Len())
	}
	if id, _ := reg.Active(); id != MainAgentID {
		t.Errorf("active id = %q after second Add, want it to stay %q", id, MainAgentID)
	}
}
