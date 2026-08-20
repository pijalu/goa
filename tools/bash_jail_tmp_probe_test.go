// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import "testing"

// Probes for item 3: does the SOLO jail catch every way a command can
// reference /tmp (outside the project)? The concern was that a bash command
// writing to /tmp "passes" under SOLO.
func TestBashJail_TmpWriteVariants(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		cmd  string
		want bool // true = should be flagged as outside-project (jail violation)
	}{
		{"redirect with space", "go test ./... > " + dir + "/../outside.txt 2>&1", true},
		{"redirect absolute /tmp", "echo hi > /tmp/out.txt", true},
		{"redirect attached no-space", "echo hi >/tmp/out.txt", true},
		{"tee to /tmp", "go test ./... | tee /tmp/out.txt", true},
		{"ls /tmp", "ls /tmp", true},
		{"inside project ok", "echo hi > out.txt", false},
		{"inside project abs ok", "echo hi > " + dir + "/out.txt", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bashReferencesOutsidePath(tc.cmd, dir)
			if got != tc.want {
				t.Errorf("bashReferencesOutsidePath(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}
