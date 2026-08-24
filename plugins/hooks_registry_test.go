// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"sync"
	"testing"
)

// noopHandler is a HookHandler that does nothing (pass-through).
func noopHandler(map[string]any) map[string]any { return nil }

func TestHookRegistryPriorityOrdering(t *testing.T) {
	reg := NewHookRegistry(nil)
	// Registered out of priority order; equal priorities must keep
	// registration order (the tiebreak).
	specs := []HookSpec{
		{PluginID: "p1", Name: "late", Point: "tool-call:pre", Mode: HookIntercept, Priority: 100},
		{PluginID: "p1", Name: "tie-a", Point: "tool-call:pre", Mode: HookIntercept, Priority: 10},
		{PluginID: "p2", Name: "early", Point: "tool-call:pre", Mode: HookIntercept, Priority: 0},
		{PluginID: "p2", Name: "tie-b", Point: "tool-call:pre", Mode: HookIntercept, Priority: 10},
	}
	for _, s := range specs {
		if err := reg.Register(s, noopHandler); err != nil {
			t.Fatalf("register %+v: %v", s, err)
		}
	}
	got := reg.Snapshot("tool-call:pre")
	want := []string{"early", "tie-a", "tie-b", "late"}
	if len(got) != len(want) {
		t.Fatalf("snapshot length = %d, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Spec.Name != name {
			t.Errorf("chain[%d] = %q, want %q (priority asc, registration-order ties)", i, got[i].Spec.Name, name)
		}
	}
}

func TestHookRegistryDuplicateNamePerPlugin(t *testing.T) {
	reg := NewHookRegistry(nil)
	first := HookSpec{PluginID: "p1", Name: "redact", Point: "message:pre-send", Mode: HookIntercept}
	if err := reg.Register(first, noopHandler); err != nil {
		t.Fatal(err)
	}
	// Same plugin + same name ⇒ error even at a different point.
	dupPoint := first
	dupPoint.Point = "tool-call:pre"
	err := reg.Register(dupPoint, noopHandler)
	if err == nil {
		t.Fatal("duplicate name per plugin must error across points")
	}
	// Same name from ANOTHER plugin is fine.
	other := first
	other.PluginID = "p2"
	if err := reg.Register(other, noopHandler); err != nil {
		t.Fatalf("same name from other plugin must be allowed: %v", err)
	}
	// Same plugin, different name at same point: allowed.
	variant := first
	variant.Name = "redact-v2"
	if err := reg.Register(variant, noopHandler); err != nil {
		t.Fatalf("different name same point must be allowed: %v", err)
	}
}

func TestHookRegistryUnregisterPlugin(t *testing.T) {
	reg := NewHookRegistry(nil)
	mk := func(id, name, point string) HookSpec {
		return HookSpec{PluginID: id, Name: name, Point: point, Mode: HookNotify}
	}
	for _, s := range []HookSpec{
		mk("a", "h1", "reply:delta"),
		mk("a", "h2", "tool-call:post"),
		mk("b", "h1", "reply:delta"),
	} {
		if err := reg.Register(s, noopHandler); err != nil {
			t.Fatal(err)
		}
	}
	if got := reg.Count(); got != 3 {
		t.Fatalf("count before unregister = %d, want 3", got)
	}
	reg.UnregisterPlugin("a")
	if got := reg.Count(); got != 1 {
		t.Fatalf("count after unregister = %d, want 1", got)
	}
	left := reg.Snapshot("reply:delta")
	if len(left) != 1 || left[0].Spec.PluginID != "b" {
		t.Fatalf("plugin b's hook must survive: %+v", left)
	}
	if got := reg.Snapshot("tool-call:post"); got != nil {
		t.Fatalf("emptied chain must return nil, got %+v", got)
	}
	reg.UnregisterPlugin("missing") // no-op
	if got := reg.Count(); got != 1 {
		t.Fatalf("unknown unregister changed count to %d", got)
	}
}

func TestHookRegistryAllowValidator(t *testing.T) {
	// M6 will inject the real grant validator; exercise the seam now.
	reg := NewHookRegistry(func(spec HookSpec) error {
		if spec.Mode == HookIntercept {
			return fmt.Errorf("intercept not granted")
		}
		return nil
	})
	err := reg.Register(HookSpec{PluginID: "p", Name: "h", Point: "reply:pre", Mode: HookIntercept}, noopHandler)
	if err == nil {
		t.Fatal("validator rejection must surface as Register error")
	}
	if err := reg.Register(HookSpec{PluginID: "p", Name: "n", Point: "reply:pre", Mode: HookNotify}, noopHandler); err != nil {
		t.Fatalf("allowed spec rejected: %v", err)
	}
	if !reg.HasInterceptors("reply:pre") && reg.Count() != 1 {
		t.Fatal("unexpected registry state")
	}
	// HasInterceptors must be false after the intercept was rejected.
	if reg.HasInterceptors("reply:pre") {
		t.Fatal("rejected intercept must not appear in HasInterceptors")
	}
}

func TestHookRegistryValidation(t *testing.T) {
	cases := []struct {
		name   string
		spec   HookSpec
		h      HookHandler
		errSub string // "" means registration must succeed
	}{
		{"ok-notify", HookSpec{PluginID: "p", Name: "h", Point: "reply:delta", Mode: HookNotify}, noopHandler, ""},
		{"ok-intercept", HookSpec{PluginID: "p", Name: "i", Point: "llm:error", Mode: HookIntercept}, noopHandler, ""},
		{"no-plugin-id", HookSpec{Name: "h", Point: "reply:delta", Mode: HookNotify}, noopHandler, "PluginID"},
		{"no-name", HookSpec{PluginID: "p", Point: "reply:delta", Mode: HookNotify}, noopHandler, "Name"},
		{"no-point", HookSpec{PluginID: "p", Name: "h", Mode: HookNotify}, noopHandler, "Point"},
		{"bad-mode", HookSpec{PluginID: "p", Name: "h", Point: "reply:delta", Mode: "both"}, noopHandler, "Mode must be"},
		{"nil-handler", HookSpec{PluginID: "p", Name: "h", Point: "reply:delta", Mode: HookNotify}, nil, "Handler"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewHookRegistry(nil)
			err := reg.Register(tc.spec, tc.h)
			if tc.errSub == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.errSub)
			}
		})
	}
}

func TestHookRegistryConcurrentSnapshotRegister(t *testing.T) {
	// Run with -race: parallel registrations, snapshots, and unregisters must
	// never race on the chains map or returned slices.
	reg := NewHookRegistry(nil)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("p%d", w)
			for i := 0; i < 50; i++ {
				spec := HookSpec{
					PluginID: id, Name: fmt.Sprintf("h%d", i),
					Point: "reply:delta", Mode: HookNotify, Priority: i,
				}
				_ = reg.Register(spec, noopHandler)
				_ = reg.Snapshot("reply:delta")
				_ = reg.HasInterceptors("reply:delta")
				_ = reg.Count()
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			reg.UnregisterPlugin(fmt.Sprintf("p%d", i%8))
		}
	}()
	wg.Wait()
}
