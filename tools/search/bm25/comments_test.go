// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package bm25

import (
	"strings"
	"testing"
)

// Helper to produce newline and tab without write-tool escaping issues.
var (
	NL = "\x0a"
	TB = "\x09"
	SQ = "\x27"
	DQ = "\x22"
)

func TestStripComments_Go(t *testing.T) {
	cmt := "//"
	tests := []struct {
		name string
		path string
		src  string
		want string
	}{
		{
			name: "single-line // removed",
			path: "main.go",
			src:  "func foo() {" + NL + TB + "return bar " + cmt + " this is a comment" + NL + "}",
			want: "this is a comment",
		},
		{
			name: "block /* */ removed",
			path: "main.go",
			src:  "x := 1 /* inline block */ + 2",
			want: "inline block",
		},
		{
			name: "multi-line block removed",
			path: "main.go",
			src:  "/*" + NL + " * Package doc" + NL + " */" + NL + "package foo",
			want: "Package doc",
		},
		{
			name: "// in string literal preserved",
			path: "main.go",
			src:  "url := " + DQ + "http://example.com" + DQ + " " + cmt + " the URL",
			want: "the URL",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripComments(tt.path, tt.src)
			if strings.Contains(got, tt.want) {
				t.Errorf("stripComments(%q, src) still contains removed text %q", tt.path, tt.want)
			}
		})
	}
}

func TestStripComments_Go_CodePreserved(t *testing.T) {
	cmt := "//"
	tests := []struct {
		name string
		path string
		src  string
		want string
	}{
		{
			name: "code before line comment",
			path: "foo.go",
			src:  "func foo() { return bar } " + cmt + " comment",
			want: "func foo() { return bar }",
		},
		{
			name: "code after block comment",
			path: "foo.go",
			src:  "x := 1 /* comment */ + 2",
			want: " + 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripComments(tt.path, tt.src)
			if !strings.Contains(got, tt.want) {
				t.Errorf("stripComments(%q, src) lost code %q", tt.path, tt.want)
			}
		})
	}
}

func TestStripComments_Python(t *testing.T) {
	h := "#"
	src := "def foo():" + NL + "    pass  " + h + " this is ignored"
	got := stripComments("test.py", src)
	if strings.Contains(got, "this is ignored") {
		t.Errorf("should remove Python comment, got: %q", got)
	}
	if !strings.HasPrefix(got, "def foo():"+NL+"    pass") {
		t.Errorf("should preserve code, got: %q", got)
	}
}

func TestStripComments_HTML(t *testing.T) {
	src := "<div><!-- this is a comment --></div>"
	got := stripComments("index.html", src)
	if strings.Contains(got, "this is a comment") {
		t.Errorf("should remove HTML comment, got: %q", got)
	}
	if !strings.Contains(got, "<div>") || !strings.Contains(got, "</div>") {
		t.Errorf("should preserve HTML tags, got: %q", got)
	}
}

func TestStripComments_NoCommentSyntax(t *testing.T) {
	src := "some text" + NL + "another line"
	got := stripComments("data.txt", src)
	if got != src {
		t.Errorf("should not modify unhandled types: got %q, want %q", got, src)
	}
}

func TestStripComments_StringLiteralHeuristic(t *testing.T) {
	cmt := "//"
	src := "url := " + DQ + "http://example.com/path" + DQ + " " + cmt + " real comment"
	got := stripComments("test.go", src)
	if strings.Contains(got, "real comment") {
		t.Errorf("should remove line comment, got: %q", got)
	}
	if !strings.Contains(got, "http://example.com/path") {
		t.Errorf("should preserve URL inside string, got: %q", got)
	}
}

func TestStripComments_MixedLineAndBlock(t *testing.T) {
	cmt := "//"
	src := cmt + " doc comment" + NL + "package main" + NL + NL +
		"/*" + NL + "block" + NL + "*/" + NL +
		"func main() {" + NL + TB + cmt + " inline" + NL + TB + "print(" + DQ + "hello" + DQ + ")" + NL + "}"
	got := stripComments("main.go", src)
	if strings.Contains(got, "doc comment") {
		t.Errorf("line comment not removed, got: %q", got)
	}
	if strings.Contains(got, "block") {
		t.Errorf("block comment not removed, got: %q", got)
	}
	if !strings.Contains(got, "package main") {
		t.Errorf("code lost: 'package main', got: %q", got)
	}
}

func TestStripComments_CSS(t *testing.T) {
	src := "/* header styles */" + NL + ".header { color: red; }"
	got := stripComments("styles.css", src)
	if strings.Contains(got, "header styles") {
		t.Errorf("CSS comment not removed, got: %q", got)
	}
	if !strings.Contains(got, ".header") {
		t.Errorf("CSS selector lost, got: %q", got)
	}
}

func TestStripComments_Makefile(t *testing.T) {
	src := "# Build the project" + NL + "build:" + NL + TB + "go build ./..." + NL
	got := stripComments("Makefile", src)
	if strings.Contains(got, "Build the project") {
		t.Errorf("makefile comment not removed, got: %q", got)
	}
	if !strings.Contains(got, "build:") {
		t.Errorf("target lost, got: %q", got)
	}
}

func TestStripComments_BlockCommentNoClobber(t *testing.T) {
	src := "a /* comment */ b"
	got := stripComments("f.go", src)
	if strings.Contains(got, "ab") {
		t.Errorf("tokens merged after block removal, got: %q", got)
	}
}

func TestStripComments_YAML(t *testing.T) {
	src := "# Configuration" + NL + "key: value" + NL + "# another comment" + NL + "foo: bar" + NL
	got := stripComments("config.yaml", src)
	if strings.Contains(got, "Configuration") {
		t.Errorf("YAML comment not removed, got: %q", got)
	}
	if !strings.Contains(got, "key: value") {
		t.Errorf("YAML content lost, got: %q", got)
	}
}

func TestStripComments_INI(t *testing.T) {
	src := "; INI comment" + NL + "key=value" + NL + "# another comment" + NL + "foo=bar" + NL
	got := stripComments("config.ini", src)
	if strings.Contains(got, "INI comment") {
		t.Errorf("INI ; comment not removed, got: %q", got)
	}
	if strings.Contains(got, "another comment") {
		t.Errorf("INI comment not removed, got: %q", got)
	}
	if !strings.Contains(got, "key=value") {
		t.Errorf("INI content lost, got: %q", got)
	}
}
