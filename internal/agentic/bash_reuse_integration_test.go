// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// Integration regression for bugs.md: the model ran the full test suite five
// times changing only the trailing `grep -c` pattern. The near-duplicate guard
// must (a) NOT block execution — each grep genuinely returns a different count
// — but (b) append a save-once-refilter hint to the re-run's result, and (c)
// stay silent when the same upstream re-runs after a state mutation (edit).
func TestBashNearDuplicate_HintOnRefilterSameEpoch(t *testing.T) {
	a := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})

	mkCall := func(id, cmd string) provider.ContentBlock {
		return provider.ContentBlock{
			Type:          provider.ContentBlockToolCall,
			ToolCallID:    id,
			ToolName:      "bash",
			ToolArguments: `{"command":` + cmd + `}`,
		}
	}
	results := map[string]ToolCallResult{
		"c1": {CallID: "c1", Output: "1895"},
		"c2": {CallID: "c2", Output: "42"},
	}

	// First run of the upstream — no hint.
	call1 := mkCall("c1", `"go test -count=1 -v . 2>&1 | grep -c \"result mismatch\""`)
	a.shouldBufferToolCall(call1)
	out1 := a.resolveToolResultContent(call1, results)
	if strings.Contains(out1, "Efficiency note") {
		t.Errorf("first run of an upstream must not be hinted; got: %q", out1)
	}

	// Second run, same upstream, different filter, same epoch → hint appended,
	// but the real output ("42") is preserved (not blocked).
	call2 := mkCall("c2", `"go test -count=1 -v . 2>&1 | grep -c \"table not found\""`)
	a.shouldBufferToolCall(call2)
	out2 := a.resolveToolResultContent(call2, results)
	if !strings.Contains(out2, "Efficiency note") {
		t.Errorf("re-run of same upstream (filter changed) should be hinted; got: %q", out2)
	}
	if !strings.Contains(out2, "42") {
		t.Errorf("the real result must be preserved (non-blocking); got: %q", out2)
	}
}

func TestBashNearDuplicate_SilentAfterMutation(t *testing.T) {
	// newLoopAgent registers a "bash" StateMutator stub so recordToolExecOutcome
	// actually advances the state epoch on success.
	a := newLoopAgent(t, Config{})

	mkCall := func(id, cmd string) provider.ContentBlock {
		return provider.ContentBlock{
			Type:          provider.ContentBlockToolCall,
			ToolCallID:    id,
			ToolName:      "bash",
			ToolArguments: `{"command":` + cmd + `}`,
		}
	}
	results := map[string]ToolCallResult{
		"c1": {CallID: "c1", Output: "1"},
		"c2": {CallID: "c2", Output: "2"},
	}

	call1 := mkCall("c1", `"go test ./... | grep -c foo"`)
	a.shouldBufferToolCall(call1)
	_ = a.resolveToolResultContent(call1, results)

	// A state-mutating tool succeeds → epoch advances. Re-running the same
	// test command is legitimate (the code changed), so no hint.
	a.recordToolExecOutcome("bash", nil)

	call2 := mkCall("c2", `"go test ./... | grep -c bar"`)
	a.shouldBufferToolCall(call2)
	out2 := a.resolveToolResultContent(call2, results)
	if strings.Contains(out2, "Efficiency note") {
		t.Errorf("re-run after a state mutation must not be hinted; got: %q", out2)
	}
}
