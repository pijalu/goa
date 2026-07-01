// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"testing"
)

func TestBashTool_BuildMasks_Matching(t *testing.T) {
	tool := &BashTool{EnvMaskPatterns: []string{"TOKEN_*", "SECRET_*"}}
	env := map[string]string{"TOKEN_API": "abc123", "SECRET_KEY": "xyz789", "PATH": "/usr/bin"}
	masks := tool.buildMasks(env)
	if len(masks) != 2 {
		t.Errorf("expected 2 masks, got %d: %v", len(masks), masks)
	}
}

func TestBashTool_BuildMasks_NoMatch(t *testing.T) {
	tool := &BashTool{EnvMaskPatterns: []string{"NONEXISTENT_*"}}
	env := map[string]string{"HOME": "/root"}
	masks := tool.buildMasks(env)
	if len(masks) != 0 {
		t.Errorf("expected 0 masks, got %d: %v", len(masks), masks)
	}
}

func TestBashTool_BuildMasks_EmptyEnv(t *testing.T) {
	tool := &BashTool{EnvMaskPatterns: []string{"TOKEN"}}
	masks := tool.buildMasks(nil)
	if len(masks) != 0 {
		t.Errorf("expected 0 masks for nil env, got %d", len(masks))
	}
}

func TestTrimTrailingEmptyLines_None(t *testing.T) {
	result := trimTrailingEmptyLines([]string{"a", "b", "c"})
	if len(result) != 3 {
		t.Errorf("expected 3 lines, got %d", len(result))
	}
}

func TestTrimTrailingEmptyLines_Some(t *testing.T) {
	result := trimTrailingEmptyLines([]string{"a", "b", "", "", ""})
	if len(result) != 2 {
		t.Errorf("expected 2 lines, got %d: %v", len(result), result)
	}
}

func TestTrimTrailingEmptyLines_AllEmpty(t *testing.T) {
	result := trimTrailingEmptyLines([]string{"", "", ""})
	if len(result) != 0 {
		t.Errorf("expected 0 lines, got %d", len(result))
	}
}

func TestShortenPath_Short(t *testing.T) {
	result := shortenPath("short.go")
	if result != "short.go" {
		t.Errorf("expected unchanged, got %q", result)
	}
}

func TestShortenPath_Long(t *testing.T) {
	result := shortenPath("/very/long/path/to/a/file.go")
	if len(result) < 5 {
		t.Errorf("expected some shortened path, got %q", result)
	}
}

func TestDefaultStr_Empty(t *testing.T) {
	result := defaultStr("", "fallback")
	if result != "fallback" {
		t.Errorf("expected 'fallback', got %q", result)
	}
}

func TestDefaultStr_NonEmpty(t *testing.T) {
	result := defaultStr("value", "fallback")
	if result != "value" {
		t.Errorf("expected 'value', got %q", result)
	}
}

func TestDefaultInt_Zero(t *testing.T) {
	result := defaultInt(0, 42)
	if result != 42 {
		t.Errorf("expected 42, got %d", result)
	}
}

func TestDefaultInt_NonZero(t *testing.T) {
	result := defaultInt(10, 42)
	if result != 10 {
		t.Errorf("expected 10, got %d", result)
	}
}

func TestStringArg_Found(t *testing.T) {
	result := stringArg(map[string]any{"path": "test.go"}, "path")
	if result != "test.go" {
		t.Errorf("expected 'test.go', got %q", result)
	}
}

func TestStringArg_NotFound(t *testing.T) {
	result := stringArg(map[string]any{}, "path")
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestIntArg_Found(t *testing.T) {
	result := intArg(map[string]any{"lines": 42}, "lines")
	if result != 42 {
		t.Errorf("expected 42, got %d", result)
	}
}

func TestIntArg_NotFound(t *testing.T) {
	result := intArg(map[string]any{}, "lines")
	if result != 0 {
		t.Errorf("expected 0, got %d", result)
	}
}

func TestClampLineRange_WithinBounds(t *testing.T) {
	start, end := clampLineRange(1, 10, 100, 200)
	if start != 1 || end != 10 {
		t.Errorf("expected 1,10 got %d,%d", start, end)
	}
}

func TestClampLineRange_ZeroStart(t *testing.T) {
	start, end := clampLineRange(0, 10, 100, 200)
	if start != 1 || end != 10 {
		t.Errorf("expected 1,10 got %d,%d", start, end)
	}
}

func TestClampLineRange_EndBeyondTotal(t *testing.T) {
	start, end := clampLineRange(1, 200, 100, 200)
	if start != 1 || end != 100 {
		t.Errorf("expected 1,100 got %d,%d", start, end)
	}
}
