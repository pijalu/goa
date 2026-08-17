// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

// TestProviderPicker_CodexOAuth_Filmstrip is the end-to-end regression for
// "adding oauth provider freezes the screen". It drives the real /provider
// picker → '+' add → codex preset → "Sign in with ChatGPT (OAuth)" choice on a
// live TUI engine whose selection callbacks run on the commandLoop (production
// wiring), with a blocking login-flow stub, asserting that:
//
//  1. the auth-kind selector is shown and its callback returns promptly,
//  2. the TUI commandLoop stays responsive while the OAuth flow is parked
//     (a synchronous flow would block the loop — the reported freeze),
//  3. the provider is added with no stored API key once sign-in completes.
func TestProviderPicker_CodexOAuth_Filmstrip(t *testing.T) {
	preset := config.FindPreset("openai-codex")
	if preset == nil {
		t.Skip("openai-codex preset not in catalog")
	}

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

	// Blocking login-flow stub: parks until released (stands in for the codex
	// browser-callback wait). A synchronous invocation on the commandLoop would
	// freeze the engine; the test asserts the loop stays responsive.
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	origRunner := loginFlowRunner
	loginFlowRunner = func(core.Context, string) error {
		startOnce.Do(func() { close(started) })
		<-release
		return nil
	}
	defer func() { loginFlowRunner = origRunner }()

	// Production-style Context: selector → engine overlay, callbacks delivered
	// on the commandLoop via ApplySync (mirrors app.apply); EventBus wired so
	// the flow would take the async path. ConfigSaver must be real enough for
	// finalizePickedProvider to persist.
	cfg := &config.Config{}
	saver := &fakeConfigSaver{}
	ctx := core.Context{
		Config:      cfg,
		ConfigSaver: saver,
		EventBus:    event.MakeBus(16, 16, 16, 16),
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
			// app.apply; replicate that here so a blocking callback would park
			// the engine loop (the freeze this test guards against).
			engine.ApplySync(func() { onSel(sel, sel != "") })
		}()
	}

	// Step 0: open the picker (seeded with a provider) and press '+' → the
	// provider-type selector.
	cfg.Providers = []config.ProviderConfig{{ID: "local", Name: "Local", Endpoint: "http://localhost:1234"}}
	var runErr error
	engine.ApplySync(func() { runErr = showProviderPicker(ctx, cfg, saver) })
	if runErr != nil {
		t.Fatalf("showProviderPicker: %v", runErr)
	}
	waitForFrame(t, engine, "Select provider:")
	term.sendKey("+") // add provider
	waitForFrame(t, engine, "Select provider type:")
	film.Capture("provider-type-selector", engine.AgentFrame(), "")

	// Step 1: pick the codex preset. The selector filters on Label, not Value,
	// so type the label to make the choice unambiguous.
	typeText(term, "OpenAI Codex")
	term.sendKey("\r") // enter → confirm preset
	waitForFrame(t, engine, "Authenticate "+preset.Name+":")
	film.Capture("auth-kind-selector", engine.AgentFrame(), "")

	// Step 2: confirm the default-highlighted "Sign in with ChatGPT (OAuth)".
	// If the OAuth branch ran the login flow synchronously on the commandLoop,
	// this Enter would park the engine loop (the reported freeze).
	term.sendKey("\r") // enter → oauth
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("OAuth flow did not start after selecting oauth — callback blocked?")
	}

	// The UI must stay responsive while the flow is parked: the commandLoop
	// must still process work.
	assertEngineResponsive(t, engine)
	engine.RenderNow()
	film.Capture("oauth-waiting", engine.AgentFrame(), "")

	// Step 3: release the flow (browser login done) → provider stays added,
	// with no stored API key (the OAuth token is used at stream time).
	close(release)
	waitForCondition(t, func() bool { return providerByID(cfg, "openai-codex") != nil })
	added := providerByID(cfg, "openai-codex")
	if added == nil {
		t.Fatal("codex provider not added after OAuth sign-in")
	}
	if added.APIKey != "" {
		t.Errorf("OAuth-added provider must not store an API key, got %q", added.APIKey)
	}

	if len(film.Frames()) < 3 {
		t.Errorf("filmstrip captured %d snapshots, want >= 3", len(film.Frames()))
	}
}

// TestStartCodexOAuthFromPicker_FailureRollsBackProvider verifies that a failed
// OAuth sign-in removes the provider the picker just added (it would otherwise
// persist a credential-less entry), while a pre-existing provider is kept.
func TestStartCodexOAuthFromPicker_FailureRollsBackProvider(t *testing.T) {
	preset := config.FindPreset("openai-codex")
	if preset == nil {
		t.Skip("openai-codex preset not in catalog")
	}

	// Swap the global runner seam ONCE for both subtests and restore it only
	// after every spawned flow goroutine has finished: a restore (or per-subtest
	// swap) racing a still-running goroutine's read of the variable is a test
	// artifact, not production behavior. doneWg tracks goroutine completion.
	var runnerRelease atomic.Pointer[chan struct{}]
	var doneWg sync.WaitGroup
	origRunner := loginFlowRunner
	loginFlowRunner = func(core.Context, string) error {
		defer doneWg.Done()
		ch := runnerRelease.Load()
		if ch == nil {
			return nil
		}
		<-*ch
		return errors.New("browser login aborted")
	}
	defer func() {
		doneWg.Wait()
		loginFlowRunner = origRunner
	}()

	t.Run("fresh provider is removed on failure", func(t *testing.T) {
		cfg := &config.Config{}
		saver := &fakeConfigSaver{}
		ctx, _, _, _ := newMenuTestContext(t, cfg)
		ctx.ConfigSaver = saver

		release := make(chan struct{})
		runnerRelease.Store(&release)
		doneWg.Add(1)

		startCodexOAuthFromPicker(*ctx, cfg, saver, preset)
		if providerByID(cfg, preset.ID) == nil {
			t.Fatal("provider should be added optimistically")
		}
		close(release)
		waitForCondition(t, func() bool { return providerByID(cfg, preset.ID) == nil })
	})

	t.Run("pre-existing provider is kept on failure", func(t *testing.T) {
		cfg := &config.Config{}
		saver := &fakeConfigSaver{}
		ctx, _, _, _ := newMenuTestContext(t, cfg)
		ctx.ConfigSaver = saver
		pickerProviderMu.Lock()
		upsertProviderConfig(cfg, preset.ID, preset.Name, preset.Endpoint, "")
		pickerProviderMu.Unlock()

		release := make(chan struct{})
		runnerRelease.Store(&release)
		doneWg.Add(1)

		startCodexOAuthFromPicker(*ctx, cfg, saver, preset)
		close(release)
		// Wait until the flow goroutine has fully run (outer defer also waits),
		// then assert it did NOT remove the pre-existing provider.
		doneWg.Wait()
		if providerByID(cfg, preset.ID) == nil {
			t.Error("pre-existing provider must be kept on sign-in failure")
		}
	})
}

// providerByID reads a provider entry under pickerProviderMu so tests stay
// race-free against the picker's async add/rollback goroutine.
func providerByID(cfg *config.Config, id string) *config.ProviderConfig {
	pickerProviderMu.Lock()
	defer pickerProviderMu.Unlock()
	p := cfg.GetProviderByID(id)
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// typeText feeds printable characters into the engine's input loop one by one.
func typeText(term *fakeTerm, s string) {
	for _, r := range s {
		term.sendKey(string(r))
	}
}

// waitForFrame polls the rendered frame until substr appears or the deadline
// passes, then fails.
func waitForFrame(t *testing.T, engine *tui.TUI, substr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		engine.RenderNow()
		if visibleHas(engine.AgentFrame(), substr) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	var b strings.Builder
	for _, l := range engine.AgentFrame().Visible {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	t.Fatalf("frame never showed %q; visible:\n%s", substr, b.String())
}

// assertEngineResponsive fails when the TUI commandLoop cannot run a trivial
// command within the deadline — i.e. the loop is parked (the freeze).
func assertEngineResponsive(t *testing.T, engine *tui.TUI) {
	t.Helper()
	responsive := make(chan struct{})
	go func() {
		engine.ApplySync(func() { close(responsive) })
	}()
	select {
	case <-responsive:
	case <-time.After(2 * time.Second):
		t.Fatal("UI engine frozen: ApplySync blocked while OAuth flow was parked")
	}
}

// waitForCondition polls cond until true or the deadline passes, then fails.
func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}
