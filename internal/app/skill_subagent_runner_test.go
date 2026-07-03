// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
)

func TestFilterToolNames(t *testing.T) {
	cases := []struct {
		name     string
		names    []string
		excluded []string
		want     []string
	}{
		{"no exclusion", []string{"bash", "read", "edit"}, nil, []string{"bash", "read", "edit"}},
		{"exclude one", []string{"bash", "terminal", "read"}, []string{"terminal"}, []string{"bash", "read"}},
		{"exclude multiple", []string{"bash", "terminal", "run_skill", "read"}, []string{"terminal", "run_skill"}, []string{"bash", "read"}},
		{"exclude all", []string{"terminal"}, []string{"terminal"}, nil},
		{"exclude missing", []string{"bash", "read"}, []string{"terminal"}, []string{"bash", "read"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterToolNames(tc.names, tc.excluded...)
			if len(got) != len(tc.want) {
				t.Fatalf("filterToolNames(%v, %v) = %v, want %v", tc.names, tc.excluded, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("filterToolNames(%v, %v) = %v, want %v", tc.names, tc.excluded, got, tc.want)
					break
				}
			}
		})
	}
}

func TestDefaultSubAgentTools(t *testing.T) {
	all := []string{"bash", "terminal", "run_skill", "read", "edit", "write", "webfetch", "custom"}
	got := defaultSubAgentTools(all)
	want := []string{"bash", "read", "edit", "write", "webfetch"}
	if len(got) != len(want) {
		t.Fatalf("defaultSubAgentTools(%v) = %v, want %v", all, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("defaultSubAgentTools(%v) = %v, want %v", all, got, want)
			break
		}
	}
}

func TestSkillSubAgentRunner_ResolveToolNames(t *testing.T) {
	pool := multiagent.NewAgentPool(provider.Model{}, provider.StreamOptions{}, []agentic.Tool{
		&mockTool{name: "bash"},
		&mockTool{name: "terminal"},
		&mockTool{name: "run_skill"},
		&mockTool{name: "read"},
	})
	runner := &skillSubAgentRunner{pool: pool}

	cases := []struct {
		name    string
		allowed []string
		want    []string
	}{
		{"default excludes restricted", nil, []string{"bash", "read"}},
		{"explicit keeps order", []string{"read", "bash"}, []string{"read", "bash"}},
		{"explicit run_skill excluded", []string{"run_skill", "bash"}, []string{"bash"}},
		{"explicit terminal excluded", []string{"terminal", "bash"}, []string{"bash"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runner.resolveToolNames(tc.allowed)
			if len(got) != len(tc.want) {
				t.Fatalf("resolveToolNames(%v) = %v, want %v", tc.allowed, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("resolveToolNames(%v) = %v, want %v", tc.allowed, got, tc.want)
					break
				}
			}
		})
	}
}

type mockTool struct{ name string }

func (m *mockTool) Schema() agentic.ToolSchema { return agentic.ToolSchema{Name: m.name} }
func (m *mockTool) Execute(input string) (string, error) {
	return "", nil
}
func (m *mockTool) IsRetryable(err error) bool { return false }

// Ensure context is imported and used for the interface compile check.
var _ context.Context = context.Background()
