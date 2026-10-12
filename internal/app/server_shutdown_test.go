// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui"
)

// These tests cover bugs.md B1: `goa server` could not be stopped from its own
// console. The session has no TTY (the process terminal is replaced by a
// VirtualTerminal), so Ctrl+C arrives as SIGINT, and NOTHING observed it: the
// signal handler consumed the signal and the session blocked forever. The fix
// ties the server's context into the session's own stop path — the very event
// `/quit` sends — so the session ends, the listener closes after it, and the
// deferred profiling flush writes its files.

// recordingTerminal is the injected terminal for the server-shaped session. It
// counts Stop calls, which is how these tests prove the shutdown went through
// TUI.Stop (the documented restore the terminal gets on /quit) instead of the
// process simply dying with the terminal in whatever state it was left in.
type recordingTerminal struct {
	testTerminal
	stops atomic.Int32
}

func (r *recordingTerminal) Stop() { r.stops.Add(1) }

// serverSessionSubsystems is the minimal subsystem set a headless interactive
// session needs: no TTY, no provider, no plugins — the same shape runWebServer
// serves. perfLoad replaces the agent turn so the test never needs a model.
func serverSessionSubsystems(t *testing.T, term tui.Terminal) *subsystems {
	t.Helper()
	return &subsystems{
		cfg:           &config.Config{},
		projectDir:    t.TempDir(),
		events:        event.MakeBus(64, 16, 16, 16),
		terminal:      term,
		noPlugins:     true,
		perfLoad:      true,
		toolRegistry:  tools.NewToolRegistry(),
		registry:      core.NewCommandRegistry(),
		skillRegistry: skills.NewSkillRegistry(nil),
	}
}

// TestRunContext_ContextCancelEndsSessionAndRestoresTerminal is the app-level
// proof that the session Run returns when its context is cancelled: it runs the
// REAL App.RunContext (the function runWebServer calls) with a real TUI engine,
// the real event readers and the real stop path.
func TestRunContext_ContextCancelEndsSessionAndRestoresTerminal(t *testing.T) {
	term := &recordingTerminal{testTerminal: testTerminal{w: 100, h: 30}}
	app := New(serverSessionSubsystems(t, term))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	returned := make(chan bool, 1)
	go func() { returned <- app.RunContext(ctx) }()

	// The session must be RUNNING before the cancel: otherwise a return below
	// could just be a startup failure that had nothing to do with the context.
	select {
	case <-returned:
		t.Fatal("session returned before its context was cancelled")
	case <-time.After(500 * time.Millisecond):
	}

	cancel()

	select {
	case relaunch := <-returned:
		if relaunch {
			t.Error("cancelled session reported a relaunch")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("session Run did not return within 10s of its context being cancelled")
	}

	if got := term.stops.Load(); got != 1 {
		t.Errorf("terminal restored %d times, want exactly 1 (TUI.Stop ordering)", got)
	}
}

// TestRunContext_AlreadyCancelledContextStopsSession covers Ctrl+C during
// startup: the signal can land before the session has installed its watcher, so
// an already-cancelled context must stop the session too, not be missed.
func TestRunContext_AlreadyCancelledContextStopsSession(t *testing.T) {
	term := &recordingTerminal{testTerminal: testTerminal{w: 100, h: 30}}
	app := New(serverSessionSubsystems(t, term))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	returned := make(chan bool, 1)
	go func() { returned <- app.RunContext(ctx) }()

	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("session Run did not return for an already-cancelled context")
	}
	if got := term.stops.Load(); got != 1 {
		t.Errorf("terminal restored %d times, want exactly 1", got)
	}
}

// TestRequestStop_SendsTheQuitControlEvent pins the MECHANISM, not just the
// outcome: the console's stop must be the same request /quit makes (a
// StopRequest control event handled on the commandLoop), because that is what
// keeps TUI.Stop's restore ordering intact.
func TestRequestStop_SendsTheQuitControlEvent(t *testing.T) {
	subs := serverSessionSubsystems(t, &testTerminal{w: 80, h: 24})
	app := New(subs)

	got := make(chan event.ControlEvent, 1)
	go func() { got <- <-subs.events.Control }()

	app.requestStop()

	select {
	case ev := <-got:
		if !ev.StopRequest {
			t.Fatalf("control event %+v is not a stop request", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("requestStop did not send a StopRequest control event")
	}
}

// TestRequestStop_FallsBackToStoppingTheEngine covers an unreachable control
// reader (a full bus with no consumer): the stop must still happen — the defect
// was an unstoppable process, so a lost event is not an acceptable outcome.
func TestRequestStop_FallsBackToStoppingTheEngine(t *testing.T) {
	subs := serverSessionSubsystems(t, &testTerminal{w: 80, h: 24})
	// Unbuffered and unread: the send can never succeed.
	subs.events.Control = make(chan event.ControlEvent)
	engine := tui.NewTUI(subs.terminal)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	subs.tuiEngine = engine

	app := New(subs)
	done := make(chan struct{})
	go func() { app.requestStop(); close(done) }()

	select {
	case <-done:
	case <-time.After(stopRequestBudget + 5*time.Second):
		t.Fatal("requestStop neither delivered the event nor stopped the engine")
	}
	select {
	case <-engine.Stopped():
	case <-time.After(time.Second):
		t.Fatal("engine was not stopped by the fallback")
	}
}

// fakeWebSession stands in for *App so the serveWebUI wiring — the listener
// stays up for the whole session, and is closed only after the session ends —
// is asserted directly. It checks the listener itself, from inside the session.
type fakeWebSession struct {
	addr string

	started    chan struct{}
	returned   chan struct{}
	listenerUp bool
	ctxWasDone bool
	reentry    int32
}

func (f *fakeWebSession) RunContext(ctx context.Context) bool {
	if !atomic.CompareAndSwapInt32(&f.reentry, 0, 1) {
		return false // RunContext must be called once
	}
	// While the session runs the listener must still accept: a browser may be
	// driving this session, and closing the listener underneath it was the
	// half-fixed behaviour the report described (listener dead, process alive).
	f.listenerUp = dialOK(f.addr)
	close(f.started)
	<-ctx.Done()
	f.ctxWasDone = true
	// Ordering proof: the session is over, but serveWebUI must not have closed
	// the listener yet.
	f.listenerUp = f.listenerUp && dialOK(f.addr)
	close(f.returned)
	return false
}

func dialOK(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// freeLoopbackAddr reserves a loopback port and releases it. The window is
// closed by the test holding the address until serveWebUI binds it.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release loopback port: %v", err)
	}
	return addr
}

// awaitSessionStart blocks until the session reports it is running.
func awaitSessionStart(t *testing.T, session *fakeWebSession) {
	t.Helper()
	select {
	case <-session.started:
	case <-time.After(10 * time.Second):
		t.Fatal("session never started")
	}
}

// awaitSessionEnd blocks until the session reports it has returned.
func awaitSessionEnd(t *testing.T, session *fakeWebSession) {
	t.Helper()
	select {
	case <-session.returned:
	case <-time.After(10 * time.Second):
		t.Fatal("session did not return after the context was cancelled")
	}
}

// waitForDialable waits until the listener answers, failing if it never does.
func waitForDialable(t *testing.T, addr string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !dialOK(addr) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
}

// TestServeWebUI_ContextCancelEndsSessionThenClosesListener is the wiring test:
// a cancelled context ends the session, the listener survives the session and
// is closed only afterwards, and serveWebUI returns cleanly (which is what lets
// runApp's deferred profiler flush run and the process exit 0).
func TestServeWebUI_ContextCancelEndsSessionThenClosesListener(t *testing.T) {
	addr := freeLoopbackAddr(t)
	subs := &subsystems{cfg: &config.Config{}}
	opts := RuntimeOptions{Server: true, ServerAddr: addr, InsecureNoAuth: true}
	session := &fakeWebSession{addr: addr, started: make(chan struct{}), returned: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- serveWebUI(subs, opts, session, ctx) }()

	awaitSessionStart(t, session)
	// The listener answered requests while the session was live.
	waitForDialable(t, addr, 5*time.Second)

	cancel()
	awaitSessionEnd(t, session)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveWebUI returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveWebUI did not return after the session ended")
	}

	if !session.ctxWasDone {
		t.Error("session returned without observing the cancelled context")
	}
	if !session.listenerUp {
		t.Error("listener was not accepting while the session ran (or was closed before it ended)")
	}
	if dialOK(addr) {
		t.Error("listener still accepts connections after the session ended")
	}
}

// TestShutdownSignals_FirstSignalCancelsContext asserts the console signal
// reaches the session as a cancelled context. The second signal escalates to a
// hard exit, which cannot be asserted in-process without killing the test run —
// covered instead by the PTY e2e (cmd/goa/e2e_server_signal_test.go).
func TestShutdownSignals_FirstSignalCancelsContext(t *testing.T) {
	ctx, release := shutdownSignals()
	defer release()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("first SIGTERM did not cancel the shutdown context")
	}
}

func TestSignalExitCode(t *testing.T) {
	if got := signalExitCode(syscall.SIGINT); got != 130 {
		t.Errorf("signalExitCode(SIGINT) = %d, want 130", got)
	}
	if got := signalExitCode(syscall.SIGTERM); got != 143 {
		t.Errorf("signalExitCode(SIGTERM) = %d, want 143", got)
	}
	if got := signalExitCode(nil); got != 1 {
		t.Errorf("signalExitCode(nil) = %d, want 1", got)
	}
}

// TestStartProfiling_StopWritesBothFiles covers the flush the server's shutdown
// depends on: runApp defers prof.stopProfiling(), so writing the files is what
// makes --cpuprofile/--memprofile survive an orderly Ctrl+C.
func TestStartProfiling_StopWritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	cpu := filepath.Join(dir, "cpu.prof")
	mem := filepath.Join(dir, "mem.prof")

	prof, err := startProfiling(RuntimeOptions{CPUProfile: cpu, MemProfile: mem})
	if err != nil {
		t.Fatalf("startProfiling: %v", err)
	}
	burnCPU(150 * time.Millisecond)
	prof.stopProfiling()

	for _, path := range []string{cpu, mem} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("profile %s not written: %v", filepath.Base(path), err)
		}
		if info.Size() == 0 {
			t.Errorf("profile %s is empty", filepath.Base(path))
		}
	}
}

// burnCPU spends d doing real work so the CPU profiler has samples to write.
func burnCPU(d time.Duration) {
	deadline := time.Now().Add(d)
	var n int
	for time.Now().Before(deadline) {
		for i := 0; i < 100000; i++ {
			n += i % 7
		}
	}
	_ = n
}
