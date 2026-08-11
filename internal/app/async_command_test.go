// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

// testAsyncCommand implements core.Command + core.AsyncCommand. It blocks in
// Run until the test closes release, so the mid-execution state (spinner,
// commandBusy, steering) can be observed. started is closed when Run begins.
type testAsyncCommand struct {
	status      *tui.StatusMsg
	started     chan struct{}
	release     chan struct{}
	spinnerText string // captured inside Run (background goroutine)
}

func (c *testAsyncCommand) Name() string      { return "asynccmd" }
func (c *testAsyncCommand) Aliases() []string { return nil }
func (c *testAsyncCommand) ShortHelp() string { return "async test command" }
func (c *testAsyncCommand) LongHelp() string  { return "async test command" }
func (c *testAsyncCommand) AsyncHint(args []string) string {
	return "Working…"
}
func (c *testAsyncCommand) Run(ctx core.Context, args []string) error {
	// Observe the spinner state from inside the background goroutine.
	if c.status != nil {
		c.spinnerText = c.status.Text()
	}
	close(c.started)
	<-c.release
	ctx.Writef("async result")
	return nil
}

// testConditionalAsyncCommand opts into async only for the "slow" sub-command.
type testConditionalAsyncCommand struct {
	started chan struct{}
	release chan struct{}
}

func (c *testConditionalAsyncCommand) Name() string      { return "condasync" }
func (c *testConditionalAsyncCommand) Aliases() []string { return nil }
func (c *testConditionalAsyncCommand) ShortHelp() string { return "conditional async" }
func (c *testConditionalAsyncCommand) LongHelp() string  { return "conditional async" }
func (c *testConditionalAsyncCommand) AsyncHint(args []string) string {
	if len(args) > 0 && args[0] == "slow" {
		return "Slow op…"
	}
	return ""
}
func (c *testConditionalAsyncCommand) Run(ctx core.Context, args []string) error {
	close(c.started)
	<-c.release
	ctx.Writef("cond result")
	return nil
}

// waitFor repeatedly checks cond on the commandLoop until it returns true or
// the timeout expires. Each check is marshalled via ApplySync so it is
// race-free with commandLoop-owned state.
func waitFor(t *testing.T, engine *tui.TUI, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var ok bool
		engine.ApplySync(func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s (condition not met within %v)", msg, timeout)
}

func newAsyncTestApp(t *testing.T, cmds ...core.Command) (*App, *tui.TUI, *tui.StatusMsg, *tui.ChatViewport) {
	t.Helper()
	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	engine.RunLoops()

	status := tui.NewStatusMsg()
	status.SetTUI(engine)
	chat := tui.NewChatViewport()

	registry := core.NewCommandRegistry()
	for _, c := range cmds {
		if err := registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.statusMsg = status
	subs.cmdRouter = core.NewCommandRouter(registry, core.NewDocEngine(registry))

	a := &App{subs: subs}
	return a, engine, status, chat
}

// TestAsyncCommand_RunsInBackgroundWithSpinner verifies that an async command
// launches in a background goroutine, shows a dedicated spinner while running,
// and renders its output (and clears the spinner) on completion.
func TestAsyncCommand_RunsInBackgroundWithSpinner(t *testing.T) {
	cmd := &testAsyncCommand{
		status:  nil, // set below after status is created
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	a, engine, status, chat := newAsyncTestApp(t, cmd)
	defer engine.Stop()
	cmd.status = status

	// Launch on the commandLoop.
	engine.ApplySync(func() { a.handleSlashCommand("/asynccmd") })

	// The command goroutine starts and blocks.
	<-cmd.started

	// Mid-execution: commandBusy true, spinner visible with the hint label.
	waitFor(t, engine, time.Second, func() bool {
		return a.commandBusy && status.IsVisible()
	}, "expected commandBusy + spinner while async command runs")
	var labelText string
	engine.ApplySync(func() { labelText = status.Text() })
	if labelText != "Working…" {
		t.Fatalf("spinner text = %q, want %q", labelText, "Working…")
	}

	// Release the command so it completes.
	close(cmd.release)

	// Post-completion: commandBusy false, spinner cleared.
	waitFor(t, engine, time.Second, func() bool {
		return !a.commandBusy && !status.IsVisible()
	}, "expected commandBusy cleared + spinner hidden after completion")

	// Output rendered in chat.
	if !containsRendered(chat, "async result") {
		t.Errorf("expected chat to contain command output")
	}
}

// TestAsyncCommand_SteeringEnqueuedDuringRun verifies that free text submitted
// while an async command runs is enqueued in the steering queue and delivered
// (flushed) when the command completes.
func TestAsyncCommand_SteeringEnqueuedDuringRun(t *testing.T) {
	cmd := &testAsyncCommand{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(16, 16, 16, 16), "")

	a, engine, status, _ := newAsyncTestApp(t, cmd)
	defer engine.Stop()
	a.subs.agentMgr = am
	cmd.status = status

	// Launch on the commandLoop.
	engine.ApplySync(func() { a.handleSlashCommand("/asynccmd") })
	<-cmd.started

	// While the command runs, submit free text — it must be enqueued as steering.
	engine.ApplySync(func() {
		consumed := a.routeSteering(engine, a.subs.chat, "do something after")
		if !consumed {
			t.Error("expected routeSteering to consume free text as steering during async command")
		}
	})

	sq := am.SteeringQueue()
	if sq.Len() != 1 {
		t.Fatalf("steering queue len = %d, want 1", sq.Len())
	}

	// Complete the command.
	close(cmd.release)
	waitFor(t, engine, time.Second, func() bool {
		return !a.commandBusy
	}, "expected command to complete")

	// The steering queue must be flushed after completion.
	if sq.Len() != 0 {
		t.Errorf("steering queue len = %d after completion, want 0 (flushed)", sq.Len())
	}
}

// TestAsyncCommand_RejectsConcurrent verifies that a second async command
// submitted while one is running is rejected rather than racing on shared state.
func TestAsyncCommand_RejectsConcurrent(t *testing.T) {
	cmd1 := &testAsyncCommand{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	cmd2 := &testAsyncCommand{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	// Both commands have the same Name, so register only one and reuse it.
	// Instead, register two distinct names via a small wrapper.
	a, engine, status, chat := newAsyncTestApp(t, cmd1)
	defer engine.Stop()
	cmd1.status = status

	// Also register cmd2 under a different name.
	registry := a.subs.cmdRouter.Registry()
	// Wrap cmd2 under a different name.
	wrapped := &renamedAsyncCommand{inner: cmd2, name: "asynccmd2"}
	if err := registry.Register(wrapped); err != nil {
		t.Fatal(err)
	}

	// Launch cmd1.
	engine.ApplySync(func() { a.handleSlashCommand("/asynccmd") })
	<-cmd1.started

	// Try to launch cmd2 while cmd1 runs.
	engine.ApplySync(func() { a.handleSlashCommand("/asynccmd2") })

	// cmd2 must NOT have started.
	select {
	case <-cmd2.started:
		t.Fatal("second async command should not start while another is running")
	case <-time.After(100 * time.Millisecond):
		// Good — cmd2 was rejected.
	}

	// An error message must appear in the chat.
	if !containsRendered(chat, "already running") {
		t.Errorf("expected 'already running' message in chat")
	}

	// Clean up.
	close(cmd1.release)
	waitFor(t, engine, time.Second, func() bool { return !a.commandBusy }, "expected cmd1 to complete")
}

// renamedAsyncCommand wraps an AsyncCommand under a different name for tests.
type renamedAsyncCommand struct {
	inner *testAsyncCommand
	name  string
}

func (c *renamedAsyncCommand) Name() string                   { return c.name }
func (c *renamedAsyncCommand) Aliases() []string              { return nil }
func (c *renamedAsyncCommand) ShortHelp() string              { return "renamed" }
func (c *renamedAsyncCommand) LongHelp() string               { return "renamed" }
func (c *renamedAsyncCommand) AsyncHint(args []string) string { return c.inner.AsyncHint(args) }
func (c *renamedAsyncCommand) Run(ctx core.Context, args []string) error {
	return c.inner.Run(ctx, args)
}

// TestAsyncCommand_DocSuffixBypassesAsync verifies that /cmd:? and /cmd:??
// (doc lookups) never trigger the async path, even for async commands.
func TestAsyncCommand_DocSuffixBypassesAsync(t *testing.T) {
	cmd := &testAsyncCommand{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	a, engine, _, _ := newAsyncTestApp(t, cmd)
	defer engine.Stop()

	// /asynccmd:? should be a synchronous doc lookup.
	engine.ApplySync(func() { a.handleSlashCommand("/asynccmd:?") })

	// The command must NOT have started (no background goroutine).
	select {
	case <-cmd.started:
		t.Fatal("doc-suffix lookup should not start the async command")
	case <-time.After(100 * time.Millisecond):
	}

	// commandBusy must be false (sync path).
	var busy bool
	engine.ApplySync(func() { busy = a.commandBusy })
	if busy {
		t.Error("commandBusy should be false after a doc-suffix lookup")
	}
}

// TestConditionalAsync_FastVariantRunsSync verifies that a command implementing
// AsyncHint conditionally runs synchronously when the hint is empty. The fast
// variant completes immediately (no blocking) so the commandLoop is never
// stalled. We verify commandBusy was never set.
func TestConditionalAsync_FastVariantRunsSync(t *testing.T) {
	fastCmd := &testConditionalAsyncCommand{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	// For the sync ("fast") variant, release immediately so Run does not block.
	close(fastCmd.release)

	a, engine, _, _ := newAsyncTestApp(t, fastCmd)
	defer engine.Stop()

	// /condasync:fast → AsyncHint returns "" → sync path.
	engine.ApplySync(func() { a.handleSlashCommand("/condasync:fast") })

	// The command ran (started was closed).
	select {
	case <-fastCmd.started:
	case <-time.After(time.Second):
		t.Fatal("command did not run within timeout")
	}

	// commandBusy was never set.
	var busy bool
	engine.ApplySync(func() { busy = a.commandBusy })
	if busy {
		t.Error("commandBusy must remain false for a sync (empty-hint) invocation")
	}
}

// TestConditionalAsync_SlowVariantRunsAsync verifies the same command opts
// into async for the "slow" sub-command.
func TestConditionalAsync_SlowVariantRunsAsync(t *testing.T) {
	slowCmd := &testConditionalAsyncCommand{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	a, engine, status, _ := newAsyncTestApp(t, slowCmd)
	defer engine.Stop()

	// /condasync:slow → AsyncHint returns "Slow op…" → async path.
	engine.ApplySync(func() { a.handleSlashCommand("/condasync:slow") })
	<-slowCmd.started

	// Mid-execution: spinner visible with the conditional label.
	waitFor(t, engine, time.Second, func() bool {
		return a.commandBusy && status.IsVisible()
	}, "expected async dispatch for slow variant")

	var labelText string
	engine.ApplySync(func() { labelText = status.Text() })
	if labelText != "Slow op…" {
		t.Fatalf("spinner text = %q, want %q", labelText, "Slow op…")
	}

	close(slowCmd.release)
	waitFor(t, engine, time.Second, func() bool { return !a.commandBusy }, "expected slow variant to complete")
}

// TestSyncCommand_NotAffectedByAsyncPath verifies that a regular command
// (not implementing AsyncCommand) still runs synchronously and never sets
// commandBusy.
func TestSyncCommand_NotAffectedByAsyncPath(t *testing.T) {
	registry := core.NewCommandRegistry()
	if err := registry.Register(&testPlaceholderCommand{status: tui.NewStatusMsg()}); err != nil {
		t.Fatal(err)
	}

	status := tui.NewStatusMsg()
	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	defer engine.Stop()
	engine.RunLoops()
	status.SetTUI(engine)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = tui.NewChatViewport()
	subs.statusMsg = status
	subs.cmdRouter = core.NewCommandRouter(registry, core.NewDocEngine(registry))

	a := &App{subs: subs}

	engine.ApplySync(func() { a.handleSlashCommand("/slowcmd") })

	// Sync command: commandBusy never set.
	var busy bool
	engine.ApplySync(func() { busy = a.commandBusy })
	if busy {
		t.Error("commandBusy must remain false for synchronous commands")
	}
}

// TestAsyncCommand_SteeringRestoredOnEscape verifies that pressing ESC during
// an async command restores enqueued steering to the input line (the command
// itself continues and completes normally).
func TestAsyncCommand_SteeringRestoredOnEscape(t *testing.T) {
	cmd := &testAsyncCommand{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(16, 16, 16, 16), "")

	inp := tui.NewEditor()
	a, engine, status, _ := newAsyncTestApp(t, cmd)
	defer engine.Stop()
	a.subs.agentMgr = am
	a.subs.inputEditor = inp
	cmd.status = status

	engine.AddChild(inp)
	engine.SetFocus(inp)

	// Launch.
	engine.ApplySync(func() { a.handleSlashCommand("/asynccmd") })
	<-cmd.started

	// Enqueue steering.
	engine.ApplySync(func() { a.routeSteering(engine, a.subs.chat, "queued text") })
	sq := am.SteeringQueue()
	if sq.Len() != 1 {
		t.Fatalf("steering queue len = %d, want 1", sq.Len())
	}

	// ESC restores steering to input.
	engine.ApplySync(func() { a.handleEscape() })

	// Queue drained.
	if sq.Len() != 0 {
		t.Errorf("steering queue len = %d after ESC, want 0", sq.Len())
	}

	// Input line has the restored text.
	var restored string
	engine.ApplySync(func() { restored = inp.Text() })
	if !strings.Contains(restored, "queued text") {
		t.Errorf("input = %q after ESC, want it to contain restored steering text", restored)
	}

	// Command still busy (ESC doesn't cancel async commands).
	var busy bool
	engine.ApplySync(func() { busy = a.commandBusy })
	if !busy {
		t.Error("commandBusy should still be true after ESC — ESC restores steering but does not cancel the command")
	}

	// Complete the command.
	close(cmd.release)
	waitFor(t, engine, time.Second, func() bool { return !a.commandBusy }, "expected command to complete after release")
}
