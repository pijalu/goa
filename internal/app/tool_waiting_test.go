// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/toolaccess"
	"github.com/pijalu/goa/tui"
)

// waitingToolWidgets returns every tool widget currently in the chat viewport.
func waitingToolWidgets(chat *tui.ChatViewport) []*tui.ToolExecutionComponent {
	var out []*tui.ToolExecutionComponent
	for _, c := range chat.Children() {
		if tc, ok := c.(*tui.ToolExecutionComponent); ok {
			out = append(out, tc)
		}
	}
	return out
}

// TestUI_NamelessToolCallDeltaNoBlankWidget reproduces "Empty tool
// TUI" end-to-end: an OpenAI-style stream ships the call id first and the
// tool name in a later chunk. The app must create exactly ONE widget (at the
// first named delta) — never a blank-header box.
func TestUI_NamelessToolCallDeltaNoBlankWidget(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	// Chunk 1: id only, NO name — historically created the blank widget.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "", ToolInput: "", IsDelta: true})
	if ws := waitingToolWidgets(sc.chat); len(ws) != 0 {
		t.Fatalf("nameless delta must create no widget, got %d", len(ws))
	}
	// Chunk 2: name + args prefix.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "bash", ToolInput: `{"command":"ls`, IsDelta: true})
	// Chunk 3: more args.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "bash", ToolInput: `{"command":"ls -la"}`, IsDelta: true})
	// Final + start + result.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "bash", ToolInput: `{"command":"ls -la"}`})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolStart, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "bash"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolCallID: "c1", ToolName: "bash", Text: "file1\nfile2"})

	ws := waitingToolWidgets(sc.chat)
	if len(ws) != 1 {
		t.Fatalf("exactly one widget expected, got %d (blank orphan!)", len(ws))
	}
	if ws[0].ToolName() != "bash" {
		t.Errorf("widget name = %q, want bash", ws[0].ToolName())
	}
	if ws[0].Status() != tui.ToolSuccess {
		t.Errorf("widget must be Success after result, got %v", ws[0].Status())
	}
}

type toolBatchCall struct {
	id, name, input string
	access          toolaccess.Access
}

type toolBatchScenario struct {
	sc      *uiScenario
	sched   *agentic.ToolScheduler
	calls   []toolBatchCall
	release chan struct{}
}

func shellBatchCall(id, command string) toolBatchCall {
	return toolBatchCall{
		id: id, name: "bash", input: `{"command":"` + command + `"}`,
		access: toolaccess.Access{Category: "shell"},
	}
}

func newToolBatchScenario(t *testing.T, calls []toolBatchCall) *toolBatchScenario {
	t.Helper()
	sc := newUIScenario(t, 100, 40)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	batch := &toolBatchScenario{
		sc: sc, sched: agentic.NewToolScheduler(ctx), calls: calls, release: make(chan struct{}),
	}
	t.Cleanup(batch.sched.Shutdown)
	batch.sched.OnStart = batch.onStart
	for _, call := range calls {
		sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
			ToolCallID: call.id, ToolName: call.name, ToolInput: call.input})
	}
	for _, call := range calls {
		batch.sched.Add(&agentic.ToolCallTask{
			Name: call.name, CallID: call.id, Access: call.access,
			Execute: blockedBatchExecution(batch.release, call.id),
		})
	}
	return batch
}

func blockedBatchExecution(release <-chan struct{}, output string) func(context.Context) (agentic.ToolResult, error) {
	return func(ctx context.Context) (agentic.ToolResult, error) {
		select {
		case <-release:
			return agentic.ToolResult{Output: output}, nil
		case <-ctx.Done():
			return agentic.ToolResult{}, ctx.Err()
		}
	}
}

func (b *toolBatchScenario) onStart(callID string) {
	b.sc.apply(&agentic.OutputEvent{Type: agentic.EventToolStart, State: agentic.StateToolCall,
		ToolCallID: callID, ToolName: b.callName(callID)})
}

func (b *toolBatchScenario) callName(callID string) string {
	for _, call := range b.calls {
		if call.id == callID {
			return call.name
		}
	}
	return ""
}

func (b *toolBatchScenario) capture(t *testing.T, label string) string {
	t.Helper()
	// Cross the duration visibility threshold, then refresh the same live tool
	// widgets that the production spinner refreshes.
	time.Sleep(20 * time.Millisecond)
	b.sc.engine.ApplySync(func() { b.sc.chat.InvalidateRunningToolWidgets() })
	b.sc.engine.RenderNow()
	b.sc.film.Capture(label, b.sc.engine.AgentFrame(), b.sc.status.Text())
	screen := visibleText(b.sc)
	t.Logf("visible terminal: %s:\n%s", label, screen)
	return screen
}

func (b *toolBatchScenario) widgets(t *testing.T) []*tui.ToolExecutionComponent {
	t.Helper()
	ws := waitingToolWidgets(b.sc.chat)
	if len(ws) != len(b.calls) {
		t.Fatalf("expected %d widgets, got %d", len(b.calls), len(ws))
	}
	return ws
}

func (b *toolBatchScenario) finish(t *testing.T) {
	t.Helper()
	close(b.release)
	results := b.sched.Collect()
	if len(results) != len(b.calls) {
		t.Fatalf("scheduler returned %d results, want %d", len(results), len(b.calls))
	}
	for _, result := range results {
		if result.Err != nil {
			t.Errorf("%s failed: %v", result.CallID, result.Err)
		}
		b.sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
			ToolCallID: result.CallID, ToolName: result.Name, Text: result.Output})
	}
}

func assertVisibleDurationCounts(t *testing.T, screen string, elapsed, waiting int) {
	t.Helper()
	if got := strings.Count(screen, "elapsed "); got != elapsed {
		t.Errorf("visible terminal elapsed rows = %d, want %d:\n%s", got, elapsed, screen)
	}
	if got := strings.Count(screen, "waiting "); got != waiting {
		t.Errorf("visible terminal waiting rows = %d, want %d:\n%s", got, waiting, screen)
	}
}

func assertRunningTool(t *testing.T, id string, w *tui.ToolExecutionComponent) {
	t.Helper()
	rendered := ansi.Strip(strings.Join(w.Render(100), "\n"))
	if w.Status() != tui.ToolRunning {
		t.Errorf("%s status = %v, want Running:\n%s", id, w.Status(), rendered)
	}
	if !strings.Contains(rendered, "elapsed ") || strings.Contains(rendered, "waiting ") {
		t.Errorf("%s must show elapsed and no waiting time:\n%s", id, rendered)
	}
}

func assertWaitingTool(t *testing.T, id string, w *tui.ToolExecutionComponent) {
	t.Helper()
	rendered := ansi.Strip(strings.Join(w.Render(100), "\n"))
	if w.Status() != tui.ToolPending {
		t.Errorf("%s status = %v, want Pending:\n%s", id, w.Status(), rendered)
	}
	if !w.ArgsComplete() {
		t.Errorf("%s arguments must be complete", id)
	}
	for _, want := range []string{"⧖", "waiting ", "…"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("%s waiting render missing %q:\n%s", id, want, rendered)
		}
	}
	if strings.Contains(rendered, "elapsed ") {
		t.Errorf("%s waiting render contains elapsed time:\n%s", id, rendered)
	}
}

// TestUI_QueuedToolsShowWaiting drives four conflicting calls through the
// production scheduler: only the first may run, and every later call must keep
// rendering its waiting state rather than switching to elapsed by queue position.
func TestUI_QueuedToolsShowWaiting(t *testing.T) {
	batch := newToolBatchScenario(t, []toolBatchCall{
		shellBatchCall("c1", "go test ./..."),
		shellBatchCall("c2", "ls -la"),
		shellBatchCall("c3", "go vet"),
		shellBatchCall("c4", "gofmt -l ."),
	})
	screen := batch.capture(t, "four calls: one running, three queued")
	assertVisibleDurationCounts(t, screen, 1, 3)
	ws := batch.widgets(t)
	assertRunningTool(t, "c1", ws[0])
	for i, w := range ws[1:] {
		assertWaitingTool(t, batch.calls[i+1].id, w)
	}
	batch.finish(t)
}

// TestUI_MixedToolBatchShowsActualExecutionStates reproduces the exported
// bash/bash/search/search batch: the shell calls conflict, while both searches
// run independently. Elapsed/waiting/elapsed/elapsed is therefore the accurate
// display, not a queue-position rendering bug.
func TestUI_MixedToolBatchShowsActualExecutionStates(t *testing.T) {
	batch := newToolBatchScenario(t, []toolBatchCall{
		shellBatchCall("bash1", "go vet ./..."),
		shellBatchCall("bash2", "go test ./..."),
		{id: "search1", name: "search", input: `{"pattern":"first"}`},
		{id: "search2", name: "search", input: `{"pattern":"second"}`},
	})
	screen := batch.capture(t, "exported mixed batch: bash queued, searches running")
	assertVisibleDurationCounts(t, screen, 3, 1)
	ws := batch.widgets(t)
	assertRunningTool(t, "bash1", ws[0])
	assertWaitingTool(t, "bash2", ws[1])
	assertRunningTool(t, "search1", ws[2])
	assertRunningTool(t, "search2", ws[3])
	batch.finish(t)
}
