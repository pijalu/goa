// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerWorkdirCreatesDirectory(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	dir, err := m.Workdir("session-1")
	if err != nil {
		t.Fatalf("Workdir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat workdir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("workdir is not a directory")
	}
	if info.Mode().Perm()&0o777 != 0o700 {
		t.Fatalf("workdir permissions = %o, want 0o700", info.Mode().Perm())
	}
}

func TestManagerInvalidSessionID(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	dir, err := m.Workdir("../../etc")
	if err != nil {
		t.Fatalf("Workdir: %v", err)
	}
	if filepath.Base(dir) != invalidSessionID {
		t.Fatalf("invalid session should map to %q, got %q", invalidSessionID, dir)
	}
}

func TestValidateSessionID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"session-1", true},
		{"a_b-c", true},
		{"", false},
		{"../../etc", false},
		{"a:b", false},
	}
	for _, tc := range cases {
		if got := ValidateSessionID(tc.id); got != tc.want {
			t.Errorf("ValidateSessionID(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}
