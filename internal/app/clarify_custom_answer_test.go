// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/tui"
)

// BUG-6 (bugs.md): a clarification/user question must ALWAYS allow the user
// to express another option. The option selector therefore carries a trailing
// "type your own answer" entry that opens the free-text main input; Esc there
// still cancels the whole clarification.

func newClarifyTestApp(t *testing.T) (*App, *clarifyKeyTerminal, *tui.TUI, *tui.Editor) {
	t.Helper()
	_, def := spinner.Default()
	tui.SetSpinner(def)
	t.Cleanup(func() { tui.SetSpinner(spinner.Definition{}) })

	term := &clarifyKeyTerminal{}
	term.w, term.h = 100, 30
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	t.Cleanup(engine.Stop)
	engine.RunLoops()

	chat := tui.NewChatViewport()
	inp := tui.NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	inp.SetTUI(engine)
	engine.SetFocus(inp)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.inputEditor = inp
	app := New(subs)
	// The real bootstrap wires the editor submit through makeSubmitHandler
	// (setupEventHandlers), Esc through inp.OnEscape → handleEscape
	// (attachInputHandlers) and Ctrl+C through OnCancelInputRequest
	// (buildTUI); replicate all three here so typed text reaches
	// handlePendingMainInput and esc/cancel reach a pending input request.
	inp.SetOnSubmit(app.makeSubmitHandler(engine, chat))
	inp.OnEscape = func() { app.handleEscape() }
	engine.OnCancelInputRequest = func() bool { return app.cancelPendingMainInput() }
	return app, term, engine, inp
}

// TestClarify_OptionsOfferCustomAnswer: the option selector lists the card's
// options PLUS an explicit "type your own answer" entry; picking it opens the
// free-text main input line, and the typed answer reaches the waiting tool
// caller.
func TestClarify_OptionsOfferCustomAnswer(t *testing.T) {
	app, term, engine, inp := newClarifyTestApp(t)

	card := tui.NewClarifyCard("OpenAI Codex login method", "", "Pick a login method",
		[]string{"Sign in with browser", "Use a device code"})

	type answer struct {
		text string
		ok   bool
	}
	ansCh := make(chan answer, 1)
	go func() {
		text, ok := app.clarify(card)
		ansCh <- answer{text, ok}
	}()

	// The affix must be visible in the selector.
	waitForVisibleText(t, engine, "Sign in with browser")
	if visible := strings.Join(engine.AgentFrame().Visible, "\n"); !strings.Contains(ansi.Strip(visible), "Type your own answer") {
		t.Errorf("selector must offer the custom-answer entry; visible:\n%s", visible)
	}
	engine.ApplySync(func() {
		if app.pendingInput != nil {
			t.Error("the custom entry must not register a main-input request before being picked")
		}
	})

	// Navigate past both options to the trailing custom entry, confirm.
	term.sendKey("\x1b[B") // down → second option
	term.sendKey("\x1b[B") // down → "Type your own answer"
	term.sendKey("\r")     // enter

	// The free-text input line takes over with the clarify prompt as title.
	waitForTitle(t, engine, inp, "OpenAI Codex login method")
	engine.ApplySync(func() {
		if app.pendingInput == nil {
			t.Error("picking the custom entry must register a main-input request")
		}
	})

	// Type a custom answer and submit it.
	term.sendKey("use the flow field, not the browser")
	term.sendKey("\r")

	select {
	case got := <-ansCh:
		if !got.ok || got.text != "use the flow field, not the browser" {
			t.Errorf("clarify = (%q, %v), want the typed custom answer with ok", got.text, got.ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clarify did not return after submitting the custom answer")
	}
}

// TestClarify_CustomAnswerEscCancels: Esc on the free-text step (after
// picking "type your own answer") cancels the whole clarification — the
// waiting tool caller gets ok==false, never a hang.
func TestClarify_CustomAnswerEscCancels(t *testing.T) {
	app, term, engine, inp := newClarifyTestApp(t)

	card := tui.NewClarifyCard("Pick", "", "?", []string{"a", "b"})
	type answer struct {
		text string
		ok   bool
	}
	ansCh := make(chan answer, 1)
	go func() {
		text, ok := app.clarify(card)
		ansCh <- answer{text, ok}
	}()

	waitForVisibleText(t, engine, "a")
	term.sendKey("\x1b[B") // down → second option
	term.sendKey("\x1b[B") // down → "Type your own answer"
	term.sendKey("\r")     // enter → free-text input

	waitForTitle(t, engine, inp, "Pick")
	term.sendKey("\x1b") // esc → cancel the clarification

	select {
	case got := <-ansCh:
		if got.ok || got.text != "" {
			t.Errorf("cancelled custom answer = (%q, %v), want (\"\", false)", got.text, got.ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clarify did not return after esc on the free-text step")
	}
}
