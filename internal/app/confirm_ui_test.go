// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/plugins"
	"github.com/pijalu/goa/tui"
)

// confirmTestEnv wires a real TUI engine (loops running) with a fresh plugin
// runtime whose UIBridge is attached through the production entry point
// (startConfirmDrain), so tests exercise the actual FIFO presenter.
type confirmTestEnv struct {
	t      *testing.T
	engine *tui.TUI
	app    *App
	rt     *pluginRuntime
}

func newConfirmTestEnv(t *testing.T) *confirmTestEnv {
	t.Helper()
	term := &testTerminal{w: 100, h: 30}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	engine.RunLoops()
	chat := tui.NewChatViewport()
	inp := tui.NewEditor()
	for _, c := range []tui.Component{chat, inp} {
		engine.AddChild(c)
	}
	inp.SetTUI(engine)
	engine.SetFocus(inp)

	rt := &pluginRuntime{ui: plugins.NewUIBridge()}
	env := &confirmTestEnv{t: t, engine: engine, app: &App{}, rt: rt}
	env.app.startConfirmDrain(engine, rt)
	return env
}

// waitForVisible polls AgentFrame text until substr shows up.
func (e *confirmTestEnv) waitForVisible(substr string) {
	e.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(e.engine.VisibleText(), substr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("card %q never became visible\n%s", substr, e.engine.VisibleText())
}

// waitForGone polls until substr disappears from the frame.
func (e *confirmTestEnv) waitForGone(substr string) {
	e.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !strings.Contains(e.engine.VisibleText(), substr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("card %q never disappeared\n%s", substr, e.engine.VisibleText())
}

func confirmReq(title string) plugins.ConfirmRequest {
	return plugins.ConfirmRequest{
		PluginID: "test-plugin",
		Title:    title,
		Options: []plugins.ConfirmOption{
			{ID: "yes", Label: "Yes", Style: plugins.ConfirmStyleDanger},
			{ID: "no", Label: "No"},
		},
		DefaultID:   "no",
		AllowCancel: true,
	}
}

// TestPluginConfirm_PresentedResolvedFIFO drives the full app glue: a request
// becomes a visible card, Enter resolves it with the highlighted ID, and a
// second queued request presents only after the first is answered.
func TestPluginConfirm_PresentedResolvedFIFO(t *testing.T) {
	env := newConfirmTestEnv(t)
	defer env.engine.Stop()

	resp1 := env.rt.ui.RequestConfirm(confirmReq("First?"))
	resp2 := env.rt.ui.RequestConfirm(confirmReq("Second?"))

	env.waitForVisible("First?")
	if vis := env.engine.VisibleText(); strings.Contains(vis, "Second?") {
		t.Fatalf("second card presented while first pending (FIFO violated):\n%s", vis)
	}

	env.engine.SendKey(tui.KeyEnter)
	select {
	case r := <-resp1:
		if r.ID != "no" || r.Cancelled {
			t.Fatalf("first response = %+v, want {no false}", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first confirm unresolved")
	}

	env.waitForVisible("Second?")
	env.engine.SendKey(tui.KeyEscape)
	select {
	case r := <-resp2:
		if !r.Cancelled || r.Err != "" {
			t.Fatalf("second response = %+v, want plain cancel", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second confirm unresolved")
	}
}

// TestPluginConfirm_TimeoutHidesGhostCard pins the out-of-band finish path:
// the bridge cap resolves an unanswered card AND the presenter removes the
// now-inert modal; afterwards the drain keeps serving requests.
func TestPluginConfirm_TimeoutHidesGhostCard(t *testing.T) {
	env := newConfirmTestEnv(t)
	defer env.engine.Stop()

	req := confirmReq("Expiring?")
	req.Timeout = 60 * time.Millisecond
	resp := env.rt.ui.RequestConfirm(req)

	env.waitForVisible("Expiring?")
	select {
	case r := <-resp:
		if !r.Cancelled || r.Err != plugins.ErrConfirmTimeout {
			t.Fatalf("response = %+v, want timeout", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout never delivered")
	}

	env.waitForGone("Expiring?")
	resp2 := env.rt.ui.RequestConfirm(confirmReq("Alive again?"))
	env.waitForVisible("Alive again?")
	env.engine.SendKey(tui.KeyEnter)
	select {
	case r := <-resp2:
		if r.ID != "no" {
			t.Fatalf("post-timeout response = %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("post-timeout confirm unresolved")
	}
}

// TestPluginConfirm_ShutdownCancelsPending pins teardown: stopping the engine
// fails an outstanding confirm with Err="shutdown" so no plugin parks forever.
func TestPluginConfirm_ShutdownCancelsPending(t *testing.T) {
	env := newConfirmTestEnv(t)

	resp := env.rt.ui.RequestConfirm(confirmReq("Shutdown test?"))
	env.waitForVisible("Shutdown test?")

	env.engine.Stop()
	select {
	case r := <-resp:
		if !r.Cancelled || r.Err != "shutdown" {
			t.Fatalf("response = %+v, want shutdown cancellation", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending confirm not cancelled on shutdown")
	}
}

// TestPluginCommandWrapper_IsAsync pins the root-cause fix that makes
// goa.ui.confirm usable from commands: plugin commands must opt into async
// execution (non-empty AsyncHint) so they never run on the TUI command loop.
func TestPluginCommandWrapper_IsAsync(t *testing.T) {
	w := &pluginCommandWrapper{name: "quota"}
	if hint := w.AsyncHint(nil); hint == "" {
		t.Fatal("plugin command wrapper does not opt into async execution")
	}
	if _, ok := any(w).(core.AsyncCommand); !ok {
		t.Fatal("pluginCommandWrapper does not implement core.AsyncCommand")
	}
}
