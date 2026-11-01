// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

func TestAgentMCPRenderer_RenderCall(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want []string // substrings the stripped header must contain
	}{
		{
			name: "call_tool with server and tool",
			args: map[string]any{"action": "call_tool", "server": "alpha", "tool": "echo"},
			want: []string{"agent_mcp", "call_tool", "alpha", "echo"},
		},
		{
			name: "read_resource with uri",
			args: map[string]any{"action": "read_resource", "server": "alpha", "uri": "mem://hello"},
			want: []string{"read_resource", "alpha", "mem://hello"},
		},
		{
			name: "get_prompt with prompt",
			args: map[string]any{"action": "get_prompt", "server": "alpha", "prompt": "greet"},
			want: []string{"get_prompt", "greet"},
		},
		{
			name: "config_add with name",
			args: map[string]any{"action": "config_add", "name": "alpha"},
			want: []string{"config_add", "alpha"},
		},
		{
			name: "list_servers with discovery filters",
			args: map[string]any{"action": "list_servers", "search": "fs", "limit": float64(5), "raw": true},
			want: []string{"list_servers", "search:fs", "limit:5", "raw"},
		},
		{
			name: "minimal action only",
			args: map[string]any{"action": "shutdown"},
			want: []string{"shutdown"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewAgentMCPRenderer()
			call := r.RenderCall(tc.args, tuirender.RenderContext{})
			stripped := ansi.Strip(call)
			for _, w := range tc.want {
				if !strings.Contains(stripped, w) {
					t.Errorf("header %q missing %q", stripped, w)
				}
			}
			if strings.Contains(call, "\n") {
				t.Errorf("header must be single-line, got %q", call)
			}
		})
	}
}

func TestAgentMCPRenderer_RenderResult_SummaryShapes(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   []string
	}{
		{
			name:   "servers listed",
			output: `{"servers":[{"name":"alpha","state":"connected"},{"name":"beta","state":"stopped"}]}`,
			want:   []string{"alpha (connected)", "beta (stopped)"},
		},
		{
			name:   "no servers configured",
			output: `{"servers":[]}`,
			want:   []string{"no servers configured"},
		},
		{
			name:   "tools with total",
			output: `{"tools":[{},{},{}],"total":3}`,
			want:   []string{"3 tools"},
		},
		{
			name:   "tools truncated",
			output: `{"tools":[{}],"total":9,"truncated":true}`,
			want:   []string{"1 tools (of 9 matched"},
		},
		{
			name:   "resources count",
			output: `{"resources":[],"total":0}`,
			want:   []string{"0 resources"},
		},
		{
			name:   "prompts count",
			output: `{"prompts":[{}],"total":1}`,
			want:   []string{"1 prompts"},
		},
		{
			name:   "config_add result",
			output: `{"name":"alpha","action":"added","path":"/tmp/cfg.toml"}`,
			want:   []string{"alpha: added (/tmp/cfg.toml)"},
		},
		{
			name:   "shutdown when absent",
			output: `{"shutdown":false}`,
			want:   []string{"daemon not running"},
		},
		{
			name:   "shutdown with sessions",
			output: `{"shutdown":true,"sessions_closed":2}`,
			want:   []string{"daemon stopped (2 session(s) closed)"},
		},
		{
			name:   "tool result content text",
			output: `{"content":[{"type":"text","text":"hello goa"}]}`,
			want:   []string{"hello goa"},
		},
		{
			name:   "daemon notice appended after blank line",
			output: "{\"servers\":[]}\n\nnotice: auto-starting daemon",
			want:   []string{"no servers configured", "auto-starting daemon"},
		},
		{
			name:   "non-JSON passes through",
			output: "plain text output",
			want:   []string{"plain text output"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewAgentMCPRenderer()
			got := r.RenderResult(tc.output, tuirender.RenderContext{})
			stripped := ansi.Strip(got)
			for _, w := range tc.want {
				if !strings.Contains(stripped, w) {
					t.Errorf("summary %q missing %q", stripped, w)
				}
			}
		})
	}
}

func TestAgentMCPRenderer_RenderResult_LongNonJSONTruncated(t *testing.T) {
	r := NewAgentMCPRenderer()
	got := ansi.Strip(r.RenderResult(strings.Repeat("x", 300), tuirender.RenderContext{}))
	if !strings.HasSuffix(got, "...") {
		t.Errorf("long passthrough should be truncated with ellipsis, got %q", got)
	}
	if len(got) > 210 {
		t.Errorf("long passthrough too long: %d chars", len(got))
	}
}

func TestAgentMCPRenderer_EmptyAndCollapseBehavior(t *testing.T) {
	r := NewAgentMCPRenderer()
	if got := r.RenderResult("", tuirender.RenderContext{}); got != "" {
		t.Errorf("empty output should render empty, got %q", got)
	}
	if r.PreviewLines() != 6 {
		t.Errorf("PreviewLines = %d, want 6", r.PreviewLines())
	}
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed = true, want false (summaries stay visible)")
	}
}
