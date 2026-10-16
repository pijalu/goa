// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

// The tests in this file pin bugs.md B27/B28: a blocking continuation started
// from an auth-kind selector callback must not park the TUI command loop, and
// the auth-kind picker must show its declared order.
//
// They run on a real TUI engine with RunLoops() so Apply/ApplySync have the
// production semantics: the loop is the sole state owner, `Apply` queues, and
// `ApplySync` blocks until the loop runs the command. The selector wiring is
// the production one (internal/app/commandcontext.go wireInteractiveCallbacks):
// the picker is shown on the loop and its selection callback is delivered BY
// the loop via Apply — which is exactly why a callback that blocks while
// waiting for a second selector (or for a clarify prompt) never gets its answer.

// loginLoopHarness is a live engine + chat + auth store wired like production.
type loginLoopHarness struct {
	engine    *tui.TUI
	term      *fakeTerm
	store     *auth.Store
	ctx       core.Context
	stopDrain func()
}

// newLoginLoopHarness starts an engine with the command/render loops and wires
// the production selector contract. stop() is deadline-bounded: when a test
// fails with the loop parked, engine.Stop() would otherwise block forever and
// hang the whole package instead of reporting the failure.
func newLoginLoopHarness(t *testing.T) *loginLoopHarness {
	t.Helper()
	term := &fakeTerm{w: 100, h: 30}
	engine := tui.NewTUI(term)
	chat := tui.NewChatViewport()
	engine.AddChild(chat)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	engine.RunLoops()

	bus := event.MakeBus(16, 16, 16, 16)
	h := &loginLoopHarness{engine: engine, term: term, store: mustStore(t)}
	h.ctx = core.Context{EventBus: bus}
	// Production-style selector wiring: show on the loop, deliver the callback
	// through the loop (app.apply). Events are drained so the flow's live output
	// reaches the chat viewport, mirroring App.runChatEventReader.
	h.stopDrain = drainChatEvents(engine, chat, bus)
	h.ctx.SelectOptionFunc = func(title string, items []tui.SelectorItem, current string, onSel func(string, bool)) {
		var ch <-chan string
		engine.ApplySync(func() { ch = engine.ShowSelector(title, items, current) })
		go func() {
			sel := <-ch
			engine.Apply(func() { onSel(sel, sel != "") })
		}()
	}
	t.Cleanup(h.stop)
	return h
}

func (h *loginLoopHarness) stop() {
	h.stopDrain()
	done := make(chan struct{})
	go func() {
		h.engine.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

// sendKeys feeds raw keys to the focused overlay and lets the dispatch settle.
func (h *loginLoopHarness) sendKeys(keys ...string) {
	for _, k := range keys {
		h.term.sendKey(k)
		time.Sleep(30 * time.Millisecond)
	}
}

// waitForSelector polls until the rendered frame shows a selector titled title.
func (h *loginLoopHarness) waitForSelector(t *testing.T, title string) {
	t.Helper()
	waitForFrame(t, h.engine, title)
}

// visibleIndex returns the first rendered line showing substr, or -1.
func visibleIndex(frame tui.AgentFrame, substr string) int {
	for i, l := range frame.Visible {
		if strings.Contains(l, substr) {
			return i
		}
	}
	return -1
}

// TestLoginKindPicker_OAuthKeepsCommandLoopResponsive is the regression for the
// reported freeze: `/login:codex` → "Sign in with OAuth (browser)" parked the
// command loop inside selectCodexMethod, so every keystroke queued behind it
// forever and Goa died (bugs.md B27).
func TestLoginKindPicker_OAuthKeepsCommandLoopResponsive(t *testing.T) {
	h := newLoginLoopHarness(t)
	flow := &blockingCodexFlow{
		release: make(chan struct{}),
		started: make(chan struct{}),
		tokens:  &oauth.Tokens{AccessToken: "codex-tok", AccountID: "acct-1"},
	}
	cmd := &LoginCommand{Store: h.store, flowFactory: func(string) oauthFlow { return flow }}

	// Production: the command body runs on the command loop; the auth-kind
	// picker returns right after being shown.
	h.engine.ApplySync(func() {
		if err := cmd.Run(h.ctx, []string{"openai-codex"}); err != nil {
			t.Errorf("login discovery run: %v", err)
		}
	})
	h.waitForSelector(t, "Sign in to openai-codex:")

	// B28: the rows follow the declared preference order (apikey default first,
	// device-code last) instead of the selector's alphabetical default.
	h.engine.RenderNow()
	frame := h.engine.AgentFrame()
	apiRow := visibleIndex(frame, "Use an API key")
	oauthRow := visibleIndex(frame, "Sign in with OAuth (browser)")
	deviceRow := visibleIndex(frame, "Sign in with ChatGPT (device code)")
	if apiRow < 0 || oauthRow < 0 || deviceRow < 0 {
		t.Fatalf("auth-kind picker rows missing (apikey=%d oauth=%d device=%d)\n%s",
			apiRow, oauthRow, deviceRow, frame.Dump())
	}
	if !(apiRow < oauthRow && oauthRow < deviceRow) {
		t.Errorf("auth-kind picker must keep the declared order apikey < oauth < oauth:device, got rows %d/%d/%d\n%s",
			apiRow, oauthRow, deviceRow, frame.Dump())
	}

	// Move to "Sign in with OAuth (browser)" (row 1) and confirm.
	h.sendKeys("\x1b[B", "\r")

	// The freeze: a parked loop can never accept work again.
	assertEngineResponsive(t, h.engine)

	// The continuation runs off-loop, so the browser/device picker must appear
	// and must be answerable.
	h.waitForSelector(t, "OpenAI Codex login method")
	h.sendKeys("\r") // browser is the first row / default

	select {
	case <-flow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("codex browser flow did not start after picking oauth — selector stuck?")
	}
	assertEngineResponsive(t, h.engine)

	close(flow.release)
	waitForCondition(t, func() bool { return h.store.HasAuth("openai") })
	waitForFrame(t, h.engine, "OAuth login for openai succeeded.")
}

// TestLoginKindPicker_APIKeyKeepsCommandLoopResponsive covers the second entry
// point of the same defect: "Use an API key" reaches handleAPIKey →
// clarifier.Prompt → ctx.ClarifyFunc, which BLOCKS by contract and therefore
// must never be called from the command loop either.
func TestLoginKindPicker_APIKeyKeepsCommandLoopResponsive(t *testing.T) {
	h := newLoginLoopHarness(t)
	// Production app.clarify: marshal onto the loop, then wait for the answer —
	// it needs a free loop to deliver the value.
	h.ctx.ClarifyFunc = func(*tui.ClarifyCard) (string, bool) {
		ch := make(chan string, 1)
		h.engine.Apply(func() { ch <- "sk-from-loop" })
		return <-ch, true
	}
	cmd := &LoginCommand{Store: h.store}

	h.engine.ApplySync(func() { _ = cmd.Run(h.ctx, []string{"openai-codex"}) })
	h.waitForSelector(t, "Sign in to openai-codex:")

	h.sendKeys("\r") // "Use an API key" is row 0 (declared order, kinds[0])

	assertEngineResponsive(t, h.engine)
	waitForCondition(t, func() bool {
		key, ok := h.store.GetAPIKey("openai")
		return ok && key == "sk-from-loop"
	})
}

// TestLoginDirectOAuth_RunsOffCommandLoop covers the third entry point: typing
// `/login:<provider>:oauth` inline. The host only runs a command body off the
// loop when it implements core.AsyncCommand, so AsyncHint must opt in — with it,
// the browser/device picker is answerable while the body waits.
func TestLoginDirectOAuth_RunsOffCommandLoop(t *testing.T) {
	h := newLoginLoopHarness(t)
	flow := &blockingCodexFlow{
		release: make(chan struct{}),
		started: make(chan struct{}),
		tokens:  &oauth.Tokens{AccessToken: "codex-tok"},
	}
	cmd := &LoginCommand{Store: h.store, flowFactory: func(string) oauthFlow { return flow }}
	args := []string{"openai-codex", "oauth"}

	if hint := core.AsyncHintOf(cmd, args); hint == "" {
		t.Fatal("AsyncHint must opt /login:<provider>:oauth into the host's off-loop path — " +
			"otherwise the command body parks the command loop on the browser/device picker (bugs.md B27)")
	}

	// What internal/app runAsyncCommand does: Run on a background goroutine.
	runDone := make(chan error, 1)
	go func() { runDone <- cmd.Run(h.ctx, args) }()

	h.waitForSelector(t, "OpenAI Codex login method")
	assertEngineResponsive(t, h.engine)
	h.sendKeys("\r") // browser

	select {
	case <-flow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("browser flow did not start")
	}
	close(flow.release)
	if err := <-runDone; err != nil {
		t.Fatalf("login run: %v", err)
	}
	waitForCondition(t, func() bool { return h.store.HasAuth("openai") })
}

// TestLoginCommand_AsyncHint pins the opt-in contract: the invocations that
// block on the user or the provider get a spinner label (and thus run off-loop),
// the instant ones stay synchronous.
func TestLoginCommand_AsyncHint(t *testing.T) {
	cmd := &LoginCommand{}
	cases := []struct {
		name string
		args []string
		want string // "" = synchronous
	}{
		{"discovery", nil, ""},
		{"kind-picker", []string{"openai-codex"}, ""},
		{"oauth", []string{"openai-codex", "oauth"}, "openai"},
		{"oauth-copilot", []string{"copilot", "oauth"}, "copilot"},
		{"oauth-device", []string{"codex", "oauth", "device"}, "openai"},
		{"apikey-interactive", []string{"codex", "apikey"}, "openai"},
		{"apikey-inline", []string{"anthropic", "apikey", "sk-abc"}, ""},
		{"legacy-inline-token", []string{"openai", "sk-abc"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cmd.AsyncHint(tc.args)
			if tc.want == "" {
				if got != "" {
					t.Errorf("AsyncHint(%v) = %q, want synchronous", tc.args, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("AsyncHint(%v) = %q, want it to mention %q", tc.args, got, tc.want)
			}
		})
	}
}

// TestLoginKindPicker_ItemOrder pins the declared order of the picker items
// (bugs.md B28) — the label sort made the highlighted default land last.
func TestLoginKindPicker_ItemOrder(t *testing.T) {
	cmd := &LoginCommand{Store: mustStore(t)}
	var got []string
	ctx := core.Context{
		SelectOptionFunc: func(_ string, items []tui.SelectorItem, _ string, _ func(string, bool)) {
			for _, it := range items {
				got = append(got, it.Value)
				if !it.PreserveOrder {
					t.Errorf("picker item %q must set PreserveOrder (else the selector sorts it alphabetically)", it.Value)
				}
			}
		},
	}
	if err := cmd.Run(ctx, []string{"openai-codex"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"apikey", "oauth", "oauth:device"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("picker order = %v, want %v", got, want)
	}
}

// TestRunOffCommandLoop_FlashOnError pins the error surface of the off-loop
// dispatch: a selector callback cannot return an error, so failures must reach
// the user as a flash.
func TestRunOffCommandLoop_FlashOnError(t *testing.T) {
	bus := event.MakeBus(1, 1, 1, 1)
	ctx := core.Context{EventBus: bus}
	done := make(chan struct{})
	runOffCommandLoop(ctx, "Login to codex", func() error {
		close(done)
		return context.DeadlineExceeded
	})
	<-done
	select {
	case ev := <-bus.Chat:
		if ev.Flash == nil || !strings.Contains(ev.Flash.Text, "Login to codex failed") {
			t.Errorf("flash = %+v, want a 'Login to codex failed' flash", ev.Flash)
		}
	case <-time.After(time.Second):
		t.Fatal("no flash posted for a failed off-loop continuation")
	}
}
