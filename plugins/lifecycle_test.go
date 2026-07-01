// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import "testing"

func TestLifecycleRegistry(t *testing.T) {
	r := NewLifecycleRegistry()
	called := false
	r.Register(HookStart, func(hook HookType, payload map[string]any) {
		called = true
	})
	r.Dispatch(string(HookStart), nil)
	if !called {
		t.Error("expected handler to be called")
	}
	if r.Count(HookStart) != 1 {
		t.Errorf("count = %d", r.Count(HookStart))
	}
}
