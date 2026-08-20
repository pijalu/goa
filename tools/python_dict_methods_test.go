// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"
	"testing"
)

// TestPythonTool_Execute_DictGet replays the reported session failure:
// tallying HTTP status codes with by_status[st] = by_status.get(st, 0) + 1
// raised "KeyError: '200'" because gpython's dict.get ignored the default
// for non-string keys.
func TestPythonTool_Execute_DictGet(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			name: "reported transcript int key default",
			code: "d = {}\nprint(d.get(200, 0))",
			want: "0",
		},
		{
			name: "reported tally idiom",
			code: "by_status = {}\nfor st in [200, 200, 404]:\n    by_status[str(st)] = by_status.get(st, 0) + 1\nprint(by_status['200'], by_status['404'])",
			want: "2 1",
		},
		{
			name: "int key normalized to string bucket",
			code: "d = {'200': 'found'}\nprint(d.get(200))",
			want: "found",
		},
		{
			name: "int key with default hits string bucket",
			code: "d = {'200': 'found'}\nprint(d.get(200, 0))",
			want: "found",
		},
		{
			name: "missing int key no default returns None",
			code: "d = {}\nprint(d.get(200))",
			want: "None",
		},
		{
			name: "string key present",
			code: "d = {'x': 1}\nprint(d.get('x'))",
			want: "1",
		},
		{
			name: "string key missing with default",
			code: "d = {'x': 1}\nprint(d.get('y', 42))",
			want: "42",
		},
		{
			name: "bool key normalized",
			code: "d = {'True': 'yes'}\nprint(d.get(True, 'no'))",
			want: "yes",
		},
		{
			name: "falsey stored value returned not default",
			code: "d = {'x': 0}\nprint(d.get('x', 42))",
			want: "0",
		},
		{
			name: "stored None returned not default",
			code: "d = {'x': None}\nprint(d.get('x', 42))",
			want: "None",
		},
		{
			name: "default of any type honored",
			code: "d = {}\nprint(d.get('k', [1, 2]))",
			want: "[1, 2]",
		},
	}
	tool := &PythonTool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tool.Execute(fmt.Sprintf(`{"code": %q}`, tt.code))
			if err != nil {
				t.Fatalf("Execute failed: %v\noutput: %s", err, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("output = %q, want %q", out, tt.want)
			}
		})
	}
}

// TestPythonTool_Execute_DictGetArgErrors covers the argument-validation
// paths: zero or too many positional args raise TypeError like CPython.
func TestPythonTool_Execute_DictGetArgErrors(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{"no args", "d = {}\nd.get()", "TypeError"},
		{"too many args", "d = {}\nd.get('k', 1, 2)", "TypeError"},
	}
	tool := &PythonTool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Execute(fmt.Sprintf(`{"code": %q}`, tt.code))
			if err == nil {
				t.Fatalf("expected error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

// TestPythonTool_Execute_DictStringKeysOnlyRestriction pins the documented
// residual restriction: d[key] = value with a non-string key still raises
// KeyError (native gpython interface method, not shim-able without forking
// gpython). If this test ever starts failing because the tool no longer
// errors, the doc row in tools/python.long.md must be updated too.
func TestPythonTool_Execute_DictStringKeysOnlyRestriction(t *testing.T) {
	tool := &PythonTool{}
	_, err := tool.Execute(`{"code": "d = {}\nd[200] = 2"}`)
	if err == nil {
		t.Fatal("expected KeyError for int-key setitem (documented restriction)")
	}
	if !strings.Contains(err.Error(), "KeyError") {
		t.Errorf("error = %q, want KeyError", err.Error())
	}
}
