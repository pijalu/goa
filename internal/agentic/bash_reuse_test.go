// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import "testing"

func TestBashUpstreamKey_StripsPipeTail(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{`go test -count=1 -v . 2>&1 | grep -c "result mismatch"`, "go test -count=1 -v . 2>&1"},
		{`go test -count=1 -v . 2>&1 | grep -c "table not found"`, "go test -count=1 -v . 2>&1"},
		{"go build ./...", "go build ./..."},
		{"ls -la | wc -l", "ls -la"},
		{"  go   test   ./...  |  grep foo  ", "go test ./..."},
	}
	for _, tc := range cases {
		if got := bashUpstreamKey(tc.cmd); got != tc.want {
			t.Errorf("bashUpstreamKey(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

func TestBashUpstreamKey_SameUpstreamSameKey(t *testing.T) {
	a := bashUpstreamKey(`go test -count=1 -v . 2>&1 | grep -c "result mismatch"`)
	b := bashUpstreamKey(`go test -count=1 -v . 2>&1 | grep -c "parse error"`)
	if a != b {
		t.Errorf("same upstream with different filters should share a key: %q vs %q", a, b)
	}
}

func TestBashCommandFromArgs(t *testing.T) {
	if got := bashCommandFromArgs(`{"command":"go test ./...","timeout":120}`); got != "go test ./..." {
		t.Errorf("got %q", got)
	}
	if got := bashCommandFromArgs(`not json`); got != "" {
		t.Errorf("invalid json should give empty, got %q", got)
	}
	if got := bashCommandFromArgs(`{"timeout":120}`); got != "" {
		t.Errorf("missing command should give empty, got %q", got)
	}
}

func TestBashReuseTracker_NearDuplicateSameEpoch(t *testing.T) {
	tr := newBashReuseTracker()
	// First run of the upstream at epoch 0 → not a duplicate.
	if tr.recordUpstream("go test ./...", 0) {
		t.Error("first run should not be flagged")
	}
	// Re-run same upstream, same epoch (only filter changed) → duplicate.
	if !tr.recordUpstream("go test ./...", 0) {
		t.Error("re-run of same upstream in same epoch should be flagged")
	}
}

func TestBashReuseTracker_MutationResets(t *testing.T) {
	tr := newBashReuseTracker()
	tr.recordUpstream("go test ./...", 0)
	// A state-mutating tool succeeded → epoch advances to 1. Re-running the
	// same test command is now legitimate (code changed), so NOT a duplicate.
	if tr.recordUpstream("go test ./...", 1) {
		t.Error("re-run after state mutation (epoch advance) must not be flagged")
	}
}

func TestBashReuseTracker_DifferentCommandsNotFlagged(t *testing.T) {
	tr := newBashReuseTracker()
	tr.recordUpstream("go test ./...", 0)
	if tr.recordUpstream("go build ./...", 0) {
		t.Error("different command should not be flagged")
	}
}

func TestBashReuseTracker_EmptyUpstreamNotFlagged(t *testing.T) {
	tr := newBashReuseTracker()
	if tr.recordUpstream("", 0) {
		t.Error("empty upstream should never be flagged")
	}
}
