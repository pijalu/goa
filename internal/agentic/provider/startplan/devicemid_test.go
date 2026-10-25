// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package startplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateDeviceMid points the device-mid state at a fresh temp dir for the
// test and restores the process-global state afterwards. DeviceMid caches
// process-wide, so every test that touches it must go through this.
func isolateDeviceMid(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	SetDeviceMidDir(dir)
	t.Cleanup(func() {
		SetDeviceMidDir("")
		ResetDeviceMid()
	})
	return dir
}

// TestDeviceMid_PersistsAndStable verifies the identity is minted once,
// persisted to GoaHomeDir()/device-mid, and reused on subsequent calls —
// the backend correlates plan usage per device, so a rotating id would
// fragment (or trip) that accounting.
func TestDeviceMid_PersistsAndStable(t *testing.T) {
	dir := isolateDeviceMid(t)

	first := DeviceMid()
	if first == "" {
		t.Fatal("DeviceMid() returned empty in a writable home")
	}
	if len(first) != 64 {
		t.Errorf("DeviceMid length = %d, want 64 hex chars", len(first))
	}
	for i := 0; i < len(first); i++ {
		c := first[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			t.Fatalf("DeviceMid %q is not lowercase hex", first)
		}
	}
	if got := DeviceMid(); got != first {
		t.Errorf("DeviceMid not stable: %q then %q", first, got)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "device-mid"))
	if err != nil {
		t.Fatalf("device-mid file not persisted: %v", err)
	}
	if stored := strings.TrimSpace(string(raw)); stored != first {
		t.Errorf("stored device-mid = %q, want %q", stored, first)
	}

	// A different state dir must mint a DIFFERENT identity (two installs
	// must never share a device id).
	other := t.TempDir()
	SetDeviceMidDir(other)
	defer func() { SetDeviceMidDir(dir) }()
	if second := DeviceMid(); second == "" || second == first {
		t.Errorf("fresh dir produced %q, want a new distinct identity", second)
	}
}

// TestDeviceMid_InMemoryFallbackOnUnwritableState verifies the request path
// survives an unwritable state location: the identity stays consistent within
// the process and nothing is written.
func TestDeviceMid_InMemoryFallbackOnUnwritableState(t *testing.T) {
	base := t.TempDir()
	blocked := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// deviceMidPath joins <dir>/device-mid; pointing the dir at a REGULAR
	// file makes MkdirAll fail.
	SetDeviceMidDir(filepath.Join(blocked, "nested"))
	t.Cleanup(func() {
		SetDeviceMidDir("")
		ResetDeviceMid()
	})

	first := DeviceMid()
	if first == "" {
		t.Fatal("unwritable state must not break the request path")
	}
	if got := DeviceMid(); got != first {
		t.Errorf("in-memory identity not stable: %q then %q", first, got)
	}
	if _, err := os.Stat(filepath.Join(blocked, "nested", "device-mid")); err == nil {
		t.Error("device-mid written into unwritable location")
	}
}

// TestNormalizeDeviceMid verifies the load path only accepts printable ASCII
// values that survive wire encoding, and regenerates on corruption.
func TestNormalizeDeviceMid(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"valid hex", "a1b2c3\n", "a1b2c3"},
		{"trimmed whitespace", "  deadbeef  ", "deadbeef"},
		{"empty", "   \n", ""},
		{"non-printable", "ab\x01cd", ""},
		{"newline inside", "ab\ncd", ""},
		{"too long", strings.Repeat("a", 129), ""},
		{"max length ok", strings.Repeat("a", 128), strings.Repeat("a", 128)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDeviceMid(tt.in); got != tt.want {
				t.Errorf("normalizeDeviceMid(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
