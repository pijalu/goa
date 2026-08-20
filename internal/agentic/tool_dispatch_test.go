// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"strings"
	"testing"

	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/perms"
)

// dispatchProbeTool is a nested-capable tool (like run_code): its
// ExecuteContext reads the ToolDispatcher the agent injected and fires a
// sub-call through it. It records the sub-call outcome so the test can assert
// that sub-calls traverse the guarded pipeline.
type dispatchProbeTool struct {
	BaseTool
	// subName is the tool name the probe dispatches as its sub-call.
	subName string
	// subInput is the JSON input for the sub-call.
	subInput string
	// result receives the sub-call outcome.
	result chan string
}

func (t *dispatchProbeTool) Schema() ToolSchema {
	return ToolSchema{Name: "probe", Description: "probe"}
}

func (t *dispatchProbeTool) Execute(input string) (string, error) {
	return "", errProbe("probe must run through ExecuteContext")
}

func (t *dispatchProbeTool) IsRetryable(err error) bool { return false }

func (t *dispatchProbeTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	d, ok := ToolDispatcherFrom(ctx)
	if !ok {
		return "", errProbe("no dispatcher in context")
	}
	res, err := d(ctx, t.subName, t.subInput, "probe:sub:1")
	if err != nil {
		t.result <- "ERR:" + err.Error()
		return "sub-error", nil
	}
	t.result <- "OK:" + res.Output
	return res.Output, nil
}

func errProbe(msg string) error {
	return &probeErr{msg: msg}
}

type probeErr struct{ msg string }

func (e *probeErr) Error() string { return e.msg }

// TestAgent_RunToolInjectsDispatcher verifies a nested-capable tool receives a
// ToolDispatcher in its execution context (the run_code contract).
func TestAgent_RunToolInjectsDispatcher(t *testing.T) {
	probe := &dispatchProbeTool{subName: "echo", subInput: "{}", result: make(chan string, 1)}
	a := NewAgent(Config{
		Model: agenticprovider.Model{ID: "test"},
		Tools: []Tool{probe, echoSoloTool{}},
	})
	out, err := a.executeToolWithResult(context.Background(), "probe", "{}", "call_1")
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if out.Output != "{}" {
		t.Errorf("probe output = %q, want the echo sub-call result", out.Output)
	}
	select {
	case got := <-probe.result:
		if !strings.HasPrefix(got, "OK:") {
			t.Errorf("sub-call outcome = %q, want OK:", got)
		}
	default:
		t.Error("sub-call was not dispatched")
	}
}

// TestAgent_SubCallEnforcesGuardPolicy verifies a sub-call re-enters the same
// guarded pipeline as a direct call: a mode guard that denies the sub-call
// tool rejects the nested execution.
func TestAgent_SubCallEnforcesGuardPolicy(t *testing.T) {
	probe := &dispatchProbeTool{subName: "write", subInput: `{"path":"/etc/passwd"}`, result: make(chan string, 1)}
	a := NewAgent(Config{
		Model: agenticprovider.Model{ID: "test"},
		Tools: []Tool{probe, echoSoloTool{}},
		GetGuardConfig: func() perms.GuardConfig {
			return perms.GuardConfig{
				Rules: []perms.GuardRule{
					{Tools: []string{"write"}, Allow: []string{`\.goa/plan`}, Message: "Planner mode restricts writes to plan directories."},
				},
			}
		},
	})
	// The probe's own dispatch is allowed (no guard rule for "probe"); its
	// sub-call to "write" outside .goa/plan must be denied by the guard.
	if _, err := a.executeToolWithResult(context.Background(), "probe", "{}", "call_1"); err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	select {
	case got := <-probe.result:
		if !strings.Contains(got, "ERR:") || !strings.Contains(got, "Planner mode restricts") {
			t.Errorf("sub-call outcome = %q, want the guard denial surfaced", got)
		}
	default:
		t.Error("sub-call was not dispatched")
	}
}

// TestAgent_SubCallSelfExclusion verifies a nested-capable tool cannot
// recursively invoke itself through its own dispatcher.
func TestAgent_SubCallSelfExclusion(t *testing.T) {
	probe := &dispatchProbeTool{subName: "probe", subInput: "{}", result: make(chan string, 1)}
	a := NewAgent(Config{
		Model: agenticprovider.Model{ID: "test"},
		Tools: []Tool{probe},
	})
	if _, err := a.executeToolWithResult(context.Background(), "probe", "{}", "call_1"); err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	select {
	case got := <-probe.result:
		if !strings.Contains(got, "cannot be invoked as a run_code sub-call") {
			t.Errorf("sub-call outcome = %q, want self-recursion refused", got)
		}
	default:
		t.Error("sub-call was not dispatched")
	}
}

// TestAgent_SubCallUnknownTool verifies a sub-call to an unregistered tool is
// rejected by the registry lookup (same as a direct call).
func TestAgent_SubCallUnknownTool(t *testing.T) {
	probe := &dispatchProbeTool{subName: "does_not_exist", subInput: "{}", result: make(chan string, 1)}
	a := NewAgent(Config{
		Model: agenticprovider.Model{ID: "test"},
		Tools: []Tool{probe},
	})
	if _, err := a.executeToolWithResult(context.Background(), "probe", "{}", "call_1"); err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	select {
	case got := <-probe.result:
		if !strings.Contains(got, "unknown tool") {
			t.Errorf("sub-call outcome = %q, want unknown-tool rejection", got)
		}
	default:
		t.Error("sub-call was not dispatched")
	}
}

// TestToolDispatcherContext verifies the context carry/read round-trip.
func TestToolDispatcherContext(t *testing.T) {
	ctx := context.Background()
	if _, ok := ToolDispatcherFrom(ctx); ok {
		t.Error("empty context should not carry a dispatcher")
	}
	called := false
	d := func(ctx context.Context, name, input, callID string) (ToolResult, error) {
		called = true
		return ToolResult{Output: "x"}, nil
	}
	ctx2 := WithToolDispatcher(ctx, d)
	got, ok := ToolDispatcherFrom(ctx2)
	if !ok {
		t.Fatal("dispatcher missing after WithToolDispatcher")
	}
	res, err := got(ctx2, "t", "{}", "id")
	if err != nil || res.Output != "x" || !called {
		t.Errorf("dispatcher round-trip failed: res=%+v err=%v called=%v", res, err, called)
	}
}
