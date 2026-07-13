// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPythonTool_Schema(t *testing.T) {
	tool := &PythonTool{}
	s := tool.Schema()
	if s.Name != "python" {
		t.Errorf("Name = %q, want %q", s.Name, "python")
	}
	props, ok := s.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing properties")
	}
	if _, ok := props["code"]; !ok {
		t.Error("missing code property")
	}
	if _, ok := props["timeout"]; !ok {
		t.Error("missing timeout property")
	}
}

func TestPythonTool_Execute_Print(t *testing.T) {
	tool := &PythonTool{}
	out, err := tool.Execute(`{"code": "print('hello world')"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("output = %q, want hello world", out)
	}
}

func TestPythonTool_Execute_Computation(t *testing.T) {
	tool := &PythonTool{}
	out, err := tool.Execute(`{"code": "print(sum(range(10)))"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(out, "45") {
		t.Errorf("output = %q, want 45", out)
	}
}

func TestPythonTool_Execute_RuntimeError(t *testing.T) {
	tool := &PythonTool{}
	_, err := tool.Execute(`{"code": "1/0"}`)
	if err == nil {
		t.Fatal("expected error for division by zero")
	}
	if !strings.Contains(err.Error(), "ZeroDivisionError") {
		t.Errorf("error = %q, want ZeroDivisionError", err.Error())
	}
}

func TestPythonTool_Execute_InvalidInput(t *testing.T) {
	tool := &PythonTool{}
	_, err := tool.Execute(`not json`)
	if err == nil {
		t.Fatal("expected error for invalid input")
	}
}

func TestPythonTool_Execute_MissingCode(t *testing.T) {
	tool := &PythonTool{}
	_, err := tool.Execute(`{"timeout": 30}`)
	if err == nil {
		t.Fatal("expected error for missing code")
	}
}

func TestPythonTool_Execute_Timeout(t *testing.T) {
	tool := &PythonTool{}
	// A tight loop in gpython should hit the short timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := tool.ExecuteContext(ctx, `{"code": "while True: pass", "timeout": 1}`)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error = %q, want timeout or cancellation", err.Error())
	}
}

func TestPythonTool_Execute_Cancellation(t *testing.T) {
	tool := &PythonTool{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tool.ExecuteContext(ctx, `{"code": "print('hello')"}`)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error = %q, want cancelled", err.Error())
	}
}

func TestPythonTool_ShortDoc(t *testing.T) {
	tool := &PythonTool{}
	if tool.ShortDoc() == "" {
		t.Error("ShortDoc should not be empty")
	}
}

func TestPythonTool_LongDoc(t *testing.T) {
	tool := &PythonTool{}
	if tool.LongDoc() == "" {
		t.Error("LongDoc should not be empty")
	}
}

func TestPythonTool_Examples(t *testing.T) {
	tool := &PythonTool{}
	if len(tool.Examples()) == 0 {
		t.Error("Examples should not be empty")
	}
}

func TestPythonTool_LoopHints(t *testing.T) {
	tool := &PythonTool{}
	hints := tool.LoopHints()
	if hints.HealArg != "code" {
		t.Errorf("HealArg = %q, want code", hints.HealArg)
	}
	if hints.Status == nil {
		t.Fatal("Status should not be nil")
	}
	status := hints.Status(`{"code": "print(1)"}`)
	if !strings.Contains(status, "print(1)") {
		t.Errorf("Status = %q, want print(1)", status)
	}
}

func TestPythonTool_Access(t *testing.T) {
	tool := &PythonTool{}
	access := tool.Access("")
	if access.Category != "shell" {
		t.Errorf("Category = %q, want shell", access.Category)
	}
}

func TestPythonTool_NormalizeTimeout(t *testing.T) {
	tests := []struct {
		input    int
		fallback int
		want     int
	}{
		{0, 0, DefaultPythonTimeoutS},
		{30, 0, 30},
		{-1, 90, 90},
		{MaxPythonTimeoutS + 1, 0, MaxPythonTimeoutS},
	}
	for _, tt := range tests {
		got := normalizePythonTimeout(tt.input, tt.fallback)
		if got != tt.want {
			t.Errorf("normalizePythonTimeout(%d, %d) = %d, want %d", tt.input, tt.fallback, got, tt.want)
		}
	}
}

func TestPythonTool_TruncateOutput(t *testing.T) {
	tool := &PythonTool{MaxOutputLines: 2}
	out := tool.truncateOutput("line1\nline2\nline3\n")
	if strings.Contains(out, "line1") {
		t.Error("expected earlier lines to be truncated")
	}
	if !strings.Contains(out, "line3") {
		t.Error("expected last line to be kept")
	}
	if !strings.Contains(out, "truncated") {
		t.Error("expected truncation notice")
	}
}
