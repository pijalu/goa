// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// TestResolveContextWindow_RegistryValueWins pins the first tier: a model the
// catalog knows resolves with its OWN window, never the provider floor. z.ai
// serves GLM-5.2 with a 1M window, so flooring it at the 131072 unknown-model
// value would compact a fifth of the usable context away.
func TestResolveContextWindow_RegistryValueWins(t *testing.T) {
	got := ResolveContextWindow(provider.ProviderZai, "glm-5.2")
	if got != 1000000 {
		t.Errorf("ResolveContextWindow(zai, glm-5.2) = %d, want 1000000 (registry value, not the floor)", got)
	}
}

// TestResolveContextWindow_ZaiFloorForUnknownModel is the bugs.md #3 guard: a
// z.ai model id the embedded snapshot does not carry must still resolve to a
// non-zero window, otherwise the pre-flight token-count thresholds are inert
// and the overflow is only ever "caught" by a context-length error z.ai never
// raises.
func TestResolveContextWindow_ZaiFloorForUnknownModel(t *testing.T) {
	for _, prov := range []provider.Provider{provider.ProviderZai, provider.ProviderZaiApi} {
		got := ResolveContextWindow(prov, "glm-99-not-in-the-snapshot")
		if got <= 0 {
			t.Errorf("ResolveContextWindow(%s, unknown) = %d, want a positive floor", prov, got)
		}
	}
}

// TestResolveContextWindow_FloorIsConservative pins the safety direction: the
// declared floor must not exceed the smallest window the provider actually
// serves to a tool-calling chat model, or compression would fire after a
// silent overflow instead of before it.
func TestResolveContextWindow_FloorIsConservative(t *testing.T) {
	def := schema.LookupProviderDef(provider.ProviderZai)
	if def == nil || def.DefaultContextWindow == 0 {
		t.Fatal("zai ProviderDef must declare a context-window floor")
	}
	var smallest int
	for _, m := range GetModels(provider.ProviderZai) {
		if m.ContextWindow <= 0 || !hasTextInput(m) {
			continue
		}
		if smallest == 0 || m.ContextWindow < smallest {
			smallest = m.ContextWindow
		}
	}
	if smallest == 0 {
		t.Fatal("no text-input zai model with a declared window — floor cannot be justified")
	}
	if def.DefaultContextWindow > smallest {
		t.Errorf("zai floor %d exceeds the smallest served chat window %d: compression could fire after a silent overflow",
			def.DefaultContextWindow, smallest)
	}
}

// TestResolveContextWindow_NoFloorStaysUnknown keeps "no bound" meaningful for
// providers that declare none: their unknown models must still resolve to 0,
// so the floor is a deliberate per-provider decision, not a global guess.
func TestResolveContextWindow_NoFloorStaysUnknown(t *testing.T) {
	if got := ResolveContextWindow(provider.ProviderOpenAI, "gpt-does-not-exist"); got != 0 {
		t.Errorf("ResolveContextWindow(openai, unknown) = %d, want 0 (no declared floor)", got)
	}
	if got := ResolveContextWindow(provider.ProviderCustom, "whatever"); got != 0 {
		t.Errorf("ResolveContextWindow(custom, unknown) = %d, want 0 (no declared floor)", got)
	}
}

func hasTextInput(m provider.Model) bool {
	if len(m.InputTypes) == 0 {
		return true // text-only when the catalog states nothing
	}
	for _, t := range m.InputTypes {
		if t == "text" {
			return true
		}
	}
	return false
}
