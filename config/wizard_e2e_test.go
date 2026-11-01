// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/tui"
)

// e2eWizardStep is one scripted wizard interaction: keys to send, then the
// screen text to wait for (empty = no assertion).
type e2eWizardStep struct {
	keys    []string
	waitFor string
}

// TestRunWizardWithTerminal_QuickStartEndToEnd drives the pruned quick-start
// flow through the real terminal loop against a local model catalog server:
// welcome → Custom provider → endpoint (digits included) → API key (digits
// included) → provider review → model select (live catalog) → model advanced
// → done. Bugs.md B42: digits typed into endpoint/key fields used to be
// swallowed by the digit quick-pick handler and the saved key lost.
func TestRunWizardWithTerminal_QuickStartEndToEnd(t *testing.T) {
	const apiKey = "sk-test-987654321"

	// Local stand-in for a /v1/models catalog so no external network is hit.
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"demo-small"},{"id":"demo-large"}]}`))
	}))
	defer modelServer.Close()

	loader, home := newWizardTestLoader(t)
	term := newWizardTestTerminal(100, 30)

	done := make(chan *WizardResult, 1)
	go func() {
		result, _ := runSetupWizardWithTerminal(loader.projectDir, loader, term)
		done <- result
	}()

	steps := []e2eWizardStep{
		{waitFor: "Start setup"},
		{keys: []string{tui.KeyEnter}, waitFor: "Choose your LLM provider"},
		{keys: []string{tui.KeyUp}, waitFor: "Custom"},
		{keys: []string{tui.KeyEnter}, waitFor: "Provider Endpoint"},
		{keys: runes(modelServer.URL), waitFor: "Provider Endpoint"},
		{keys: []string{tui.KeyEnter}, waitFor: "API Key"},
		{keys: runes(apiKey), waitFor: "API Key"},
		{keys: []string{tui.KeyEnter}, waitFor: "Provider Review"},
		{keys: []string{tui.KeyEnter}, waitFor: "Select Model"},
		{keys: []string{tui.KeyEnter}, waitFor: "Thinking lvl: high"},
		{keys: []string{tui.KeyEnter}, waitFor: "Setup Complete"},
		{keys: []string{tui.KeyEnter}},
	}
	playWizardSteps(t, term, steps)

	select {
	case result := <-done:
		if result == nil || !result.ConfigWritten {
			t.Fatalf("wizard result not saved: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wizard did not exit after finishing")
	}

	// The full transcript must not contain any of the removed long-form
	// screens (companion, execution mode, skill mode, webfetch, dream,
	// prompt/workflow copy).
	stripped := term.strippedOutput()
	for _, banned := range []string{"Companion", "Execution Mode", "Skill", "Web Fetch", "Dream", "copy prompts", "copy workflows"} {
		if strings.Contains(stripped, banned) {
			t.Errorf("quick-start flow rendered removed screen text %q", banned)
		}
	}

	// The saved home config must carry the typed values verbatim — digits
	// included — plus the quick-start posture.
	data, err := os.ReadFile(filepath.Join(home, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	saved := string(data)
	for _, want := range []string{apiKey, modelServer.URL, "coding-posture", "yolo", "thinking_level: high"} {
		if !strings.Contains(saved, want) {
			t.Errorf("saved config missing %q:\n%s", want, saved)
		}
	}
}

// playWizardSteps sends each step's keys and waits for its screen text.
func playWizardSteps(t *testing.T, term *wizardTestTerminal, steps []e2eWizardStep) {
	t.Helper()
	for i, step := range steps {
		for _, k := range step.keys {
			term.send(k)
		}
		if step.waitFor == "" {
			continue
		}
		if !term.waitForContent(t, step.waitFor, 2*time.Second) {
			t.Fatalf("step %d: %q never rendered. Output:\n%s", i, step.waitFor, term.strippedOutput())
		}
	}
}

// runes explodes s into per-character key sends.
func runes(s string) []string {
	out := make([]string, 0, len(s))
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}
