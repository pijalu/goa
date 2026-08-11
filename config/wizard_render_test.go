// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

// wizardTestTerminal is a fake tui.Terminal for testing the wizard's render
// lifecycle. It captures the onInput callback (so tests can inject keys) and
// accumulates all Write output (so tests can assert rendered content). It does
// NOT simulate raw-mode or escape-debounce; tests pass decoded key names
// directly (tui.KeyEnter, tui.KeyDown, …) which decodeKeyForRouting passes
// through via its bulk-text passthrough.
type wizardTestTerminal struct {
	mu      sync.Mutex
	w, h    int
	onInput func(string)
	output  strings.Builder
	writes  int
}

func (ft *wizardTestTerminal) Start(onInput func(string), _ func()) {
	ft.mu.Lock()
	ft.onInput = onInput
	ft.mu.Unlock()
}

func (ft *wizardTestTerminal) Stop() {}

func (ft *wizardTestTerminal) Write(p []byte) (int, error) {
	ft.mu.Lock()
	ft.output.Write(p)
	ft.writes++
	ft.mu.Unlock()
	return len(p), nil
}

func (ft *wizardTestTerminal) WriteString(s string) { ft.Write([]byte(s)) }

func (ft *wizardTestTerminal) Size() (int, int) { return ft.w, ft.h }

func (ft *wizardTestTerminal) SetRaw() (func(), error) { return func() {}, nil }

func (ft *wizardTestTerminal) HideCursor()     {}
func (ft *wizardTestTerminal) ShowCursor()     {}
func (ft *wizardTestTerminal) ClearScreen()    {}
func (ft *wizardTestTerminal) SetTitle(string) {}

// reset clears the accumulated output buffer so subsequent waitForContent
// calls only see frames rendered after this point. Needed because the
// compositor uses differential rendering — old output stays in the buffer.
func (ft *wizardTestTerminal) reset() {
	ft.mu.Lock()
	ft.output.Reset()
	ft.mu.Unlock()
}

// strippedOutput returns the accumulated terminal output with ANSI escapes
// removed, so assertions match on visible text only.
func (ft *wizardTestTerminal) strippedOutput() string {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ansi.Strip(ft.output.String())
}

// send injects a decoded key via the captured onInput callback.
func (ft *wizardTestTerminal) send(key string) {
	ft.mu.Lock()
	cb := ft.onInput
	ft.mu.Unlock()
	if cb != nil {
		cb(key)
	}
}

// waitForContent polls the rendered output until needle appears or timeout
// expires. Returns true if found. This is the core synchronization primitive
// for validating that an async renderLoop frame reached the terminal after an
// input-driven state change.
func (ft *wizardTestTerminal) waitForContent(tb testing.TB, needle string, timeout time.Duration) bool {
	tb.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(ft.strippedOutput(), needle) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// newWizardTestLoader creates a CascadeLoader isolated under a temp HOME so the
// wizard's saveConfig never touches the real filesystem.
func newWizardTestLoader(t *testing.T) (*CascadeLoader, string) {
	t.Helper()
	home := t.TempDir()
	projectDir := t.TempDir()
	loader := NewCascadeLoader(projectDir, "", nil)
	loader.homeDir = home
	return loader, home
}

// TestRunWizardWithTerminal_FirstFrameRenders validates the core bug fix:
// runSetupWizardWithTerminal must paint the welcome screen via RenderNow +
// RunLoops. Before the fix, Start() alone left a black screen (dirtyChan nil,
// RequestRender a no-op) so no wizard text ever reached the terminal.
func TestRunWizardWithTerminal_FirstFrameRenders(t *testing.T) {
	loader, _ := newWizardTestLoader(t)
	term := &wizardTestTerminal{w: 100, h: 30}

	done := make(chan *WizardResult, 1)
	go func() {
		result, _ := runSetupWizardWithTerminal(loader.projectDir, loader, term)
		done <- result
	}()

	if !term.waitForContent(t, "Start setup", 2*time.Second) {
		t.Fatalf("first frame did not render welcome text. Output:\n%s", term.strippedOutput())
	}

	// Exit the wizard.
	term.send(tui.KeyEscape)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wizard did not exit after Escape")
	}
}

// TestRunWizardWithTerminal_RefreshOnEveryChange is the central
// "refresh-occurs-on-all-changes" validation. It drives the wizard through
// multiple state transitions (Enter→advance, Down→navigate, Up→navigate,
// Escape→go-back) and asserts that each one produces a new rendered frame on
// the terminal. This proves the RunLoops-driven
// commandLoop→applyCommand→RequestRender chain fires for every input-driven
// mutation, not just the initial RenderNow.
func TestRunWizardWithTerminal_RefreshOnEveryChange(t *testing.T) {
	loader, _ := newWizardTestLoader(t)
	term := &wizardTestTerminal{w: 100, h: 30}

	done := make(chan *WizardResult, 1)
	go func() {
		result, _ := runSetupWizardWithTerminal(loader.projectDir, loader, term)
		done <- result
	}()

	// 1. First frame: welcome screen (RenderNow).
	if !term.waitForContent(t, "Start setup", 2*time.Second) {
		t.Fatalf("welcome screen not rendered. Output:\n%s", term.strippedOutput())
	}

	// 2. Enter → advance to provider type screen (refresh via RunLoops).
	term.send(tui.KeyEnter)
	if !term.waitForContent(t, "Choose your LLM provider", 2*time.Second) {
		t.Fatalf("Enter did not refresh to provider screen. Output:\n%s", term.strippedOutput())
	}

	// 3. Down → selection moves from OpenAI (index 0) to LM Studio (index 1).
	// Reset output first: differential rendering leaves old frames in the
	// buffer, so without resetting the Up assertion below would find stale text.
	term.reset()
	term.send(tui.KeyDown)
	if !term.waitForContent(t, ">   2) LM Studio", 2*time.Second) {
		t.Fatalf("Down did not refresh provider list with LM Studio selected. Output:\n%s", term.strippedOutput())
	}

	// 4. Up → selection moves back to OpenAI (index 0). Reset output first.
	term.reset()
	term.send(tui.KeyUp)
	if !term.waitForContent(t, ">   1) OpenAI", 2*time.Second) {
		t.Fatalf("Up did not move selection back to OpenAI. Output:\n%s", term.strippedOutput())
	}

	// 5. Escape → go back to welcome screen.
	term.send(tui.KeyEscape)
	if !term.waitForContent(t, "Start setup", 2*time.Second) {
		t.Fatalf("Escape did not refresh back to welcome. Output:\n%s", term.strippedOutput())
	}

	// 6. Second Escape → exit the wizard.
	term.send(tui.KeyEscape)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wizard did not exit after second Escape")
	}
}

// TestRunWizardWithTerminal_NumberKeyRefreshes validates that number-key
// quick-pick (1-9) also triggers a refresh: selecting a provider via number
// key changes the highlighted item and the terminal output reflects it.
func TestRunWizardWithTerminal_NumberKeyRefreshes(t *testing.T) {
	loader, _ := newWizardTestLoader(t)
	term := &wizardTestTerminal{w: 100, h: 30}

	done := make(chan *WizardResult, 1)
	go func() {
		result, _ := runSetupWizardWithTerminal(loader.projectDir, loader, term)
		done <- result
	}()

	if !term.waitForContent(t, "Start setup", 2*time.Second) {
		t.Fatalf("welcome screen not rendered. Output:\n%s", term.strippedOutput())
	}

	term.send(tui.KeyEnter)
	if !term.waitForContent(t, "Choose your LLM provider", 2*time.Second) {
		t.Fatalf("provider screen not rendered. Output:\n%s", term.strippedOutput())
	}

	// Press "3" to select Ollama (index 2).
	term.reset()
	term.send("3")
	if !term.waitForContent(t, ">   3) Ollama", 2*time.Second) {
		t.Fatalf("number key '3' did not refresh selection to Ollama. Output:\n%s", term.strippedOutput())
	}

	// Exit.
	term.send(tui.KeyEscape) // back to welcome
	term.waitForContent(t, "Start setup", 2*time.Second)
	term.send(tui.KeyEscape) // exit
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wizard did not exit")
	}
}

// TestRunWizardWithTerminal_TextInputRefreshes validates that text input
// (typing characters into an input field) also triggers a refresh. This
// completes the refresh-on-all-changes matrix: action keys (Enter/Escape),
// navigation keys (Up/Down), number keys (1-9), and now free-form text — all
// routed through the same applyCommand→RequestRender path.
//
// It navigates to the Custom-endpoint input screen and types characters,
// asserting the rendered output updates to show the typed text.
func TestRunWizardWithTerminal_TextInputRefreshes(t *testing.T) {
	loader, _ := newWizardTestLoader(t)
	term := &wizardTestTerminal{w: 100, h: 30}

	done := make(chan *WizardResult, 1)
	go func() {
		result, _ := runSetupWizardWithTerminal(loader.projectDir, loader, term)
		done <- result
	}()

	if !term.waitForContent(t, "Start setup", 2*time.Second) {
		t.Fatalf("welcome screen not rendered. Output:\n%s", term.strippedOutput())
	}

	// Enter → provider screen.
	term.send(tui.KeyEnter)
	if !term.waitForContent(t, "Choose your LLM provider", 2*time.Second) {
		t.Fatalf("provider screen not rendered. Output:\n%s", term.strippedOutput())
	}

	// Navigate to Custom (Up wraps from OpenAI index 0 to Custom, the last item).
	term.reset()
	term.send(tui.KeyUp)
	if !term.waitForContent(t, "Custom", 2*time.Second) {
		t.Fatalf("Up did not reach Custom option. Output:\n%s", term.strippedOutput())
	}

	// Enter on Custom → endpoint input screen.
	term.reset()
	term.send(tui.KeyEnter)
	if !term.waitForContent(t, "Provider Endpoint", 2*time.Second) {
		t.Fatalf("endpoint input screen not rendered. Output:\n%s", term.strippedOutput())
	}

	// Type 'h' — should refresh the input line to show 'h'.
	term.reset()
	term.send("h")
	if !term.waitForContent(t, "h", 2*time.Second) {
		t.Fatalf("typing 'h' did not refresh the input line. Output:\n%s", term.strippedOutput())
	}

	// Type 't' — should refresh to show 'ht'.
	term.reset()
	term.send("t")
	if !term.waitForContent(t, "ht", 2*time.Second) {
		t.Fatalf("typing 't' did not refresh the input line. Output:\n%s", term.strippedOutput())
	}

	// Exit the wizard.
	term.send(tui.KeyEscape) // back to provider type
	term.send(tui.KeyEscape) // back to welcome
	term.send(tui.KeyEscape) // exit
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wizard did not exit")
	}
}
