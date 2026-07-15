// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tooltracker

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

// newTestTracker builds a tracker whose creator records every widget it makes
// so tests can assert creation counts and inspect final states. The widget
// list is returned via a pointer so closure appends stay visible to the
// caller (a returned slice header would diverge after reallocation).
func newTestTracker(t *testing.T) (*Tracker, *[]*tui.ToolExecutionComponent) {
	t.Helper()
	made := &[]*tui.ToolExecutionComponent{}
	tr := New(func(name, input string) *tui.ToolExecutionComponent {
		tc := tui.NewToolExecution(name, input)
		*made = append(*made, tc)
		return tc
	})
	return tr, made
}

func ptr[T any](v T) *T { return &v }

// toolStatuses collects the status of every widget the creator ever produced.
func toolStatuses(widgets []*tui.ToolExecutionComponent) []tui.ToolStatus {
	out := make([]tui.ToolStatus, len(widgets))
	for i, w := range widgets {
		out[i] = w.Status()
	}
	return out
}

// TestTracker_LateIDAdoption reproduces the stuck-widget bug: partials stream
// with an EMPTY id, then the completed call arrives with the real id, then the
// result. Exactly one widget must exist and it must be Success.
func TestTracker_LateIDAdoption(t *testing.T) {
	tr, made := newTestTracker(t)

	tr.OnCall(&agentic.OutputEvent{Type: agentic.EventToolCall, IsDelta: true,
		ToolName: "write", ToolInput: `{"content":"package main`, ToolCallID: ""})
	tr.OnCall(&agentic.OutputEvent{Type: agentic.EventToolCall, IsDelta: true,
		ToolName: "write", ToolInput: `{"content":"package main\nfunc main(){}","path":"m.go"}`, ToolCallID: ""})

	if _, created := tr.OnCall(&agentic.OutputEvent{Type: agentic.EventToolCall, IsDelta: false,
		ToolName: "write", ToolInput: `{"content":"package main\nfunc main(){}","path":"m.go"}`, ToolCallID: "real-id"}); created {
		t.Fatalf("final call should ADOPT the streaming widget, not create a new one")
	}

	tr.OnResult(&agentic.OutputEvent{Type: agentic.EventToolResult,
		ToolName: "write", ToolCallID: "real-id", Text: "[write: m.go]\nok"})

	if len(*made) != 1 {
		t.Fatalf("expected exactly 1 widget, got %d (orphan!) statuses=%v", len(*made), toolStatuses(*made))
	}
	if (*made)[0].Status() != tui.ToolSuccess {
		t.Fatalf("expected adopted widget Success, got %v", (*made)[0].Status())
	}
	if !(*made)[0].ArgsComplete() {
		t.Fatalf("adopted widget args should be complete")
	}
}

// TestTracker_ConsistentID is the baseline: partials already carry the id.
func TestTracker_ConsistentID(t *testing.T) {
	tr, made := newTestTracker(t)

	tr.OnCall(&agentic.OutputEvent{Type: agentic.EventToolCall, IsDelta: true,
		ToolName: "write", ToolInput: `{"content":"x"}`, ToolCallID: "w1"})
	if _, created := tr.OnCall(&agentic.OutputEvent{Type: agentic.EventToolCall, IsDelta: false,
		ToolName: "write", ToolInput: `{"content":"x","path":"m.go"}`, ToolCallID: "w1"}); created {
		t.Fatalf("consistent-id final should reuse the widget, not create one")
	}
	tr.OnResult(&agentic.OutputEvent{Type: agentic.EventToolResult,
		ToolName: "write", ToolCallID: "w1", Text: "ok"})

	if len(*made) != 1 || (*made)[0].Status() != tui.ToolSuccess {
		t.Fatalf("baseline: expected 1 Success widget, got %v", toolStatuses(*made))
	}
}

// TestTracker_MultipleConcurrentEmptyID matches results FIFO when the provider
// omits ids entirely.
func TestTracker_MultipleConcurrentEmptyID(t *testing.T) {
	tr, made := newTestTracker(t)

	tr.OnCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"a"}`})
	tr.OnCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"b"}`})

	tr.OnResult(&agentic.OutputEvent{Type: agentic.EventToolResult, ToolName: "bash", Text: "ra"})
	tr.OnResult(&agentic.OutputEvent{Type: agentic.EventToolResult, ToolName: "bash", Text: "rb"})

	if len(*made) != 2 {
		t.Fatalf("expected 2 widgets, got %d", len(*made))
	}
	for i, w := range *made {
		if w.Status() != tui.ToolSuccess {
			t.Fatalf("widget %d: expected Success, got %v", i, w.Status())
		}
	}
}

// TestTracker_ProgressNeverRetires ensures EventToolProgress updates output but
// leaves the widget tracked so the later result still resolves it.
func TestTracker_ProgressNeverRetires(t *testing.T) {
	tr, made := newTestTracker(t)

	tr.OnCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"go test"}`, ToolCallID: "b1"})
	got := tr.OnProgress(&agentic.OutputEvent{Type: agentic.EventToolProgress, ToolName: "bash", ToolCallID: "b1", Text: "ok pkg/a"})
	if got == nil || got.Status() != tui.ToolRunning {
		t.Fatalf("progress should update the running widget, got %v", got)
	}
	// Still running, not retired.
	if (*made)[0].Status() != tui.ToolRunning {
		t.Fatalf("widget should still be Running after progress, got %v", (*made)[0].Status())
	}
	tr.OnResult(&agentic.OutputEvent{Type: agentic.EventToolResult, ToolName: "bash", ToolCallID: "b1", Text: "ok pkg/a\nok pkg/b"})
	if (*made)[0].Status() != tui.ToolSuccess {
		t.Fatalf("expected Success after result, got %v", (*made)[0].Status())
	}
}

// TestTracker_FailAll marks inflight widgets as interrupted.
func TestTracker_FailAll(t *testing.T) {
	tr, made := newTestTracker(t)
	tr.OnCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{}`, ToolCallID: "x"})
	tr.FailAll()
	if (*made)[0].Status() != tui.ToolError {
		t.Fatalf("expected inflight widget interrupted (Error), got %v", (*made)[0].Status())
	}
}

// TestTracker_ErrorResultClassifiesStatus checks Error: results map to ToolError.
func TestTracker_ErrorResultClassifiesStatus(t *testing.T) {
	tr, made := newTestTracker(t)
	tr.OnCall(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{}`, ToolCallID: "e1"})
	tr.OnResult(&agentic.OutputEvent{Type: agentic.EventToolResult, ToolName: "bash", ToolCallID: "e1", Text: "Error: boom"})
	if (*made)[0].Status() != tui.ToolError {
		t.Fatalf("expected ToolError for Error: result, got %v", (*made)[0].Status())
	}
}

// TestTracker_NilSafe guards against nil events / nil creator.
func TestTracker_NilSafe(t *testing.T) {
	tr := New(nil)
	if _, created := tr.OnCall(nil); created {
		t.Fatal("nil event must not create")
	}
	if tr.OnProgress(nil) != nil {
		t.Fatal("nil progress must return nil")
	}
	if tr.OnResult(nil) != nil {
		t.Fatal("nil result must return nil")
	}
	tr.FailAll()
	tr.Reset()
}
