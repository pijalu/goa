// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"os"
	"strings"
	"testing"
)

// limitReadLines tests

func TestLimitReadLines_Expanded(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	result, remaining := limitReadLines(lines, true)
	if len(result) != 12 {
		t.Errorf("expected all 12 lines when expanded, got %d", len(result))
	}
	if remaining != 0 {
		t.Errorf("expected 0 remaining when expanded, got %d", remaining)
	}
}

func TestLimitReadLines_UnderLimit(t *testing.T) {
	lines := []string{"a", "b", "c"}
	result, remaining := limitReadLines(lines, false)
	if len(result) != 3 {
		t.Errorf("expected all 3 lines when under limit, got %d", len(result))
	}
	if remaining != 0 {
		t.Errorf("expected 0 remaining, got %d", remaining)
	}
}

func TestLimitReadLines_OverLimit(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	result, remaining := limitReadLines(lines, false)
	if len(result) != 10 {
		t.Errorf("expected 10 lines, got %d", len(result))
	}
	if remaining != 10 {
		t.Errorf("expected 10 remaining, got %d", remaining)
	}
}

func TestLimitReadLines_ExactlyLimit(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "line"
	}
	result, remaining := limitReadLines(lines, false)
	if len(result) != 10 {
		t.Errorf("expected 10 lines, got %d", len(result))
	}
	if remaining != 0 {
		t.Errorf("expected 0 remaining, got %d", remaining)
	}
}

// themeHex tests

func TestThemeHex_KnownTokens(t *testing.T) {
	tests := []struct {
		token string
		want  string
	}{
		{"toolTitle", "#ffffff"},
		{"bash_prompt", "#7dd3fc"},
		{"toolOutput", "#8b949e"},
		{"warning", "#d29922"},
		{"error", "#f85149"},
		{"token_prompt", "#1f6feb"},
	}
	for _, tt := range tests {
		got := themeHex(tt.token)
		if got != tt.want {
			t.Errorf("themeHex(%q) = %q, want %q", tt.token, got, tt.want)
		}
	}
}

func TestThemeHex_UnknownToken(t *testing.T) {
	result := themeHex("nonexistent_token")
	if result != "#888888" {
		t.Errorf("expected default '#888888', got %q", result)
	}
}

func TestThemeHex_SystemMsg(t *testing.T) {
	result := themeHex("system_msg")
	if result != "#8b949e" {
		t.Errorf("expected '#8b949e', got %q", result)
	}
}

// writeGoComment tests

func TestWriteGoComment_CommentLine(t *testing.T) {
	var out strings.Builder
	c := &hlColors{comm: "<comm>", reset: "</>"}
	line := "\t// this is a comment"
	i := 1
	result := writeGoComment(line, &i, &out, c)
	if !result {
		t.Error("expected true for comment line")
	}
	if !strings.Contains(out.String(), "<comm>") {
		t.Errorf("expected color tag, got: %s", out.String())
	}
	if i != len(line) {
		t.Errorf("expected i to advance to end (%d), got %d", len(line), i)
	}
}

func TestWriteGoComment_NotAComment(t *testing.T) {
	var out strings.Builder
	c := &hlColors{}
	line := "not a comment"
	i := 0
	result := writeGoComment(line, &i, &out, c)
	if result {
		t.Error("expected false for non-comment line")
	}
}

// globalMemoryDir tests

func TestMementoTool_GlobalMemoryDir_EmptyGlobalDir(t *testing.T) {
	tool := &MementoTool{GlobalDir: ""}
	dir := tool.globalMemoryDir()
	if dir == "" {
		t.Error("expected non-empty dir")
	}
	if !strings.Contains(dir, ".goa") || !strings.Contains(dir, "memory") {
		t.Errorf("expected .goa/memory path, got %q", dir)
	}
}

func TestMementoTool_GlobalMemoryDir_HomeEnv(t *testing.T) {
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)
	os.Setenv("HOME", "/custom/home")
	tool := &MementoTool{GlobalDir: ""}
	dir := tool.globalMemoryDir()
	if !strings.Contains(dir, "/custom/home/.goa/memory") {
		t.Errorf("expected /custom/home/.goa/memory, got %q", dir)
	}
}

// newBashCommand tests

func TestNewBashCommand(t *testing.T) {
	cmd := newBashCommand("echo hello")
	if cmd == nil {
		t.Fatal("expected non-nil command")
	}
	if cmd.Path == "" {
		t.Fatal("expected non-empty shell path")
	}
}

// formatReadLines tests

func TestFormatReadLines_WithLang(t *testing.T) {
	lines := []string{"line1", "line2"}
	result := formatReadLines(lines, 0, "go", "key")
	if !strings.Contains(result, "line1") || !strings.Contains(result, "line2") {
		t.Errorf("expected lines in output, got: %s", result)
	}
}

func TestFormatReadLines_WithRemaining(t *testing.T) {
	lines := []string{"line1"}
	result := formatReadLines(lines, 5, "", "Ctrl+O")
	if !strings.Contains(result, "5 more lines") {
		t.Errorf("expected remaining indicator, got: %s", result)
	}
	if !strings.Contains(result, "Ctrl+O") {
		t.Errorf("expected expansion key, got: %s", result)
	}
}

func TestFormatReadLines_NoLang(t *testing.T) {
	lines := []string{"line1"}
	result := formatReadLines(lines, 0, "", "key")
	if result == "" {
		t.Error("expected non-empty output")
	}
}

// highlightBash edge cases

func TestHighlightBash_Variable(t *testing.T) {
	result := highlightBash("echo $HOME")
	if result == "" {
		t.Error("expected non-empty highlighted output")
	}
}

func TestHighlightBash_String(t *testing.T) {
	result := highlightBash(`echo "hello"`)
	if result == "" {
		t.Error("expected non-empty highlighted output")
	}
}

// highlightGo edge cases

func TestHighlightGo_Syntax(t *testing.T) {
	result := highlightGo("func main() {")
	if result == "" {
		t.Error("expected non-empty highlighted output")
	}
}

func TestHighlightGo_Comment(t *testing.T) {
	result := highlightGo("// comment")
	if !strings.Contains(result, "comment") {
		t.Errorf("expected comment preserved, got: %s", result)
	}
}

func TestHighlightGo_StringLiteral(t *testing.T) {
	result := highlightGo(`s := "hello"`)
	if result == "" {
		t.Error("expected non-empty highlighted output")
	}
}

func TestHighlightGo_Number(t *testing.T) {
	result := highlightGo("x := 42")
	if result == "" {
		t.Error("expected non-empty highlighted output")
	}
}

func TestHighlightGo_Keyword(t *testing.T) {
	result := highlightGo("package main")
	if result == "" {
		t.Error("expected non-empty highlighted output")
	}
}

// writeGoNumber tests

func TestWriteGoNumber_Decimal(t *testing.T) {
	var out strings.Builder
	c := &hlColors{num: "<num>", reset: "</>"}
	line := "x := 42"
	i := 5
	result := writeGoNumber(line, &i, &out, c)
	if !result {
		t.Error("expected true for number")
	}
	if !strings.Contains(out.String(), "<num>") {
		t.Errorf("expected color tag, got: %s", out.String())
	}
}

func TestWriteGoNumber_NotANumber(t *testing.T) {
	var out strings.Builder
	c := &hlColors{}
	line := "hello"
	i := 0
	result := writeGoNumber(line, &i, &out, c)
	if result {
		t.Error("expected false for non-number")
	}
}

// writeGoIdent tests

func TestWriteGoIdent_Keyword(t *testing.T) {
	var out strings.Builder
	c := &hlColors{kw: "<kw>", reset: "</>", fn: "<fn>", typ: "<ty>"}
	keywords := map[string]bool{"func": true}
	types := map[string]bool{}
	line := "func main()"
	i := 0
	result := writeGoIdent(line, &i, &out, keywords, types, c)
	if !result {
		t.Error("expected true for ident")
	}
	if !strings.Contains(out.String(), "<kw>") {
		t.Errorf("expected keyword coloring, got: %s", out.String())
	}
}

func TestWriteGoIdent_FunctionCall(t *testing.T) {
	var out strings.Builder
	c := &hlColors{fn: "<fn>", reset: "</>"}
	keywords := map[string]bool{}
	types := map[string]bool{}
	line := `print("hello")`
	i := 0
	result := writeGoIdent(line, &i, &out, keywords, types, c)
	if !result {
		t.Error("expected true for function call")
	}
	if !strings.Contains(out.String(), "<fn>") {
		t.Errorf("expected function coloring, got: %s", out.String())
	}
}

func TestWriteGoIdent_NotIdent(t *testing.T) {
	var out strings.Builder
	c := &hlColors{}
	keywords := map[string]bool{}
	types := map[string]bool{}
	line := "   spaces"
	i := 0
	result := writeGoIdent(line, &i, &out, keywords, types, c)
	if result {
		t.Error("expected false for non-ident")
	}
}

// writeGoString tests

func TestWriteGoString_DoubleQuote(t *testing.T) {
	var out strings.Builder
	c := &hlColors{str: "<str>", reset: "</>"}
	line := `s := "hello"`
	i := 5
	result := writeGoString(line, &i, &out, c)
	if !result {
		t.Error("expected true for string")
	}
	if !strings.Contains(out.String(), "<str>") {
		t.Errorf("expected string coloring, got: %s", out.String())
	}
}

func TestWriteGoString_Backtick(t *testing.T) {
	var out strings.Builder
	c := &hlColors{str: "<str>", reset: "</>"}
	line := "s := `raw`"
	i := 5
	result := writeGoString(line, &i, &out, c)
	if !result {
		t.Error("expected true for backtick string")
	}
}

func TestWriteGoString_NotAString(t *testing.T) {
	var out strings.Builder
	c := &hlColors{}
	line := "abc"
	i := 0
	result := writeGoString(line, &i, &out, c)
	if result {
		t.Error("expected false for non-string")
	}
}

func TestWriteGoString_EscapedChar(t *testing.T) {
	var out strings.Builder
	c := &hlColors{str: "<str>", reset: "</>"}
	line := `s := "hello\"world"`
	i := 5
	result := writeGoString(line, &i, &out, c)
	if !result {
		t.Error("expected true for string with escape")
	}
}
