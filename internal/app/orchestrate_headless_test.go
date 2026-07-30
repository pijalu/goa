// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/orchestrator"
)

// TestHeadless_OrchestrateFlagParses confirms --orchestrate is wired through
// flag parsing into RuntimeOptions and forces headless mode.
func TestHeadless_OrchestrateFlagParses(t *testing.T) {
	opts := RuntimeOptions{Orchestrate: "run-xyz"}
	if !opts.Headless() {
		t.Error("--orchestrate should imply headless")
	}
	if err := opts.validateModes(); err != nil {
		t.Errorf("--orchestrate validate failed: %v", err)
	}
}

// TestHeadless_OrchestrateRejectsFinishedRun proves the resume path reads the
// event log and refuses an already-finished run (returns before rendering).
func TestHeadless_OrchestrateRejectsFinishedRun(t *testing.T) {
	dir := t.TempDir()
	rootDir := filepath.Join(dir, ".goa", "orchestrator")
	store := orchestrator.NewFileEventStore(rootDir, "run-fin")
	_ = store.Append(orchestrator.Event{Type: orchestrator.EventRunStarted,
		Payload: map[string]any{"objective": "done already", "topology": "fanout"}})
	_ = store.Append(orchestrator.Event{Type: orchestrator.EventRunFinished})

	subs := &subsystems{
		cfg:         &config.Config{},
		projectDir:  dir,
		orchAdapter: NewOrchestratorAdapter(nil, &config.Config{}, ""),
		orchActive:  orchestrator.NewActiveRuntime(),
	}
	h := &HeadlessApp{subs: subs, opts: RuntimeOptions{Orchestrate: "run-fin"}}
	err := mustStartErr(h, "run-fin")
	if err == nil {
		t.Fatalf("expected error resuming a finished run")
	}
	if !strings.Contains(err.Error(), "finished") {
		t.Errorf("expected 'finished' in error, got %v", err)
	}
}

// mustStartErr invokes startOrchestrate with the app's adapter as builder and
// returns only the error (tests that never reach a successful launch).
func mustStartErr(h *HeadlessApp, runID string) error {
	_, err := h.startOrchestrate(context.Background(), runID, h.subs.orchAdapter)
	return err
}

// TestHeadless_OrchestrateRejectsUnknownRun proves a missing run-id errors.
func TestHeadless_OrchestrateRejectsUnknownRun(t *testing.T) {
	subs := &subsystems{
		cfg:         &config.Config{},
		projectDir:  t.TempDir(),
		orchAdapter: NewOrchestratorAdapter(nil, &config.Config{}, ""),
		orchActive:  orchestrator.NewActiveRuntime(),
	}
	h := &HeadlessApp{subs: subs, opts: RuntimeOptions{Orchestrate: "ghost"}}
	if err := mustStartErr(h, "ghost"); err == nil {
		t.Fatalf("expected error resuming an unknown run")
	}
}

// TestHeadless_OrchestrateRejectsNoAdapter proves a missing adapter errors
// cleanly instead of panicking (the guard lives in startWork).
func TestHeadless_OrchestrateRejectsNoAdapter(t *testing.T) {
	subs := &subsystems{cfg: &config.Config{}, projectDir: t.TempDir()}
	h := &HeadlessApp{subs: subs, opts: RuntimeOptions{Orchestrate: "x"}}
	dc := &doneCloser{done: make(chan struct{})}
	if err := h.startWork(context.Background(), dc, ""); err == nil {
		t.Fatalf("expected error with no adapter")
	}
}

// --- regression tests: headless orchestrate race + resume fork + error exit --

// noopHeadlessRenderer satisfies HeadlessRenderer with no-ops.
type noopHeadlessRenderer struct{}

func (noopHeadlessRenderer) UserPrompt(string)                       {}
func (noopHeadlessRenderer) AssistantChunk(string)                   {}
func (noopHeadlessRenderer) ThinkingStart()                          {}
func (noopHeadlessRenderer) ThinkingChunk(string)                    {}
func (noopHeadlessRenderer) ThinkingEnd()                            {}
func (noopHeadlessRenderer) ToolCall(string, string, string)         {}
func (noopHeadlessRenderer) ToolResult(string, string, string)       {}
func (noopHeadlessRenderer) Stats(sessionStats, int)                 {}
func (noopHeadlessRenderer) Summary(sessionStats, int, time.Duration) {}
func (noopHeadlessRenderer) Error(string)                            {}
func (noopHeadlessRenderer) AssistantStreamEnd()                     {}
func (noopHeadlessRenderer) CompanionStart(int)                      {}
func (noopHeadlessRenderer) CompanionEnd(int)                        {}
func (noopHeadlessRenderer) CompanionThinkingStart()                 {}
func (noopHeadlessRenderer) CompanionThinkingChunk(string)           {}
func (noopHeadlessRenderer) CompanionThinkingEnd()                   {}
func (noopHeadlessRenderer) CompanionChunk(string)                   {}
func (noopHeadlessRenderer) Flush()                                  {}

// fakeRuntimeBuilder returns a prebuilt runtime, letting tests drive
// startOrchestrate without a live provider pool.
type fakeRuntimeBuilder struct {
	rt  *orchestrator.Runtime
	err error
}

func (f fakeRuntimeBuilder) NewRuntime(config.OrchestratorConfig, string) (*orchestrator.Runtime, error) {
	return f.rt, f.err
}

// nopAgentFactory returns handles whose Run is a no-op success.
func nopAgentFactory(role, model string, _ orchestrator.AcquireOptions) (*orchestrator.AgentHandle, error) {
	return orchestrator.NewAgentHandle("", role, model), nil
}

func bareFanoutRuntime(t *testing.T, factory orchestrator.AgentFactory) *orchestrator.Runtime {
	t.Helper()
	oCfg := config.OrchestratorConfig{
		Roles:    map[string]config.OrchestratorRole{"coder": {Model: "m"}},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "fanout"},
	}
	rt, err := orchestrator.NewRuntime(oCfg, orchestrator.NewBoundedAgentPool(oCfg, factory), nil, "")
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt
}

// TestHeadless_OrchestrateContinuesSameRun is the regression test for the
// headless resume fork: before the fix, --orchestrate built a runtime with a
// NEW run id and never called Runtime.Resume, so events landed in a second
// run dir and the original run stayed unfinished forever. After the fix the
// resumed run must continue the SAME event log (same run-id, continuing seq,
// run_finished appended) and no second run dir may appear.
func TestHeadless_OrchestrateContinuesSameRun(t *testing.T) {
	dir := t.TempDir()
	rootDir := filepath.Join(dir, ".goa", "orchestrator")
	seed := orchestrator.NewFileEventStore(rootDir, "run-cont")
	if err := seed.Append(orchestrator.Event{Type: orchestrator.EventRunStarted, RunID: "run-cont",
		Payload: map[string]any{"objective": "continue me", "topology": "fanout"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seed.Close()

	rt := bareFanoutRuntime(t, nopAgentFactory)
	subs := &subsystems{
		cfg:        &config.Config{},
		projectDir: dir,
		orchActive: orchestrator.NewActiveRuntime(),
	}
	h := &HeadlessApp{subs: subs, opts: RuntimeOptions{Orchestrate: "run-cont"}, renderer: noopHeadlessRenderer{}}

	got, err := h.startOrchestrate(context.Background(), "run-cont", fakeRuntimeBuilder{rt: rt})
	if err != nil {
		t.Fatalf("startOrchestrate: %v", err)
	}
	if got == nil {
		t.Fatal("expected a runtime")
	}
	if subs.orchActive.Get() != rt {
		t.Error("orchActive must hold the runtime before startOrchestrate returns")
	}

	select {
	case <-rt.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("run did not finish")
	}

	assertSingleRunDir(t, rootDir, "run-cont")
	assertRunLogContinues(t, rootDir, "run-cont", "continue me")
}

// assertSingleRunDir proves the resume did not fork: exactly one run dir
// named runID exists under rootDir.
func assertSingleRunDir(t *testing.T, rootDir, runID string) {
	t.Helper()
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 1 && entries[0].Name() == runID {
		return
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	t.Fatalf("resume forked the run: dirs = %v, want [%s]", names, runID)
}

// assertRunLogContinues proves the resumed run appended to the SAME log:
// every event carries runID, seq is strictly increasing across the seed
// boundary, run_finished is present, and the runtime's own run_started
// repeats the seeded objective.
func assertRunLogContinues(t *testing.T, rootDir, runID, objective string) {
	t.Helper()
	events, err := orchestrator.NewFileEventStore(rootDir, runID).Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) < 4 {
		t.Fatalf("resumed run appended too few events: %d", len(events))
	}
	var lastSeq int64
	sawRunFinished := false
	for i, ev := range events {
		if ev.RunID != runID {
			t.Errorf("event %d run_id = %q, want %s (fork?)", i, ev.RunID, runID)
		}
		if ev.Seq <= lastSeq {
			t.Errorf("event %d seq %d not increasing after %d", i, ev.Seq, lastSeq)
		}
		lastSeq = ev.Seq
		sawRunFinished = sawRunFinished || ev.Type == orchestrator.EventRunFinished
	}
	if !sawRunFinished {
		t.Error("run_finished missing from the continued log")
	}
	if obj := events[1].Payload["objective"]; obj != objective {
		t.Errorf("second run_started objective = %v, want %q", obj, objective)
	}
}

// TestHeadless_WaitForOrchWaitsForRuntime is the regression test for the
// start-up race: the watcher must wait for the runtime's Done channel and
// never close dc while the run is still in flight (before the fix it read a
// not-yet-installed orchActive and closed dc immediately, exiting the
// process mid-run).
func TestHeadless_WaitForOrchWaitsForRuntime(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	factory := func(role, model string, _ orchestrator.AcquireOptions) (*orchestrator.AgentHandle, error) {
		h := orchestrator.NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, _ string) error {
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return h, nil
	}
	rt := bareFanoutRuntime(t, factory)
	h := &HeadlessApp{renderer: noopHeadlessRenderer{}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan struct{})
	go func() { _ = rt.Run(ctx, "obj"); close(runDone) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("agent never started")
	}

	dc := &doneCloser{done: make(chan struct{})}
	go h.waitForOrch(ctx, dc, rt)

	select {
	case <-dc.done:
		t.Fatal("dc closed while the run was still in flight (start-up race)")
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	select {
	case <-dc.done:
	case <-time.After(5 * time.Second):
		t.Fatal("dc never closed after the run finished")
	}
	<-runDone
}

// TestHeadless_TerminalOrchestrateCode pins the exit-code mapping: a recorded
// run error must surface as headlessExitOrchFailed instead of exit 0.
func TestHeadless_TerminalOrchestrateCode(t *testing.T) {
	h := &HeadlessApp{renderer: noopHeadlessRenderer{}}
	if code := h.terminalOrchestrateCode(); code != 0 {
		t.Errorf("nil orchErr: code = %d, want 0", code)
	}
	h.setOrchErr(errors.New("boom"))
	if code := h.terminalOrchestrateCode(); code != headlessExitOrchFailed {
		t.Errorf("run error: code = %d, want %d", code, headlessExitOrchFailed)
	}
}
