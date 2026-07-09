// SPDX-License-Identifier: GPL-3.0-or-later

package app

import "testing"

// TestAgentStreamRegistry_LabelReuseReusesBaseRoleWhenIdle verifies that the
// base role label is reused when the previous agent of that role has finished,
// and disambiguating suffixes are only used for concurrent agents.
func TestAgentStreamRegistry_LabelReuseReusesBaseRoleWhenIdle(t *testing.T) {
	r := newAgentStreamRegistry()

	s1 := r.begin("coder", "a-1")
	if s1.label != "coder" {
		t.Errorf("first agent label = %q, want coder", s1.label)
	}

	// A concurrent agent gets a disambiguated label.
	s2 := r.begin("coder", "a-2")
	if s2.label != "coder·2" {
		t.Errorf("concurrent agent label = %q, want coder·2", s2.label)
	}

	// Finish the first agent; its label becomes available.
	r.end("a-1")
	s3 := r.begin("coder", "a-3")
	if s3.label != "coder" {
		t.Errorf("after a-1 finished, new agent label = %q, want coder", s3.label)
	}

	// Finish the remaining two agents.
	r.end("a-2")
	r.end("a-3")

	// With no active streams, the next agent reuses the base label.
	s4 := r.begin("coder", "a-4")
	if s4.label != "coder" {
		t.Errorf("all idle, new agent label = %q, want coder", s4.label)
	}
}

// TestAgentStreamRegistry_DifferentRolesDoNotCollide verifies that labels for
// different roles are independent.
func TestAgentStreamRegistry_DifferentRolesDoNotCollide(t *testing.T) {
	r := newAgentStreamRegistry()

	s1 := r.begin("coder", "a-1")
	s2 := r.begin("reviewer", "a-2")
	if s1.label != "coder" {
		t.Errorf("coder label = %q, want coder", s1.label)
	}
	if s2.label != "reviewer" {
		t.Errorf("reviewer label = %q, want reviewer", s2.label)
	}
}

// TestAgentStreamRegistry_SameAgentIDReturnsExistingState verifies that
// calling begin again for the same agentID returns the existing state.
func TestAgentStreamRegistry_SameAgentIDReturnsExistingState(t *testing.T) {
	r := newAgentStreamRegistry()

	s1 := r.begin("coder", "a-1")
	s1.thinking.WriteString("thought")
	s2 := r.begin("coder", "a-1")
	if s1 != s2 {
		t.Error("begin with same agentID should return the same state")
	}
	if s2.thinking.String() != "thought" {
		t.Errorf("returned state should preserve thinking buffer, got %q", s2.thinking.String())
	}
}
