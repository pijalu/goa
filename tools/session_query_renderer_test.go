// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/tuirender"
)

func TestSessionSearchRenderer_RenderCall(t *testing.T) {
	r := &SessionSearchRenderer{}

	tests := []struct {
		name     string
		args     map[string]any
		wantPats []string
	}{
		{
			name:     "query only",
			args:     map[string]any{"query": "postgres"},
			wantPats: []string{"session_search", "postgres"},
		},
		{
			name:     "query with session ids and max",
			args:     map[string]any{"query": "wal", "session_ids": []any{"a", "b"}, "max_results": float64(5)},
			wantPats: []string{"session_search", "wal", "2 session(s)", "max:5"},
		},
		{
			name:     "no args",
			args:     map[string]any{},
			wantPats: []string{"session_search"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.RenderCall(tt.args, tuirender.RenderContext{})
			for _, want := range tt.wantPats {
				if !strings.Contains(got, want) {
					t.Errorf("RenderCall(%v) = %q, want contains %q", tt.args, got, want)
				}
			}
		})
	}
}

func TestSessionSearchRenderer_RenderResult(t *testing.T) {
	r := &SessionSearchRenderer{}
	got := r.RenderResult("Session search results (1):\n1. Session x", tuirender.RenderContext{})
	if !strings.Contains(got, "Session search results") {
		t.Errorf("RenderResult = %q, want search output preserved", got)
	}
	if r.PreviewLines() <= 0 {
		t.Errorf("PreviewLines = %d, want > 0", r.PreviewLines())
	}
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed should be false")
	}
}

func TestSessionEventReadRenderer_RenderCall(t *testing.T) {
	r := &SessionEventReadRenderer{}

	tests := []struct {
		name     string
		args     map[string]any
		wantPats []string
	}{
		{
			name:     "seq with current session",
			args:     map[string]any{"seq": float64(3)},
			wantPats: []string{"session_event_read", "seq 3", "(current)"},
		},
		{
			name:     "seq in named session with window",
			args:     map[string]any{"seq": float64(2), "session_id": "1786527447_1bsonzb9", "before": float64(1), "after": float64(2)},
			wantPats: []string{"session_event_read", "seq 2", "1786527447", "-1", "+2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.RenderCall(tt.args, tuirender.RenderContext{})
			for _, want := range tt.wantPats {
				if !strings.Contains(got, want) {
					t.Errorf("RenderCall(%v) = %q, want contains %q", tt.args, got, want)
				}
			}
		})
	}
}

func TestSessionEventReadRenderer_RenderResult(t *testing.T) {
	r := &SessionEventReadRenderer{}
	got := r.RenderResult("Session s1\nTarget event seq 2:", tuirender.RenderContext{})
	if !strings.Contains(got, "Target event seq 2") {
		t.Errorf("RenderResult = %q, want read output preserved", got)
	}
	if r.PreviewLines() <= 0 {
		t.Errorf("PreviewLines = %d, want > 0", r.PreviewLines())
	}
}
