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

// registryWithThreeViews builds a registry holding main + two coder
// delegation views (insertion order).
func registryWithThreeViews() *AgentViewRegistry {
	reg := NewAgentViewRegistry()
	for _, id := range []string{MainAgentID, "dlg-coder-03", "dlg-coder-07"} {
		tr := NewAgentTranscript(id)
		reg.Add(id, &AgentView{Transcript: tr, Compositor: tr.Compositor()})
	}
	return reg
}

// TestAgentViewRegistry_Order asserts the insertion-order surface: IDs and
// Index agree with the order views were added.
func TestAgentViewRegistry_Order(t *testing.T) {
	reg := registryWithThreeViews()
	if reg.Len() != 3 {
		t.Fatalf("Len = %d, want 3", reg.Len())
	}
	if got := reg.IDs(); len(got) != 3 || got[0] != MainAgentID || got[2] != "dlg-coder-07" {
		t.Fatalf("IDs = %v, want insertion order", got)
	}
	if idx := reg.Index("dlg-coder-07"); idx != 2 {
		t.Errorf("Index(dlg-coder-07) = %d, want 2", idx)
	}
	if idx := reg.Index("nope"); idx != -1 {
		t.Errorf("Index(nope) = %d, want -1", idx)
	}
	assertActive(t, reg, MainAgentID, 0)
}

// TestAgentViewRegistry_Cycle covers pointer cycling: forward moves through
// the insertion order, wraps at the end, and backward wraps the other way.
func TestAgentViewRegistry_Cycle(t *testing.T) {
	reg := registryWithThreeViews()

	// Cycle forward: main → dlg-coder-03 → dlg-coder-07 → wraps to main.
	for _, want := range []string{"dlg-coder-03", "dlg-coder-07", MainAgentID} {
		id, v := reg.Cycle(1)
		if id != want || v == nil {
			t.Errorf("Cycle(1) = (%q, %v), want active %q", id, v, want)
		}
	}
	// Cycle backward wraps the other way: main → dlg-coder-07.
	if id, _ := reg.Cycle(-1); id != "dlg-coder-07" {
		t.Errorf("Cycle(-1) = %q, want dlg-coder-07", id)
	}
	assertActive(t, reg, "dlg-coder-07", 2)
}

// TestAgentViewRegistry_SelectByID covers direct selection: known ids move
// the pointer, unknown ids report false and leave it untouched.
func TestAgentViewRegistry_SelectByID(t *testing.T) {
	reg := registryWithThreeViews()

	if v, ok := reg.SelectByID("dlg-coder-07"); !ok || v == nil {
		t.Fatal("SelectByID(dlg-coder-07) should succeed")
	}
	assertActive(t, reg, "dlg-coder-07", 2)
	if _, ok := reg.SelectByID("dlg-nope-99"); ok {
		t.Error("SelectByID of unknown id should report false")
	}
	assertActive(t, reg, "dlg-coder-07", 2)
}

// assertActive verifies the active id and its tab-order index.
func assertActive(t *testing.T, reg *AgentViewRegistry, wantID string, wantIdx int) {
	t.Helper()
	if id, v := reg.Active(); id != wantID || v == nil {
		t.Errorf("Active = (%q, %v), want (%q, view)", id, v, wantID)
	}
	if idx := reg.ActiveIndex(); idx != wantIdx {
		t.Errorf("ActiveIndex = %d, want %d", idx, wantIdx)
	}
}

// TestAgentViewRegistry_RemoveInactive covers removing non-active views: the
// order shrinks, unknown ids report false, and the active pointer is unmoved.
func TestAgentViewRegistry_RemoveInactive(t *testing.T) {
	reg := registryWithThreeViews()

	if reg.Remove("dlg-nope") {
		t.Error("Remove of unknown id should report false")
	}
	if !reg.Remove("dlg-coder-07") {
		t.Fatal("Remove(dlg-coder-07) should succeed")
	}
	if reg.Len() != 2 {
		t.Fatalf("Len = %d after remove, want 2", reg.Len())
	}
	assertActive(t, reg, MainAgentID, 0)
	if got := reg.IDs(); len(got) != 2 || got[1] != "dlg-coder-03" {
		t.Errorf("IDs after remove = %v, want [main dlg-coder-03]", got)
	}
}

// TestAgentViewRegistry_RemoveActive covers removing the ACTIVE view: the
// pointer hands off to the nearest neighbor (previous in insertion order when
// present, else next) and never dangles, down to the empty registry.
func TestAgentViewRegistry_RemoveActive(t *testing.T) {
	reg := registryWithThreeViews()

	// Removing the MIDDLE view while it is active lands on its predecessor.
	if _, ok := reg.SelectByID("dlg-coder-03"); !ok {
		t.Fatal("SelectByID(dlg-coder-03) should succeed")
	}
	if !reg.Remove("dlg-coder-03") {
		t.Fatal("Remove(dlg-coder-03) should succeed")
	}
	assertActive(t, reg, MainAgentID, 0)

	// Removing the FIRST view while active lands on its successor.
	if !reg.Remove(MainAgentID) {
		t.Fatal("Remove(main) should succeed")
	}
	assertActive(t, reg, "dlg-coder-07", 0)

	// Removing the last view empties the registry; navigation stays inert.
	reg.Remove("dlg-coder-07")
	if reg.Len() != 0 {
		t.Fatalf("Len = %d, want 0", reg.Len())
	}
	if id, v := reg.Active(); id != "" || v != nil {
		t.Errorf("empty registry Active = (%q, %v), want (\"\", nil)", id, v)
	}
	if id, v := reg.Cycle(1); id != "" || v != nil {
		t.Errorf("Cycle on empty registry = (%q, %v), want (\"\", nil)", id, v)
	}
}

// TestAgentViewRegistry_BadgeBookkeeping covers the T2 badge STATE (rendering
// lands in T5): background activity and errors are recorded per view, and
// activating a view acknowledges (clears) its badges.
func TestAgentViewRegistry_BadgeBookkeeping(t *testing.T) {
	reg := NewAgentViewRegistry()
	main := NewAgentTranscript(MainAgentID)
	other := NewAgentTranscript("dlg-coder-03")
	reg.Add(MainAgentID, &AgentView{Transcript: main, Compositor: main.Compositor()})
	reg.Add("dlg-coder-03", &AgentView{Transcript: other, Compositor: other.Compositor()})

	reg.MarkActivity("dlg-coder-03")
	reg.MarkError("dlg-coder-03")
	activity, hadErr := reg.Badges("dlg-coder-03")
	if !activity || !hadErr {
		t.Fatalf("Badges = (%v, %v), want (true, true)", activity, hadErr)
	}
	// Unknown ids are inert.
	reg.MarkActivity("dlg-nope")
	if activity, _ := reg.Badges("dlg-nope"); activity {
		t.Error("MarkActivity on unknown id should be a no-op")
	}

	// Activating the view acknowledges both badges.
	if _, ok := reg.SelectByID("dlg-coder-03"); !ok {
		t.Fatal("SelectByID failed")
	}
	activity, hadErr = reg.Badges("dlg-coder-03")
	if activity || hadErr {
		t.Errorf("Badges after activation = (%v, %v), want (false, false)", activity, hadErr)
	}

	// Cycle acknowledges the target's badges too.
	reg.MarkActivity(MainAgentID)
	reg.Cycle(1) // → main (wrap)
	if activity, _ := reg.Badges(MainAgentID); activity {
		t.Error("Cycle to a view should clear its activity badge")
	}
}
