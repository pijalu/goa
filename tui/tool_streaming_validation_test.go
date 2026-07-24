// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// toolStreamingTestCase describes the partial arguments and output used to
// exercise a renderer in streaming mode.
type toolStreamingTestCase struct {
	name       string
	args       map[string]any
	partial    map[string]any
	output     string
	outPartial map[string]any
}

// TestToolStreaming_AllRenderersSupportPartialArgs validates that every registered tool
// renderer can render a tool call and result while arguments are still
// streaming (partial/missing). It catches renderers that would panic or produce
// an empty header when the args map is incomplete.
func TestToolStreaming_AllRenderersSupportPartialArgs(t *testing.T) {
	cases := []toolStreamingTestCase{
		{"read", map[string]any{"path": "README.md"}, map[string]any{"path": "README.md"}, "read file README.md:1:10", nil},
		{"write", map[string]any{"path": "main.go", "content": "package main\n"}, map[string]any{"path": "main.go", "content": "package main\n"}, "[write: main.go]", map[string]any{"content": "package main\n"}},
		{"edit", map[string]any{"path": "main.go", "operation": "replace"}, map[string]any{"path": "main.go"}, "- old\n+ new\n", nil},
		{"bash", map[string]any{"command": "echo hi"}, map[string]any{"command": "echo"}, "hi\nDuration: 0.01s\n", nil},
		{"python", map[string]any{"code": "print(1)\n"}, map[string]any{"code": "print(1)\n"}, "1\n", map[string]any{"code": "print(1)\n"}},
		{"verify", map[string]any{"command": "go test ./...", "args": []string{"-run", "TestFoo"}}, map[string]any{"command": "go test"}, "PASS\n", nil},
		{"terminal", map[string]any{"command": "ls"}, map[string]any{"command": "ls"}, "file.txt\n", nil},
		{"webfetch", map[string]any{"url": "https://example.com"}, map[string]any{"url": "https://example.com"}, "<html></html>", nil},
		{"search", map[string]any{"pattern": "foo", "path": "."}, map[string]any{"pattern": "foo"}, "a.go:1:foo", nil},
		{"smartsearch", map[string]any{"query": "foo", "path": "."}, map[string]any{"query": "foo"}, "a.go:1:foo", nil},
		{"goal", map[string]any{"action": "update", "status": "complete"}, map[string]any{"action": "update"}, `{"goal":{"objective":"Refactor parser","status":"complete","turnsUsed":3,"tokensUsed":1200,"wallClockMs":4000}}`, nil},
		{"agent", map[string]any{"description": "Review code", "subagent_type": "reviewer"}, map[string]any{"description": "Review code"}, "Done\n", nil},
		{"agent_swarm", map[string]any{"task": "Fix bugs", "items": []string{"a", "b"}}, map[string]any{"task": "Fix bugs"}, "Done\n", nil},
		{"plan", map[string]any{"action": "get"}, map[string]any{"action": "get"}, "# Plan: test\n\n**Objective:** test\n", nil},
		{"task_outcome", map[string]any{"status": "done", "summary": "completed"}, map[string]any{"status": "done"}, `{"status":"done","summary":"completed"}`, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := GetToolRenderer(tc.name)
			if r == nil {
				t.Fatalf("no renderer registered for %q", tc.name)
			}

			// 1. Complete args: must produce a non-empty header.
			call := r.RenderCall(tc.args, tuirender.RenderContext{})
			if stripped := ansi.Strip(call); stripped == "" {
				t.Errorf("RenderCall with complete args produced empty header")
			}

			// 2. Partial args while streaming: must produce a non-empty header
			// and must not panic.
			callPartial := r.RenderCall(tc.partial, tuirender.RenderContext{IsPartial: true, ArgsComplete: false, Args: tc.partial})
			if stripped := ansi.Strip(callPartial); stripped == "" {
				t.Errorf("RenderCall with partial args produced empty header")
			}

			// 3. Result while streaming (no output yet): must not panic.
			var resultPartial string
			if tc.outPartial != nil {
				resultPartial = r.RenderResult("", tuirender.RenderContext{IsPartial: true, ArgsComplete: false, Args: tc.outPartial})
			} else {
				resultPartial = r.RenderResult("", tuirender.RenderContext{IsPartial: true, ArgsComplete: false, Args: tc.partial})
			}
			_ = resultPartial

			// 4. Result with output: must not panic.
			result := r.RenderResult(tc.output, tuirender.RenderContext{})
			_ = result
		})
	}
}

// TestToolStreaming_RegistryIsComplete ensures every renderer registered in
// the production registry is covered by the streaming validation above. If a
// new tool is added without updating the test, this fails.
func TestToolStreaming_RegistryIsComplete(t *testing.T) {
	required := map[string]bool{}
	for name := range ToolRendererRegistry {
		required[name] = false
	}
	cases := []string{
		"read", "write", "edit", "bash", "python", "verify", "terminal",
		"webfetch", "search", "smartsearch", "goal", "agent", "agent_swarm",
		"plan", "task_outcome",
	}
	for _, name := range cases {
		if _, ok := required[name]; !ok {
			t.Errorf("test case %q is not in the registry", name)
			continue
		}
		required[name] = true
	}
	for name, seen := range required {
		if !seen {
			t.Errorf("registry tool %q is not covered by streaming validation", name)
		}
	}
}

// TestToolStreaming_ContentToolsShowPartialBody validates that tools whose
// body content streams (write, python) render the partial content in the body
// before the tool result arrives.
func TestToolStreaming_ContentToolsShowPartialBody(t *testing.T) {
	contentCases := []struct {
		name     string
		args     map[string]any
		partial  map[string]any
		expected string
	}{
		{
			name:     "write",
			args:     map[string]any{"path": "main.go", "content": "package main\n\nfunc main() {}"},
			partial:  map[string]any{"content": "package main\n"},
			expected: "package main",
		},
		{
			name:     "python",
			args:     map[string]any{"code": "x = 1\nprint(x)\n"},
			partial:  map[string]any{"code": "x = 1\nprint(x)\n"},
			expected: "x = 1",
		},
	}
	for _, tc := range contentCases {
		t.Run(tc.name, func(t *testing.T) {
			r := GetToolRenderer(tc.name)
			if r == nil {
				t.Fatalf("no renderer for %q", tc.name)
			}
			body := r.RenderResult("", tuirender.RenderContext{IsPartial: true, ArgsComplete: false, Args: tc.partial})
			stripped := ansi.Strip(body)
			if !strings.Contains(stripped, tc.expected) {
				t.Errorf("partial body = %q, want it to contain %q", stripped, tc.expected)
			}
		})
	}
}
