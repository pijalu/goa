// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"os"
	"path/filepath"
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

func TestIsValidRunName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"happy.hare", true},
		{"custom-id", true},
		{"run_42", true},
		{"", false},
		{"Custom.ID", false},
		{"custom id", false},
		{"custom/id", false},
		{"UPPER", false},
		{"a", true},
	}
	for _, tc := range cases {
		if got := IsValidRunName(tc.in); got != tc.want {
			t.Errorf("IsValidRunName(%q) = %v, want %v", tc.in, got, tc.want)
		}
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

// resetAnonymousUserIDMemo clears the process-lifetime memo so tests using
// fresh temp homes start from an unpolluted cache.
func resetAnonymousUserIDMemo() {
	anonymousUserIDMu.Lock()
	defer anonymousUserIDMu.Unlock()
	anonymousUserIDMemo = map[string]string{}
}

func TestAnonymousUserID_PersistsAndIsStable(t *testing.T) {
	resetAnonymousUserIDMemo()
	home := t.TempDir()
	SetGoaHome(home)
	defer SetGoaHome("")

	id1 := AnonymousUserID()
	id2 := AnonymousUserID()

	if id1 == "" {
		t.Fatal("AnonymousUserID returned empty string")
	}
	if id1 != id2 {
		t.Fatalf("AnonymousUserID not memoized: %q != %q", id1, id2)
	}
	if !isUUIDv4(id1) {
		t.Fatalf("AnonymousUserID %q is not a UUID v4", id1)
	}

	// Persisted as a bare line under the goa home.
	b, err := os.ReadFile(filepath.Join(home, ".goa", anonymousUserIDFile))
	if err != nil {
		t.Fatalf("identity file not persisted: %v", err)
	}
	got := strings.TrimSpace(string(b))
	if got != id1 {
		t.Fatalf("persisted id %q != returned id %q", got, id1)
	}
}

func TestAnonymousUserID_AdoptsExistingFile(t *testing.T) {
	resetAnonymousUserIDMemo()
	home := t.TempDir()
	SetGoaHome(home)
	defer SetGoaHome("")

	existing := uuidV4()
	dir := filepath.Join(home, ".goa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, anonymousUserIDFile), []byte(existing+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := AnonymousUserID(); got != existing {
		t.Fatalf("AnonymousUserID = %q, want persisted %q", got, existing)
	}
}

func TestAnonymousUserID_ReplacesCorruptFile(t *testing.T) {
	resetAnonymousUserIDMemo()
	home := t.TempDir()
	SetGoaHome(home)
	defer SetGoaHome("")

	dir := filepath.Join(home, ".goa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	corrupt := "not-a-uuid\n"
	if err := os.WriteFile(filepath.Join(dir, anonymousUserIDFile), []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}

	id := AnonymousUserID()
	if !isUUIDv4(id) {
		t.Fatalf("AnonymousUserID returned %q after corrupt file, want fresh UUID v4", id)
	}
	b, err := os.ReadFile(filepath.Join(dir, anonymousUserIDFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != id {
		t.Fatalf("corrupt file not replaced: persisted %q != %q", got, id)
	}
}

func TestAnonymousUserID_DifferentHomesDifferentIdentities(t *testing.T) {
	resetAnonymousUserIDMemo()
	homeA, homeB := t.TempDir(), t.TempDir()

	SetGoaHome(homeA)
	idA := AnonymousUserID()

	SetGoaHome(homeB)
	idB := AnonymousUserID()

	SetGoaHome("")
	resetAnonymousUserIDMemo()

	if idA == idB {
		t.Fatalf("distinct homes must not share an identity: %q", idA)
	}
}

func TestAnonymousUserID_NoHomeIsProcessLocal(t *testing.T) {
	resetAnonymousUserIDMemo()

	// A home that cannot be created: the parent path is a regular file, so
	// MkdirAll fails and the function must fall back to a process-local UUID
	// without panicking.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	unwritable := filepath.Join(blocker, "home")

	id := loadOrCreateAnonymousUserID(unwritable)
	if !isUUIDv4(id) {
		t.Fatalf("process-local fallback %q is not a UUID v4", id)
	}
	// The unwritable home must not gain an identity file: stat fails either
	// with ENOENT (never created) or ENOTDIR (a parent component is a file).
	if _, err := os.Stat(filepath.Join(unwritable, anonymousUserIDFile)); err == nil {
		t.Fatalf("unwritable home must not create the identity file")
	}
}

func TestIsUUIDv4(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"123e4567-e89b-42d3-a456-426614174000", true},
		{"", false},
		{"123e4567-e89b-42d3-a456-42661417400", false},   // too short
		{"123e4567e89b42d3a456426614174000", false},      // no dashes
		{"123e4567-e89b-52d3-a456-426614174000", false},  // version 5
		{"123e4567-e89b-42d3-g456-426614174000", false},  // non-hex
		{"123e4567-e89b-42d3-a456-4266141740000", false}, // too long
	}
	for _, tc := range cases {
		if got := isUUIDv4(tc.in); got != tc.want {
			t.Errorf("isUUIDv4(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
