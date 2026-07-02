// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// commandStatusHint mimics the bash/terminal status for controller unit tests
// (the real implementation lives on BashTool/TerminalTool in the tools pkg).
func commandStatusHint(arguments string) string {
	cmd := extractJSONStringField(arguments, "command")
	if len(cmd) > 60 {
		cmd = cmd[:57] + "..."
	}
	if cmd == "" {
		return "Running command..."
	}
	return "Running: " + cmd
}

func terminalHints() map[string]ToolLoopHints {
	return map[string]ToolLoopHints{
		"terminal": {HealArg: "command", Status: commandStatusHint},
	}
}

func TestPrepareCallExecute(t *testing.T) {
	c := NewToolLoopController([]ToolSchema{{Name: "terminal"}}, terminalHints(), true)
	d := c.PrepareCall("terminal", `{"command":"ls"}`, "c1")
	if d.Action != ActionExecute {
		t.Fatalf("expected execute, got %v", d.Action)
	}
	if d.StatusText != "Running: ls" {
		t.Errorf("status = %q", d.StatusText)
	}
}

func TestPrepareCallDisabled(t *testing.T) {
	c := NewToolLoopController([]ToolSchema{{Name: "terminal"}}, terminalHints(), true)
	d := c.PrepareCall("web_search", `{"query":"x"}`, "c1")
	if d.Action != ActionDisabled {
		t.Fatalf("expected disabled, got %v", d.Action)
	}
}

func TestPrepareCallHealing(t *testing.T) {
	c := NewToolLoopController([]ToolSchema{{Name: "terminal"}}, terminalHints(), true)
	d := c.PrepareCall("terminal", `ls -la`, "c1")
	if d.Action != ActionExecute {
		t.Fatalf("expected execute, got %v", d.Action)
	}
	if !d.Healed {
		t.Error("expected healed")
	}
	if !strings.Contains(d.Arguments, `"command":"ls -la"`) {
		t.Errorf("arguments = %q", d.Arguments)
	}
}

func TestDuplicateDetection(t *testing.T) {
	c := NewToolLoopController([]ToolSchema{{Name: "terminal"}}, terminalHints(), true)
	d1 := c.PrepareCall("terminal", `{"command":"ls"}`, "c1")
	c.RecordResult(d1, "out", false)
	d2 := c.PrepareCall("terminal", `{"command":"ls"}`, "c2")
	if d2.Action != ActionDuplicate {
		t.Fatalf("expected duplicate, got %v", d2.Action)
	}
}

func TestForceFinalAnswerAfterRepeats(t *testing.T) {
	c := NewToolLoopController([]ToolSchema{{Name: "terminal"}}, terminalHints(), true)
	d1 := c.PrepareCall("terminal", `{"command":"ls"}`, "c1")
	c.RecordResult(d1, "out", false)
	for i := 0; i < 3; i++ {
		d := c.PrepareCall("terminal", `{"command":"ls"}`, fmt.Sprintf("call_%d", i))
		c.RecordNoop(d)
	}
	if !c.ForceFinalAnswer() {
		t.Error("expected force final answer")
	}
}

// extractJSONStringField reads a string field from a JSON object argument. Test
// helper for the commandStatusHint above.
func extractJSONStringField(argsJSON, key string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}
