// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/plan"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

// newOverlayTestPlan creates an in-review plan with one item and returns an
// open store (caller owns closing it, or the overlay handler does).
func newOverlayTestPlan(t *testing.T) *plan.Store {
	t.Helper()
	store, err := plan.Create(t.TempDir(), "overlay plan")
	if err != nil {
		t.Fatalf("plan.Create: %v", err)
	}
	if _, err := store.AddItem("task one", "do the thing", "", nil, "coder"); err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if err := store.SubmitRevision(); err != nil {
		t.Fatalf("SubmitRevision: %v", err)
	}
	return store
}

// overlayVisibleLines renders the current frame and returns ANSI-stripped
// visible text.
func overlayVisibleLines(sc *uiScenario) string {
	var frame tui.AgentFrame
	sc.engine.ApplySync(func() { frame = sc.engine.AgentFrame() })
	return ansi.Strip(strings.Join(frame.Visible, "\n"))
}

// TestShowPlanPager_OpensOverlayAndCloseClosesStore validates the wiring
// added in the review-fix round: a ShowPlanPager chat event opens the plan
// pager overlay, and dismissing it (q) hides the overlay and closes the
// store via the command-wired OnClose chain.
func TestShowPlanPager_OpensOverlayAndCloseClosesStore(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	store := newOverlayTestPlan(t)

	pager := tui.NewPlanPager(store)
	storeClosed := false
	pager.OnClose = func() { _ = store.Close(); storeClosed = true }

	sc.engine.ApplySync(func() {
		sc.app.showPlanPager(&event.ShowPlanPager{Pager: pager})
	})
	sc.engine.RequestRender()

	if got := overlayVisibleLines(sc); !strings.Contains(got, "task one") {
		t.Fatalf("expected plan content visible in overlay, got:\n%s", got)
	}

	// Dismiss via the pager's own key handling.
	sc.engine.ApplySync(func() { pager.HandleInput("q") })
	sc.engine.RequestRender()

	if !storeClosed {
		t.Error("expected store closed after pager dismiss")
	}
	if got := overlayVisibleLines(sc); strings.Contains(got, "task one") {
		t.Errorf("expected overlay hidden after dismiss, got:\n%s", got)
	}
}

// TestShowPlanPager_ApproveChainsAndCloses verifies the approve wrapper: the
// command's OnApprovePlan runs, and on success the overlay closes through the
// same path as 'q'.
func TestShowPlanPager_ApproveChainsAndCloses(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	store := newOverlayTestPlan(t)

	pager := tui.NewPlanPager(store)
	storeClosed := false
	pager.OnClose = func() { _ = store.Close(); storeClosed = true }
	approved := false
	pager.OnApprovePlan = func() {
		if err := store.Approve(); err != nil {
			t.Errorf("Approve: %v", err)
			return
		}
		approved = true
	}

	sc.engine.ApplySync(func() {
		sc.app.showPlanPager(&event.ShowPlanPager{Pager: pager})
	})
	sc.engine.RequestRender()

	sc.engine.ApplySync(func() { pager.OnApprovePlan() })
	sc.engine.RequestRender()

	if !approved {
		t.Error("expected command approve callback to run")
	}
	if !storeClosed {
		t.Error("expected overlay close (store closed) after successful approve")
	}
}

// TestShowPlanStatus_OpensOverlayAndClosesStore covers /plan status: the
// overlay shows plan status and dismissing it closes the store the command
// opened.
func TestShowPlanStatus_OpensOverlayAndClosesStore(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	store := newOverlayTestPlan(t)

	var overlay *tui.PlanStatusOverlay
	sc.engine.ApplySync(func() {
		overlay = sc.app.showPlanStatus(&event.ShowPlanStatus{Store: store})
	})
	sc.engine.RequestRender()

	if overlay == nil {
		t.Fatal("showPlanStatus returned nil overlay")
	}
	if got := overlayVisibleLines(sc); !strings.Contains(got, "task one") {
		t.Fatalf("expected plan items visible in status overlay, got:\n%s", got)
	}

	// Dismiss via the overlay's own key handling.
	sc.engine.ApplySync(func() { overlay.HandleInput("q") })
	sc.engine.RequestRender()

	if got := overlayVisibleLines(sc); strings.Contains(got, "task one") {
		t.Errorf("expected status overlay hidden after dismiss, got:\n%s", got)
	}

	// The store must be closed: Store.append panics on write-after-close by
	// design, so probe with a mutation and recover.
	closed := func() (panicked bool) {
		defer func() { panicked = recover() != nil }()
		_, _ = store.AddComment("", "probe")
		return false
	}()
	if !closed {
		t.Error("expected store closed after status overlay dismiss")
	}
}
