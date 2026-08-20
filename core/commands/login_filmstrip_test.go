// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"github.com/pijalu/goa/core"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTerm is a minimal tui.Terminal for driving a real TUI engine in tests.
type fakeTerm struct {
	mu      sync.Mutex
	w, h    int
	onInput func(string)
}

func (f *fakeTerm) Start(onInput func(string), _ func()) {
	f.mu.Lock()
	f.onInput = onInput
	f.mu.Unlock()
}
func (f *fakeTerm) Stop()                       {}
func (f *fakeTerm) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakeTerm) WriteString(s string)        {}
func (f *fakeTerm) Size() (int, int)            { return f.w, f.h }
func (f *fakeTerm) SetRaw() (func(), error)     { return func() {}, nil }
func (f *fakeTerm) HideCursor()                 {}
func (f *fakeTerm) ShowCursor()                 {}
func (f *fakeTerm) ClearScreen()                {}
func (f *fakeTerm) SetTitle(string)             {}

// sendKey feeds a raw key into the engine's input loop.
func (f *fakeTerm) sendKey(s string) {
	f.mu.Lock()
	h := f.onInput
	f.mu.Unlock()
	if h != nil {
		h(s)
	}
}

// blockingCodexFlow mimics the real Codex browser login: it emits the auth URL
// through the bridged writer, then blocks until released (standing in for the
// browser-callback wait). It proves the UI is not frozen while parked. The URL
// is emitted via the production codexUIFromWriter bridge so the test also
// exercises the OSC-8 hyperlink + click-hint rendering end to end. The
// OpenURL bridge is neutralized (no real browser in tests).
type blockingCodexFlow struct {
	release chan struct{}
	started chan struct{}
	once    sync.Once
	tokens  *oauth.Tokens
}

const filmstripAuthURL = "https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann"

func (f *blockingCodexFlow) Run(ctx context.Context, w uiWriter, p prompter) (*oauth.Tokens, error) {
	opts := codexUIFromWriter(w, p)
	opts.OpenURL = nil // no real browser in tests
	if opts.NotifyURL != nil {
		opts.NotifyURL(filmstripAuthURL)
	}
	f.once.Do(func() { close(f.started) })
	select {
	case <-f.release:
		return f.tokens, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestLoginCodexOAuth_Filmstrip is the end-to-end regression for "adding a
// provider — selecting OAuth does not bring anything / TUI frozen". It drives
// the real /login:openai-codex flow through a live TUI engine with a
// production-style Context (selector + event bus) and a blocking codex flow,
// asserting that:
//  1. the auth-kind selector is shown,
//  2. picking OAuth returns promptly (no UI freeze) while the flow is parked,
//  3. the browser auth URL is surfaced to the user,
//  4. tokens are stored once the flow completes.
func TestLoginCodexOAuth_Filmstrip(t *testing.T) {
	term := &fakeTerm{w: 100, h: 30}
	engine := tui.NewTUI(term)
	chat := tui.NewChatViewport()
	engine.AddChild(chat)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	defer engine.Stop()
	engine.RunLoops() // start the command loop so ApplySync serializes on it
	film := tui.NewFilmstrip()

	store, err := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	flow := &blockingCodexFlow{
		release: make(chan struct{}),
		started: make(chan struct{}),
		tokens:  &oauth.Tokens{AccessToken: "codex-tok", AccountID: "acct-1"},
	}
	cmd := &LoginCommand{
		Store:       store,
		flowFactory: func(string) oauthFlow { return flow },
	}

	// Production-style Context: selector → engine overlay, EventBus wired so
	// runOAuthFlow takes the async (non-freezing) path and posts the auth URL
	// live to the chat via WriteSystem. drainChatEvents mirrors the production
	// chat-event reader so that output actually lands in the viewport.
	bus := event.MakeBus(16, 16, 16, 16)
	stopDrain := drainChatEvents(engine, chat, bus)
	defer stopDrain()
	ctx := core.Context{
		EventBus: bus,
	}
	ctx.SelectOptionFunc = func(title string, items []tui.SelectorItem, current string, onSel func(string, bool)) {
		// Show the selector on the commandLoop (ApplySync): showSelector
		// mutates selector state (SetDone) which the loop concurrently reads
		// in HandleInput — calling it from the caller goroutine races.
		var ch <-chan string
		engine.ApplySync(func() { ch = engine.ShowSelector(title, items, current) })
		go func() {
			sel := <-ch
			// Production runs the selection callback on the UI goroutine via
			// app.apply; replicate that here so a synchronous flow would block
			// the engine loop (the freeze this test guards against).
			engine.ApplySync(func() { onSel(sel, sel != "") })
		}()
	}

	// Step 0: run /login:openai-codex → selector appears (non-blocking).
	var runErr error
	engine.ApplySync(func() { runErr = cmd.Run(ctx, []string{"openai-codex"}) })
	if runErr != nil {
		t.Fatalf("login run: %v", runErr)
	}
	engine.RenderNow()
	film.Capture("auth-kind-selector", engine.AgentFrame(), "")
	if !visibleHas(engine.AgentFrame(), "Sign in with ChatGPT") &&
		!visibleHas(engine.AgentFrame(), "OAuth") {
		t.Fatalf("auth-kind selector not rendered; visible = %v", engine.AgentFrame().Visible)
	}

	// Step 1: move to "oauth" (selector opens with apikey highlighted) and press
	// Enter. The selector overlay captures input; the flow must start without
	// freezing the UI.
	term.sendKey("\x1b[B") // down → oauth
	term.sendKey("\r")     // enter → confirm
	select {
	case <-flow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("OAuth flow did not start after selecting oauth — UI frozen?")
	}

	// UI stays responsive while the flow is parked (a synchronous flow on the
	// UI goroutine would park the engine loop — the reported freeze).
	assertEngineResponsive(t, engine)
	engine.RenderNow()
	film.Capture("oauth-waiting", engine.AgentFrame(), "")

	// The browser auth URL is surfaced live in the chat as a clickable
	// (OSC-8) link with a click-to-open hint — the regression that was
	// previously lost to the dead command OutputBuffer.
	waitForFrame(t, engine, "auth.openai.com/oauth/authorize")
	if !visibleHas(engine.AgentFrame(), "click to open") {
		t.Errorf("oauth-waiting frame missing click-to-open hint; visible = %v", engine.AgentFrame().Visible)
	}
	engine.RenderNow()
	film.Capture("oauth-url-shown", engine.AgentFrame(), "")

	// Step 2: release the flow (browser login done) → tokens stored.
	close(flow.release)
	waitForCondition(t, func() bool { return store.HasAuth("openai") })
	engine.RenderNow()
	film.Capture("oauth-success", engine.AgentFrame(), "")

	// The success line is also surfaced live in the chat.
	waitForFrame(t, engine, "OAuth login for openai succeeded.")

	// The filmstrip recorded the full evolution; assert it has the key steps.
	if len(film.Frames()) < 4 {
		t.Errorf("filmstrip captured %d snapshots, want >= 4", len(film.Frames()))
	}
}

// visibleHas reports whether any ANSI-stripped visible viewport line contains substr.
func visibleHas(frame tui.AgentFrame, substr string) bool {
	for _, l := range frame.Visible {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

// drainChatEvents consumes the event bus's Chat channel and applies each
// SystemMessage / Flash to the chat viewport, mirroring the production
// App.runChatEventReader → handleChatEvent path. It returns a stop func.
// Without this, a filmstrip ctx that wires an EventBus never surfaces the
// live OAuth WriteSystem output into the chat, so assertions on the rendered
// auth URL / device code would see nothing.
func drainChatEvents(engine *tui.TUI, chat *tui.ChatViewport, bus *event.Bus) func() {
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case ev := <-bus.Chat:
				applyChatEvent(engine, chat, ev)
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// applyChatEvent renders one drained ChatEvent onto the chat viewport on the
// commandLoop (ApplySync), mirroring App.showSystemMessage / showFlash.
func applyChatEvent(engine *tui.TUI, chat *tui.ChatViewport, ev event.ChatEvent) {
	switch {
	case ev.SystemMessage != nil:
		m := ev.SystemMessage
		engine.ApplySync(func() {
			if m.Preformatted {
				chat.AddSystemMessagePreformatted(m.Text)
			} else {
				chat.AddSystemMessage(m.Text)
			}
		})
	case ev.Flash != nil:
		f := ev.Flash
		engine.ApplySync(func() { chat.AddFlashMessage("⚡ " + f.Text) })
	}
}

// TestLoginCodexMethod_Filmstrip pins the Pi showAuthSelect UX for the codex
// browser-vs-device method choice on a live TUI engine: the choice is a
// navigable option list (selector overlay with both methods rendered), not a
// free-text clarify prompt. It drives /login:openai-codex oauth, picks
// "device" via arrow-down + enter, and asserts the device flow is selected
// and the engine stays responsive throughout.
func TestLoginCodexMethod_Filmstrip(t *testing.T) {
	term := &fakeTerm{w: 100, h: 30}
	engine := tui.NewTUI(term)
	chat := tui.NewChatViewport()
	engine.AddChild(chat)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	defer engine.Stop()
	engine.RunLoops()
	film := tui.NewFilmstrip()

	store, err := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	flow := &blockingCodexFlow{
		release: make(chan struct{}),
		started: make(chan struct{}),
		tokens:  &oauth.Tokens{AccessToken: "device-tok"},
	}
	cmd := &LoginCommand{
		Store:       store,
		flowFactory: func(string) oauthFlow { return flow },
	}

	// Production-style Context: selector → engine overlay with callbacks
	// marshalled onto the commandLoop; EventBus wired so the oauth flow runs
	// on its background goroutine while the method selector is shown. The
	// callback is QUEUED (engine.Apply, like app.apply) — ApplySync here would
	// park the loop: selectCodexMethod legitimately blocks on the selection
	// result, so the callback must not hold the loop while delivering it.
	ctx := core.Context{
		EventBus: event.MakeBus(16, 16, 16, 16),
	}
	ctx.SelectOptionFunc = func(title string, items []tui.SelectorItem, current string, onSel func(string, bool)) {
		// Show the selector on the commandLoop (ApplySync): showSelector
		// mutates selector state (SetDone) which the loop concurrently reads
		// in HandleInput — calling it from the caller goroutine races.
		var ch <-chan string
		engine.ApplySync(func() { ch = engine.ShowSelector(title, items, current) })
		go func() {
			sel := <-ch
			engine.Apply(func() { onSel(sel, sel != "") })
		}()
	}

	// Step 0: /login:openai-codex oauth runs on a background goroutine (as
	// runOAuthFlow does in production — the synchronous Run would otherwise
	// block inside SelectOption while the method selector waits for keys).
	// The method selector then appears as an overlay.
	runDone := make(chan error, 1)
	go func() { runDone <- cmd.Run(ctx, []string{"openai-codex", "oauth"}) }()
	waitForFrame(t, engine, "OpenAI Codex login method")
	engine.RenderNow()
	film.Capture("method-selector", engine.AgentFrame(), "")

	// Both methods must be visible in the list (not a bare free-text prompt).
	for _, want := range []string{"Sign in with browser", "Use a device code"} {
		if !visibleHas(engine.AgentFrame(), want) {
			t.Fatalf("method selector missing %q; visible = %v", want, engine.AgentFrame().Visible)
		}
	}

	// Step 1: arrow-down to "Use a device code" and confirm. The flow starts
	// (on the runOAuthFlow goroutine) without parking the engine loop.
	term.sendKey("\x1b[B") // down → device
	term.sendKey("\r")     // enter
	select {
	case <-flow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("device flow did not start after picking device — selector stuck?")
	}
	assertEngineResponsive(t, engine)
	engine.RenderNow()
	film.Capture("device-flow-waiting", engine.AgentFrame(), "")

	// Step 2: release the flow → tokens stored; the command returns cleanly.
	close(flow.release)
	waitForCondition(t, func() bool { return store.HasAuth("openai") })
	if err := <-runDone; err != nil {
		t.Fatalf("login run: %v", err)
	}
	engine.RenderNow()
	film.Capture("device-success", engine.AgentFrame(), "")

	if len(film.Frames()) < 3 {
		t.Errorf("filmstrip captured %d snapshots, want >= 3", len(film.Frames()))
	}
}

// blockingCodexDeviceFlow mimics the real Codex device-code login: it emits
// the device authorization (verification URL + user code) through the
// production codexUIFromWriter bridge, then blocks until released (standing
// in for the device-token poll). It lets the filmstrip assert the "Enter
// code:" panel reaches the chat live.
type blockingCodexDeviceFlow struct {
	release  chan struct{}
	started  chan struct{}
	once     sync.Once
	tokens   *oauth.Tokens
	userCode string
}

func (f *blockingCodexDeviceFlow) Run(ctx context.Context, w uiWriter, p prompter) (*oauth.Tokens, error) {
	opts := codexUIFromWriter(w, p)
	if opts.NotifyDevice != nil {
		opts.NotifyDevice(oauth.CodexDeviceAuth{UserCode: f.userCode, IntervalSeconds: 5})
	}
	f.once.Do(func() { close(f.started) })
	select {
	case <-f.release:
		return f.tokens, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestLoginCodexDeviceCode_Filmstrip validates the headless (device-code)
// sign-on end to end on a live TUI engine: after the method list picks
// "device", the verification URL (as a clickable OSC-8 link), the prominent
// "Enter code: <userCode>" line, and the "Waiting for authentication..."
// message must appear in the chat viewport live — the regression where
// selecting device returned to the input with no message and no link.
func TestLoginCodexDeviceCode_Filmstrip(t *testing.T) {
	term := &fakeTerm{w: 100, h: 30}
	engine := tui.NewTUI(term)
	chat := tui.NewChatViewport()
	engine.AddChild(chat)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	defer engine.Stop()
	engine.RunLoops()
	film := tui.NewFilmstrip()

	store, err := auth.NewStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	flow := &blockingCodexDeviceFlow{
		release:  make(chan struct{}),
		started:  make(chan struct{}),
		tokens:   &oauth.Tokens{AccessToken: "device-tok", AccountID: "acct-d"},
		userCode: "WXYZ-9876",
	}
	cmd := &LoginCommand{
		Store:       store,
		flowFactory: func(string) oauthFlow { return flow },
	}

	bus := event.MakeBus(16, 16, 16, 16)
	stopDrain := drainChatEvents(engine, chat, bus)
	defer stopDrain()
	ctx := core.Context{EventBus: bus}
	ctx.SelectOptionFunc = func(title string, items []tui.SelectorItem, current string, onSel func(string, bool)) {
		var ch <-chan string
		engine.ApplySync(func() { ch = engine.ShowSelector(title, items, current) })
		go func() {
			sel := <-ch
			engine.Apply(func() { onSel(sel, sel != "") })
		}()
	}

	// Drive /login:openai-codex oauth on a background goroutine (production
	// parity — see TestLoginCodexMethod_Filmstrip).
	runDone := make(chan error, 1)
	go func() { runDone <- cmd.Run(ctx, []string{"openai-codex", "oauth"}) }()
	waitForFrame(t, engine, "OpenAI Codex login method")
	film.Capture("method-selector", engine.AgentFrame(), "")

	// Pick "Use a device code".
	term.sendKey("\x1b[B") // down → device
	term.sendKey("\r")     // enter
	select {
	case <-flow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("device flow did not start after picking device")
	}
	assertEngineResponsive(t, engine)

	// The device-code panel must be rendered live in the chat.
	waitForFrame(t, engine, "Enter code: WXYZ-9876")
	engine.RenderNow()
	film.Capture("device-code-shown", engine.AgentFrame(), "")
	for _, want := range []string{"auth.openai.com/codex/device", "click to open", "Waiting for authentication..."} {
		if !visibleHas(engine.AgentFrame(), want) {
			t.Errorf("device-code frame missing %q; visible = %v", want, engine.AgentFrame().Visible)
		}
	}

	// Complete the flow → tokens stored + success line shown.
	close(flow.release)
	if err := <-runDone; err != nil {
		t.Fatalf("login run: %v", err)
	}
	waitForCondition(t, func() bool { return store.HasAuth("openai") })
	waitForFrame(t, engine, "OAuth login for openai succeeded.")
	engine.RenderNow()
	film.Capture("device-success", engine.AgentFrame(), "")

	if len(film.Frames()) < 3 {
		t.Errorf("filmstrip captured %d snapshots, want >= 3", len(film.Frames()))
	}
}
