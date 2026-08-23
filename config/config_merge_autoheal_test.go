// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import "testing"

// Regression for bugs.md "/config tool fixes are not saved / do not survive
// next load": mergeExecution never copied AutoHealToolCalls, so a
// execution.auto_heal_tool_calls value persisted to any layer was silently
// dropped by every merge — /config's "Tool call fixing" toggle reported
// success but reverted on the next launch.
func TestMergeExecution_AutoHealToolCallsSurvivesCascade(t *testing.T) {
	base := &Config{Execution: ExecutionConfig{Mode: "yolo"}}
	overlay := &Config{Execution: ExecutionConfig{AutoHealToolCalls: true}}

	mergeExecution(&base.Execution, &overlay.Execution)

	if !base.Execution.AutoHealToolCalls {
		t.Fatal("mergeExecution dropped AutoHealToolCalls: overlay value lost")
	}
}

// End-to-end through the cascade: SaveHomeField writes the toggle; Load must
// return it (previously the merge dropped it).
func TestSaveHomeField_AutoHealToolCallsSurvivesReload(t *testing.T) {
	home := t.TempDir()
	cl := NewCascadeLoader(t.TempDir(), "", nil)
	cl.homeDir = home

	if err := cl.SaveHomeField([]string{"execution", "auto_heal_tool_calls"}, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Execution.AutoHealToolCalls {
		t.Fatal("execution.auto_heal_tool_calls=true did not survive reload")
	}

	// And back off.
	if err := cl.SaveHomeField([]string{"execution", "auto_heal_tool_calls"}, false); err != nil {
		t.Fatalf("save off: %v", err)
	}
	cfg2, err := cl.Load()
	if err != nil {
		t.Fatalf("load 2: %v", err)
	}
	if cfg2.Execution.AutoHealToolCalls {
		t.Fatal("execution.auto_heal_tool_calls=false did not survive reload")
	}
}
