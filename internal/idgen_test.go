// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"strings"
	"testing"
)

func TestFriendlyName_Format(t *testing.T) {
	for i := 0; i < 200; i++ {
		name := FriendlyName()
		if name == "" {
			t.Fatal("FriendlyName returned empty string")
		}
		parts := strings.Split(name, ".")
		if len(parts) != 2 {
			t.Fatalf("FriendlyName %q is not adjective.noun", name)
		}
		if parts[0] == "" || parts[1] == "" {
			t.Fatalf("FriendlyName %q has empty component", name)
		}
		// Both halves must come from the embedded pools.
		if !contains(friendlyAdjectives, parts[0]) {
			t.Fatalf("adjective %q not in pool", parts[0])
		}
		if !contains(friendlyNouns, parts[1]) {
			t.Fatalf("noun %q not in pool", parts[1])
		}
	}
}

func TestFriendlyNameUnique_NoCollision(t *testing.T) {
	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		name := FriendlyNameUnique(seen)
		if seen[name] {
			t.Fatalf("collision on %q after %d draws", name, i)
		}
		seen[name] = true
	}
}

func TestFriendlyNameUnique_FillsEntirePool(t *testing.T) {
	// Drawing more names than the pool size must still succeed via suffixing.
	poolSize := len(friendlyAdjectives) * len(friendlyNouns)
	seen := make(map[string]bool, poolSize+50)
	for i := 0; i < poolSize+50; i++ {
		name := FriendlyNameUnique(seen)
		if seen[name] {
			t.Fatalf("collision on %q after exhausting pool", name)
		}
		seen[name] = true
	}
}

func TestSplitFriendlyName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"happy.fox", true},
		{"a.b", true},
		{"", false},
		{"nopart", false},
		{".", false},
		{"happy.", false},
		{".fox", false},
		{"a.b.c", true}, // first segment is adjective.noun-shaped
	}
	for _, tc := range cases {
		if got := SplitFriendlyName(tc.in); got != tc.want {
			t.Errorf("SplitFriendlyName(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestEmbeddedWordLists_NonEmpty(t *testing.T) {
	if len(friendlyAdjectives) < 20 {
		t.Errorf("adjective pool too small: %d", len(friendlyAdjectives))
	}
	if len(friendlyNouns) < 20 {
		t.Errorf("noun pool too small: %d", len(friendlyNouns))
	}
	for _, w := range friendlyAdjectives {
		if w == "" || strings.TrimSpace(w) != w {
			t.Errorf("bad adjective entry %q", w)
		}
	}
	for _, w := range friendlyNouns {
		if w == "" || strings.TrimSpace(w) != w {
			t.Errorf("bad noun entry %q", w)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
