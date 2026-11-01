// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"testing"

	"github.com/pijalu/goa/tui"
)

// allWizardStates enumerates every declared wizardState in declaration order.
// It is the single source the invariant tests below walk, so a newly added
// state is automatically audited. Keep in sync with the const block in
// wizard_core.go (the final runtime check guards against drift).
func allWizardStates() []wizardState {
	return []wizardState{
		stateWelcome,
		stateProviderType,
		stateProviderEndpoint,
		stateProviderKey,
		stateProviderTest,
		stateModelSelect,
		stateModelSetup,
		stateModelAdvanced,
		stateDone,
	}
}

// stateNames is for readable test failures.
var stateNames = map[wizardState]string{
	stateWelcome:          "stateWelcome",
	stateProviderType:     "stateProviderType",
	stateProviderEndpoint: "stateProviderEndpoint",
	stateProviderKey:      "stateProviderKey",
	stateProviderTest:     "stateProviderTest",
	stateModelSelect:      "stateModelSelect",
	stateModelSetup:       "stateModelSetup",
	stateModelAdvanced:    "stateModelAdvanced",
	stateDone:             "stateDone",
}

func newTestWizard(t *testing.T) *wizardComponent {
	t.Helper()
	done := make(chan *WizardResult, 1)
	return newWizardComponent(&Config{}, nil, "/tmp", done)
}

// Guard: allWizardStates must cover every declared state. stateDone is the
// highest; if a state is added after it this fails and the list needs updating.
func TestWizardStates_enumerationComplete(t *testing.T) {
	states := allWizardStates()
	if int(stateDone) != len(states)-1 {
		t.Fatalf("stateDone = %d, but allWizardStates has %d entries (last index %d); a state was added without updating allWizardStates/stateNames",
			stateDone, len(states), len(states)-1)
	}
	for i, st := range states {
		if int(st) != i {
			t.Errorf("allWizardStates[%d] = %d, want contiguous declaration order", i, st)
		}
		if _, ok := stateNames[st]; !ok {
			t.Errorf("state %d missing from stateNames map", st)
		}
	}
}

// Invariant: every state renders without panicking and produces output.
// (A state missing from wizardRenderers silently renders nothing.)
func TestWizardInvariant_everyStateRenders(t *testing.T) {
	for _, st := range allWizardStates() {
		w := newTestWizard(t)
		w.state = st
		lines := w.Render(80)
		if len(lines) == 0 {
			t.Errorf("%s: Render returned no lines (missing from wizardRenderers?)", stateNames[st])
		}
	}
}

// Invariant: every non-terminal state advances on Enter without freezing.
// stateDone is terminal (advance() returns true → finish). States requiring
// text input still advance (they commit whatever is in the editor).
func TestWizardInvariant_everyStateAdvancesOnEnter(t *testing.T) {
	for _, st := range allWizardStates() {
		if st == stateDone {
			continue // terminal
		}
		w := newTestWizard(t)
		w.state = st
		before := w.state
		w.advance()
		if w.state == before {
			t.Errorf("%s: advance() did not change state (no advance case → Enter freezes)", stateNames[st])
		}
	}
}

// Invariant: every state except the entry screen has a working back path.
// stateWelcome cancels (finish) instead of going back; stateDone goes back too.
func TestWizardInvariant_everyStateHasBackPath(t *testing.T) {
	for _, st := range allWizardStates() {
		if st == stateWelcome {
			continue // entry screen: Esc cancels the wizard
		}
		w := newTestWizard(t)
		w.state = st
		before := w.state
		w.HandleInput(tui.KeyEscape)
		if w.state == before {
			t.Errorf("%s: Escape did not navigate back (missing from staticPreviousStates/dynamicPreviousState → frozen)", stateNames[st])
		}
	}
}

// Invariant: every option list responds to Up/Down by changing its selection
// with wraparound. This is the exact regression class from the old Yes/No
// screens (stateDreamModel froze on Down).
func TestWizardInvariant_optionScreensRespondToUpDown(t *testing.T) {
	// Each option screen maps to a probe that reads its current selection.
	probes := map[wizardState]func(*wizardComponent) int{
		stateProviderType: func(w *wizardComponent) int { return w.main.selectedPresetIndex },
		stateModelSelect:  func(w *wizardComponent) int { return w.main.selectedModelIdx },
		stateModelSetup:   func(w *wizardComponent) int { return w.main.modelFieldIdx },
	}
	for st, probe := range probes {
		w := newTestWizard(t)
		w.main.availableModels = []string{"m0", "m1"} // stateModelSelect needs rows
		w.state = st
		before := probe(w)
		w.HandleInput(tui.KeyDown)
		if probe(w) == before {
			t.Errorf("%s: Down did not change selection (missing from handleVertical → frozen)", stateNames[st])
		}
		mid := probe(w)
		w.HandleInput(tui.KeyUp)
		if probe(w) == mid {
			t.Errorf("%s: Up did not change selection", stateNames[st])
		}
	}
}

// Invariant: every state has a defined step number (never the fallthrough 1
// unless it genuinely is step 1). A header falling through stepForState shows
// "Step 1/2" on a later screen.
func TestWizardInvariant_everyStateHasStepNumber(t *testing.T) {
	// step-1 group is legitimately 1.
	stepOne := map[wizardState]bool{
		stateProviderType: true, stateProviderEndpoint: true,
		stateProviderKey: true, stateProviderTest: true,
	}
	for _, st := range allWizardStates() {
		if st == stateWelcome {
			continue // welcome renders no header
		}
		w := newTestWizard(t)
		got := w.stepForState(st)
		if stepOne[st] {
			if got != 1 {
				t.Errorf("%s: stepForState = %d, want 1", stateNames[st], got)
			}
			continue
		}
		if got <= 1 {
			t.Errorf("%s: stepForState = %d (fell through to default 1; header will show wrong step)", stateNames[st], got)
		}
	}
}

// Invariant: every footer that advertises a quick-pick has a number-key
// handler whose keys actually change the selection. Guards against dead or
// mismatched quick-pick hints. Each entry picks a key + probe such that a
// working handler necessarily moves the probe.
func TestWizardInvariant_advertisedQuickPicksAreHandled(t *testing.T) {
	cases := []struct {
		state wizardState
		key   string
		probe func(*wizardComponent) int
	}{
		// "2" selects preset index 1 (defaults to 0).
		{stateProviderType, "2", func(w *wizardComponent) int { return w.main.selectedPresetIndex }},
		// model list: seed two models, "2" selects index 1 (default 0).
		{stateModelSelect, "2", func(w *wizardComponent) int { return w.main.selectedModelIdx }},
		// "1" toggles reasoning (default true -> false).
		{stateModelAdvanced, "1", func(w *wizardComponent) int { return boolToInt(w.main.modelReasoning) }},
		// "2" cycles thinking level (default high -> xhigh).
		{stateModelAdvanced, "2", func(w *wizardComponent) int {
			return boolToInt(w.main.modelThinkingLevel == "xhigh")
		}},
	}
	for _, tc := range cases {
		w := newTestWizard(t)
		// Seed the model list so stateModelSelect has something to pick from.
		w.main.availableModels = []string{"m0", "m1"}
		w.state = tc.state
		before := tc.probe(w)
		w.handleNumber(tc.key)
		if got := tc.probe(w); got == before {
			t.Errorf("%s: advertised quick-pick key %q left selection at %d (dead key in footer)", stateNames[tc.state], tc.key, got)
		}
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
