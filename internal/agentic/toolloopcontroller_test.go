// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
	"strings"
	"testing"
)

func TestPrepareCallExecute(t *testing.T) {
	c := NewToolLoopController([]ToolSchema{{Name: "terminal"}}, true)
	d := c.PrepareCall("terminal", `{"command":"ls"}`, "c1")
	if d.Action != ActionExecute {
		t.Fatalf("expected execute, got %v", d.Action)
	}
	if d.StatusText != "Running: ls" {
		t.Errorf("status = %q", d.StatusText)
	}
}

func TestPrepareCallDisabled(t *testing.T) {
	c := NewToolLoopController([]ToolSchema{{Name: "terminal"}}, true)
	d := c.PrepareCall("web_search", `{"query":"x"}`, "c1")
	if d.Action != ActionDisabled {
		t.Fatalf("expected disabled, got %v", d.Action)
	}
}

func TestPrepareCallHealing(t *testing.T) {
	c := NewToolLoopController([]ToolSchema{{Name: "terminal"}}, true)
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
	c := NewToolLoopController([]ToolSchema{{Name: "terminal"}}, true)
	d1 := c.PrepareCall("terminal", `{"command":"ls"}`, "c1")
	c.RecordResult(d1, "out", false)
	d2 := c.PrepareCall("terminal", `{"command":"ls"}`, "c2")
	if d2.Action != ActionDuplicate {
		t.Fatalf("expected duplicate, got %v", d2.Action)
	}
}

func TestForceFinalAnswerAfterRepeats(t *testing.T) {
	c := NewToolLoopController([]ToolSchema{{Name: "terminal"}}, true)
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
