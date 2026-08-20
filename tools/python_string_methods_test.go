// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPythonTool_Execute_Splitlines replays the reported session failure:
// open(...).read().splitlines() raised "AttributeError: 'str' has no
// attribute 'splitlines'" because gpython does not implement str.splitlines.
func TestPythonTool_Execute_Splitlines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	code := fmt.Sprintf("lines = open(%q).read().splitlines()\nprint(lines)", path)
	tool := &PythonTool{}
	out, err := tool.Execute(fmt.Sprintf(`{"code": %q}`, code))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "['alpha', 'beta', 'gamma']") {
		t.Errorf("output = %q, want ['alpha', 'beta', 'gamma']", out)
	}
}

// TestPythonTool_Execute_SplitlinesBoundaries drives the full CPython
// line-boundary set through the real tool.
func TestPythonTool_Execute_SplitlinesBoundaries(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{"newline", `print("a\nb".splitlines())`, "['a', 'b']"},
		{"carriage return", `print("a\rb".splitlines())`, "['a', 'b']"},
		{"crlf is one boundary", `print("a\r\nb".splitlines())`, "['a', 'b']"},
		{"cr alone then lf", `print("a\r\n\rb".splitlines())`, "['a', '', 'b']"},
		{"vertical tab", `print("a\vb".splitlines())`, "['a', 'b']"},
		{"form feed", `print("a\fb".splitlines())`, "['a', 'b']"},
		{"file separator", `print("a\x1cb".splitlines())`, "['a', 'b']"},
		{"group separator", `print("a\x1db".splitlines())`, "['a', 'b']"},
		{"record separator", `print("a\x1eb".splitlines())`, "['a', 'b']"},
		{"next line", `print("a\x85b".splitlines())`, "['a', 'b']"},
		{"unicode line separator", `print("a\u2028b".splitlines())`, "['a', 'b']"},
		{"unicode paragraph separator", `print("a\u2029b".splitlines())`, "['a', 'b']"},
		{"mixed boundaries", `print("a\nb\r\nc\rd".splitlines())`, "['a', 'b', 'c', 'd']"},
		{"interior blank lines kept", `print("a\n\nb".splitlines())`, "['a', '', 'b']"},
	}
	tool := &PythonTool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tool.Execute(fmt.Sprintf(`{"code": %q}`, tt.code))
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("output = %q, want %q", out, tt.want)
			}
		})
	}
}

// TestPythonTool_Execute_SplitlinesKeepends covers keepends=True/False and
// the trailing/empty/no-newline edge cases.
func TestPythonTool_Execute_SplitlinesKeepends(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{"keepends true", `print("a\nb\r\nc".splitlines(True))`, `['a\n', 'b\r\n', 'c']`},
		{"keepends false explicit", `print("a\nb".splitlines(False))`, "['a', 'b']"},
		{"keepends int accepted", `print("a\nb".splitlines(1))`, `['a\n', 'b']`},
		{"keepends int zero", `print("a\nb".splitlines(0))`, "['a', 'b']"},
		{"keepends crlf single boundary", `print("a\r\nb".splitlines(True))`, `['a\r\n', 'b']`},
		{"trailing newline no empty element", `print("a\n".splitlines())`, "['a']"},
		{"trailing newline keepends", `print("a\n".splitlines(True))`, `['a\n']`},
		{"only a newline", `print("\n".splitlines())`, "['']"},
		{"empty string", `print("".splitlines())`, "[]"},
		{"no newline", `print("abc".splitlines())`, "['abc']"},
	}
	tool := &PythonTool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tool.Execute(fmt.Sprintf(`{"code": %q}`, tt.code))
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("output = %q, want %q", out, tt.want)
			}
		})
	}
}

// TestPythonTool_Execute_SplitlinesArgValidation covers argument errors:
// too many args and a non-int keepends both raise TypeError.
func TestPythonTool_Execute_SplitlinesArgValidation(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{"too many args", `print("a".splitlines(1, 2))`, "TypeError"},
		{"string keepends rejected", `print("a".splitlines("x"))`, "TypeError"},
	}
	tool := &PythonTool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tool.Execute(fmt.Sprintf(`{"code": %q}`, tt.code))
			if err == nil {
				t.Fatalf("expected error, got output %q", out)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.want)
			}
		})
	}
}
