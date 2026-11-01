// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"fmt"
	"sync"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

// WizardResult carries the configured provider + profile back to main.go.
type WizardResult struct {
	ProviderAdded bool
	ConfigWritten bool
	Cancelled     bool
}

// Quick-start defaults applied by the wizard without asking (bugs.md B42):
// the setup flow only collects provider + key + model; everything else runs
// on the embedded configuration with these postures.
const (
	// wizardDefaultMajor is the persona the wizard activates on finish.
	wizardDefaultMajor = "coding-posture"
	// wizardDefaultAutonomy is the tool-execution posture for the persona:
	// all tool calls run without confirmation.
	wizardDefaultAutonomy = internal.AutonomyYolo
)

// RunSetupWizard launches the interactive setup wizard using the TUI engine.
func RunSetupWizard(projectDir string, loader *CascadeLoader) (*WizardResult, error) {
	return runSetupWizardWithTerminal(projectDir, loader, tui.NewProcessTerminal())
}

// runSetupWizardWithTerminal is the testable core of RunSetupWizard. It accepts
// a Terminal so tests can inject a fake terminal and assert rendered output.
// Production callers use RunSetupWizard, which passes a real ProcessTerminal.
//
// The engine lifecycle mirrors the production app: Start (raw mode + initial
// clear), RenderNow (paint the first frame synchronously), RunLoops (launch the
// commandLoop + renderLoop goroutines). Skipping RunLoops/RenderNow leaves the
// wizard on a black screen: dirtyChan stays nil so RequestRender is a no-op,
// and the terminal callback runs inline via Apply (no RequestRender), so no
// frame ever reaches the terminal even though keystrokes are processed.
func runSetupWizardWithTerminal(projectDir string, loader *CascadeLoader, term tui.Terminal) (*WizardResult, error) {
	cfg, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("load config for wizard: %w", err)
	}

	engine := tui.NewTUI(term)

	done := make(chan *WizardResult, 1)
	w := newWizardComponent(cfg, loader, projectDir, done)
	engine.AddChild(w)
	engine.SetFocus(w)
	w.initDefaults()

	if err := engine.Start(); err != nil {
		return nil, err
	}
	// Paint the first frame synchronously so the user sees the welcome screen
	// immediately, then launch the Actor-model loops so every subsequent
	// input-driven state change flows through commandLoop → applyCommand →
	// RequestRender → renderLoop (the refresh-on-all-changes guarantee).
	engine.RenderNow()
	engine.RunLoops()

	tuiStopped := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		select {
		case <-engine.Stopped():
		case <-tuiStopped:
		}
		stopOnce.Do(func() { close(tuiStopped) })
	}()

	var result *WizardResult
	select {
	case result = <-done:
	case <-tuiStopped:
	}

	engine.Stop()
	stopOnce.Do(func() { close(tuiStopped) })

	if result == nil {
		result = &WizardResult{Cancelled: true}
	}
	return result, nil
}

// -- Wizard state machine -----------------------------------------

type wizardState int

const (
	stateWelcome wizardState = iota
	stateProviderType
	stateProviderEndpoint
	stateProviderKey
	stateProviderTest
	stateModelSelect
	stateModelSetup
	stateModelAdvanced
	stateDone
)

// modelSlotProviderModelFieldIndex defines fields in the model setup form.
const (
	modelFieldModelID = iota
	modelFieldModelName
	modelFieldModelTemp
	modelFieldModelMaxTokens
	modelFieldCount // total number of model fields
)

// modelSlot holds the provider + model configuration for one slot (main or companion).
type modelSlot struct {
	providerID          string
	providerName        string
	selectedPresetIndex int
	endpoint            string
	apiKey              string
	modelID             string
	modelName           string
	modelTemp           string
	modelMaxTokens      string
	modelFieldIdx       int
	modelReasoning      bool
	modelThinkingLevel  string
	availableModels     []string
	selectedModelIdx    int
}

type wizardComponent struct {
	config     *Config
	loader     *CascadeLoader
	projectDir string
	done       chan<- *WizardResult
	state      wizardState
	cancelled  bool
	saved      bool
	saveErr    error

	main modelSlot

	inputMode string
	editor    *tui.LineEditor

	focused bool
}

func newWizardComponent(cfg *Config, loader *CascadeLoader, projectDir string, done chan<- *WizardResult) *wizardComponent {
	w := &wizardComponent{
		config:     cfg,
		loader:     loader,
		projectDir: projectDir,
		done:       done,
		state:      stateWelcome,
		editor:     tui.NewLineEditor(),
	}
	w.focusProvider(0)
	w.initDefaults()
	return w
}

func (w *wizardComponent) initDefaults() {
	w.main.modelTemp = "0.2"
	w.main.modelMaxTokens = "0"
	w.main.modelReasoning = true
	w.main.modelThinkingLevel = "high"
}

func (w *wizardComponent) finish() {
	select {
	case w.done <- &WizardResult{ProviderAdded: w.main.providerID != "", ConfigWritten: w.saved, Cancelled: w.cancelled}:
	default:
	}
}

// -- Component interface ------------------------------------------
