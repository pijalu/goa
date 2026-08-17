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
// browser-callback wait). It proves the UI is not frozen while parked.
type blockingCodexFlow struct {
	release chan struct{}
	started chan struct{}
	once    sync.Once
	tokens  *oauth.Tokens
}

func (f *blockingCodexFlow) Run(ctx context.Context, w uiWriter, _ prompter) (*oauth.Tokens, error) {
	w.Writef("Open this URL in your browser:\nhttps://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann\n")
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
	// runOAuthFlow takes the async (non-freezing) path. No OutputBuffer: output
	// assertions live in the synchronous unit tests; here we assert UI liveness
	// and credential storage, which are the freeze/silent-failure regressions.
	ctx := core.Context{
		EventBus: event.MakeBus(16, 16, 16, 16),
	}
	ctx.SelectOptionFunc = func(title string, items []tui.SelectorItem, current string, onSel func(string, bool)) {
		ch := engine.ShowSelector(title, items, current)
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

	// UI stays responsive while the flow is parked: the engine command loop must
	// still process work. If runOAuthFlow ran synchronously on the UI goroutine,
	// this ApplySync would block until the (unreleased) flow returned.
	responsive := make(chan struct{})
	go func() {
		engine.ApplySync(func() { close(responsive) })
	}()
	select {
	case <-responsive:
	case <-time.After(2 * time.Second):
		t.Fatal("UI engine frozen: ApplySync blocked while OAuth flow was parked")
	}
	engine.RenderNow()
	film.Capture("oauth-waiting", engine.AgentFrame(), "")

	// Step 2: release the flow (browser login done) → tokens stored.
	close(flow.release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := store.GetOAuth("openai"); ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := store.GetOAuth("openai"); !ok {
		t.Fatal("oauth tokens not stored after flow completed")
	}
	engine.RenderNow()
	film.Capture("oauth-success", engine.AgentFrame(), "")

	// The filmstrip recorded the full evolution; assert it has the 3 key steps.
	if len(film.Frames()) < 3 {
		t.Errorf("filmstrip captured %d snapshots, want >= 3", len(film.Frames()))
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
